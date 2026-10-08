package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/example/auth-service/internal/domain"
	"golang.org/x/oauth2"
)

const defaultOAuthReturnTo = "/student"

type OAuthStartResult struct {
	AuthorizationURL string `json:"authorization_url"`
}

func (s *authService) OAuthStart(ctx context.Context, traceID, providerName, returnTo string) (*OAuthStartResult, error) {
	if s.oauth == nil || s.oauthTx == nil {
		return nil, fmt.Errorf("oauth is not available")
	}
	provider, err := s.oauth.Get(providerName)
	if err != nil {
		return nil, err
	}
	returnTo, err = safeReturnTo(returnTo)
	if err != nil {
		return nil, err
	}
	state, err := randomOAuthState()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	now := time.Now().UTC()
	ttl := s.cfg.OAuthTransactionTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	transaction := &domain.OAuthTransaction{
		StateHash:    hashOAuthState(state),
		Provider:     string(provider.Name()),
		CodeVerifier: verifier,
		ReturnTo:     returnTo,
		ExpiresAt:    now.Add(ttl),
	}
	if err := s.oauthTx.Create(ctx, transaction); err != nil {
		return nil, err
	}
	_ = s.oauthTx.DeleteExpired(ctx, now)

	s.logger.Info().Str("trace_id", traceID).Str("provider", string(provider.Name())).Msg("oauth flow started")
	return &OAuthStartResult{AuthorizationURL: provider.AuthorizationURL(state, verifier)}, nil
}

func (s *authService) OAuthCallback(ctx context.Context, traceID, providerName, code, state string) (*domain.AuthUser, *Tokens, error) {
	if s.oauth == nil || s.oauthTx == nil {
		return nil, nil, fmt.Errorf("oauth is not available")
	}
	if strings.TrimSpace(code) == "" || strings.TrimSpace(state) == "" {
		return nil, nil, fmt.Errorf("oauth code and state are required")
	}
	provider, err := s.oauth.Get(providerName)
	if err != nil {
		return nil, nil, err
	}
	transaction, err := s.oauthTx.Consume(ctx, hashOAuthState(state), string(provider.Name()), time.Now().UTC())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, fmt.Errorf("oauth state is invalid, expired, or already used")
		}
		return nil, nil, err
	}

	profile, err := provider.Authenticate(ctx, code, transaction.CodeVerifier)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(profile.ProviderUserID) == "" {
		return nil, nil, fmt.Errorf("provider user id is empty")
	}
	if !profile.EmailVerified {
		return nil, nil, fmt.Errorf("provider email is not verified")
	}
	email := normalizeEmail(profile.Email)
	if err := validateEmail(email); err != nil {
		return nil, nil, fmt.Errorf("provider returned invalid email: %w", err)
	}

	identity := &domain.AuthIdentity{
		Provider:       string(provider.Name()),
		ProviderUserID: strings.TrimSpace(profile.ProviderUserID),
		Email:          email,
		RawProfile:     profile.RawProfile,
	}
	user, created, err := s.identities.ResolveUser(ctx, identity)
	if err != nil {
		return nil, nil, err
	}
	if s.userClient != nil {
		if err := s.userClient.CreateUser(ctx, domain.UserProvisionRequest{
			ID: user.ID, Email: user.Email, Source: "auth", Type: "oauth",
			OAuthProfile: &domain.OAuthProfile{
				Provider:  string(provider.Name()),
				FirstName: profile.FirstName,
				LastName:  profile.LastName,
				BirthYear: profile.BirthYear,
				Gender:    profile.Gender,
				AvatarURL: profile.AvatarURL,
			},
		}); err != nil {
			return nil, nil, fmt.Errorf("provision oauth user: %w", err)
		}
	}
	if s.rbacClient != nil {
		hasRole, err := s.rbacClient.CheckRole(ctx, user.ID, s.cfg.DefaultRole)
		if err != nil {
			return nil, nil, fmt.Errorf("check oauth user role: %w", err)
		}
		if !hasRole {
			if err := s.rbacClient.AssignRole(ctx, user.ID, s.cfg.DefaultRole); err != nil {
				return nil, nil, fmt.Errorf("assign oauth user role: %w", err)
			}
		}
	}

	now := time.Now().UTC()
	user.LastLoginAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return nil, nil, err
	}
	tokens, err := s.issueTokens(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	tokens.ReturnTo = transaction.ReturnTo

	s.logger.Info().Str("trace_id", traceID).Str("provider", string(provider.Name())).Str("user_id", user.ID).Bool("created", created).Msg("oauth signin")
	return user, tokens, nil
}

func randomOAuthState() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashOAuthState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func safeReturnTo(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultOAuthReturnTo, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return "", fmt.Errorf("return_to must be a local absolute path")
	}
	return parsed.RequestURI(), nil
}
