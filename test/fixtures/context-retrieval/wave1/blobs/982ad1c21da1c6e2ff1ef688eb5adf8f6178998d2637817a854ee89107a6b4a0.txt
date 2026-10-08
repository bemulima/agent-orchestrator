//go:build integration

package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	natsadapter "github.com/example/auth-service/internal/infrastructure/messaging/nats"
	repo "github.com/example/auth-service/internal/infrastructure/persistence/postgres"
	"github.com/example/auth-service/internal/usecase"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestT16ActualSignupRecovery exercises production Auth repositories/use cases,
// Auth-owned direct authenticated Tarantool verification, and production NATS clients
// against registered real PostgreSQL User/RBAC providers. Named local wrappers
// discard successful acknowledgements only after those real commits.
func TestT16ActualSignupRecovery(t *testing.T) {
	if os.Getenv("T16_COMPOSED") != "true" {
		t.Skip("requires root-owned disposable composed stores and providers")
	}
	if os.Getenv("T16_ACTUAL_NATS") != "true" {
		t.Fatal("final proof requires actual User/RBAC production NATS providers")
	}
	db := t16AuthDB(t)

	cases := []t16CaseResult{}
	run := func(name string, test func(*testing.T)) {
		passed := t.Run(name, test)
		cases = append(cases, t16CaseResult{Name: name, Passed: passed})
	}
	t.Cleanup(func() { t16WriteChainResult(t, db, cases) })
	proof, fixture := t16DirectVerification(t)
	natsURL := os.Getenv("NATS_URL")
	parsed, err := url.Parse(natsURL)
	if err != nil || parsed.Scheme != "nats" || !t16Loopback(parsed.Hostname()) {
		t.Fatal("dedicated loopback NATS_URL required")
	}
	conn, err := nats.Connect(natsURL, nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		t.Fatal("connect dedicated real NATS broker")
	}
	t.Cleanup(conn.Close)
	userClient := natsadapter.NewUserClient(conn, t16Env("T16_USER_CREATE_SUBJECT", "user.create-user"))
	roles, err := natsadapter.NewRBACClientWithSignupProof(conn, t16Env("T16_RBAC_ASSIGN_SUBJECT", "rbac.assign-role"), t16Env("T16_RBAC_CHECK_SUBJECT", "rbac.checkRole"), os.Getenv("AUTH_RBAC_SIGNUP_PRIVATE_KEY"))
	if err != nil {
		t.Fatal("construct dedicated signup proof signer")
	}
	userControl := t16ProvisioningURL(t, "T16_USER_READY_FILE")
	roleControl := t16ProvisioningURL(t, "T16_RBAC_READY_FILE")
	t.Log("classification=actual-Auth-PG-usecase-production-JWT_Auth-owned-direct-Tarantool-authenticated-store_actual-production-NATS-User-and-RBAC-PG-providers; fault wrappers=named-local-after-real-commit-or-ACK")

	for _, cut := range []string{"proof", "principal", "user", "rbac"} {
		run("reconstruct_after_"+cut+"_commit", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			email, password := t16Email(t, cut), "T16-disposable-password-41"
			signer := t16Signer(t)
			authRepo := repo.NewAuthUserRepository(db)
			completionRepo, ok := authRepo.(domain.SignupCompletionRepository)
			if !ok {
				t.Fatal("production Auth repository lacks durable completion port")
			}
			originalRepo := &t16AfterPrincipalCommit{AuthUserRepository: authRepo, SignupCompletionRepository: completionRepo, fail: cut == "principal"}
			originalProof := &t16AfterProofCommit{VerificationClient: proof, SignupProofConsumer: proof.(domain.SignupProofConsumer), fail: cut == "proof"}
			originalUser := &t16AfterUserACK{UserProvisioner: userClient, fail: cut == "user"}
			originalRoles := &t16AfterRoleACK{RoleClient: roles, fail: cut == "rbac"}
			first := t16Service(t, db, originalRepo, originalProof, originalUser, originalRoles, signer)
			if first.StartSignup(ctx, "t16", email, password) != nil {
				t.Fatal("actual signup start failed")
			}
			code := t16CapturedCode(t, fixture, email)
			if _, tokens, err := first.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
				t.Fatal("named after-commit loss must prevent token response")
			}
			t16SessionCount(t, db, email, 0)
			if signer.attempts() != 0 {
				t.Fatal("pending actor reached production signer")
			}
			stage := "verified"
			if cut == "proof" {
				stage = "pending"
			}
			operation, principal := t16Completion(t, db, email, code, stage, cut != "proof")
			var consume struct {
				SignupExists bool `json:"signup_exists"`
				ReceiptCount int  `json:"receipt_count"`
			}
			consume.SignupExists, consume.ReceiptCount = fixture.inspect(t, email)
			if consume.SignupExists || consume.ReceiptCount != 1 {
				t.Fatal("actual proof commit must leave one receipt and no live proof")
			}
			wantUser, wantRole := 0, 0
			if cut == "user" || cut == "rbac" {
				wantUser = 1
			}
			if cut == "rbac" {
				wantRole = 1
			}
			t16ProviderCounts(t, userControl, roleControl, principal, wantUser, wantRole)

			// These probes use the actual protected production proof consumer; neither
			// operation nor email equality alone permits a retained receipt replay.
			wrongOp, _ := usecase.GenerateJTI()
			consumer := proof.(domain.SignupProofConsumer)
			if receipt, err := consumer.ConsumeSignupProof(ctx, email, code, wrongOp); err == nil || receipt != nil {
				t.Fatal("receipt accepted a different operation")
			}
			if receipt, err := consumer.ConsumeSignupProof(ctx, t16Email(t, cut+"-foreign"), code, operation); err == nil || receipt != nil {
				t.Fatal("receipt accepted another email")
			}
			if receipt, err := consumer.ConsumeSignupProof(ctx, email, "00000000-wrong", operation); err == nil || receipt != nil {
				t.Fatal("receipt accepted a different code")
			}
			pending := t16Service(t, db, nil, proof, &t16NamedUserOutage{fail: true}, roles, signer)
			if _, tokens, err := pending.SignIn(ctx, "t16", email, password); err == nil || tokens != nil {
				t.Fatal("pending password signin issued before User/RBAC readiness")
			}
			t16SessionCount(t, db, email, 0)
			if signer.attempts() != 0 {
				t.Fatal("pending signin reached signer")
			}
			// New production repositories and use case resume from committed storage.
			retry := t16Service(t, db, nil, proof, userClient, roles, signer)
			user, tokens, err := retry.VerifySignup(ctx, "t16", email, code)
			if err != nil || user == nil || tokens == nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
				t.Fatal("same-operation retry did not recover actual chain")
			}
			if user.ID != principal {
				t.Fatal("recovery changed reserved canonical principal")
			}
			t16Completion(t, db, email, code, "completed", true)
			t16ProviderCounts(t, userControl, roleControl, principal, 1, 1)
			t16SessionCount(t, db, email, 1)
			if signer.attempts() != 1 {
				t.Fatal("completion must own exactly one token issuance attempt")
			}
			if _, tokens, err := retry.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
				t.Fatal("terminal code replay issued tokens")
			}
			t16SessionCount(t, db, email, 1)
			signedIn, tokens, err := t16Service(t, db, nil, proof, userClient, roles, signer).SignIn(ctx, "t16", email, password)
			if err != nil || signedIn == nil || signedIn.ID != principal || tokens == nil {
				t.Fatal("legitimate completed password signin failed")
			}
			t16SessionCount(t, db, email, 2)
			t16ProviderCounts(t, userControl, roleControl, principal, 1, 1)
			t.Log("actual receipt/Auth/User/RBAC readback converged; no sessions before completion; terminal code denied; legitimate password signin allowed")
		})
	}

	run("direct_committed_ACK_loss_new_connection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		email := t16Email(t, "direct-cut")
		lostProof, _ := t16DirectVerification(t)
		cut := &t16AfterProofCommit{VerificationClient: lostProof, SignupProofConsumer: lostProof.(domain.SignupProofConsumer), fail: true}
		signer := t16Signer(t)
		first := t16Service(t, db, nil, cut, userClient, roles, signer)
		if first.StartSignup(ctx, "t16", email, "T16-disposable-password-41") != nil {
			t.Fatal("direct cut signup start")
		}
		code := t16CapturedCode(t, fixture, email)
		if _, tokens, err := first.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("discarded direct committed ACK must prevent issuance")
		}
		if live, receipts := fixture.inspect(t, email); live || receipts != 1 {
			t.Fatal("direct proof commit must leave exactly one frozen receipt")
		}
		t16SessionCount(t, db, email, 0)
		freshProof, _ := t16DirectVerification(t)
		user, tokens, err := t16Service(t, db, nil, freshProof, userClient, roles, signer).VerifySignup(ctx, "t16", email, code)
		if err != nil || user == nil || tokens == nil || signer.attempts() != 1 {
			t.Fatal("new direct storage connection did not recover frozen receipt")
		}
		t16SessionCount(t, db, email, 1)
		t16ProviderCounts(t, userControl, roleControl, user.ID, 1, 1)
	})

	run("concurrent_same_proof_one_CAS", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		email := t16Email(t, "concurrent")
		signer := t16Signer(t)
		pending := t16Service(t, db, nil, proof, &t16NamedUserOutage{fail: true}, roles, signer)
		if pending.StartSignup(ctx, "t16", email, "T16-disposable-password-41") != nil {
			t.Fatal("concurrent signup start")
		}
		code := t16CapturedCode(t, fixture, email)
		if _, tokens, err := pending.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("concurrent pending setup")
		}
		t16SessionCount(t, db, email, 0)
		_, principal := t16Completion(t, db, email, code, "verified", true)
		var wg sync.WaitGroup
		var successes atomic.Int32
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				user, tokens, err := t16Service(t, db, nil, proof, userClient, roles, signer).VerifySignup(ctx, "t16", email, code)
				if err == nil && user != nil && tokens != nil && user.ID == principal {
					successes.Add(1)
				}
			}()
		}
		wg.Wait()
		if successes.Load() != 1 || signer.attempts() != 1 {
			t.Fatal("concurrent completions did not elect exactly one issuance owner")
		}
		t16SessionCount(t, db, email, 1)
		t16Completion(t, db, email, code, "completed", true)
		t16ProviderCounts(t, userControl, roleControl, principal, 1, 1)
	})

	run("legitimate_password_repairs_pending", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		email, password := t16Email(t, "password-repair"), "T16-disposable-password-41"
		signer := t16Signer(t)
		pending := t16Service(t, db, nil, proof, &t16NamedUserOutage{fail: true}, roles, signer)
		if pending.StartSignup(ctx, "t16", email, password) != nil {
			t.Fatal("password repair signup start")
		}
		code := t16CapturedCode(t, fixture, email)
		if _, tokens, err := pending.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("password repair outage setup")
		}
		_, principal := t16Completion(t, db, email, code, "verified", true)
		if _, tokens, err := pending.SignIn(ctx, "t16", email, password); err == nil || tokens != nil {
			t.Fatal("password signin bypassed actual pending dependency")
		}
		t16SessionCount(t, db, email, 0)
		user, tokens, err := t16Service(t, db, nil, proof, userClient, roles, signer).SignIn(ctx, "t16", email, password)
		if err != nil || user == nil || user.ID != principal || tokens == nil {
			t.Fatal("legitimate password did not repair pending real actor")
		}
		t16Completion(t, db, email, code, "completed", true)
		t16SessionCount(t, db, email, 1)
		t16ProviderCounts(t, userControl, roleControl, principal, 1, 1)
		if signer.attempts() != 1 {
			t.Fatal("password repair issued before completion or more than once")
		}
		if _, tokens, err := t16Service(t, db, nil, proof, userClient, roles, signer).VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("password repair re-enabled terminal code")
		}
	})

	run("existing_account_takeover_denied", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		email := t16Email(t, "takeover")
		signer := t16Signer(t)
		svc := t16Service(t, db, nil, proof, userClient, roles, signer)
		if svc.StartSignup(ctx, "t16", email, "T16-disposable-password-41") != nil {
			t.Fatal("takeover signup start")
		}
		code := t16CapturedCode(t, fixture, email)
		reservation := t16Service(t, db, nil, &t16AfterProofCommit{VerificationClient: proof, SignupProofConsumer: proof.(domain.SignupProofConsumer), fail: true}, userClient, roles, signer)
		if _, tokens, err := reservation.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("reserve actual receipt before competing account creation")
		}
		t16Completion(t, db, email, code, "pending", false)
		hash, err := bcrypt.GenerateFromPassword([]byte("T16-existing-account-password-82"), bcrypt.DefaultCost)
		if err != nil {
			t.Fatal("prepare legitimate existing account credential")
		}
		stored := string(hash)
		existing := &domain.AuthUser{Email: email, PasswordHash: &stored}
		if repo.NewAuthUserRepository(db).Create(ctx, existing) != nil {
			t.Fatal("create actual competing existing account")
		}
		if _, tokens, err := svc.VerifySignup(ctx, "t16", email, code); err == nil || tokens != nil {
			t.Fatal("signup proof authenticated competing existing account")
		}
		preserved, err := repo.NewAuthUserRepository(db).FindByEmail(ctx, email)
		if err != nil || preserved.ID != existing.ID || preserved.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*preserved.PasswordHash), []byte("T16-existing-account-password-82")) != nil {
			t.Fatal("competing account identity or credential changed")
		}
		if signer.attempts() != 0 {
			t.Fatal("takeover attempt reached signer")
		}
		t16SessionCount(t, db, email, 0)
		t16ProviderCounts(t, userControl, roleControl, existing.ID, 0, 0)
	})
}

func t16AuthDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AUTH_TEST_DATABASE_URL")
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatal("AUTH_TEST_DATABASE_URL must target loopback owned database ending _test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open owned Auth test database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("obtain Auth database handle")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var name string
	if err := db.Raw("SELECT current_database()").Scan(&name).Error; err != nil || !strings.HasSuffix(name, "_test") {
		t.Fatal("actual connected database must end _test")
	}
	return db
}

func t16Email(t *testing.T, label string) string {
	t.Helper()
	prefix := os.Getenv("T16_RUN_PREFIX")
	if prefix == "" || strings.ContainsAny(prefix, " @/\\") {
		t.Fatal("unique safe T16_RUN_PREFIX required")
	}
	return prefix + "-" + label + "@example.test"
}

func t16SessionCount(t *testing.T, db *gorm.DB, email string, want int64) {
	t.Helper()
	var count int64
	err := db.Table("auth_refresh_token").Joins("JOIN auth_user ON auth_user.id = auth_refresh_token.user_id").Where("auth_user.email = ?", email).Count(&count).Error
	if err != nil || count != want {
		t.Fatalf("actual refresh session count=%d want=%d", count, want)
	}
}

type t16NamedUserOutage struct{ fail bool }

func (f *t16NamedUserOutage) CreateUser(context.Context, domain.UserProvisionRequest) error {
	if f.fail {
		return errors.New("t16-named-User-outage-fixture")
	}
	return nil
}

func t16Loopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
func t16Env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func t16Service(t *testing.T, db *gorm.DB, users domain.AuthUserRepository, proof domain.VerificationClient, userClient domain.UserProvisioner, roles domain.RoleClient, signer usecase.JWTSigner) usecase.Service {
	t.Helper()
	if users == nil {
		users = repo.NewAuthUserRepository(db)
	}
	cfg := &config.Config{JWTSecret: "disposable-t16-test-signer-only", JWTIssuer: "t16", JWTAudience: "t16", AccessTTL: time.Minute, RefreshTTL: time.Hour}
	return usecase.NewAuthService(cfg, zerolog.New(io.Discard), users, repo.NewAuthIdentityRepository(db), repo.NewOAuthTransactionRepository(db), nil, repo.NewRefreshTokenRepository(db), proof, userClient, roles, signer)
}

func t16Completion(t *testing.T, db *gorm.DB, email, _, wantState string, wantPrincipal bool) (string, string) {
	t.Helper()
	var row struct {
		OperationID string
		PrincipalID string
		State       string
	}
	result := db.Table("auth_signup_completion").Select("operation_id,principal_id,state").Where("email = ?", email).Scan(&row)
	if result.Error != nil || result.RowsAffected != 1 || row.OperationID == "" || row.PrincipalID == "" {
		t.Fatal("one durable owned signup operation absent")
	}
	if wantState != "" && row.State != wantState {
		t.Fatalf("durable signup state=%s want=%s", row.State, wantState)
	}
	var count int64
	if db.Model(&domain.AuthUser{}).Where("email = ?", email).Count(&count).Error != nil {
		t.Fatal("read actual Auth principal count")
	}
	expected := int64(0)
	if wantPrincipal {
		expected = 1
	}
	if wantState != "" && count != expected {
		t.Fatal("actual Auth principal count did not match completion stage")
	}
	if wantPrincipal {
		var canonical int64
		if db.Model(&domain.AuthUser{}).Where("id = ? AND email = ?", row.PrincipalID, email).Count(&canonical).Error != nil || canonical != 1 {
			t.Fatal("actual Auth principal did not match reserved operation")
		}
	}
	return row.OperationID, row.PrincipalID
}

func t16ProvisioningURL(t *testing.T, env string) string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv(env))
	if err != nil {
		t.Fatal("read actual provisioning provider ready file")
	}
	var ready struct {
		ControlURL     string `json:"control_url"`
		Classification string `json:"classification"`
	}
	if json.Unmarshal(data, &ready) != nil {
		t.Fatal("decode provisioning provider ready file")
	}
	parsed, err := url.Parse(ready.ControlURL)
	if err != nil || parsed.Scheme != "http" || !t16Loopback(parsed.Hostname()) || !strings.Contains(ready.Classification, "actual") {
		t.Fatal("real provisioning provider classification and loopback control required")
	}
	return strings.TrimRight(ready.ControlURL, "/")
}

func t16ProviderCounts(t *testing.T, userURL, roleURL, principal string, wantUser, wantRole int) {
	t.Helper()
	var user struct {
		UserCount    int `json:"user_count"`
		ProfileCount int `json:"profile_count"`
	}
	var role struct {
		Assigned bool `json:"assigned_student"`
		Count    int  `json:"assignment_count"`
	}
	t16ProviderInspect(t, userURL, principal, &user)
	t16ProviderInspect(t, roleURL, principal, &role)
	if user.UserCount != wantUser || user.ProfileCount != wantUser || role.Count != wantRole || role.Assigned != (wantRole == 1) {
		t.Fatalf("actual provider readback user=%d profile=%d role=%d assigned=%t; want user/profile=%d role=%d", user.UserCount, user.ProfileCount, role.Count, role.Assigned, wantUser, wantRole)
	}
}

func t16ProviderInspect(t *testing.T, baseURL, principal string, result interface{}) {
	t.Helper()
	token := os.Getenv("T16_PROVIDER_TOKEN")
	if len(token) < 32 {
		t.Fatal("private provisioning control token required")
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/inspect?principal_id="+url.QueryEscape(principal), nil)
	if err != nil {
		t.Fatal("construct provisioning inspect")
	}
	req.Header.Set("X-T16-Provider-Token", token)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal("actual provisioning inspect failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(result) != nil {
		t.Fatal("actual provisioning inspect did not acknowledge")
	}
}

type t16AfterProofCommit struct {
	domain.VerificationClient
	domain.SignupProofConsumer
	fail bool
}

func (w *t16AfterProofCommit) ConsumeSignupProof(ctx context.Context, email, code, operation string) (*domain.SignupProofReceipt, error) {
	receipt, err := w.SignupProofConsumer.ConsumeSignupProof(ctx, email, code, operation)
	if err == nil && w.fail {
		w.fail = false
		return nil, errors.New("t16-local-loss-after-real-proof-commit-ACK")
	}
	return receipt, err
}

type t16AfterPrincipalCommit struct {
	domain.AuthUserRepository
	domain.SignupCompletionRepository
	fail bool
}

func (w *t16AfterPrincipalCommit) EnsureSignupPrincipal(ctx context.Context, operation string) (*domain.AuthUser, error) {
	user, err := w.SignupCompletionRepository.EnsureSignupPrincipal(ctx, operation)
	if err == nil && w.fail {
		w.fail = false
		return nil, errors.New("t16-local-loss-after-real-Auth-principal-COMMIT")
	}
	return user, err
}

type t16AfterUserACK struct {
	domain.UserProvisioner
	fail bool
}

func (w *t16AfterUserACK) CreateUser(ctx context.Context, request domain.UserProvisionRequest) error {
	err := w.UserProvisioner.CreateUser(ctx, request)
	if err == nil && w.fail {
		w.fail = false
		return errors.New("t16-local-loss-after-real-User-NATS-durable-ACK")
	}
	return err
}

type t16AfterRoleACK struct {
	domain.RoleClient
	fail bool
}

func (w *t16AfterRoleACK) AssignSignupRole(ctx context.Context, principal, operationID string) error {
	provisioner, ok := w.RoleClient.(domain.SignupRoleProvisioner)
	if !ok {
		return errors.New("signup role port unavailable")
	}
	err := provisioner.AssignSignupRole(ctx, principal, operationID)
	if err == nil && w.fail {
		w.fail = false
		return errors.New("t16-local-loss-after-real-RBAC-NATS-durable-ACK")
	}
	return err
}

func (w *t16AfterRoleACK) AssignRole(ctx context.Context, principal, role string) error {
	err := w.RoleClient.AssignRole(ctx, principal, role)
	if err == nil && w.fail {
		w.fail = false
		return errors.New("t16-local-loss-after-real-RBAC-NATS-durable-ACK")
	}
	return err
}

type t16CountingSigner struct {
	usecase.JWTSigner
	accessAttempts atomic.Int32
}

func t16Signer(t *testing.T) *t16CountingSigner {
	t.Helper()
	signer, err := usecase.NewJWTSigner(&config.Config{JWTSecret: "disposable-t16-test-signer-only", JWTIssuer: "t16", JWTAudience: "t16"})
	if err != nil {
		t.Fatal("construct production JWT signer")
	}
	return &t16CountingSigner{JWTSigner: signer}
}
func (s *t16CountingSigner) SignAccessToken(subject string, claims map[string]interface{}, ttl time.Duration) (string, error) {
	s.accessAttempts.Add(1)
	return s.JWTSigner.SignAccessToken(subject, claims, ttl)
}
func (s *t16CountingSigner) attempts() int32 { return s.accessAttempts.Load() }

type t16CaseResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

func t16WriteChainResult(t *testing.T, db *gorm.DB, cases []t16CaseResult) {
	t.Helper()
	path := os.Getenv("T16_CHAIN_RESULT_FILE")
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) || filepath.Ext(path) != ".json" {
		t.Error("T16_CHAIN_RESULT_FILE must be an absolute JSON output path")
		return
	}
	var principalCount, sessionCount int64
	prefix := os.Getenv("T16_RUN_PREFIX") + "%"
	if db.Model(&domain.AuthUser{}).Where("email LIKE ?", prefix).Count(&principalCount).Error != nil {
		t.Error("read safe chain principal count")
		return
	}
	if db.Table("auth_refresh_token").Joins("JOIN auth_user ON auth_user.id = auth_refresh_token.user_id").Where("auth_user.email LIKE ?", prefix).Count(&sessionCount).Error != nil {
		t.Error("read safe chain session count")
		return
	}
	var operations []struct {
		OperationID string `json:"operation_id"`
		PrincipalID string `json:"principal_id"`
		State       string `json:"state"`
	}
	if db.Table("auth_signup_completion").Select("operation_id,principal_id,state").Where("email LIKE ?", prefix).Order("operation_id").Scan(&operations).Error != nil {
		t.Error("read safe chain operation scalars")
		return
	}
	data, err := json.MarshalIndent(struct {
		Passed         bool            `json:"passed"`
		Classification string          `json:"classification"`
		AuthEntry      string          `json:"auth_entry"`
		Faults         string          `json:"faults"`
		Cases          []t16CaseResult `json:"cases"`
		PrincipalCount int64           `json:"auth_principal_count"`
		SessionCount   int64           `json:"refresh_session_count"`
		Operations     interface{}     `json:"operations"`
	}{!t.Failed(), "actual Auth production PG repositories/JWT; Auth-owned direct Tarantool/authenticated real store; production NATS User/RBAC PostgreSQL providers", "production Auth usecase (Auth HTTP transport not exercised by this test)", "four named local losses only after actual commits/ACKs; direct committed ACK loss with a fresh authenticated storage connection", cases, principalCount, sessionCount, operations}, "", "  ")
	if err != nil {
		t.Error("encode safe chain result")
		return
	}
	if os.WriteFile(path, append(data, '\n'), 0600) != nil {
		t.Error("write safe chain result")
	}
}
