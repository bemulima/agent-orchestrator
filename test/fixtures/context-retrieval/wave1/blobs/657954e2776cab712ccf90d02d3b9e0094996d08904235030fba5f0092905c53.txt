package unit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	"github.com/example/auth-service/internal/usecase"
	pkglog "github.com/example/auth-service/pkg/log"
	"golang.org/x/crypto/bcrypt"
)

type mockUserRepo struct {
	users          map[string]*domain.AuthUser
	next           int
	findByEmailErr error
	completions    map[string]*domain.SignupCompletion
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: map[string]*domain.AuthUser{}, completions: map[string]*domain.SignupCompletion{}}
}

func (r *mockUserRepo) Create(_ context.Context, user *domain.AuthUser) error {
	if user.ID == "" {
		r.next++
		user.ID = fmt.Sprintf("user-%d", r.next)
	}
	r.users[user.ID] = user
	return nil
}

func (r *mockUserRepo) FindByEmail(_ context.Context, email string) (*domain.AuthUser, error) {
	if r.findByEmailErr != nil {
		return nil, r.findByEmailErr
	}

	for _, u := range r.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *mockUserRepo) FindByID(_ context.Context, id string) (*domain.AuthUser, error) {
	if u, ok := r.users[id]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

func (r *mockUserRepo) Update(_ context.Context, user *domain.AuthUser) error {
	r.users[user.ID] = user
	return nil
}

// Completion storage is a unit-test fake only. Production requires PostgreSQL.
func (r *mockUserRepo) BeginSignupCompletion(ctx context.Context, email, proof, operationID, principalID string) (*domain.SignupCompletion, error) {
	for _, op := range r.completions {
		if op.Email == email && op.ProofFingerprint == proof {
			v := *op
			return &v, nil
		}
	}
	if _, err := r.FindByEmail(ctx, email); err == nil {
		return nil, errors.New("existing signup identity")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	op := &domain.SignupCompletion{OperationID: operationID, PrincipalID: principalID, Email: email, ProofFingerprint: proof, State: domain.SignupPending}
	r.completions[operationID] = op
	v := *op
	return &v, nil
}
func (r *mockUserRepo) StoreSignupReceipt(_ context.Context, id string, receipt *domain.SignupProofReceipt) (*domain.SignupCompletion, error) {
	op, ok := r.completions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if receipt.OperationID != id || receipt.Email != op.Email || !receipt.ExpiresAt.After(time.Now()) || op.State == domain.SignupCompleted {
		return nil, errors.New("receipt binding")
	}
	if op.State == domain.SignupVerified && (op.PasswordHash != receipt.PasswordHash || op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.Equal(receipt.ExpiresAt)) {
		return nil, errors.New("frozen receipt mismatch")
	}
	op.PasswordHash = receipt.PasswordHash
	op.ReceiptExpiresAt = &receipt.ExpiresAt
	op.State = domain.SignupVerified
	v := *op
	return &v, nil
}
func (r *mockUserRepo) EnsureSignupPrincipal(ctx context.Context, id string) (*domain.AuthUser, error) {
	op, ok := r.completions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if op.State != domain.SignupVerified {
		return nil, errors.New("operation not verified")
	}
	if user, err := r.FindByEmail(ctx, op.Email); err == nil {
		if !op.PrincipalCreated || user.ID != op.PrincipalID || user.PasswordHash == nil || *user.PasswordHash != op.PasswordHash {
			return nil, errors.New("principal ownership mismatch")
		}
		return user, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	hash := op.PasswordHash
	now := time.Now()
	user := &domain.AuthUser{ID: op.PrincipalID, Email: op.Email, PasswordHash: &hash, PasswordUpdatedAt: &now}
	if err := r.Create(ctx, user); err != nil {
		return nil, err
	}
	op.PrincipalCreated = true
	return user, nil
}
func (r *mockUserRepo) CompleteSignup(_ context.Context, id string, now time.Time, fresh bool) (bool, error) {
	op, ok := r.completions[id]
	if !ok {
		return false, domain.ErrNotFound
	}
	if op.State != domain.SignupVerified {
		return false, nil
	}
	if !op.PrincipalCreated {
		return false, errors.New("principal not owned")
	}
	if fresh && (op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.After(now)) {
		return false, nil
	}
	op.State = domain.SignupCompleted
	return true, nil
}
func (r *mockUserRepo) SignupPrincipalPending(_ context.Context, id string) (bool, error) {
	for _, op := range r.completions {
		if op.PrincipalID == id {
			return op.State != domain.SignupCompleted, nil
		}
	}
	return false, nil
}
func (r *mockUserRepo) FindSignupCompletionByPrincipal(_ context.Context, id string) (*domain.SignupCompletion, error) {
	for _, op := range r.completions {
		if op.PrincipalID == id {
			v := *op
			return &v, nil
		}
	}
	return nil, domain.ErrNotFound
}

type mockIdentityRepo struct{}

func (mockIdentityRepo) FindByProvider(_ context.Context, _, _ string) (*domain.AuthIdentity, error) {
	return nil, domain.ErrNotFound
}
func (mockIdentityRepo) Create(_ context.Context, _ *domain.AuthIdentity) error { return nil }
func (mockIdentityRepo) ResolveUser(_ context.Context, _ *domain.AuthIdentity) (*domain.AuthUser, bool, error) {
	return nil, false, domain.ErrNotFound
}
func (mockIdentityRepo) ListByUser(_ context.Context, _ string) ([]domain.AuthIdentity, error) {
	return nil, nil
}
func (mockIdentityRepo) Delete(_ context.Context, _, _, _ string) error { return nil }

type mockRefreshRepo struct {
	tokens    map[string]domain.RefreshToken
	createErr error
}

func newMockRefreshRepo() *mockRefreshRepo {
	return &mockRefreshRepo{tokens: map[string]domain.RefreshToken{}}
}

func (r *mockRefreshRepo) Create(_ context.Context, token *domain.RefreshToken) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.tokens[token.RefreshTokenHash] = *token
	return nil
}

func (r *mockRefreshRepo) FindActive(_ context.Context, hash string) (*domain.RefreshToken, error) {
	tok, ok := r.tokens[hash]
	if !ok || tok.RevokedAt != nil || tok.ExpiresAt.Before(time.Now()) {
		return nil, domain.ErrNotFound
	}
	return &tok, nil
}

func (r *mockRefreshRepo) RevokeByHash(_ context.Context, hash string) error {
	if tok, ok := r.tokens[hash]; ok {
		now := time.Now()
		tok.RevokedAt = &now
		r.tokens[hash] = tok
	}
	return nil
}

type mockTarantool struct {
	signupReceipts       map[string]*domain.SignupProofReceipt
	lastSignupEmail      string
	lastSignupPassword   string
	emailChangeUUID      string
	lastEmailChangeUser  string
	lastEmailChangeEmail string
	passwordResetUUID    string
	lastPasswordReset    string

	startSignupErr          error
	verifySignupPassword    string
	verifySignupErr         error
	verifyEmailChangeUserID string
	verifyEmailChangeEmail  string
	verifyEmailChangeErr    error
	verifyPasswordResetErr  error
}

func (m *mockTarantool) ConsumeSignupProof(_ context.Context, email, code, operationID string) (*domain.SignupProofReceipt, error) {
	if m.verifySignupErr != nil {
		return nil, m.verifySignupErr
	}
	if m.signupReceipts == nil {
		m.signupReceipts = map[string]*domain.SignupProofReceipt{}
	}
	key := email + "\x00" + code
	if receipt, ok := m.signupReceipts[key]; ok {
		if receipt.OperationID != operationID {
			return nil, domain.ErrNotFound
		}
		v := *receipt
		return &v, nil
	}
	receipt := &domain.SignupProofReceipt{OperationID: operationID, Email: email, PasswordHash: m.verifySignupPassword, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	m.signupReceipts[key] = receipt
	v := *receipt
	return &v, nil
}
func (m *mockTarantool) StartSignup(_ context.Context, email, passwordHash string) error {
	m.lastSignupEmail = email
	m.lastSignupPassword = passwordHash
	return m.startSignupErr
}

func (m *mockTarantool) VerifySignup(_ context.Context, _ string, _ string) (string, error) {
	if m.verifySignupErr != nil {
		return "", m.verifySignupErr
	}
	return m.verifySignupPassword, nil
}

func (m *mockTarantool) StartEmailChange(_ context.Context, userID, newEmail string) (string, error) {
	m.lastEmailChangeUser = userID
	m.lastEmailChangeEmail = newEmail
	return m.emailChangeUUID, nil
}

func (m *mockTarantool) VerifyEmailChange(_ context.Context, _ string) (string, string, error) {
	return m.verifyEmailChangeUserID, m.verifyEmailChangeEmail, m.verifyEmailChangeErr
}

func (m *mockTarantool) StartPasswordReset(_ context.Context, email string) (string, error) {
	m.lastPasswordReset = email
	return m.passwordResetUUID, nil
}

func (m *mockTarantool) VerifyPasswordReset(_ context.Context, _ string, _ string) error {
	return m.verifyPasswordResetErr
}

type recordingUserClient struct {
	calls []domain.UserProvisionRequest
}

func (r *recordingUserClient) CreateUser(_ context.Context, request domain.UserProvisionRequest) error {
	r.calls = append(r.calls, request)
	return nil
}

type recordingRBACClient struct {
	calls []struct {
		userID string
		role   string
	}
	checkCalls []struct {
		userID string
		role   string
	}
	roleByUser map[string]string
}

// Unit-only signup facet preserves the existing provisioning assertions.
func (r *recordingRBACClient) AssignSignupRole(ctx context.Context, userID, operationID string) error {
	if operationID == "" {
		return errors.New("missing signup operation")
	}
	return r.AssignRole(ctx, userID, "student")
}

func (r *recordingRBACClient) AssignRole(_ context.Context, userID, role string) error {
	r.calls = append(r.calls, struct {
		userID string
		role   string
	}{userID: userID, role: role})
	if r.roleByUser == nil {
		r.roleByUser = make(map[string]string)
	}
	r.roleByUser[userID] = role
	return nil
}

func (r *recordingRBACClient) CheckRole(_ context.Context, userID, role string) (bool, error) {
	r.checkCalls = append(r.checkCalls, struct {
		userID string
		role   string
	}{userID: userID, role: role})
	if r.roleByUser == nil {
		return false, nil
	}
	return r.roleByUser[userID] == role, nil
}

func TestSignInIncludesRoleInAccessToken(t *testing.T) {
	rbacClient := &recordingRBACClient{
		roleByUser: map[string]string{"user-1": "admin"},
	}
	svc, deps := newTestServiceWithClients(t, nil, rbacClient)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.DefaultCost)
	_ = deps.users.Create(context.Background(), &domain.AuthUser{ID: "user-1", Email: "user@example.com", PasswordHash: stringPtr(string(hash))})
	_, tokens, err := svc.SignIn(context.Background(), "trace", "user@example.com", "secret123")
	if err != nil {
		t.Fatalf("signin: %v", err)
	}
	_, claims, err := deps.signer.Parse(tokens.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	role, _ := claims["role"].(string)
	if role != "admin" {
		t.Fatalf("expected role claim admin, got %q", role)
	}
}

type testDeps struct {
	users      *mockUserRepo
	refresh    *mockRefreshRepo
	tara       *mockTarantool
	signer     usecase.JWTSigner
	cfg        *config.Config
	userClient domain.UserProvisioner
	rbacClient domain.RoleClient
}

func newTestService(t *testing.T) (usecase.Service, *testDeps) {
	return newTestServiceWithClients(t, nil, nil)
}

func newTestServiceWithClients(t *testing.T, userClient domain.UserProvisioner, rbacClient domain.RoleClient) (usecase.Service, *testDeps) {
	t.Helper()
	cfg := &config.Config{
		JWTSecret:   "test-secret",
		JWTIssuer:   "auth",
		JWTAudience: "frontend",
		AccessTTL:   time.Minute,
		RefreshTTL:  time.Hour,
		DefaultRole: "user",
	}
	signer, err := usecase.NewJWTSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	users := newMockUserRepo()
	refresh := newMockRefreshRepo()
	tara := &mockTarantool{
		emailChangeUUID:         "email-change-uuid",
		passwordResetUUID:       "pwd-reset-uuid",
		verifyEmailChangeUserID: "user-1",
		verifyEmailChangeEmail:  "new@example.com",
	}
	svc := usecase.NewAuthService(cfg, pkglog.New("test"), users, mockIdentityRepo{}, nil, nil, refresh, tara, userClient, rbacClient, signer)
	return svc, &testDeps{users: users, refresh: refresh, tara: tara, signer: signer, cfg: cfg, userClient: userClient, rbacClient: rbacClient}
}

func TestStartSignup(t *testing.T) {
	svc, deps := newTestService(t)
	// existing user
	_ = deps.users.Create(context.Background(), &domain.AuthUser{Email: "user@example.com"})
	if err := svc.StartSignup(context.Background(), "trace", "user@example.com", "password123"); err == nil {
		t.Fatalf("expected error for existing user")
	}
	// new user
	if err := svc.StartSignup(context.Background(), "trace", "New@Example.com", "password123"); err != nil {
		t.Fatalf("start signup: %v", err)
	}
	if deps.tara.lastSignupEmail != "new@example.com" {
		t.Fatalf("email not normalized: %s", deps.tara.lastSignupEmail)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(deps.tara.lastSignupPassword), []byte("password123")); err != nil {
		t.Fatalf("expected bcrypt password hash to be stored in tarantool")
	}
}

func TestStartSignupAllowsDeclaredLookupAbsence(t *testing.T) {
	svc, deps := newTestService(t)
	deps.users.findByEmailErr = domain.ErrNotFound

	if err := svc.StartSignup(context.Background(), "trace", "user@example.com", "password123"); err != nil {
		t.Fatal("expected declared absence to continue signup")
	}
	if deps.tara.lastSignupEmail == "" {
		t.Fatal("expected identity signup after declared absence")
	}
}

func TestStartSignupAllowsDeclaredIdentityAbsence(t *testing.T) {
	svc, deps := newTestService(t)
	deps.tara.startSignupErr = domain.ErrNotFound

	if err := svc.StartSignup(context.Background(), "trace", "user@example.com", "password123"); err != nil {
		t.Fatal("expected declared identity absence to continue signup")
	}
	if deps.tara.lastSignupEmail == "" {
		t.Fatal("expected identity signup attempt")
	}
}

func TestStartSignupPropagatesLookupFailure(t *testing.T) {
	svc, deps := newTestService(t)
	expected := errors.New("lookup failed")
	deps.users.findByEmailErr = expected

	err := svc.StartSignup(context.Background(), "trace", "user@example.com", "password123")
	if !errors.Is(err, expected) {
		t.Fatal("expected lookup failure to be returned")
	}
	if deps.tara.lastSignupEmail != "" {
		t.Fatal("identity signup must not run after lookup failure")
	}
}

func TestStartSignupPropagatesIdentityFailure(t *testing.T) {
	svc, deps := newTestService(t)
	expected := errors.New("identity unavailable")
	deps.tara.startSignupErr = expected

	err := svc.StartSignup(context.Background(), "trace", "user@example.com", "password123")
	if !errors.Is(err, expected) {
		t.Fatal("expected identity failure to be returned")
	}
}

func TestVerifySignupCreatesUserAndTokens(t *testing.T) {
	svc, deps := newTestServiceWithClients(t, &recordingUserClient{}, &recordingRBACClient{})
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	deps.tara.verifySignupPassword = string(hash)
	user, tokens, err := svc.VerifySignup(context.Background(), "trace", "User@Example.com", "code")
	if err != nil {
		t.Fatalf("verify signup: %v", err)
	}
	if user == nil || tokens == nil {
		t.Fatalf("expected user and tokens")
		return
	}
	if user.Email != "user@example.com" {
		t.Fatalf("expected normalized email, got %s", user.Email)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("tokens should be issued")
	}
	if len(deps.refresh.tokens) == 0 {
		t.Fatalf("refresh token not stored")
	}
}

func TestVerifySignupNotifiesUserAndRBAC(t *testing.T) {
	userClient := &recordingUserClient{}
	rbacClient := &recordingRBACClient{}
	svc, deps := newTestServiceWithClients(t, userClient, rbacClient)

	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	deps.tara.verifySignupPassword = string(hash)
	user, _, err := svc.VerifySignup(context.Background(), "trace", "notify@example.com", "code")
	if err != nil {
		t.Fatalf("verify signup: %v", err)
	}
	if user == nil {
		t.Fatalf("user is nil")
		return
	}
	if len(userClient.calls) != 1 {
		t.Fatalf("expected CreateUser to be called once, got %d", len(userClient.calls))
	}
	call := userClient.calls[0]
	if call.ID != user.ID || call.Email != user.Email || call.Source != "auth" || call.Type != "signup" || call.OAuthProfile != nil {
		t.Fatalf("CreateUser call mismatch: %+v", call)
	}
	if len(rbacClient.calls) != 1 {
		t.Fatalf("expected AssignRole to be called once, got %d", len(rbacClient.calls))
	}
	rbacCall := rbacClient.calls[0]
	if rbacCall.userID != user.ID || rbacCall.role != "student" {
		t.Fatalf("AssignRole call mismatch: %+v", rbacCall)
	}
	if len(rbacClient.checkCalls) == 0 {
		t.Fatal("expected CheckRole to confirm the signup role")
	}
}

func TestSignIn(t *testing.T) {
	svc, deps := newTestService(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.DefaultCost)
	_ = deps.users.Create(context.Background(), &domain.AuthUser{Email: "user@example.com", PasswordHash: stringPtr(string(hash))})
	user, tokens, err := svc.SignIn(context.Background(), "trace", "user@example.com", "secret123")
	if err != nil {
		t.Fatalf("signin: %v", err)
	}
	if user.LastLoginAt == nil {
		t.Fatalf("last login not set")
	}
	if tokens.AccessToken == "" {
		t.Fatalf("access token missing")
	}
}

func TestRefresh(t *testing.T) {
	svc, deps := newTestService(t)
	user := &domain.AuthUser{ID: "user-1", Email: "user@example.com"}
	_ = deps.users.Create(context.Background(), user)
	jti := "jti-1"
	refreshTok, err := deps.signer.SignRefreshToken(user.ID, jti, deps.cfg.RefreshTTL)
	if err != nil {
		t.Fatalf("sign refresh: %v", err)
	}
	if err := deps.refresh.Create(context.Background(), &domain.RefreshToken{
		UserID:           user.ID,
		RefreshTokenHash: hashToken(jti),
		ExpiresAt:        time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create initial refresh session: %v", err)
	}
	tokens, err := svc.Refresh(context.Background(), "trace", refreshTok)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tokens.AccessToken == "" {
		t.Fatalf("expected new access token")
	}
	oldSession, ok := deps.refresh.tokens[hashToken(jti)]
	if !ok {
		t.Fatalf("old refresh session not found")
	}
	if oldSession.RevokedAt == nil {
		t.Fatalf("old refresh session must be revoked after refresh")
	}

	_, claims, err := deps.signer.Parse(tokens.RefreshToken)
	if err != nil {
		t.Fatalf("parse new refresh token: %v", err)
	}
	newJTI, _ := claims["jti"].(string)
	if newJTI == "" {
		t.Fatalf("new refresh token jti missing")
	}
	if newJTI == jti {
		t.Fatalf("refresh token must be rotated")
	}
	if _, ok := deps.refresh.tokens[hashToken(newJTI)]; !ok {
		t.Fatalf("new refresh session was not persisted")
	}
}

func TestRefreshRejectsMismatchedSession(t *testing.T) {
	svc, deps := newTestService(t)
	user := &domain.AuthUser{ID: "user-1", Email: "user@example.com"}
	_ = deps.users.Create(context.Background(), user)
	jti := "jti-2"
	refreshTok, err := deps.signer.SignRefreshToken(user.ID, jti, deps.cfg.RefreshTTL)
	if err != nil {
		t.Fatalf("sign refresh: %v", err)
	}
	if err := deps.refresh.Create(context.Background(), &domain.RefreshToken{
		UserID:           "other-user",
		RefreshTokenHash: hashToken(jti),
		ExpiresAt:        time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create initial refresh session: %v", err)
	}
	if _, err := svc.Refresh(context.Background(), "trace", refreshTok); err == nil {
		t.Fatalf("expected error for mismatched session")
	}
}

func TestRefreshFailsWhenNewSessionPersistFails(t *testing.T) {
	svc, deps := newTestService(t)
	user := &domain.AuthUser{ID: "user-1", Email: "user@example.com"}
	_ = deps.users.Create(context.Background(), user)
	jti := "jti-3"
	refreshTok, err := deps.signer.SignRefreshToken(user.ID, jti, deps.cfg.RefreshTTL)
	if err != nil {
		t.Fatalf("sign refresh: %v", err)
	}
	if err := deps.refresh.Create(context.Background(), &domain.RefreshToken{
		UserID:           user.ID,
		RefreshTokenHash: hashToken(jti),
		ExpiresAt:        time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create initial refresh session: %v", err)
	}
	deps.refresh.createErr = errors.New("db insert failed")

	if _, err := svc.Refresh(context.Background(), "trace", refreshTok); err == nil {
		t.Fatalf("expected refresh failure when persisting new session fails")
	}
}

func TestRevokeRefreshToken(t *testing.T) {
	svc, deps := newTestService(t)
	user := &domain.AuthUser{ID: "user-1", Email: "user@example.com"}
	_ = deps.users.Create(context.Background(), user)
	jti := "jti-4"
	refreshTok, err := deps.signer.SignRefreshToken(user.ID, jti, deps.cfg.RefreshTTL)
	if err != nil {
		t.Fatalf("sign refresh: %v", err)
	}
	if err := deps.refresh.Create(context.Background(), &domain.RefreshToken{
		UserID:           user.ID,
		RefreshTokenHash: hashToken(jti),
		ExpiresAt:        time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create initial refresh session: %v", err)
	}

	if err := svc.RevokeRefreshToken(context.Background(), "trace", refreshTok); err != nil {
		t.Fatalf("revoke refresh token: %v", err)
	}

	session, ok := deps.refresh.tokens[hashToken(jti)]
	if !ok {
		t.Fatalf("session not found")
	}
	if session.RevokedAt == nil {
		t.Fatalf("session should be revoked")
	}
}

func TestEmailChangeFlow(t *testing.T) {
	svc, deps := newTestService(t)
	user := &domain.AuthUser{ID: "user-1", Email: "user@example.com"}
	_ = deps.users.Create(context.Background(), user)

	uuid, err := svc.StartEmailChange(context.Background(), "trace", user.ID, "NewEmail@example.com")
	if err != nil {
		t.Fatalf("start email change: %v", err)
	}
	if uuid != deps.tara.emailChangeUUID {
		t.Fatalf("unexpected uuid: %s", uuid)
	}
	if deps.tara.lastEmailChangeEmail != "newemail@example.com" {
		t.Fatalf("email not normalized: %s", deps.tara.lastEmailChangeEmail)
	}

	updated, err := svc.VerifyEmailChange(context.Background(), "trace", "code")
	if err != nil {
		t.Fatalf("verify email change: %v", err)
	}
	if updated.Email != "new@example.com" {
		t.Fatalf("email not updated: %s", updated.Email)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	svc, deps := newTestService(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpass123"), bcrypt.DefaultCost)
	_ = deps.users.Create(context.Background(), &domain.AuthUser{Email: "user@example.com", PasswordHash: stringPtr(string(hash))})

	uuid, err := svc.StartPasswordReset(context.Background(), "trace", "user@example.com")
	if err != nil {
		t.Fatalf("start password reset: %v", err)
	}
	if uuid != deps.tara.passwordResetUUID {
		t.Fatalf("unexpected uuid: %s", uuid)
	}
	if deps.tara.lastPasswordReset != "user@example.com" {
		t.Fatalf("email not normalized: %s", deps.tara.lastPasswordReset)
	}

	if err := svc.FinishPasswordReset(context.Background(), "trace", "user@example.com", "code", "newpass123"); err != nil {
		t.Fatalf("finish password reset: %v", err)
	}
	updated, _ := deps.users.FindByEmail(context.Background(), "user@example.com")
	if updated.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*updated.PasswordHash), []byte("newpass123")) != nil {
		t.Fatalf("password was not updated")
	}
}

func hashToken(jti string) string {
	return fmt.Sprintf("rt:%s", jti)
}

func stringPtr(value string) *string { return &value }
