//go:build integration

package usecase_test

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
	store "github.com/example/auth-service/internal/infrastructure/persistence/tarantool"
	verification "github.com/example/auth-service/internal/usecase/verification"
	tarantool "github.com/tarantool/go-tarantool/v2"
)

// The coordinator supplies credentials for an isolated identity store. This
// fixture only reads run-owned verification records; it cannot migrate, reset,
// expose an arbitrary Eval, or inspect another identity's credential.
type t16TarantoolFixture struct {
	conn   *tarantool.Connection
	prefix string
}

func t16DirectVerification(t *testing.T) (domain.VerificationClient, *t16TarantoolFixture) {
	return t16DirectVerificationWithOptions(t, t16VerificationOptions())
}

func t16DirectVerificationWithOptions(t *testing.T, opts verification.Options) (*verification.Service, *t16TarantoolFixture) {
	t.Helper()
	address := os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_ADDRESS")
	host, port, err := net.SplitHostPort(address)
	user, password := os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_USER"), os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_PASSWORD")
	fixtureUser, fixturePassword := os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_FIXTURE_USER"), os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_FIXTURE_PASSWORD")
	prefix := os.Getenv("T16_RUN_PREFIX")
	if err != nil || !t16Loopback(host) || port == "" || user == "" || user == "guest" || password == "" || fixtureUser == "" || fixtureUser == "guest" || fixturePassword == "" || fixtureUser == user || fixturePassword == password || prefix == "" || strings.ContainsAny(prefix, " @/\\") {
		t.Fatal("explicit authenticated loopback identity store, distinct fixture credentials, and owned run prefix required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := store.Connect(ctx, store.ConnectionConfig{Address: address, User: user, Password: password, RequestTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal("cannot connect Auth-owned direct verification adapter")
	}
	t.Cleanup(func() { _ = client.Close() })
	conn, err := tarantool.Connect(ctx, tarantool.NetDialer{Address: address, User: fixtureUser, Password: fixturePassword}, tarantool.Opts{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal("cannot connect authenticated read-only test fixture")
	}
	t.Cleanup(func() { _ = conn.Close() })
	return verification.NewService(client, opts), &t16TarantoolFixture{conn: conn, prefix: prefix}
}

func (f *t16TarantoolFixture) owned(email string) bool {
	return email == strings.ToLower(strings.TrimSpace(email)) && strings.HasPrefix(email, f.prefix) && strings.HasSuffix(email, "@example.test") && !strings.ContainsAny(email, "\r\n\x00")
}

func (f *t16TarantoolFixture) selectRows(space, index, email string) ([][]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := f.conn.Do(tarantool.NewSelectRequest(space).Index(index).Limit(2).Iterator(tarantool.IterEq).Key([]interface{}{email}).Context(ctx)).Get()
	if err != nil {
		return nil, err
	}
	rows := make([][]interface{}, 0, len(data))
	for _, item := range data {
		row, ok := item.([]interface{})
		if !ok {
			return nil, domain.ErrNotFound
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *t16TarantoolFixture) code(email string) (string, bool) {
	if !f.owned(email) {
		return "", false
	}
	rows, err := f.selectRows("user_signup_space", "primary", email)
	if err != nil || len(rows) != 1 || len(rows[0]) != 7 {
		return "", false
	}
	code, ok := rows[0][2].(string)
	return code, ok && code != ""
}

func t16CapturedCode(t *testing.T, fixture *t16TarantoolFixture, email string) string {
	t.Helper()
	code, ok := fixture.code(email)
	if !ok {
		t.Fatal("one owned actual signup proof code required")
	}
	return code
}

func (f *t16TarantoolFixture) inspect(t *testing.T, email string) (bool, int) {
	t.Helper()
	if !f.owned(email) {
		t.Fatal("fixture inspection requires owned synthetic identity")
	}
	proofs, err := f.selectRows("user_signup_space", "primary", email)
	if err != nil {
		t.Fatal("cannot inspect owned live proof")
	}
	receipts, err := f.selectRows("user_signup_consumption_receipts", "email", email)
	if err != nil {
		t.Fatal("cannot inspect owned receipt count")
	}
	for _, row := range receipts {
		if len(row) != 5 {
			t.Fatal("frozen receipt tuple format changed")
		}
	}
	return len(proofs) != 0, len(receipts)
}

func t16VerificationOptions() verification.Options {
	return verification.Options{SignupCodeTTL: 5 * time.Minute, SignupHardTTL: 24 * time.Hour, EmailCodeTTL: 5 * time.Minute, EmailHardTTL: 24 * time.Hour}
}
