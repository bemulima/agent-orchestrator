// Package nats hosts the real provisioning provider for the opt-in T16 chain.
package nats

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	natsgo "github.com/nats-io/nats.go"

	repo "github.com/example/user-service/internal/infrastructure/persistence/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestT16SignupUserProvider is a root-coordinated provider, not a fake RPC.
// The coordinator creates and migrates a NEW owned store before enabling it.
// It retains all committed facts; the coordinator owns runtime and DB cleanup.
func TestT16SignupUserProvider(t *testing.T) {
	if os.Getenv("T16_SERVE_USER") != "true" {
		t.Skip("opt-in actual User provider requires T16_SERVE_USER=true")
	}
	dsn, natsURL, token, ready, stop := t16ProviderConfig(t, "USER_TEST_DATABASE_URL", "USER")
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot open owned User database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("cannot access User database connection")
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("cannot close owned User database connection")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var database, server string
	if err := sqlDB.QueryRowContext(ctx, "SELECT current_database(), host(inet_server_addr())").Scan(&database, &server); err != nil ||
		!t16ProviderStoreMatches(dsn, database, server, os.Getenv("T16_POSTGRES_EXPECTED_SERVER_IP")) {
		t.Fatal("connected User database must match the configured owned store and attested backend")
	}
	var existing int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM "user") + (SELECT count(*) FROM user_profile) +
		(SELECT count(*) FROM user_identity) + (SELECT count(*) FROM user_provider)`).Scan(&existing); err != nil {
		t.Fatal("owned User schema must be migrated by the coordinator")
	}
	if existing != 0 {
		t.Fatal("User provider requires empty identity/profile tables; no reset is performed")
	}

	conn, err := natsgo.Connect(natsURL, natsgo.Timeout(5*time.Second), natsgo.NoReconnect())
	if err != nil {
		t.Fatal("cannot connect isolated User NATS server")
	}
	defer conn.Close()
	handler := NewCreateUserHandler(repo.NewUserRepository(db), repo.NewUserProfileRepository(db), nil)
	if err := (Server{Conn: conn}).Subscribe(t16Subject(t, "T16_USER_CREATE_SUBJECT", "user.create-user"), "ms-go-user", handler.Handle); err != nil {
		t.Fatal("cannot register production User provisioning handler")
	}
	if err := conn.FlushTimeout(5 * time.Second); err != nil {
		t.Fatal("cannot flush User provider subscription")
	}

	control := httptest.NewServer(t16InspectHandler(token, func(ctx context.Context, id string) (any, error) {
		var result struct {
			UserCount         int64 `json:"user_count"`
			ProfileCount      int64 `json:"profile_count"`
			TotalUserCount    int64 `json:"total_user_count"`
			TotalProfileCount int64 `json:"total_profile_count"`
		}
		err := sqlDB.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM "user" WHERE id=$1),
			(SELECT count(*) FROM user_profile WHERE user_id=$1),
			(SELECT count(*) FROM "user"),
			(SELECT count(*) FROM user_profile)`, id).Scan(
			&result.UserCount, &result.ProfileCount, &result.TotalUserCount, &result.TotalProfileCount)
		return result, err
	}))
	defer control.Close()
	t16PublishReadyAndWait(t, ready, stop, control.URL)
}

func t16ProviderConfig(t *testing.T, databaseEnv, service string) (string, string, string, string, string) {
	t.Helper()
	dsn := os.Getenv(databaseEnv)
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !t16Loopback(u.Hostname()) {
		t.Fatalf("%s must be an explicit loopback PostgreSQL URL", databaseEnv)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !t16Loopback(cfg.Host) || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("%s must name an owned database ending in _test", databaseEnv)
	}
	for _, fallback := range cfg.Fallbacks {
		if !t16Loopback(fallback.Host) {
			t.Fatal("non-loopback PostgreSQL fallback is forbidden")
		}
	}
	if expected := os.Getenv("T16_POSTGRES_EXPECTED_SERVER_IP"); expected != "" && net.ParseIP(expected) == nil {
		t.Fatal("T16_POSTGRES_EXPECTED_SERVER_IP must be one exact root-attested IP address")
	}
	natsURL := os.Getenv("NATS_URL")
	nu, err := url.Parse(natsURL)
	if err != nil || nu.Scheme != "nats" || !t16Loopback(nu.Hostname()) || strings.Contains(natsURL, ",") {
		t.Fatal("NATS_URL must name one isolated loopback NATS server")
	}
	token := os.Getenv("T16_PROVIDER_TOKEN")
	if len(token) < 32 {
		t.Fatal("T16_PROVIDER_TOKEN must contain at least 32 bytes")
	}
	ready := os.Getenv("T16_" + service + "_READY_FILE")
	stop := os.Getenv("T16_" + service + "_STOP_FILE")
	if ready == "" {
		ready = os.Getenv("T16_READY_FILE")
	}
	if stop == "" {
		stop = os.Getenv("T16_STOP_FILE")
	}
	if !filepath.IsAbs(ready) || !filepath.IsAbs(stop) || ready == stop {
		t.Fatal("distinct absolute ready and stop file paths are required")
	}
	for _, path := range []string{ready, stop} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("provider ready/stop paths must not exist before startup")
		}
	}
	return dsn, natsURL, token, ready, stop
}

// The coordinator attests only its owned Docker backend's exact inspected IP.
// This does not relax the separate loopback-only client URL/fallback guards.
func t16ProviderStoreMatches(dsn, database, server, expectedServer string) bool {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") || database != cfg.Database {
		return false
	}
	expected := net.ParseIP(expectedServer)
	if expectedServer != "" && expected == nil {
		return false
	}
	actual := net.ParseIP(server)
	return actual != nil && (actual.IsLoopback() || (expected != nil && actual.Equal(expected)))
}

func TestT16ProviderConnectedStoreGuard(t *testing.T) {
	const dsn = "postgres://test@127.0.0.1:5432/owned_t16_test"
	cases := []struct {
		name, database, server, expected string
		want                             bool
	}{
		{"native loopback", "owned_t16_test", "127.0.0.1", "", true},
		{"attested Docker backend", "owned_t16_test", "172.23.0.2", "172.23.0.2", true},
		{"unattested private backend", "owned_t16_test", "172.23.0.2", "", false},
		{"wrong attested backend", "owned_t16_test", "172.23.0.3", "172.23.0.2", false},
		{"wrong owned database", "different_test", "172.23.0.2", "172.23.0.2", false},
		{"invalid attestation", "owned_t16_test", "127.0.0.1", "172.23.0.0/24", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := t16ProviderStoreMatches(dsn, tc.database, tc.server, tc.expected); got != tc.want {
				t.Fatalf("connected-store guard returned %v, want %v", got, tc.want)
			}
		})
	}
}

func t16Loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func t16Subject(t *testing.T, env, fallback string) string {
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

var t16PrincipalUUID = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

func t16InspectHandler(token string, inspect func(context.Context, string) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-T16-Provider-Token")), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/inspect" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ids := r.URL.Query()["principal_id"]
		if len(ids) != 1 || !t16PrincipalUUID.MatchString(ids[0]) {
			http.Error(w, "one canonical principal UUID is required", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := inspect(ctx, ids[0])
		if err != nil {
			http.Error(w, "inspection failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func t16PublishReadyAndWait(t *testing.T, ready, stop, controlURL string) {
	t.Helper()
	file, err := os.OpenFile(ready, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("cannot exclusively create provider ready file")
	}
	err = json.NewEncoder(file).Encode(map[string]string{
		"control_url":    controlURL,
		"classification": "actual-provider",
	})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal("cannot write provider readiness")
	}
	timer := time.NewTimer(20 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("provider coordinator did not stop harness within 20 minutes")
		case <-ticker.C:
			if info, err := os.Lstat(stop); err == nil {
				if !info.Mode().IsRegular() {
					t.Fatal("provider stop path must be a regular file")
				}
				return
			} else if !os.IsNotExist(err) {
				t.Fatal("cannot inspect provider stop file")
			}
		}
	}
}
