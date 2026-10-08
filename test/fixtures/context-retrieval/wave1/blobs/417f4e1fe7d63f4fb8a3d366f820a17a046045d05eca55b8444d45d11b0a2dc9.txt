package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	pkglog "github.com/example/auth-service/pkg/log"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var errT16Failure = errors.New("injected dependency failure")

// Shared fake storage survives service reconstruction; no production fallback
// uses it. Mutex/CAS behavior allows concurrent completion assertions.
type t16Store struct {
	mu                                                  sync.Mutex
	ops                                                 map[string]*domain.SignupCompletion
	users                                               map[string]*domain.AuthUser
	beginErr, storeErr, ensureAfterErr, casErr, readErr error
}

func newT16Store() *t16Store {
	return &t16Store{ops: map[string]*domain.SignupCompletion{}, users: map[string]*domain.AuthUser{}}
}
func (r *t16Store) Create(_ context.Context, u *domain.AuthUser) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[u.ID] = u
	return nil
}
func (r *t16Store) Update(ctx context.Context, u *domain.AuthUser) error { return r.Create(ctx, u) }
func (r *t16Store) FindByID(_ context.Context, id string) (*domain.AuthUser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	v := *u
	return &v, nil
}
func (r *t16Store) FindByEmail(_ context.Context, email string) (*domain.AuthUser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Email == email {
			v := *u
			return &v, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *t16Store) BeginSignupCompletion(_ context.Context, email, proof, id, principal string) (*domain.SignupCompletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	for _, op := range r.ops {
		if op.Email == email && op.ProofFingerprint == proof {
			v := *op
			return &v, nil
		}
	}
	for _, u := range r.users {
		if u.Email == email {
			return nil, errT16Failure
		}
	}
	op := &domain.SignupCompletion{OperationID: id, PrincipalID: principal, Email: email, ProofFingerprint: proof, State: domain.SignupPending}
	r.ops[id] = op
	v := *op
	return &v, nil
}
func (r *t16Store) StoreSignupReceipt(_ context.Context, id string, p *domain.SignupProofReceipt) (*domain.SignupCompletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.storeErr != nil {
		return nil, r.storeErr
	}
	op := r.ops[id]
	if op == nil || op.State == domain.SignupCompleted || p.OperationID != id || p.Email != op.Email || !p.ExpiresAt.After(time.Now()) {
		return nil, errT16Failure
	}
	if op.State == domain.SignupVerified && (p.PasswordHash != op.PasswordHash || op.ReceiptExpiresAt == nil || !p.ExpiresAt.Equal(*op.ReceiptExpiresAt)) {
		return nil, errT16Failure
	}
	op.PasswordHash = p.PasswordHash
	expiry := p.ExpiresAt
	op.ReceiptExpiresAt = &expiry
	op.State = domain.SignupVerified
	v := *op
	return &v, nil
}
func (r *t16Store) EnsureSignupPrincipal(_ context.Context, id string) (*domain.AuthUser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op := r.ops[id]
	if op == nil || op.State != domain.SignupVerified {
		return nil, errT16Failure
	}
	var user *domain.AuthUser
	for _, u := range r.users {
		if u.Email == op.Email || u.ID == op.PrincipalID {
			user = u
			break
		}
	}
	if user != nil {
		if !op.PrincipalCreated || !signupPrincipalMatches(op, user) {
			return nil, errT16Failure
		}
	} else {
		if op.PrincipalCreated {
			return nil, errT16Failure
		}
		hash := op.PasswordHash
		user = &domain.AuthUser{ID: op.PrincipalID, Email: op.Email, PasswordHash: &hash}
		r.users[user.ID] = user
		op.PrincipalCreated = true
	}
	if r.ensureAfterErr != nil {
		return nil, r.ensureAfterErr
	}
	v := *user
	return &v, nil
}
func (r *t16Store) CompleteSignup(_ context.Context, id string, now time.Time, fresh bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.casErr != nil {
		return false, r.casErr
	}
	op := r.ops[id]
	if op == nil || op.State != domain.SignupVerified {
		return false, nil
	}
	if !op.PrincipalCreated || !signupPrincipalMatches(op, r.users[op.PrincipalID]) {
		return false, errT16Failure
	}
	if fresh && (op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.After(now)) {
		return false, nil
	}
	op.State = domain.SignupCompleted
	return true, nil
}
func (r *t16Store) SignupPrincipalPending(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return false, r.readErr
	}
	for _, op := range r.ops {
		if op.PrincipalID == id {
			return op.State != domain.SignupCompleted, nil
		}
	}
	return false, nil
}
func (r *t16Store) FindSignupCompletionByPrincipal(_ context.Context, id string) (*domain.SignupCompletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return nil, r.readErr
	}
	for _, op := range r.ops {
		if op.PrincipalID == id {
			v := *op
			return &v, nil
		}
	}
	return nil, domain.ErrNotFound
}

type t16Proof struct {
	domain.VerificationClient
	mu              sync.Mutex
	hash            string
	receipt         *domain.SignupProofReceipt
	calls, consumed int
	loseResponse    bool
	alter           func(*domain.SignupProofReceipt)
}

func (p *t16Proof) ConsumeSignupProof(_ context.Context, email, code, id string) (*domain.SignupProofReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.receipt == nil {
		p.receipt = &domain.SignupProofReceipt{OperationID: id, Email: email, PasswordHash: p.hash, ExpiresAt: time.Now().UTC().Add(time.Hour)}
		p.consumed++
	}
	if p.receipt.OperationID != id || p.receipt.Email != email {
		return nil, errT16Failure
	}
	if p.loseResponse {
		p.loseResponse = false
		return nil, errT16Failure
	}
	v := *p.receipt
	if p.alter != nil {
		p.alter(&v)
	}
	return &v, nil
}

type t16UserProvider struct {
	mu    sync.Mutex
	err   error
	calls int
	ids   map[string]bool
}

func (p *t16UserProvider) CreateUser(_ context.Context, req domain.UserProvisionRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return p.err
	}
	p.ids[req.ID] = true
	return nil
}

type t16Roles struct {
	mu  sync.Mutex
	err error
	ids map[string]bool
}

// Unit-only signup port double; the real adapter signs the persisted binding.
func (p *t16Roles) AssignSignupRole(ctx context.Context, id, operationID string) error {
	if operationID == "" {
		return errT16Failure
	}
	return p.AssignRole(ctx, id, signupRole)
}

func (p *t16Roles) AssignRole(_ context.Context, id, role string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if role != signupRole {
		return errT16Failure
	}
	p.ids[id] = true
	return nil
}
func (p *t16Roles) CheckRole(_ context.Context, id, role string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return false, p.err
	}
	return role == signupRole && p.ids[id], nil
}

type t16Sessions struct {
	domain.RefreshTokenRepository
	mu    sync.Mutex
	count int
	err   error
}

func (p *t16Sessions) Create(_ context.Context, _ *domain.RefreshToken) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.count++
	return nil
}

type t16Signer struct {
	JWTSigner
	mu       sync.Mutex
	attempts int
	err      error
}

func (s *t16Signer) SignAccessToken(sub string, c map[string]interface{}, ttl time.Duration) (string, error) {
	s.mu.Lock()
	s.attempts++
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.JWTSigner.SignAccessToken(sub, c, ttl)
}
func (s *t16Signer) Parse(token string) (*jwt.Token, jwt.MapClaims, error) {
	return s.JWTSigner.Parse(token)
}

type t16Fixture struct {
	store    *t16Store
	proof    *t16Proof
	users    *t16UserProvider
	roles    *t16Roles
	sessions *t16Sessions
	signer   *t16Signer
	cfg      *config.Config
}

func newT16(t *testing.T) *t16Fixture {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("t16-private-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{JWTSecret: "t16-test-only", JWTIssuer: "auth", JWTAudience: "test", AccessTTL: time.Minute, RefreshTTL: time.Hour}
	signer, err := NewJWTSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &t16Fixture{store: newT16Store(), proof: &t16Proof{hash: string(hash)}, users: &t16UserProvider{ids: map[string]bool{}}, roles: &t16Roles{ids: map[string]bool{}}, sessions: &t16Sessions{}, signer: &t16Signer{JWTSigner: signer}, cfg: cfg}
}
func (f *t16Fixture) service() *authService {
	return NewAuthService(f.cfg, pkglog.New("test"), f.store, nil, nil, nil, f.sessions, f.proof, f.users, f.roles, f.signer).(*authService)
}
func (f *t16Fixture) verify() (*domain.AuthUser, *Tokens, error) {
	return f.service().VerifySignup(context.Background(), "t16", "t16@example.test", "private-test-proof")
}
func t16NoTokens(t *testing.T, f *t16Fixture, tokens *Tokens, err error) {
	t.Helper()
	if err == nil || tokens != nil || f.signer.attempts != 0 || f.sessions.count != 0 {
		t.Fatal("incomplete signup attempted tokens")
	}
}

func TestT16RecoverLostConsumeResponse(t *testing.T) {
	f := newT16(t)
	f.proof.loseResponse = true
	_, tokens, err := f.verify()
	t16NoTokens(t, f, tokens, err)
	if len(f.store.ops) != 1 || len(f.store.users) != 0 {
		t.Fatal("operation must precede consumption and principal must follow durable receipt")
	}
	u, tokens, err := f.verify()
	if err != nil || u == nil || tokens == nil || f.proof.consumed != 1 || f.proof.calls != 2 {
		t.Fatal("same operation receipt did not recover lost response")
	}
}
func TestT16PreconsumePersistenceFailure(t *testing.T) {
	f := newT16(t)
	f.store.beginErr = errT16Failure
	_, tokens, err := f.verify()
	t16NoTokens(t, f, tokens, err)
	if f.proof.calls != 0 {
		t.Fatal("consumed proof before durable operation")
	}
}
func TestT16RecoveryPersistenceFailures(t *testing.T) {
	for _, phase := range []string{"receipt", "principal-committed-response-lost", "terminal-cas"} {
		t.Run(phase, func(t *testing.T) {
			f := newT16(t)
			switch phase {
			case "receipt":
				f.store.storeErr = errT16Failure
			case "principal-committed-response-lost":
				f.store.ensureAfterErr = errT16Failure
			case "terminal-cas":
				f.store.casErr = errT16Failure
			}
			_, tokens, err := f.verify()
			t16NoTokens(t, f, tokens, err)
			var principal string
			for _, op := range f.store.ops {
				principal = op.PrincipalID
			}
			f.store.storeErr = nil
			f.store.ensureAfterErr = nil
			f.store.casErr = nil
			u, tokens, err := f.verify()
			if err != nil || u == nil || u.ID != principal || tokens == nil || f.proof.consumed != 1 || len(f.store.users) != 1 {
				t.Fatal("durable retry failed or replaced principal")
			}
		})
	}
}
func TestT16RejectReceiptMismatch(t *testing.T) {
	cases := map[string]func(*domain.SignupProofReceipt){"operation": func(p *domain.SignupProofReceipt) { p.OperationID = "other" }, "email": func(p *domain.SignupProofReceipt) { p.Email = "other@example.test" }, "expiry": func(p *domain.SignupProofReceipt) { p.ExpiresAt = time.Now().Add(-time.Second) }, "credential": func(p *domain.SignupProofReceipt) { p.PasswordHash = "invalid" }}
	for name, alter := range cases {
		t.Run(name, func(t *testing.T) {
			f := newT16(t)
			f.proof.alter = alter
			_, tokens, err := f.verify()
			t16NoTokens(t, f, tokens, err)
			if len(f.store.users) != 0 {
				t.Fatal("mismatched receipt created principal")
			}
		})
	}
}
func TestT16PendingGateAndPasswordRepairAfterExpiry(t *testing.T) {
	f := newT16(t)
	f.users.err = errT16Failure
	_, tokens, err := f.verify()
	t16NoTokens(t, f, tokens, err)
	var u *domain.AuthUser
	for _, v := range f.store.users {
		u = v
	}
	if u == nil {
		t.Fatal("missing verified principal")
		return
	}
	tokens, err = f.service().issueTokens(context.Background(), u)
	t16NoTokens(t, f, tokens, err)
	_, tokens, err = f.service().SignIn(context.Background(), "t16", u.Email, "t16-private-test-password")
	t16NoTokens(t, f, tokens, err)
	for _, op := range f.store.ops {
		past := time.Now().Add(-time.Minute)
		op.ReceiptExpiresAt = &past
	}
	f.users.err = nil
	_, tokens, err = f.verify()
	t16NoTokens(t, f, tokens, err)
	_, tokens, err = f.service().SignIn(context.Background(), "t16", u.Email, "t16-private-test-password")
	if err != nil || tokens == nil || f.sessions.count != 1 {
		t.Fatal("password proof must repair expired verified completion")
	}
	_, tokens, err = f.verify()
	if err == nil || tokens != nil || f.signer.attempts != 1 {
		t.Fatal("terminal proof replay attempted another token")
	}
}
func TestT16PendingCredentialMutationDenied(t *testing.T) {
	f := newT16(t)
	f.users.err = errT16Failure
	_, tokens, err := f.verify()
	t16NoTokens(t, f, tokens, err)
	hash, _ := bcrypt.GenerateFromPassword([]byte("replacement-password"), bcrypt.MinCost)
	for _, u := range f.store.users {
		u.PasswordHash = ptr(string(hash))
	}
	f.users.err = nil
	_, tokens, err = f.service().SignIn(context.Background(), "t16", "t16@example.test", "replacement-password")
	t16NoTokens(t, f, tokens, err)
}
func TestT16TerminalBeforeTokenAttempt(t *testing.T) {
	for _, phase := range []string{"signer", "refresh-persistence"} {
		t.Run(phase, func(t *testing.T) {
			f := newT16(t)
			if phase == "signer" {
				f.signer.err = errT16Failure
			} else {
				f.sessions.err = errT16Failure
			}
			_, tokens, err := f.verify()
			if err == nil || tokens != nil || f.signer.attempts != 1 {
				t.Fatal("expected exactly one failed token attempt")
			}
			for _, op := range f.store.ops {
				if op.State != domain.SignupCompleted {
					t.Fatal("terminal CAS must precede token attempt")
				}
			}
			_, tokens, err = f.verify()
			if err == nil || tokens != nil || f.signer.attempts != 1 {
				t.Fatal("replayed terminal code after token failure")
			}
			f.signer.err = nil
			f.sessions.err = nil
			_, tokens, err = f.service().SignIn(context.Background(), "t16", "t16@example.test", "t16-private-test-password")
			if err != nil || tokens == nil {
				t.Fatal("completed actor ordinary password signin unavailable")
			}
		})
	}
}
func TestT16ConcurrentCompletionOneTokenAttempt(t *testing.T) {
	f := newT16(t)
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, tokens, err := f.verify()
			if err == nil && tokens != nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 || f.signer.attempts != 1 || f.sessions.count != 1 || len(f.store.users) != 1 || f.proof.consumed != 1 {
		t.Fatal("concurrent completion did not own exactly one token attempt")
	}
}
func TestT16IssuanceGatePersistenceError(t *testing.T) {
	f := newT16(t)
	hash := f.proof.hash
	u := &domain.AuthUser{ID: "legacy", Email: "legacy@example.test", PasswordHash: &hash}
	f.store.readErr = errT16Failure
	tokens, err := f.service().issueTokens(context.Background(), u)
	t16NoTokens(t, f, tokens, err)
	f.store.readErr = nil
	tokens, err = f.service().issueTokens(context.Background(), u)
	if err != nil || tokens == nil {
		t.Fatal("legacy account without signup operation must remain usable")
	}
}
func TestT16NoProductionFallbackPort(t *testing.T) {
	f := newT16(t)
	type legacyUsers struct{ domain.AuthUserRepository }
	svc := NewAuthService(f.cfg, pkglog.New("test"), legacyUsers{f.store}, nil, nil, nil, f.sessions, f.proof, f.users, f.roles, f.signer)
	_, tokens, err := svc.VerifySignup(context.Background(), "t16", "t16@example.test", "proof")
	t16NoTokens(t, f, tokens, err)
	if f.proof.calls != 0 {
		t.Fatal("missing persistence fell back to destructive proof verification")
	}
}
func TestT16FingerprintEncoding(t *testing.T) {
	if signupProofFingerprint("ab", "c") == signupProofFingerprint("a", "bc") {
		t.Fatal("ambiguous proof fingerprint encoding")
	}
}

func TestT16EquivalentWhitespaceProofRecoversSameOperation(t *testing.T) {
	f := newT16(t)
	f.users.err = errT16Failure
	_, tokens, err := f.service().VerifySignup(context.Background(), "t16", " T16@Example.Test ", "  private-test-proof  ")
	t16NoTokens(t, f, tokens, err)
	var principal string
	for _, op := range f.store.ops {
		principal = op.PrincipalID
	}
	f.users.err = nil
	u, tokens, err := f.verify()
	if err != nil || u == nil || u.ID != principal || tokens == nil || len(f.store.ops) != 1 || f.proof.consumed != 1 {
		t.Fatal("equivalent normalized proof created a different recovery operation")
	}
}
