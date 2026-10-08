package unit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	authmiddleware "github.com/example/auth-service/internal/transport/http/api/v1/middleware"
	res "github.com/example/auth-service/pkg/http"
)

type stubSigner struct {
	respToken  *jwt.Token
	respClaims jwt.MapClaims
	respErr    error
}

func (s stubSigner) SignAccessToken(string, map[string]interface{}, time.Duration) (string, error) {
	return "", errors.New("not implemented")
}
func (s stubSigner) SignRefreshToken(string, string, time.Duration) (string, error) {
	return "", errors.New("not implemented")
}
func (s stubSigner) Parse(string) (*jwt.Token, jwt.MapClaims, error) {
	return s.respToken, s.respClaims, s.respErr
}

func TestAuthMiddlewareMissingToken(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := authmiddleware.NewAuthMiddleware(stubSigner{})
	handler := mw.Handler(func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	_ = handler(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	var errResp res.ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error.Code != "unauthorized" {
		t.Fatalf("unexpected error code: %s", errResp.Error.Code)
	}
}

func TestAuthMiddlewareInvalidToken(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer bad")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := authmiddleware.NewAuthMiddleware(stubSigner{respErr: errors.New("parse error")})
	handler := mw.Handler(func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	_ = handler(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddlewareSubjectMissing(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer token")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := authmiddleware.NewAuthMiddleware(stubSigner{
		respToken:  &jwt.Token{Valid: true},
		respClaims: jwt.MapClaims{},
	})
	handler := mw.Handler(func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	_ = handler(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
