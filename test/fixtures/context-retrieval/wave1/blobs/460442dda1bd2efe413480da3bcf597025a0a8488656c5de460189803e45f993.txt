//go:build integration

package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	driver "github.com/tarantool/go-tarantool/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
	"github.com/example/auth-service/internal/usecase"
	verification "github.com/example/auth-service/internal/usecase/verification"
	"golang.org/x/crypto/bcrypt"
)

type verificationRestartState struct {
	Email        string         `json:"email"`
	OperationID  string         `json:"operation_id"`
	Code         string         `json:"code"`
	PasswordHash string         `json:"password_hash"`
	ExpiresAt    time.Time      `json:"expires_at"`
	PendingEmail string         `json:"pending_email"`
	PendingCode  string         `json:"pending_code"`
	ExpiredEmail string         `json:"expired_email"`
	ExpiredCode  string         `json:"expired_code"`
	ExpiredAt    time.Time      `json:"expired_at"`
	LegacyCounts map[string]int `json:"legacy_counts,omitempty"`
}

// The root coordinator stops and restarts this same disposable store/volume
// between phases. Private fixture state is never a public evidence artifact.
func TestActualVerificationRestart(t *testing.T) {
	phase := os.Getenv("AUTH_VERIFICATION_RESTART_PHASE")
	if phase == "" {
		t.Skip("requires coordinator-owned store restart phases")
	}
	if phase != "seed" && phase != "resume" {
		t.Fatal("invalid restart phase")
	}
	statePath := os.Getenv("AUTH_VERIFICATION_RESTART_STATE_FILE")
	if !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath {
		t.Fatal("absolute private restart state path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	options := t16VerificationOptions()
	service, fixture := t16DirectVerificationWithOptions(t, options)
	if phase == "seed" {
		hash, err := bcrypt.GenerateFromPassword([]byte("disposable-restart-proof-password"), bcrypt.MinCost)
		if err != nil {
			t.Fatal("prepare private restart credential")
		}
		operation, _ := usecase.GenerateJTI()
		state := verificationRestartState{Email: t16Email(t, "restart-consumed"), OperationID: operation, PasswordHash: string(hash), PendingEmail: t16Email(t, "restart-pending"), ExpiredEmail: t16Email(t, "restart-expired")}
		if service.StartSignup(ctx, state.Email, state.PasswordHash) != nil {
			t.Fatal("seed consumed restart proof")
		}
		state.Code = t16CapturedCode(t, fixture, state.Email)
		receipt, err := service.ConsumeSignupProof(ctx, state.Email, state.Code, state.OperationID)
		if err != nil || receipt == nil {
			t.Fatal("seed frozen restart receipt")
		}
		state.ExpiresAt = receipt.ExpiresAt
		if service.StartSignup(ctx, state.PendingEmail, state.PasswordHash) != nil {
			t.Fatal("seed live pending restart proof")
		}
		state.PendingCode = t16CapturedCode(t, fixture, state.PendingEmail)
		options.SignupCodeTTL = time.Second
		short, shortFixture := t16DirectVerificationWithOptions(t, options)
		if short.StartSignup(ctx, state.ExpiredEmail, state.PasswordHash) != nil {
			t.Fatal("seed expiring restart proof")
		}
		state.ExpiredCode = t16CapturedCode(t, shortFixture, state.ExpiredEmail)
		rows, err := shortFixture.selectRows("user_signup_space", "primary", state.ExpiredEmail)
		if err != nil || len(rows) != 1 || len(rows[0]) != 7 {
			t.Fatal("read expiring proof deadline")
		}
		expires, ok := directTestUnix(rows[0][3])
		if !ok {
			t.Fatal("invalid expiring proof deadline")
		}
		state.ExpiredAt = time.Unix(expires, 0).UTC()
		if os.Getenv("AUTH_VERIFICATION_RESTART_LEGACY_COUNTS") == "true" {
			state.LegacyCounts = map[string]int{}
			for _, space := range []string{"user", "sandbox"} {
				state.LegacyCounts[space] = restartLegacyCount(t, fixture, space)
			}
		}
		file, err := os.OpenFile(statePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal("create private restart state")
		}
		if err := json.NewEncoder(file).Encode(state); err != nil {
			_ = file.Close()
			t.Fatal("write private restart state")
		}
		if file.Close() != nil {
			t.Fatal("close private restart state")
		}
		t.Log("restart seed committed frozen receipt and retained pending/expiring proofs; private state written")
		return
	}
	info, err := os.Lstat(statePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("restart state must be private regular file")
	}
	file, err := os.Open(statePath)
	if err != nil {
		t.Fatal("read private restart state")
	}
	defer file.Close()
	var state verificationRestartState
	if json.NewDecoder(file).Decode(&state) != nil || !fixture.owned(state.Email) || !fixture.owned(state.PendingEmail) || !fixture.owned(state.ExpiredEmail) || state.OperationID == "" || state.PasswordHash == "" || !state.ExpiresAt.After(time.Now()) {
		t.Fatal("invalid private restart binding")
	}
	if state.LegacyCounts != nil {
		for _, space := range []string{"user", "sandbox"} {
			expected, ok := state.LegacyCounts[space]
			if !ok || restartLegacyCount(t, fixture, space) != expected {
				t.Fatal("restart changed retained legacy record count")
			}
		}
	}
	receipt, err := service.ConsumeSignupProof(ctx, state.Email, state.Code, state.OperationID)
	if err != nil || receipt == nil || receipt.OperationID != state.OperationID || receipt.Email != state.Email || receipt.PasswordHash != state.PasswordHash || !receipt.ExpiresAt.Equal(state.ExpiresAt) {
		t.Fatal("restart did not retain exact frozen receipt")
	}
	if live, count := fixture.inspect(t, state.Email); live || count != 1 {
		t.Fatal("restart changed proof/receipt multiplicity")
	}
	if credential, err := service.VerifySignup(ctx, state.Email, state.Code); err == nil || credential != "" {
		t.Fatal("legacy verify replayed restart receipt")
	}
	conflicting, _ := usecase.GenerateJTI()
	if r, err := service.ConsumeSignupProof(ctx, state.Email, state.Code, conflicting); err == nil || r != nil {
		t.Fatal("restart accepted conflicting receipt owner")
	}
	pendingOp, _ := usecase.GenerateJTI()
	pending, err := service.ConsumeSignupProof(ctx, state.PendingEmail, state.PendingCode, pendingOp)
	if err != nil || pending == nil || pending.PasswordHash != state.PasswordHash || pending.Email != state.PendingEmail {
		t.Fatal("restart lost still valid pending proof")
	}
	wait := time.Until(state.ExpiredAt) + 30*time.Millisecond
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("expired proof wait timed out")
		}
	}
	expiredOp, _ := usecase.GenerateJTI()
	if r, err := service.ConsumeSignupProof(ctx, state.ExpiredEmail, state.ExpiredCode, expiredOp); !errors.Is(err, verification.ErrExpired) || r != nil {
		t.Fatal("restart accepted expired proof")
	}
	var _ domain.SignupProofConsumer = service
	t.Log("actual store restart retained frozen receipt and live pending proof, rejected replay/conflicting owner/expired proof")
}

func restartLegacyCount(t *testing.T, fixture *t16TarantoolFixture, space string) int {
	t.Helper()
	if space != "user" && space != "sandbox" {
		t.Fatal("legacy retention inventory space forbidden")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := fixture.conn.Do(driver.NewSelectRequest(space).Index("primary").Limit(^uint32(0)).Iterator(driver.IterAll).Key([]interface{}{}).Context(ctx)).Get()
	if err != nil {
		t.Fatal("cannot read permitted legacy retention inventory")
	}
	return len(rows)
}
