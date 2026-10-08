package unit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	oauthprovider "github.com/example/auth-service/internal/infrastructure/oauth"
	"github.com/example/auth-service/internal/usecase"
	pkglog "github.com/example/auth-service/pkg/log"
)

type fakeOAuthProvider struct {
	name         oauthprovider.ProviderName
	profile      *oauthprovider.Profile
	state        string
	verifier     string
	authVerifier string
}

func (p *fakeOAuthProvider) Name() oauthprovider.ProviderName { return p.name }
func (p *fakeOAuthProvider) Validate() error                  { return nil }
func (p *fakeOAuthProvider) AuthorizationURL(state, verifier string) string {
	p.state = state
	p.verifier = verifier
	return "https://provider.example/authorize?state=" + state
}
func (p *fakeOAuthProvider) Authenticate(_ context.Context, code, verifier string) (*oauthprovider.Profile, error) {
	if code != "valid-code" {
		return nil, errors.New("invalid code")
	}
	p.authVerifier = verifier
	return p.profile, nil
}

type memoryOAuthTransactionRepo struct {
	transaction *domain.OAuthTransaction
}

func (r *memoryOAuthTransactionRepo) Create(_ context.Context, transaction *domain.OAuthTransaction) error {
	copy := *transaction
	copy.ID = "oauth-tx-1"
	r.transaction = &copy
	return nil
}
func (r *memoryOAuthTransactionRepo) Consume(_ context.Context, stateHash, provider string, now time.Time) (*domain.OAuthTransaction, error) {
	if r.transaction == nil || r.transaction.StateHash != stateHash || r.transaction.Provider != provider || r.transaction.ConsumedAt != nil || !r.transaction.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	r.transaction.ConsumedAt = &now
	copy := *r.transaction
	return &copy, nil
}
func (r *memoryOAuthTransactionRepo) DeleteExpired(_ context.Context, _ time.Time) error { return nil }

type linkingIdentityRepo struct {
	users      *mockUserRepo
	identities map[string]*domain.AuthIdentity
}

func newLinkingIdentityRepo(users *mockUserRepo) *linkingIdentityRepo {
	return &linkingIdentityRepo{users: users, identities: make(map[string]*domain.AuthIdentity)}
}
func (r *linkingIdentityRepo) FindByProvider(_ context.Context, provider, providerUserID string) (*domain.AuthIdentity, error) {
	identity, ok := r.identities[provider+":"+providerUserID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return identity, nil
}
func (r *linkingIdentityRepo) Create(_ context.Context, identity *domain.AuthIdentity) error {
	r.identities[identity.Provider+":"+identity.ProviderUserID] = identity
	return nil
}
func (r *linkingIdentityRepo) ResolveUser(ctx context.Context, identity *domain.AuthIdentity) (*domain.AuthUser, bool, error) {
	key := identity.Provider + ":" + identity.ProviderUserID
	if existing, ok := r.identities[key]; ok {
		user, err := r.users.FindByID(ctx, existing.UserID)
		return user, false, err
	}
	user, err := r.users.FindByEmail(ctx, identity.Email)
	created := false
	if errors.Is(err, domain.ErrNotFound) {
		user = &domain.AuthUser{Email: identity.Email}
		if err := r.users.Create(ctx, user); err != nil {
			return nil, false, err
		}
		created = true
	} else if err != nil {
		return nil, false, err
	}
	copy := *identity
	copy.UserID = user.ID
	r.identities[key] = &copy
	return user, created, nil
}
func (r *linkingIdentityRepo) ListByUser(_ context.Context, userID string) ([]domain.AuthIdentity, error) {
	identities := make([]domain.AuthIdentity, 0)
	for _, identity := range r.identities {
		if identity.UserID == userID {
			identities = append(identities, *identity)
		}
	}
	return identities, nil
}
func (r *linkingIdentityRepo) Delete(_ context.Context, userID, provider, providerUserID string) error {
	key := provider + ":" + providerUserID
	identity, ok := r.identities[key]
	if !ok || identity.UserID != userID {
		return domain.ErrNotFound
	}
	delete(r.identities, key)
	return nil
}

func TestOAuthFlowLinksProvidersByVerifiedEmailAndSetsPassword(t *testing.T) {
	users := newMockUserRepo()
	identities := newLinkingIdentityRepo(users)
	refresh := newMockRefreshRepo()
	userClient := &recordingUserClient{}
	birthYear := 1998
	google := &fakeOAuthProvider{name: oauthprovider.ProviderGoogle, profile: &oauthprovider.Profile{
		ProviderUserID: "google-user", Email: "Student@Example.com", EmailVerified: true,
		FirstName: "Ada", LastName: "Lovelace", BirthYear: &birthYear, Gender: "female",
		AvatarURL: "https://lh3.googleusercontent.com/a/avatar",
	}}
	github := &fakeOAuthProvider{name: oauthprovider.ProviderGitHub, profile: &oauthprovider.Profile{
		ProviderUserID: "github-user", Email: "student@example.com", EmailVerified: true,
		AvatarURL: "https://avatars.githubusercontent.com/u/1?v=4",
	}}
	registry := oauthprovider.NewRegistry(google, github)
	cfg := &config.Config{
		JWTSecret: "test-secret", JWTIssuer: "auth", JWTAudience: "frontend",
		AccessTTL: time.Minute, RefreshTTL: time.Hour, DefaultRole: "student", OAuthTransactionTTL: 10 * time.Minute,
	}
	signer, err := usecase.NewJWTSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	startAndFinish := func(provider *fakeOAuthProvider) (*domain.AuthUser, *usecase.Tokens) {
		t.Helper()
		txRepo := &memoryOAuthTransactionRepo{}
		service := usecase.NewAuthService(cfg, pkglog.New("test"), users, identities, txRepo, registry, refresh, &mockTarantool{}, userClient, nil, signer)
		start, err := service.OAuthStart(context.Background(), "trace", string(provider.name), "/student/courses?tab=active")
		if err != nil {
			t.Fatalf("oauth start: %v", err)
		}
		if !strings.Contains(start.AuthorizationURL, provider.state) || provider.state == "" || provider.verifier == "" {
			t.Fatalf("authorization URL must contain generated state and PKCE must be generated")
		}
		if txRepo.transaction.StateHash == provider.state {
			t.Fatalf("plaintext state must not be persisted")
		}
		user, tokens, err := service.OAuthCallback(context.Background(), "trace", string(provider.name), "valid-code", provider.state)
		if err != nil {
			t.Fatalf("oauth callback: %v", err)
		}
		if provider.authVerifier != provider.verifier {
			t.Fatalf("callback did not reuse PKCE verifier")
		}
		if tokens.ReturnTo != "/student/courses?tab=active" {
			t.Fatalf("unexpected return_to: %q", tokens.ReturnTo)
		}
		if _, _, err := service.OAuthCallback(context.Background(), "trace", string(provider.name), "valid-code", provider.state); err == nil {
			t.Fatalf("oauth state replay must fail")
		}
		return user, tokens
	}

	googleUser, _ := startAndFinish(google)
	githubUser, _ := startAndFinish(github)
	if googleUser.ID != githubUser.ID {
		t.Fatalf("same verified email must resolve to one account: %s != %s", googleUser.ID, githubUser.ID)
	}
	if googleUser.PasswordHash != nil {
		t.Fatalf("oauth-only user must not have a password")
	}
	if len(identities.identities) != 2 {
		t.Fatalf("expected two linked identities, got %d", len(identities.identities))
	}
	if len(userClient.calls) != 2 {
		t.Fatalf("expected two user provisioning calls, got %d", len(userClient.calls))
	}
	googleProfile := userClient.calls[0].OAuthProfile
	if googleProfile == nil || googleProfile.Provider != "google" || googleProfile.FirstName != "Ada" || googleProfile.LastName != "Lovelace" || googleProfile.BirthYear == nil || *googleProfile.BirthYear != birthYear || googleProfile.Gender != "female" || googleProfile.AvatarURL == "" {
		t.Fatalf("google OAuth profile was not forwarded: %+v", googleProfile)
	}

	service := usecase.NewAuthService(cfg, pkglog.New("test"), users, identities, &memoryOAuthTransactionRepo{}, registry, refresh, &mockTarantool{}, nil, nil, signer)
	if err := service.RemoveIdentity(context.Background(), "trace", googleUser.ID, "github", "github-user"); err != nil {
		t.Fatalf("remove second identity: %v", err)
	}
	if err := service.RemoveIdentity(context.Background(), "trace", googleUser.ID, "google", "google-user"); err == nil {
		t.Fatalf("oauth-only user must not be allowed to remove the last login method")
	}
	if err := service.SetPassword(context.Background(), "trace", googleUser.ID, "newpass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if err := service.RemoveIdentity(context.Background(), "trace", googleUser.ID, "google", "google-user"); err != nil {
		t.Fatalf("password user should be allowed to remove the last identity: %v", err)
	}
	signedIn, _, err := service.SignIn(context.Background(), "trace", "student@example.com", "newpass123")
	if err != nil || signedIn.ID != googleUser.ID {
		t.Fatalf("password signin after OAuth setup failed: user=%v err=%v", signedIn, err)
	}
	if err := service.SetPassword(context.Background(), "trace", googleUser.ID, "anotherpass123"); err == nil {
		t.Fatalf("setting an existing password must require the change-password flow")
	}
}

func TestOAuthRejectsUnverifiedEmailAndUnsafeReturnTo(t *testing.T) {
	users := newMockUserRepo()
	provider := &fakeOAuthProvider{name: oauthprovider.ProviderGoogle, profile: &oauthprovider.Profile{
		ProviderUserID: "google-user", Email: "student@example.com", EmailVerified: false,
	}}
	txRepo := &memoryOAuthTransactionRepo{}
	cfg := &config.Config{JWTSecret: "test-secret", JWTIssuer: "auth", JWTAudience: "frontend", AccessTTL: time.Minute, RefreshTTL: time.Hour}
	signer, _ := usecase.NewJWTSigner(cfg)
	service := usecase.NewAuthService(cfg, pkglog.New("test"), users, newLinkingIdentityRepo(users), txRepo, oauthprovider.NewRegistry(provider), newMockRefreshRepo(), &mockTarantool{}, nil, nil, signer)

	if _, err := service.OAuthStart(context.Background(), "trace", "google", "https://evil.example/steal"); err == nil {
		t.Fatalf("external return_to must be rejected")
	}
	if _, err := service.OAuthStart(context.Background(), "trace", "unknown", "/student"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported provider must be rejected: %v", err)
	}
	if _, err := service.OAuthStart(context.Background(), "trace", "google", "/student"); err != nil {
		t.Fatalf("oauth start: %v", err)
	}
	if _, _, err := service.OAuthCallback(context.Background(), "trace", "google", "valid-code", provider.state); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("unverified email must be rejected: %v", err)
	}
}
