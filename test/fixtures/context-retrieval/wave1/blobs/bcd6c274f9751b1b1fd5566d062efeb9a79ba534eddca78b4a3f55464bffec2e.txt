//go:build integration

package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/example/auth-service/internal/usecase"
)

// Uses the existing actual T17 Auth HTTP provider and its real User/RBAC/
// Tarantool dependencies. Only signup and its normal session continuation run.
func TestT17StudentProvisioningHTTPContract(t *testing.T) {
	if os.Getenv("T17_HTTP_CONTRACT") != "true" {
		t.Skip("requires isolated existing T17 provider harnesses")
	}
	base := t17AuthProviderURL(t, "T17_AUTH_READY_FILE", "url", "actual-registered-Auth-HTTP-production-usecase-PG-JWT-direct-Tarantool-CoreNATS-User-RBAC")
	_, proof := t16DirectVerification(t)
	db := t16AuthDB(t)
	email, password := t16Email(t, "student-contract"), "T17-disposable-student-password-41"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, value any, want int, output any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal("encode contract request")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("construct contract request")
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("actual Auth HTTP contract request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s status=%d want=%d", path, response.StatusCode, want)
		}
		if output != nil && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(output) != nil {
			t.Fatal("decode contract response")
		}
	}
	post("signup/start", map[string]string{"email": email, "password": password}, http.StatusAccepted, nil)
	code := t16CapturedCode(t, proof, email)
	var signup usecase.Tokens
	post("signup/verify", map[string]string{"email": email, "code": code}, http.StatusOK, &signup)
	if signup.AccessToken == "" || signup.RefreshToken == "" {
		t.Fatal("completed signup lacks session tokens")
	}
	operation, principal := t16Completion(t, db, email, code, "completed", true)
	if operation == "" || principal == "" {
		t.Fatal("canonical completed operation missing")
	}
	t16ProviderCounts(t, t16ProvisioningURL(t, "T16_USER_READY_FILE"), t16ProvisioningURL(t, "T16_RBAC_READY_FILE"), principal, 1, 1)
	t16SessionCount(t, db, email, 1)
	// Terminal signup replay does not issue a second session. Downstream receipt
	// replay is checked independently by RBAC's SQL contract regression.
	post("signup/verify", map[string]string{"email": email, "code": code}, http.StatusBadRequest, nil)
	t16SessionCount(t, db, email, 1)
	var signin, refresh usecase.Tokens
	post("signin", map[string]string{"email": email, "password": password}, http.StatusOK, &signin)
	post("refresh", map[string]string{"refresh_token": signin.RefreshToken}, http.StatusOK, &refresh)
	if signin.AccessToken == "" || refresh.AccessToken == "" || refresh.RefreshToken == "" || refresh.RefreshToken == signin.RefreshToken {
		t.Fatal("ordinary signin/refresh session regression")
	}
	var verified struct {
		UserID string `json:"user_id"`
	}
	post("verify", map[string]string{"token": refresh.AccessToken}, http.StatusOK, &verified)
	if verified.UserID != principal {
		t.Fatal("session continuation changed canonical principal")
	}
	t16ProviderCounts(t, t16ProvisioningURL(t, "T16_USER_READY_FILE"), t16ProvisioningURL(t, "T16_RBAC_READY_FILE"), principal, 1, 1)
}
