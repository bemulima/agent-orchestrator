package private

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/transport/http/private/handlers"
	service "github.com/example/user-service/internal/usecase"
)

type activeUserResolverStub struct {
	result service.ActiveUserResolution
	err    error
	calls  int
}

func (s *activeUserResolverStub) ResolveActiveUsers(context.Context, []string) (service.ActiveUserResolution, error) {
	s.calls++
	return s.result, s.err
}

func TestResolveActiveUsersRequiresExactInternalToken(t *testing.T) {
	resolver := &activeUserResolverStub{}
	e := echo.New()
	Register(e.Group("/internal"), handlers.NewHandler(resolver), "secret-token")
	for _, token := range []string{"", "wrong-token", " secret-token"} {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/active/resolve", bytes.NewBufferString(`{"user_ids":["11111111-1111-4111-8111-111111111111"]}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set("X-Internal-Token", token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q returned %d", token, rec.Code)
		}
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver called without valid internal token")
	}
}

func TestResolveActiveUsersReturnsBoundedResolution(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	resolver := &activeUserResolverStub{result: service.ActiveUserResolution{ActiveUserIDs: []string{id}, UnavailableUserIDs: []string{}}}
	e := echo.New()
	Register(e.Group("/internal"), handlers.NewHandler(resolver), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/active/resolve", bytes.NewBufferString(`{"user_ids":["`+id+`"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Internal-Token", "secret-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data service.ActiveUserResolution `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.ActiveUserIDs) != 1 || response.Data.ActiveUserIDs[0] != id || response.Data.UnavailableUserIDs == nil {
		t.Fatalf("unexpected response: %+v", response.Data)
	}
}

func TestResolveActiveUsersRejectsMalformedOrInvalidRequests(t *testing.T) {
	resolver := &activeUserResolverStub{err: service.ErrInvalidActiveUserBatch}
	e := echo.New()
	Register(e.Group("/internal"), handlers.NewHandler(resolver), "secret-token")
	requests := []string{
		`{"user_ids":[]}`,
		`{"user_ids":["11111111-1111-4111-8111-111111111111"],"extra":true}`,
		`{"user_ids":["11111111-1111-4111-8111-111111111111"]}{}`,
	}
	for _, body := range requests {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/active/resolve", bytes.NewBufferString(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set("X-Internal-Token", "secret-token")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s returned %d: %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestResolveActiveUsersHidesDependencyErrors(t *testing.T) {
	resolver := &activeUserResolverStub{err: errors.New("database credentials leaked")}
	e := echo.New()
	Register(e.Group("/internal"), handlers.NewHandler(resolver), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/active/resolve", bytes.NewBufferString(`{"user_ids":["11111111-1111-4111-8111-111111111111"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Internal-Token", "secret-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || bytes.Contains(rec.Body.Bytes(), []byte("credentials")) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
