package verification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
)

type repositoryFunc func(context.Context, string, []interface{}) ([]interface{}, error)

func (f repositoryFunc) Execute(ctx context.Context, action string, args []interface{}) ([]interface{}, error) {
	return f(ctx, action, args)
}
func testOptions() Options {
	return Options{SignupCodeTTL: 5 * time.Minute, SignupHardTTL: 24 * time.Hour, EmailCodeTTL: 5 * time.Minute, EmailHardTTL: 24 * time.Hour}
}

func TestConsumeReceiptValidatesEveryImmutableBinding(t *testing.T) {
	operation := "9be4b753-57a7-4f11-8d8d-bb93e56de461"
	email, code := "owned@example.test", "1234"
	valid := []interface{}{operation, email, fingerprint(email, code), "frozen-credential", time.Now().Add(time.Hour).Unix()}
	for _, tc := range []struct {
		name   string
		change func([]interface{})
	}{
		{"operation", func(r []interface{}) { r[0] = "a5c72a56-148d-4a82-bb76-d2d8ba83f258" }},
		{"email", func(r []interface{}) { r[1] = "foreign@example.test" }},
		{"fingerprint", func(r []interface{}) { r[2] = fingerprint(email, "9876") }},
		{"credential", func(r []interface{}) { r[3] = "" }},
		{"expired", func(r []interface{}) { r[4] = time.Now().Add(-time.Second).Unix() }},
		{"fractional expiry", func(r []interface{}) { r[4] = float64(time.Now().Add(time.Hour).Unix()) + 0.5 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := append([]interface{}(nil), valid...)
			tc.change(row)
			service := NewService(repositoryFunc(func(_ context.Context, action string, args []interface{}) ([]interface{}, error) {
				if action != "signup_consume" || args[0] != email || args[1] != code || args[2] != operation || args[3] != fingerprint(email, code) {
					t.Fatal("consume must bind normalized exact proof")
				}
				return []interface{}{"ok", row}, nil
			}), testOptions())
			if receipt, err := service.ConsumeSignupProof(context.Background(), " Owned@Example.Test ", " 1234 ", operation); err == nil || receipt != nil {
				t.Fatal("malformed or mismatched receipt accepted")
			}
		})
	}
	service := NewService(repositoryFunc(func(context.Context, string, []interface{}) ([]interface{}, error) {
		return []interface{}{"ok", valid}, nil
	}), testOptions())
	r, err := service.ConsumeSignupProof(context.Background(), email, code, operation)
	if err != nil || r == nil || r.PasswordHash != "frozen-credential" || r.ExpiresAt.Unix() != valid[4] {
		t.Fatal("valid frozen receipt rejected or changed")
	}
}

func TestVerificationUnavailableNeverFallsBackOrLeaksStorageError(t *testing.T) {
	var actions []string
	service := NewService(repositoryFunc(func(_ context.Context, action string, _ []interface{}) ([]interface{}, error) {
		actions = append(actions, action)
		return nil, errors.New("driver tuple contains private credential material")
	}), testOptions())
	receipt, err := service.ConsumeSignupProof(context.Background(), "owned@example.test", "1234", "9be4b753-57a7-4f11-8d8d-bb93e56de461")
	if receipt != nil || !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "credential") || len(actions) != 1 || actions[0] != "signup_consume" {
		t.Fatal("storage failure did not fail closed at consume boundary")
	}
	if _, err := NewService(nil, testOptions()).VerifySignup(context.Background(), "owned@example.test", "1234"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing repository did not fail closed")
	}
}

func TestEmailCodeCollisionRetriesAreBounded(t *testing.T) {
	var generated, calls int
	opts := testOptions()
	opts.GenerateCode = func() (string, error) { generated++; return "1234", nil }
	service := NewService(repositoryFunc(func(_ context.Context, action string, args []interface{}) ([]interface{}, error) {
		calls++
		if action != "email_start" || args[1] != "principal" || args[2] != "owned@example.test" {
			t.Fatal("email code reservation binding changed")
		}
		return []interface{}{"code_conflict"}, nil
	}), opts)
	if id, err := service.StartEmailChange(context.Background(), "principal", " Owned@Example.Test "); err == nil || id != "" || generated != 10 || calls != 10 {
		t.Fatal("collision allocation exceeded or bypassed ten retries")
	}
}

func TestFingerprintUsesLengthFraming(t *testing.T) {
	if fingerprint("a", "bc") == fingerprint("ab", "c") || len(fingerprint("owned@example.test", "1234")) != 64 {
		t.Fatal("provider fingerprint lost framed SHA256 binding")
	}
}

func TestLegacyVerifyNeverConsumesRetainedReceipt(t *testing.T) {
	service := NewService(repositoryFunc(func(_ context.Context, action string, _ []interface{}) ([]interface{}, error) {
		if action != "signup_verify" {
			t.Fatal("legacy verify attempted receipt recovery")
		}
		return []interface{}{"not_found"}, nil
	}), testOptions())
	if credential, err := service.VerifySignup(context.Background(), "owned@example.test", "1234"); credential != "" || !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("legacy replay should return no credential")
	}
}

func TestReceiptOperationMustBeCanonicalNonNilUUID(t *testing.T) {
	for _, operation := range []string{"00000000-0000-0000-0000-000000000000", "9BE4B753-57A7-4F11-8D8D-BB93E56DE461", "not-a-uuid"} {
		calls := 0
		service := NewService(repositoryFunc(func(context.Context, string, []interface{}) ([]interface{}, error) { calls++; return nil, nil }), testOptions())
		if receipt, err := service.ConsumeSignupProof(context.Background(), "owned@example.test", "1234", operation); err == nil || receipt != nil || calls != 0 {
			t.Fatal("noncanonical operation reached storage")
		}
	}
}
