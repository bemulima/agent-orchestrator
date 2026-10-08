package tokenverify

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken   = errors.New("invalid_token")
	ErrTokenExpired   = errors.New("token_expired")
	ErrSubjectMissing = errors.New("subject_missing")
)

type Parser interface {
	Parse(token string) (*jwt.Token, jwt.MapClaims, error)
}

type Result struct {
	UserID string
	Email  string
	Claims map[string]any
}

// Verify parses and validates JWT, returning user info and custom claims (without sub/email).
func Verify(parser Parser, token string, nowFn func() time.Time) (*Result, error) {
	if parser == nil {
		return nil, ErrInvalidToken
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	tok, claims, err := parser.Parse(token)
	if err != nil || tok == nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	if exp, err := claims.GetExpirationTime(); err != nil || exp == nil || nowFn().After(exp.Time) {
		return nil, ErrTokenExpired
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	if sub == "" {
		return nil, ErrSubjectMissing
	}
	filtered := map[string]any{}
	for k, v := range claims {
		if k == "sub" || k == "email" {
			continue
		}
		filtered[k] = v
	}
	return &Result{UserID: sub, Email: email, Claims: filtered}, nil
}
