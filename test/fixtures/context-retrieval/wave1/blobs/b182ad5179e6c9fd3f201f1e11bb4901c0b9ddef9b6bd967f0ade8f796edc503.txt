package repo

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Root runs this explicitly on an owned loopback *_test database. A fresh
// schema isolates all migration/ownership fixtures from the actual chain.
func TestT16SignupCompletionPostgres(t *testing.T) {
	if os.Getenv("T16_AUTH_REPOSITORY_INTEGRATION") != "true" {
		t.Skip("requires root-owned disposable PostgreSQL run")
	}
	dsn := os.Getenv("AUTH_TEST_DATABASE_URL")
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("requires owned loopback *_test database")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("open owned PostgreSQL")
	}
	var name string
	if err := base.Raw("SELECT current_database()").Scan(&name).Error; err != nil || !strings.HasSuffix(name, "_test") {
		t.Fatal("connected database ownership guard failed")
	}
	raw, err := base.DB()
	if err != nil {
		t.Fatal("database handle")
	}
	t.Cleanup(func() { _ = raw.Close() })
	schema := "t16_signup_repo_" + strings.ReplaceAll(t16RepoUUID(t), "-", "")
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal("create isolated repository schema")
	}
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("open isolated repository schema")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("isolated database handle")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	apply := func(name string) error {
		data, err := os.ReadFile(filepath.Join("../../../../migrations", name))
		if err != nil {
			return err
		}
		return db.Exec(string(data)).Error
	}
	for _, migration := range []string{"0001_init.up.sql", "0003_oauth_flow.up.sql", "0004_signup_completion.up.sql"} {
		if apply(migration) != nil {
			t.Fatalf("apply isolated %s", migration)
		}
	}
	if apply("0004_signup_completion.up.sql") != nil {
		t.Fatal("idempotent migration reapply")
	}
	if apply("0004_signup_completion.down.sql") != nil {
		t.Fatal("empty completion rollback")
	}
	if apply("0004_signup_completion.up.sql") != nil {
		t.Fatal("reapply after empty rollback")
	}
	r := NewAuthUserRepository(db).(domain.SignupCompletionRepository)
	ctx := context.Background()
	email := "owned-repository@example.test"
	proof := strings.Repeat("a", 64)
	// Concurrent reservation must return the single durable winner.
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	failed := false
	for i := 0; i < 8; i++ {
		operation, principal := t16RepoUUID(t), t16RepoUUID(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := r.BeginSignupCompletion(ctx, email, proof, operation, principal)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || op == nil {
				failed = true
				return
			}
			ids[op.OperationID] = true
		}()
	}
	wg.Wait()
	if failed || len(ids) != 1 {
		t.Fatal("concurrent reservation did not converge")
	}
	var operation string
	for id := range ids {
		operation = id
	}
	op, err := r.BeginSignupCompletion(ctx, email, proof, t16RepoUUID(t), t16RepoUUID(t))
	if err != nil || op.OperationID != operation {
		t.Fatal("reconstruction changed operation")
	}
	beforePrincipal := op.PrincipalID
	if apply("0004_signup_completion.down.sql") == nil {
		t.Fatal("rollback erased pending operation")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("t16-private-repository-credential"), bcrypt.MinCost)
	if err != nil {
		t.Fatal("test credential")
	}
	receipt := &domain.SignupProofReceipt{OperationID: operation, Email: email, PasswordHash: string(hash), ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(time.Hour)}
	wrong := *receipt
	wrong.OperationID = t16RepoUUID(t)
	if _, err := r.StoreSignupReceipt(ctx, operation, &wrong); err == nil {
		t.Fatal("wrong operation receipt persisted")
	}
	op, err = r.StoreSignupReceipt(ctx, operation, receipt)
	if err != nil || op.State != domain.SignupVerified {
		t.Fatal("persist verified receipt")
	}
	wrong = *receipt
	otherHash, _ := bcrypt.GenerateFromPassword([]byte("different-repository-credential"), bcrypt.MinCost)
	wrong.PasswordHash = string(otherHash)
	if _, err := r.StoreSignupReceipt(ctx, operation, &wrong); err == nil {
		t.Fatal("frozen credential changed")
	}
	if apply("0004_signup_completion.down.sql") == nil {
		t.Fatal("rollback erased verified operation")
	}
	user, err := r.EnsureSignupPrincipal(ctx, operation)
	if err != nil || user.ID != beforePrincipal {
		t.Fatal("create reserved principal")
	}
	reconstructed := NewAuthUserRepository(db).(domain.SignupCompletionRepository)
	again, err := reconstructed.EnsureSignupPrincipal(ctx, operation)
	if err != nil || again.ID != user.ID {
		t.Fatal("owned principal retry changed identity")
	}
	// Credential drift must fail ownership and terminal checks.
	if db.Model(&domain.AuthUser{}).Where("id = ?", user.ID).Update("password_hash", string(otherHash)).Error != nil {
		t.Fatal("inject owned credential drift")
	}
	if _, err := r.EnsureSignupPrincipal(ctx, operation); err == nil {
		t.Fatal("credential drift adopted")
	}
	if _, err := r.CompleteSignup(ctx, operation, time.Now().UTC(), true); err == nil {
		t.Fatal("credential drift completed")
	}
	if db.Model(&domain.AuthUser{}).Where("id = ?", user.ID).Update("password_hash", string(hash)).Error != nil {
		t.Fatal("restore owned credential")
	}
	past := time.Now().UTC().Add(-time.Minute)
	if db.Model(&domain.SignupCompletion{}).Where("operation_id = ?", operation).Update("receipt_expires_at", past).Error != nil {
		t.Fatal("inject owned expired receipt")
	}
	won, err := r.CompleteSignup(ctx, operation, time.Now().UTC(), true)
	if err != nil || won {
		t.Fatal("expired code CAS completed")
	}
	pending, err := r.SignupPrincipalPending(ctx, user.ID)
	if err != nil || !pending {
		t.Fatal("expired verified actor escaped pending gate")
	}
	// Password-authenticated repair explicitly uses the no-receipt-freshness CAS.
	won, err = r.CompleteSignup(ctx, operation, time.Now().UTC(), false)
	if err != nil || !won {
		t.Fatal("password repair CAS after receipt expiry")
	}
	won, err = r.CompleteSignup(ctx, operation, time.Now().UTC(), false)
	if err != nil || won {
		t.Fatal("terminal CAS won twice")
	}
	if apply("0004_signup_completion.down.sql") == nil {
		t.Fatal("rollback erased terminal replay tombstone")
	}
	pending, err = r.SignupPrincipalPending(ctx, user.ID)
	if err != nil || pending {
		t.Fatal("completed principal pending gate incorrect")
	}
	// Existing email cannot be assigned another operation, even with a valid proof.
	if _, err := r.BeginSignupCompletion(ctx, email, strings.Repeat("b", 64), t16RepoUUID(t), t16RepoUUID(t)); err == nil {
		t.Fatal("existing account adopted")
	}
	// An exact-looking pre-existing row without ownership cannot be adopted either.
	email2 := "unowned-repository@example.test"
	op2, err := r.BeginSignupCompletion(ctx, email2, strings.Repeat("c", 64), t16RepoUUID(t), t16RepoUUID(t))
	if err != nil {
		t.Fatal("reserve unowned fixture")
	}
	receipt2 := *receipt
	receipt2.OperationID = op2.OperationID
	receipt2.Email = email2
	op2, err = r.StoreSignupReceipt(ctx, op2.OperationID, &receipt2)
	if err != nil {
		t.Fatal("verify unowned fixture")
	}
	existing := &domain.AuthUser{ID: op2.PrincipalID, Email: email2, PasswordHash: &receipt2.PasswordHash}
	if NewAuthUserRepository(db).Create(ctx, existing) != nil {
		t.Fatal("inject exact unowned fixture")
	}
	if _, err := r.EnsureSignupPrincipal(ctx, op2.OperationID); err == nil {
		t.Fatal("exact principal without completion ownership adopted")
	}
	var count int64
	if db.Model(&domain.SignupCompletion{}).Count(&count).Error != nil || count != 2 {
		t.Fatal("rollback changed completion rows")
	}
	t.Log("owned isolated repository schema preserved with pending ownership and terminal replay evidence")
}
func t16RepoUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("test UUID")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
