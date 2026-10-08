//go:build integration

package usecase_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
	"github.com/example/auth-service/internal/usecase"
	verification "github.com/example/auth-service/internal/usecase/verification"
	"github.com/tarantool/go-iproto"
	driver "github.com/tarantool/go-tarantool/v2"
	"golang.org/x/crypto/bcrypt"
)

// TestActualDirectVerification must run against the coordinator's disposable,
// authenticated identity store with Auth's actual additive schema installed.
// Every competing service has an independent authenticated driver connection.
func TestActualDirectVerification(t *testing.T) {
	if os.Getenv("T16_DIRECT_VERIFICATION") != "true" {
		t.Skip("requires coordinator-owned authenticated disposable identity store")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var peers []domain.VerificationClient
	for i := 0; i < 8; i++ {
		peer, _ := t16DirectVerification(t)
		peers = append(peers, peer)
	}
	opts := t16VerificationOptions()
	var code atomic.Int32
	code.Store(6000)
	opts.GenerateCode = func() (string, error) {
		// Deterministic test codes never leave this isolated test's memory/store.
		n := code.Add(1)
		return string([]byte{'6', byte('0' + (n/100)%10), byte('0' + (n/10)%10), byte('0' + n%10)}), nil
	}
	owner, fixture := t16DirectVerificationWithOptions(t, opts)
	hash, err := bcrypt.GenerateFromPassword([]byte("disposable-direct-proof-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal("prepare disposable bcrypt credential")
	}

	t.Run("same_operation_frozen_receipt_multi_instance", func(t *testing.T) {
		email := t16Email(t, "direct-same-op")
		operation, _ := usecase.GenerateJTI()
		if owner.StartSignup(ctx, email, string(hash)) != nil {
			t.Fatal("start actual signup proof")
		}
		proofCode := t16CapturedCode(t, fixture, email)
		var successes atomic.Int32
		var receipts sync.Map
		parallelVerification(peers, func(peer domain.VerificationClient) {
			r, err := peer.(domain.SignupProofConsumer).ConsumeSignupProof(ctx, email, proofCode, operation)
			if err == nil && r != nil && r.OperationID == operation && r.Email == email && r.PasswordHash == string(hash) {
				successes.Add(1)
				receipts.Store(r.ExpiresAt.Unix(), true)
			}
		})
		var expiries int
		receipts.Range(func(_, _ interface{}) bool { expiries++; return true })
		if successes.Load() != 8 || expiries != 1 {
			t.Fatal("exact retries did not return one frozen credential/expiry")
		}
		if live, count := fixture.inspect(t, email); live || count != 1 {
			t.Fatal("proof/receipt atomicity failed")
		}
		if password, err := owner.VerifySignup(ctx, email, proofCode); err == nil || password != "" {
			t.Fatal("legacy verify replayed consumed receipt")
		}
		if err := owner.ResendSignup(ctx, email); err == nil {
			t.Fatal("resend revived consumed proof")
		}
	})

	t.Run("competing_operations_one_owner", func(t *testing.T) {
		email := t16Email(t, "direct-distinct-op")
		if owner.StartSignup(ctx, email, string(hash)) != nil {
			t.Fatal("start competing proof")
		}
		proofCode := t16CapturedCode(t, fixture, email)
		var successes atomic.Int32
		parallelVerification(peers, func(peer domain.VerificationClient) {
			operation, _ := usecase.GenerateJTI()
			if r, err := peer.(domain.SignupProofConsumer).ConsumeSignupProof(ctx, email, proofCode, operation); err == nil && r != nil {
				successes.Add(1)
			}
		})
		if successes.Load() != 1 {
			t.Fatal("distinct operations did not elect exactly one receipt owner")
		}
		if live, count := fixture.inspect(t, email); live || count != 1 {
			t.Fatal("competing operations changed receipt multiplicity")
		}
	})

	t.Run("resend_invalidates_old_code", func(t *testing.T) {
		email := t16Email(t, "direct-resend")
		if owner.StartSignup(ctx, email, string(hash)) != nil {
			t.Fatal("start resend proof")
		}
		old := t16CapturedCode(t, fixture, email)
		if owner.ResendSignup(ctx, email) != nil {
			t.Fatal("resend proof")
		}
		fresh := t16CapturedCode(t, fixture, email)
		if old == fresh {
			t.Fatal("resend did not replace code")
		}
		operation, _ := usecase.GenerateJTI()
		if receipt, err := owner.ConsumeSignupProof(ctx, email, old, operation); err == nil || receipt != nil {
			t.Fatal("old code survived resend")
		}
		if receipt, err := owner.ConsumeSignupProof(ctx, email, fresh, operation); err != nil || receipt == nil {
			t.Fatal("new code did not consume")
		}
	})

	t.Run("email_change_atomic_single_consumer", func(t *testing.T) {
		email := t16Email(t, "direct-email-change")
		user, _ := usecase.GenerateJTI()
		if id, err := owner.StartEmailChange(ctx, user, email); err != nil || id == "" {
			t.Fatal("start actual email change")
		}
		proofCode := directTestCode(code.Load())
		var successes atomic.Int32
		parallelVerification(peers, func(peer domain.VerificationClient) {
			actualUser, actualEmail, err := peer.VerifyEmailChange(ctx, proofCode)
			if err == nil && actualUser == user && actualEmail == email {
				successes.Add(1)
			}
		})
		if successes.Load() != 1 {
			t.Fatal("email change had multiple consumers or lost its bound identity")
		}
	})

	t.Run("password_reset_atomic_single_consumer_and_replacement", func(t *testing.T) {
		email := t16Email(t, "direct-reset")
		if id, err := owner.StartPasswordReset(ctx, email); err != nil || id == "" {
			t.Fatal("start actual reset")
		}
		old := directTestCode(code.Load())
		if id, err := owner.StartPasswordReset(ctx, email); err != nil || id == "" {
			t.Fatal("replace actual reset")
		}
		fresh := directTestCode(code.Load())
		if err := owner.VerifyPasswordReset(ctx, email, old); err == nil {
			t.Fatal("replacement retained old reset code")
		}
		var successes atomic.Int32
		parallelVerification(peers, func(peer domain.VerificationClient) {
			if peer.VerifyPasswordReset(ctx, email, fresh) == nil {
				successes.Add(1)
			}
		})
		if successes.Load() != 1 {
			t.Fatal("password reset was not single consumer across connections")
		}
	})
	t.Run("email_collision_preserves_first_owner", func(t *testing.T) {
		collisionOptions := t16VerificationOptions()
		collisionOptions.GenerateCode = func() (string, error) { return "9867", nil }
		first, _ := t16DirectVerificationWithOptions(t, collisionOptions)
		second, _ := t16DirectVerificationWithOptions(t, collisionOptions)
		firstEmail, secondEmail := t16Email(t, "direct-collision-first"), t16Email(t, "direct-collision-second")
		firstUser, _ := usecase.GenerateJTI()
		secondUser, _ := usecase.GenerateJTI()
		if id, err := first.StartEmailChange(ctx, firstUser, firstEmail); err != nil || id == "" {
			t.Fatal("reserve first collision code")
		}
		if id, err := second.StartEmailChange(ctx, secondUser, secondEmail); err == nil || id != "" {
			t.Fatal("active code collision was accepted")
		}
		actualUser, actualEmail, err := first.VerifyEmailChange(ctx, "9867")
		if err != nil || actualUser != firstUser || actualEmail != firstEmail {
			t.Fatal("collision changed original code owner")
		}
	})

	t.Run("invalid_proofs_do_not_consume_valid_owner", func(t *testing.T) {
		email := t16Email(t, "direct-invalid")
		if owner.StartSignup(ctx, email, string(hash)) != nil {
			t.Fatal("start invalid proof case")
		}
		actualCode := t16CapturedCode(t, fixture, email)
		operation, _ := usecase.GenerateJTI()
		if r, err := owner.ConsumeSignupProof(ctx, email, "0000", operation); !errors.Is(err, verification.ErrInvalidCode) || r != nil {
			t.Fatal("invalid signup proof accepted or misclassified")
		}
		if r, err := owner.ConsumeSignupProof(ctx, email, actualCode, operation); err != nil || r == nil {
			t.Fatal("invalid attempt destroyed original valid proof")
		}
		resetEmail := t16Email(t, "direct-invalid-reset")
		if _, err := owner.StartPasswordReset(ctx, resetEmail); err != nil {
			t.Fatal("start invalid reset case")
		}
		resetCode := directTestCode(code.Load())
		if err := owner.VerifyPasswordReset(ctx, resetEmail, "0000"); !errors.Is(err, verification.ErrInvalidCode) {
			t.Fatal("invalid reset proof accepted or misclassified")
		}
		if err := owner.VerifyPasswordReset(ctx, resetEmail, resetCode); err != nil {
			t.Fatal("invalid reset attempt destroyed original valid proof")
		}
		emailChange := t16Email(t, "direct-invalid-email")
		user, _ := usecase.GenerateJTI()
		if _, err := owner.StartEmailChange(ctx, user, emailChange); err != nil {
			t.Fatal("start invalid email case")
		}
		emailCode := directTestCode(code.Load())
		if u, e, err := owner.VerifyEmailChange(ctx, "0000"); err == nil || u != "" || e != "" {
			t.Fatal("unknown email proof accepted")
		}
		if u, e, err := owner.VerifyEmailChange(ctx, emailCode); err != nil || u != user || e != emailChange {
			t.Fatal("invalid email attempt destroyed original owner")
		}
	})
	t.Run("code_expiry_rejects_signup_email_reset", func(t *testing.T) {
		options := t16VerificationOptions()
		options.SignupCodeTTL = time.Second
		options.EmailCodeTTL = time.Second
		short, shortFixture := t16DirectVerificationWithOptions(t, options)
		signupEmail := t16Email(t, "direct-code-expiry")
		resetEmail := t16Email(t, "direct-reset-expiry")
		newEmail := t16Email(t, "direct-email-expiry")
		user, _ := usecase.GenerateJTI()
		var signupCode, resetCode, emailCode string
		options.GenerateCode = func() (string, error) { return "9785", nil }
		short, shortFixture = t16DirectVerificationWithOptions(t, options)
		if short.StartSignup(ctx, signupEmail, string(hash)) != nil {
			t.Fatal("start expiring signup")
		}
		signupCode = t16CapturedCode(t, shortFixture, signupEmail)
		options.GenerateCode = func() (string, error) { return "9786", nil }
		other, _ := t16DirectVerificationWithOptions(t, options)
		if _, err := other.StartPasswordReset(ctx, resetEmail); err != nil {
			t.Fatal("start expiring reset")
		}
		resetCode = "9786"
		options.GenerateCode = func() (string, error) { return "9787", nil }
		emailService, _ := t16DirectVerificationWithOptions(t, options)
		if _, err := emailService.StartEmailChange(ctx, user, newEmail); err != nil {
			t.Fatal("start expiring email")
		}
		emailCode = "9787"
		timer := time.NewTimer(1100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("proof expiry test timed out")
		}
		operation, _ := usecase.GenerateJTI()
		if r, err := short.ConsumeSignupProof(ctx, signupEmail, signupCode, operation); !errors.Is(err, verification.ErrExpired) || r != nil {
			t.Fatal("expired signup code accepted or misclassified")
		}
		if err := other.VerifyPasswordReset(ctx, resetEmail, resetCode); !errors.Is(err, verification.ErrExpired) {
			t.Fatal("expired reset code accepted or misclassified")
		}
		if u, e, err := emailService.VerifyEmailChange(ctx, emailCode); !errors.Is(err, verification.ErrExpired) || u != "" || e != "" {
			t.Fatal("expired email code accepted or misclassified")
		}
	})
	t.Run("runtime_principal_has_no_eval_or_direct_space_privileges", func(t *testing.T) {
		conn, err := driver.Connect(ctx, driver.NetDialer{Address: os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_ADDRESS"), User: os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_USER"), Password: os.Getenv("AUTH_VERIFICATION_TEST_TARANTOOL_PASSWORD")}, driver.Opts{Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal("connect permission test runtime principal")
		}
		defer conn.Close()
		// Resolve numeric IDs through the separate authorized fixture. The
		// runtime client's filtered schema intentionally hides these names;
		// numeric requests reach the server and test its permission boundary.
		schema, err := driver.GetSchema(fixture.conn)
		if err != nil {
			t.Fatal("read authorized fixture schema for permission probes")
		}
		signupSpace, ok := schema.Spaces["user_signup_space"]
		if !ok {
			t.Fatal("fixture signup schema unavailable")
		}
		legacySpace, ok := schema.Spaces["user"]
		if !ok {
			t.Fatal("fixture legacy schema read grant required")
		}
		requests := []driver.Request{
			driver.NewEvalRequest("return 1").Args([]interface{}{}).Context(ctx),
			driver.NewSelectRequest(signupSpace.Id).Index(uint32(0)).Limit(1).Iterator(driver.IterEq).Key([]interface{}{t16Email(t, "direct-permission")}).Context(ctx),
			driver.NewSelectRequest(legacySpace.Id).Index(uint32(0)).Limit(1).Iterator(driver.IterAll).Key([]interface{}{}).Context(ctx),
			driver.NewCallRequest("auth_verification_fixture_forbidden").Args([]interface{}{}).Context(ctx),
		}
		for index, request := range requests {
			_, err := conn.Do(request).Get()
			var serverErr driver.Error
			if !errors.As(err, &serverErr) || serverErr.Code != iproto.ER_ACCESS_DENIED {
				t.Fatalf("runtime privilege boundary request_index=%d server_error_code=%d expected_access_denied=%d", index, serverErr.Code, iproto.ER_ACCESS_DENIED)
			}
		}
	})

	t.Run("receipt_expiry_is_original_hard_deadline", func(t *testing.T) {
		expiryOptions := t16VerificationOptions()
		expiryOptions.SignupCodeTTL, expiryOptions.SignupHardTTL = 2*time.Second, 3*time.Second
		expiryOptions.GenerateCode = func() (string, error) { return "9876", nil }
		short, shortFixture := t16DirectVerificationWithOptions(t, expiryOptions)
		email := t16Email(t, "direct-expiry")
		operation, _ := usecase.GenerateJTI()
		if short.StartSignup(ctx, email, string(hash)) != nil {
			t.Fatal("start short-lived actual proof")
		}
		rows, err := shortFixture.selectRows("user_signup_space", "primary", email)
		if err != nil || len(rows) != 1 || len(rows[0]) != 7 {
			t.Fatal("read original proof creation deadline")
		}
		created, ok := directTestUnix(rows[0][4])
		if !ok {
			t.Fatal("unexpected original proof timestamp type")
		}
		first, err := short.ConsumeSignupProof(ctx, email, "9876", operation)
		if err != nil || first == nil || first.ExpiresAt.Unix() != created+3 {
			t.Fatal("receipt deadline was not frozen from original creation")
		}
		second, err := peers[0].(domain.SignupProofConsumer).ConsumeSignupProof(ctx, email, "9876", operation)
		if err != nil || second == nil || !second.ExpiresAt.Equal(first.ExpiresAt) {
			t.Fatal("different instance extended frozen deadline")
		}
		wait := time.Until(first.ExpiresAt) + 30*time.Millisecond
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal("expiry acceptance timed out")
			}
		}
		if receipt, err := short.ConsumeSignupProof(ctx, email, "9876", operation); err == nil || receipt != nil {
			t.Fatal("expired receipt recovered a credential")
		}
	})
	t.Log("actual direct authenticated identity store; eight independent clients; frozen receipt, competing operations, resend, email/reset single-consumer checks passed")
}

func directTestCode(n int32) string {
	return string([]byte{'6', byte('0' + (n/100)%10), byte('0' + (n/10)%10), byte('0' + n%10)})
}

func parallelVerification(peers []domain.VerificationClient, consume func(domain.VerificationClient)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, peer := range peers {
		wg.Add(1)
		go func(peer domain.VerificationClient) { defer wg.Done(); <-start; consume(peer) }(peer)
	}
	close(start)
	wg.Wait()
}

// The MessagePack driver may decode an unsigned timestamp using any fitting
// integer width. Preserve exact epoch seconds; reject fractional/negative data.
func directTestUnix(value interface{}) (int64, bool) {
	switch n := value.(type) {
	case uint64:
		if n > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint:
		if uint64(n) > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, n >= 0
	case int32:
		return int64(n), n >= 0
	case int16:
		return int64(n), n >= 0
	case int8:
		return int64(n), n >= 0
	case int:
		return int64(n), n >= 0
	default:
		return 0, false
	}
}
