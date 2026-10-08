package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/infrastructure/tarantool"
)

const (
	contractSignupCode      = "contract-code-123"
	contractEmailChangeCode = "contract-code-456"
)

func TestTarantoolClientContract(t *testing.T) {
	server := newContractServer()
	ts := startServerOrSkip(t, server)
	if ts == nil {
		return
	}
	defer ts.Close()

	client := tarantool.NewHTTPClient(ts.URL, 2*time.Second)

	ctx := context.Background()
	uuid, err := client.StartRegistration(ctx, "user@example.com", "password123")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", uuid)

	result, err := client.VerifyRegistration(ctx, uuid, contractSignupCode)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", result.Email)
	require.Equal(t, "password123", result.Password)

	changeUUID, err := client.StartEmailChange(ctx, "user-1", "new@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, changeUUID)

	changeResult, err := client.VerifyEmailChange(ctx, changeUUID, contractEmailChangeCode)
	require.NoError(t, err)
	require.Equal(t, "new@example.com", changeResult.Email)
}

type contractServer struct {
	signupEmail       string
	signupPassword    string
	emailChangeUUID   string
	emailChangeTarget string
}

func newContractServer() *contractServer {
	return &contractServer{}
}

func (s *contractServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/set-new-user":
		s.handleSetNewUser(w, r)
	case "/check-new-user-code":
		s.handleCheckNewUserCode(w, r)
	case "/start-email-change":
		s.handleStartEmailChange(w, r)
	case "/verify-email-change":
		s.handleVerifyEmailChange(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *contractServer) handleSetNewUser(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Value struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		} `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	s.signupEmail = payload.Value.Email
	s.signupPassword = payload.Value.Password
	writeJSON(w, http.StatusOK, map[string]string{"uuid": s.signupEmail})
}

func (s *contractServer) handleCheckNewUserCode(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Value struct {
			UUID string `json:"uuid"`
			Code string `json:"code"`
		} `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if payload.Value.Code != contractSignupCode {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": s.signupEmail, "password": s.signupPassword})
}

func (s *contractServer) handleStartEmailChange(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Value struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
		} `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	s.emailChangeTarget = payload.Value.Email
	s.emailChangeUUID = fmt.Sprintf("%s-req", payload.Value.UserID)
	writeJSON(w, http.StatusOK, map[string]string{"uuid": s.emailChangeUUID})
}

func (s *contractServer) handleVerifyEmailChange(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Value struct {
			UUID string `json:"uuid"`
			Code string `json:"code"`
		} `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if payload.Value.UUID != s.emailChangeUUID || payload.Value.Code != contractEmailChangeCode {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": s.emailChangeTarget})
}
