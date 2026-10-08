package unit

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"

	natsadapter "github.com/example/auth-service/internal/infrastructure/messaging/nats"
)

type stubParser struct {
	responses map[string]parseResult
}

type parseResult struct {
	token  *jwt.Token
	claims jwt.MapClaims
	err    error
}

func (s stubParser) Parse(token string) (*jwt.Token, jwt.MapClaims, error) {
	if res, ok := s.responses[token]; ok {
		return res.token, res.claims, res.err
	}
	return nil, nil, errors.New("unexpected token")
}

func TestVerifyHandlerHandleSuccess(t *testing.T) {
	exp := float64(time.Now().Add(time.Minute).Unix())
	parser := stubParser{responses: map[string]parseResult{
		"good": {
			token:  &jwt.Token{Valid: true},
			claims: jwt.MapClaims{"sub": "user-1", "email": "user@example.com", "role": "student", "exp": exp},
			err:    nil,
		},
	}}
	handler := natsadapter.NewVerifyHandler(parser)
	var captured natsadapter.VerifyResponse
	handler.SetResponder(func(_ *nats.Msg, resp natsadapter.VerifyResponse) { captured = resp })

	payload, _ := json.Marshal(map[string]string{"token": "good"})
	handler.Handle(&nats.Msg{Data: payload})

	if !captured.OK || captured.UserID != "user-1" || captured.Email != "user@example.com" {
		t.Fatalf("unexpected response: %+v", captured)
	}
	if captured.Claims["role"] != "student" {
		t.Fatalf("claims not propagated: %+v", captured.Claims)
	}
}

func TestVerifyHandlerInvalidToken(t *testing.T) {
	parser := stubParser{responses: map[string]parseResult{
		"bad": {token: nil, claims: nil, err: errors.New("bad token")},
	}}
	handler := natsadapter.NewVerifyHandler(parser)
	var captured natsadapter.VerifyResponse
	handler.SetResponder(func(_ *nats.Msg, resp natsadapter.VerifyResponse) { captured = resp })

	payload, _ := json.Marshal(map[string]string{"token": "bad"})
	handler.Handle(&nats.Msg{Data: payload})

	if captured.OK || captured.Error != "invalid_token" {
		t.Fatalf("expected invalid_token, got %+v", captured)
	}
}

func TestVerifyHandlerSubjectMissing(t *testing.T) {
	exp := float64(time.Now().Add(time.Minute).Unix())
	parser := stubParser{responses: map[string]parseResult{
		"nosub": {
			token:  &jwt.Token{Valid: true},
			claims: jwt.MapClaims{"email": "user@example.com", "exp": exp},
			err:    nil,
		},
	}}
	handler := natsadapter.NewVerifyHandler(parser)
	var captured natsadapter.VerifyResponse
	handler.SetResponder(func(_ *nats.Msg, resp natsadapter.VerifyResponse) { captured = resp })

	payload, _ := json.Marshal(map[string]string{"token": "nosub"})
	handler.Handle(&nats.Msg{Data: payload})

	if captured.OK || captured.Error != "subject_missing" {
		t.Fatalf("expected subject_missing, got %+v", captured)
	}
}
