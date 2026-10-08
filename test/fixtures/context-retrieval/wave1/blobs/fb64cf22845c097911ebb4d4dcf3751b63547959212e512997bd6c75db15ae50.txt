package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	"github.com/example/auth-service/internal/tokenverify"
	pkglog "github.com/example/auth-service/pkg/log"
	"golang.org/x/crypto/bcrypt"
)

var (
	errInvalidCredentials = errors.New("invalid credentials")
	tokenRoleCandidates   = []string{"admin", "moderator", "teacher", "student", "user", "guest"}
)

const signupRole = "student"

type Service interface {
	StartSignup(ctx context.Context, traceID, email, password string) error
	VerifySignup(ctx context.Context, traceID, email, code string) (*domain.AuthUser, *Tokens, error)
	SignIn(ctx context.Context, traceID, email, password string) (*domain.AuthUser, *Tokens, error)
	OAuthStart(ctx context.Context, traceID, provider, returnTo string) (*OAuthStartResult, error)
	OAuthCallback(ctx context.Context, traceID, provider, code, state string) (*domain.AuthUser, *Tokens, error)
	Refresh(ctx context.Context, traceID, refreshToken string) (*Tokens, error)
	RevokeRefreshToken(ctx context.Context, traceID, refreshToken string) error
	GetMe(ctx context.Context, traceID, userID string) (*AuthMe, error)
	StartEmailChange(ctx context.Context, traceID, userID, newEmail string) (string, error)
	VerifyEmailChange(ctx context.Context, traceID, code string) (*domain.AuthUser, error)
	StartPasswordReset(ctx context.Context, traceID, email string) (string, error)
	FinishPasswordReset(ctx context.Context, traceID, email, code, newPassword string) error
	ChangePassword(ctx context.Context, traceID, userID, oldPassword, newPassword string) error
	SetPassword(ctx context.Context, traceID, userID, newPassword string) error
	ListIdentities(ctx context.Context, traceID, userID string) ([]domain.AuthIdentity, error)
	RemoveIdentity(ctx context.Context, traceID, userID, provider, providerUserID string) error
	VerifyToken(ctx context.Context, traceID, token string) (*VerificationResult, error)
}

type AuthMe struct {
	UserID            string     `json:"user_id"`
	Email             string     `json:"email"`
	PasswordUpdatedAt *time.Time `json:"password_updated_at"`
}

type authService struct {
	cfg         *config.Config
	logger      pkglog.Logger
	users       domain.AuthUserRepository
	identities  domain.AuthIdentityRepository
	oauthTx     domain.OAuthTransactionRepository
	oauth       domain.OAuthRegistry
	refresh     domain.RefreshTokenRepository
	tarantoool  domain.VerificationClient
	userClient  domain.UserProvisioner
	rbacClient  domain.RoleClient
	signer      JWTSigner
	completions domain.SignupCompletionRepository
	signupProof domain.SignupProofConsumer
}

func NewAuthService(cfg *config.Config, logger pkglog.Logger, users domain.AuthUserRepository, identities domain.AuthIdentityRepository, oauthTx domain.OAuthTransactionRepository, oauthRegistry domain.OAuthRegistry, refresh domain.RefreshTokenRepository, tara domain.VerificationClient, userClient domain.UserProvisioner, rbacClient domain.RoleClient, signer JWTSigner) Service {
	completions, _ := users.(domain.SignupCompletionRepository)
	proof, _ := tara.(domain.SignupProofConsumer)
	return &authService{completions: completions, signupProof: proof, cfg: cfg, logger: logger, users: users, identities: identities, oauthTx: oauthTx, oauth: oauthRegistry, refresh: refresh, tarantoool: tara, userClient: userClient, rbacClient: rbacClient, signer: signer}
}

func (s *authService) StartSignup(ctx context.Context, traceID, email, password string) error {
	norm := normalizeEmail(email)
	if err := validateEmail(norm); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	if _, err := s.users.FindByEmail(ctx, norm); err == nil {
		return fmt.Errorf("user already exists")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.tarantoool.StartSignup(ctx, norm, string(hash)); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	s.logger.Info().Str("trace_id", traceID).Str("email", norm).Msg("signup initiated")
	return nil
}

func (s *authService) VerifySignup(ctx context.Context, traceID, email, code string) (*domain.AuthUser, *Tokens, error) {
	norm := normalizeEmail(email)
	if err := validateEmail(norm); err != nil {
		return nil, nil, err
	}

	return s.completeSignupProof(ctx, traceID, norm, code)
}

func (s *authService) provisionSignupActor(ctx context.Context, user *domain.AuthUser, operationID string) error {
	if user == nil || user.ID == "" || operationID == "" {
		return errors.New("signup role binding unavailable")
	}
	if s.userClient == nil {
		return errors.New("signup user provisioning is unavailable")
	}
	if err := s.userClient.CreateUser(ctx, domain.UserProvisionRequest{
		ID: user.ID, Email: user.Email, Source: "auth", Type: "signup",
	}); err != nil {
		return fmt.Errorf("provision signup user: %w", err)
	}

	if s.rbacClient == nil {
		return errors.New("signup role provisioning is unavailable")
	}
	provisioner, ok := s.rbacClient.(domain.SignupRoleProvisioner)
	if !ok {
		return errors.New("authenticated signup role provisioning unavailable")
	}
	if err := provisioner.AssignSignupRole(ctx, user.ID, operationID); err != nil {
		return fmt.Errorf("assign signup role: %w", err)
	}
	hasRole, err := s.rbacClient.CheckRole(ctx, user.ID, signupRole)
	if err != nil {
		return fmt.Errorf("check signup role: %w", err)
	}
	if !hasRole {
		return errors.New("signup role is not readable after assignment")
	}
	return nil
}

func (s *authService) SignIn(ctx context.Context, traceID, email, password string) (*domain.AuthUser, *Tokens, error) {
	norm := normalizeEmail(email)
	user, err := s.users.FindByEmail(ctx, norm)
	if err != nil {
		return nil, nil, errInvalidCredentials
	}
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)) != nil {
		return nil, nil, errInvalidCredentials
	}
	if err := s.repairPasswordSignup(ctx, user); err != nil {
		return nil, nil, err
	}
	now := time.Now()
	user.LastLoginAt = &now
	_ = s.users.Update(ctx, user)
	tokens, err := s.issueTokens(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("signin")
	return user, tokens, nil
}

func (s *authService) Refresh(ctx context.Context, traceID, refreshToken string) (*Tokens, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errInvalidCredentials
	}
	tok, claims, err := s.signer.Parse(refreshToken)
	if err != nil || tok == nil || !tok.Valid {
		return nil, errInvalidCredentials
	}
	if typ, _ := claims["typ"].(string); typ != "refresh" {
		return nil, errInvalidCredentials
	}
	jti, _ := claims["jti"].(string)
	sub, _ := claims["sub"].(string)
	if jti == "" || sub == "" {
		return nil, errInvalidCredentials
	}
	hash := hashToken(jti)
	session, err := s.refresh.FindActive(ctx, hash)
	if err != nil {
		return nil, errInvalidCredentials
	}
	if session.UserID != sub {
		return nil, errInvalidCredentials
	}
	user, err := s.users.FindByID(ctx, sub)
	if err != nil {
		return nil, errInvalidCredentials
	}
	if err := s.refresh.RevokeByHash(ctx, hash); err != nil {
		return nil, err
	}
	return s.issueTokens(ctx, user)
}

func (s *authService) RevokeRefreshToken(ctx context.Context, traceID, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return errInvalidCredentials
	}
	tok, claims, err := s.signer.Parse(refreshToken)
	if err != nil || tok == nil || !tok.Valid {
		return errInvalidCredentials
	}
	if typ, _ := claims["typ"].(string); typ != "refresh" {
		return errInvalidCredentials
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		return errInvalidCredentials
	}

	if err := s.refresh.RevokeByHash(ctx, hashToken(jti)); err != nil {
		return err
	}

	s.logger.Info().Str("trace_id", traceID).Msg("refresh token revoked")
	return nil
}

func (s *authService) StartEmailChange(ctx context.Context, traceID, userID, newEmail string) (string, error) {
	norm := normalizeEmail(newEmail)
	if err := validateEmail(norm); err != nil {
		return "", err
	}
	uuid, err := s.tarantoool.StartEmailChange(ctx, userID, norm)
	if err != nil {
		return "", err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", userID).Msg("email change started")
	return uuid, nil
}

func (s *authService) GetMe(ctx context.Context, traceID, userID string) (*AuthMe, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	s.logger.Info().Str("trace_id", traceID).Str("user_id", userID).Msg("auth me loaded")

	return &AuthMe{
		UserID:            user.ID,
		Email:             maskEmail(user.Email),
		PasswordUpdatedAt: user.PasswordUpdatedAt,
	}, nil
}

func (s *authService) VerifyEmailChange(ctx context.Context, traceID, code string) (*domain.AuthUser, error) {
	userID, newEmail, err := s.tarantoool.VerifyEmailChange(ctx, code)
	if err != nil {
		return nil, err
	}
	norm := normalizeEmail(newEmail)
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	user.Email = norm
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("email change verified")
	return user, nil
}

func (s *authService) StartPasswordReset(ctx context.Context, traceID, email string) (string, error) {
	norm := normalizeEmail(email)
	if err := validateEmail(norm); err != nil {
		return "", err
	}
	if _, err := s.users.FindByEmail(ctx, norm); err != nil {
		return "", errInvalidCredentials
	}
	uuid, err := s.tarantoool.StartPasswordReset(ctx, norm)
	if err != nil {
		return "", err
	}
	s.logger.Info().Str("trace_id", traceID).Str("email", norm).Msg("password reset start")
	return uuid, nil
}

func (s *authService) FinishPasswordReset(ctx context.Context, traceID, email, code, newPassword string) error {
	norm := normalizeEmail(email)
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	if err := s.tarantoool.VerifyPasswordReset(ctx, norm, code); err != nil {
		return err
	}
	user, err := s.users.FindByEmail(ctx, norm)
	if err != nil {
		return errInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	hashString := string(hash)
	now := time.Now().UTC()
	user.PasswordHash = &hashString
	user.PasswordUpdatedAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("password reset finished")
	return nil
}

func (s *authService) ChangePassword(ctx context.Context, traceID, userID, oldPassword, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return errInvalidCredentials
	}
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(oldPassword)) != nil {
		return errInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	hashString := string(hash)
	now := time.Now().UTC()
	user.PasswordHash = &hashString
	user.PasswordUpdatedAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("password changed")
	return nil
}

func (s *authService) SetPassword(ctx context.Context, traceID, userID, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return errInvalidCredentials
	}
	if user.PasswordHash != nil {
		return fmt.Errorf("password is already set; use password change")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	hashString := string(hash)
	now := time.Now().UTC()
	user.PasswordHash = &hashString
	user.PasswordUpdatedAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("password set")
	return nil
}

func (s *authService) VerifyToken(ctx context.Context, traceID, token string) (*VerificationResult, error) {
	result, err := tokenverify.Verify(s.signer, token, time.Now)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", result.UserID).Msg("token verified")
	return result, nil
}

func (s *authService) issueTokens(ctx context.Context, user *domain.AuthUser) (*Tokens, error) {
	if s.completions == nil {
		return nil, errors.New("signup completion persistence unavailable")
	}
	pending, err := s.completions.SignupPrincipalPending(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("read signup readiness: %w", err)
	}
	if pending {
		return nil, errors.New("signup principal is pending")
	}
	claims := map[string]interface{}{"email": user.Email}
	if role := s.resolveRole(ctx, user.ID); role != "" {
		claims["role"] = role
	}
	access, err := s.signer.SignAccessToken(user.ID, claims, s.cfg.AccessTTL)
	if err != nil {
		return nil, err
	}
	jti, err := GenerateJTI()
	if err != nil {
		return nil, err
	}
	refresh, err := s.signer.SignRefreshToken(user.ID, jti, s.cfg.RefreshTTL)
	if err != nil {
		return nil, err
	}
	// persist refresh
	if err := s.refresh.Create(ctx, &domain.RefreshToken{
		UserID:           user.ID,
		RefreshTokenHash: hashToken(jti),
		ExpiresAt:        time.Now().Add(s.cfg.RefreshTTL),
	}); err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int64(s.cfg.AccessTTL.Seconds())}, nil
}

func (s *authService) resolveRole(ctx context.Context, userID string) string {
	if s.rbacClient == nil {
		return ""
	}
	candidates := collectRoleCandidates(s.cfg.DefaultRole)
	for _, role := range candidates {
		ok, err := s.rbacClient.CheckRole(ctx, userID, role)
		if err != nil {
			return ""
		}
		if ok {
			return role
		}
	}
	return ""
}

func collectRoleCandidates(defaultRole string) []string {
	defaultRole = strings.ToLower(strings.TrimSpace(defaultRole))
	seen := make(map[string]struct{}, len(tokenRoleCandidates)+1)
	candidates := make([]string, 0, len(tokenRoleCandidates)+1)
	for _, role := range tokenRoleCandidates {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		candidates = append(candidates, role)
	}
	if defaultRole != "" {
		if _, ok := seen[defaultRole]; !ok {
			candidates = append(candidates, defaultRole)
		}
	}
	return candidates
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func maskEmail(email string) string {
	normalized := normalizeEmail(email)
	parts := strings.Split(normalized, "@")
	if len(parts) != 2 {
		return normalized
	}

	localRunes := []rune(parts[0])
	if len(localRunes) == 0 {
		return normalized
	}

	domain := parts[1]
	domainName := domain
	domainSuffix := ""
	if dot := strings.LastIndex(domain, "."); dot > 0 && dot < len(domain)-1 {
		domainName = domain[:dot]
		domainSuffix = domain[dot:]
	}

	maskedDomain := "***"
	domainRunes := []rune(domainName)
	if len(domainRunes) > 0 {
		maskedDomain += string(domainRunes[len(domainRunes)-1])
	}

	return string(localRunes[0]) + "****@" + maskedDomain + domainSuffix
}

func validateEmail(email string) error {
	if !strings.Contains(email, "@") || len(email) > 255 {
		return fmt.Errorf("invalid email")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password too short")
	}
	return nil
}

func hashToken(jti string) string {
	return fmt.Sprintf("rt:%s", jti)
}
