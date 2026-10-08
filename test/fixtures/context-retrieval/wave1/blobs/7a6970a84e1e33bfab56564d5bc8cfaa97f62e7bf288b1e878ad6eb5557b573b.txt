package natsadapter

import (
	"encoding/json"
	"errors"
	"time"

	nats "github.com/nats-io/nats.go"

	"github.com/example/auth-service/internal/tokenverify"
)

type VerifyHandler struct {
	parser    tokenverify.Parser
	respondFn func(msg *nats.Msg, resp VerifyResponse)
}

type verifyRequest struct {
	Token string `json:"token"`
}

type VerifyResponse struct {
	OK     bool           `json:"ok"`
	UserID string         `json:"user_id,omitempty"`
	Email  string         `json:"email,omitempty"`
	Error  string         `json:"error,omitempty"`
	Claims map[string]any `json:"claims,omitempty"`
}

func NewVerifyHandler(parser tokenverify.Parser) *VerifyHandler {
	return &VerifyHandler{parser: parser, respondFn: respond}
}

// SetResponder allows overriding the responder function (useful for tests).
func (h *VerifyHandler) SetResponder(responder func(msg *nats.Msg, resp VerifyResponse)) {
	if responder != nil {
		h.respondFn = responder
	}
}

func (h *VerifyHandler) Subscribe(conn *nats.Conn, subject, queue string) error {
	if conn == nil {
		return errors.New("nats connection is nil")
	}
	_, err := conn.QueueSubscribe(subject, queue, h.Handle)
	return err
}

func (h *VerifyHandler) Handle(msg *nats.Msg) {
	var req verifyRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.respondFn(msg, VerifyResponse{OK: false, Error: "invalid_payload"})
		return
	}
	result, err := tokenverify.Verify(h.parser, req.Token, time.Now)
	if err != nil {
		switch {
		case errors.Is(err, tokenverify.ErrTokenExpired):
			h.respondFn(msg, VerifyResponse{OK: false, Error: "expired"})
		case errors.Is(err, tokenverify.ErrSubjectMissing):
			h.respondFn(msg, VerifyResponse{OK: false, Error: "subject_missing"})
		default:
			h.respondFn(msg, VerifyResponse{OK: false, Error: "invalid_token"})
		}
		return
	}
	h.respondFn(msg, VerifyResponse{OK: true, UserID: result.UserID, Email: result.Email, Claims: result.Claims})
}

func respond(msg *nats.Msg, resp VerifyResponse) {
	data, _ := json.Marshal(resp)
	_ = msg.Respond(data)
}
