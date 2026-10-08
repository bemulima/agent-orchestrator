//go:build integration

package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/domain"
	natsadapter "github.com/example/auth-service/internal/infrastructure/messaging/nats"
	repo "github.com/example/auth-service/internal/infrastructure/persistence/postgres"
	httpadapter "github.com/example/auth-service/internal/transport/http"
	apiv1 "github.com/example/auth-service/internal/transport/http/api/v1"
	"github.com/example/auth-service/internal/transport/http/api/v1/handlers"
	authmw "github.com/example/auth-service/internal/transport/http/api/v1/middleware"
	"github.com/example/auth-service/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestT17ServeAuthHTTP composes the registered production public HTTP router,
// use case, JWT signer, PostgreSQL repositories, Auth-owned direct Tarantool verification,
// and User/RBAC Core NATS clients. The coordinator creates and migrates a NEW
// empty database before opting in, and owns lifecycle and retained data cleanup.
// This harness neither migrates nor resets stores, and never captures tokens or
// verification codes. Its only inbound fixture restricts synthetic identities
// and the six public routes needed by the BFF session proof.
func TestT17ServeAuthHTTP(t *testing.T) {
	if os.Getenv("T17_SERVE_AUTH") != "true" {
		t.Skip("root-controlled actual Auth HTTP provider disabled")
	}
	prefix := os.Getenv("T16_RUN_PREFIX")
	if !regexp.MustCompile(`^t16-t17-[a-z0-9-]+-$`).MatchString(prefix) {
		t.Fatal("T16_RUN_PREFIX must identify this owned t16-t17 run")
	}
	ready, stop := os.Getenv("T17_AUTH_READY_FILE"), os.Getenv("T17_AUTH_STOP_FILE")
	t17AuthArtifactPaths(t, ready, stop)
	jwtSecret := os.Getenv("AUTH_JWT_SECRET")
	if len(jwtSecret) < 32 {
		t.Fatal("root-generated JWT secret of at least 32 bytes required")
	}
	proof, proofFixture := t16DirectVerification(t)
	// Read actual providers' ready files before connecting their isolated broker.
	t17AuthProviderURL(t, "T16_USER_READY_FILE", "control_url", "actual-provider")
	t17AuthProviderURL(t, "T16_RBAC_READY_FILE", "control_url", "actual-provider")
	natsURL := os.Getenv("NATS_URL")
	nu, err := url.Parse(natsURL)
	if err != nil || nu.Scheme != "nats" || !t17AuthLoopback(nu.Hostname()) || nu.Port() == "" || strings.Contains(natsURL, ",") || nu.User != nil || nu.RawQuery != "" || (nu.Path != "" && nu.Path != "/") || nu.Fragment != "" {
		t.Fatal("NATS_URL must name one isolated loopback NATS broker")
	}
	db := t17AuthDatabase(t)
	conn, err := nats.Connect(natsURL, nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		t.Fatal("cannot connect actual isolated NATS broker")
	}
	defer conn.Close()
	cfg := &config.Config{
		AppName: "t17-auth-http", AppEnv: "integration", HTTPBasePath: "/api/v1",
		JWTSecret: jwtSecret, JWTIssuer: "t17-auth", JWTAudience: "t17-bff",
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, DefaultRole: "student",
		DBMigrateOnStart: false,
	}
	signer, err := usecase.NewJWTSigner(cfg)
	if err != nil {
		t.Fatal("cannot construct production JWT signer")
	}
	users := repo.NewAuthUserRepository(db)
	if _, ok := users.(domain.SignupCompletionRepository); !ok {
		t.Fatal("production Auth repository lacks durable completion port")
	}
	userClient := natsadapter.NewUserClient(conn, t17AuthSubject(t, "T16_USER_CREATE_SUBJECT", "user.create-user"))
	roles, err := natsadapter.NewRBACClientWithSignupProof(conn, t17AuthSubject(t, "T16_RBAC_ASSIGN_SUBJECT", "rbac.assign-role"), t17AuthSubject(t, "T16_RBAC_CHECK_SUBJECT", "rbac.checkRole"), os.Getenv("AUTH_RBAC_SIGNUP_PRIVATE_KEY"))
	if err != nil {
		t.Fatal("construct dedicated signup proof signer")
	}
	service := usecase.NewAuthService(cfg, zerolog.New(io.Discard), users,
		repo.NewAuthIdentityRepository(db), repo.NewOAuthTransactionRepository(db), nil,
		repo.NewRefreshTokenRepository(db), proof, userClient, roles, signer)
	e := echo.New()
	// Production request logging can include arbitrary query strings. Suppressing
	// its output keeps token/code material out of the test's evidence stream.
	e.Logger.SetOutput(io.Discard)
	httpadapter.NewRouter(cfg, apiv1.NewRouter(handlers.NewAuthHandler(service), authmw.NewAuthMiddleware(signer).Handler)).Setup(e)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot bind owned loopback Auth listener")
	}
	server := &http.Server{
		Handler: t17AuthProofCapture(t17AuthOwnedRequests(e, prefix), proofFixture), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			t.Error("Auth HTTP provider did not shut down gracefully")
		}
	}()
	file, err := os.OpenFile(ready, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("cannot exclusively create safe Auth readiness file")
	}
	err = json.NewEncoder(file).Encode(map[string]string{
		"url":            "http://" + listener.Addr().String(),
		"classification": "actual-registered-Auth-HTTP-production-usecase-PG-JWT-direct-Tarantool-CoreNATS-User-RBAC",
	})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal("cannot write safe Auth readiness metadata")
	}
	t.Log("classification=actual registered Auth public HTTP; production usecase/PG/JWT; Auth-owned direct authenticated Tarantool; production User/RBAC Core NATS clients; test-only owned-email guard")
	timer := time.NewTimer(20 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("Auth provider coordinator did not stop harness within 20 minutes")
		case <-serverErrors:
			t.Fatal("Auth provider HTTP server stopped unexpectedly")
		case <-ticker.C:
			if info, err := os.Lstat(stop); err == nil {
				if !info.Mode().IsRegular() {
					t.Fatal("Auth provider stop path must be a regular file")
				}
				return
			} else if !os.IsNotExist(err) {
				t.Fatal("cannot inspect Auth provider stop signal")
			}
		}
	}
}

func t17AuthDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AUTH_TEST_DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !t17AuthLoopback(u.Hostname()) {
		t.Fatal("AUTH_TEST_DATABASE_URL must be an explicit loopback PostgreSQL URL")
	}
	parsed, err := pgx.ParseConfig(dsn)
	name, attestation := os.Getenv("T17_AUTH_DATABASE_NAME"), os.Getenv("T16_POSTGRES_EXPECTED_SERVER_IP")
	if err != nil || !t17AuthLoopback(parsed.Host) || !regexp.MustCompile(`^(remediation_)?t17_[a-z0-9_]+_test$`).MatchString(name) || parsed.Database != name || net.ParseIP(attestation) == nil {
		t.Fatal("exact owned T17_AUTH_DATABASE_NAME ending _test and root-attested PostgreSQL server IP required")
	}
	for _, fallback := range parsed.Fallbacks {
		if !t17AuthLoopback(fallback.Host) {
			t.Fatal("non-loopback PostgreSQL fallback is forbidden")
		}
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot open owned Auth database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("cannot access Auth database connection")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var database, server string
	if err := sqlDB.QueryRowContext(ctx, "SELECT current_database(), host(inet_server_addr())").Scan(&database, &server); err != nil || !t17AuthStoreMatches(name, database, server, attestation) {
		t.Fatal("connected Auth store must equal exact owned database and attested backend IP")
	}
	var existing int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM auth_user) + (SELECT count(*) FROM auth_identity) +
		(SELECT count(*) FROM auth_refresh_token) + (SELECT count(*) FROM auth_oauth_transaction) +
		(SELECT count(*) FROM auth_signup_completion)`).Scan(&existing); err != nil {
		t.Fatal("owned Auth schema must be migrated by the coordinator")
	}
	if existing != 0 {
		t.Fatal("Auth provider requires empty owned tables; no reset is performed")
	}
	return db
}

func t17AuthStoreMatches(expectedName, database, server, attestation string) bool {
	expected, actual := net.ParseIP(attestation), net.ParseIP(server)
	return regexp.MustCompile(`^(remediation_)?t17_[a-z0-9_]+_test$`).MatchString(expectedName) && database == expectedName && expected != nil && actual != nil && actual.Equal(expected)
}

func t17AuthLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func t17AuthArtifactPaths(t *testing.T, ready, stop string) {
	t.Helper()
	if !filepath.IsAbs(ready) || !filepath.IsAbs(stop) || filepath.Clean(ready) == filepath.Clean(stop) {
		t.Fatal("distinct absolute Auth ready and stop paths required")
	}
	for _, path := range []string{ready, stop} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("Auth ready/stop paths must not exist before startup")
		}
	}
}

func t17AuthProviderURL(t *testing.T, env, field, classification string) string {
	t.Helper()
	path := os.Getenv(env)
	if !filepath.IsAbs(path) {
		t.Fatalf("%s must name an absolute actual-provider ready file", env)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read actual provider ready metadata")
	}
	var ready map[string]string
	if json.Unmarshal(data, &ready) != nil || ready["classification"] != classification {
		t.Fatal("actual production provider readiness classification required")
	}
	u, err := url.Parse(ready[field])
	if err != nil || u.Scheme != "http" || !t17AuthLoopback(u.Hostname()) || u.Port() == "" || u.User != nil || u.RawQuery != "" || (u.Path != "" && u.Path != "/") || u.Fragment != "" {
		t.Fatal("actual provider must expose a loopback HTTP origin")
	}
	return strings.TrimRight(ready[field], "/")
}

func t17AuthSubject(t *testing.T, env, fallback string) string {
	t.Helper()
	subject := os.Getenv(env)
	if subject == "" {
		subject = fallback
	}
	if strings.ContainsAny(subject, "*> \t\r\n") || strings.HasPrefix(subject, ".") || strings.HasSuffix(subject, ".") || strings.Contains(subject, "..") {
		t.Fatalf("%s must be a concrete NATS subject", env)
	}
	return subject
}

func t17AuthOwnedRequests(production http.Handler, prefix string) http.Handler {
	ownedEmail := regexp.MustCompile(`^[a-z0-9-]+@example\.test$`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/auth/me" || r.URL.Path == "/internal/health") {
			production.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/v1/auth/signup/start", "/api/v1/auth/signup/verify", "/api/v1/auth/signin":
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
			if err != nil {
				http.Error(w, "invalid payload", http.StatusBadRequest)
				return
			}
			var payload struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(body, &payload) != nil {
				http.Error(w, "invalid payload", http.StatusBadRequest)
				return
			}
			email := strings.ToLower(strings.TrimSpace(payload.Email))
			if !strings.HasPrefix(email, prefix) || !ownedEmail.MatchString(email) {
				http.Error(w, "owned synthetic identity required", http.StatusForbidden)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		case "/api/v1/auth/refresh", "/api/v1/auth/verify":
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		default:
			http.NotFound(w, r)
			return
		}
		production.ServeHTTP(w, r)
	})
}

func TestT17AuthProviderConnectedStoreGuard(t *testing.T) {
	for _, tc := range []struct {
		name, database, server, expected string
		want                             bool
	}{
		{"attested Docker backend", "t17_auth_test", "172.23.0.2", "172.23.0.2", true},
		{"attested loopback", "t17_auth_test", "127.0.0.1", "127.0.0.1", true},
		{"wrong database", "other_test", "172.23.0.2", "172.23.0.2", false},
		{"wrong backend", "t17_auth_test", "172.23.0.3", "172.23.0.2", false},
		{"unattested backend", "t17_auth_test", "172.23.0.2", "", false},
		{"invalid subnet attestation", "t17_auth_test", "172.23.0.2", "172.23.0.0/24", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := t17AuthStoreMatches("t17_auth_test", tc.database, tc.server, tc.expected); got != tc.want {
				t.Fatal("Auth connected-store guard did not match expected decision")
			}
		})
	}
}

// Test harness control only: the production Auth router has no capture route.
// A separate coordinator token and the run-owned identity guard protect code
// readback; response bodies never enter the test evidence stream.
func t17AuthProofCapture(production http.Handler, fixture *t16TarantoolFixture) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__t16/capture" {
			production.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		token := os.Getenv("T16_CONTROL_TOKEN")
		if len(token) < 32 || r.Header.Get("X-Internal-Token") != token || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		code, ok := fixture.code(r.URL.Query().Get("email"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
	})
}
