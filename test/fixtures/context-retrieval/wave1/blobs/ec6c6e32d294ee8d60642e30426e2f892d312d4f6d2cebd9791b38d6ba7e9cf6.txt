package nats

import (
	"encoding/json"
	"errors"

	natsgo "github.com/nats-io/nats.go"
)

// Server wraps NATS connection for RPC handlers.
type Server struct {
	Conn *natsgo.Conn
}

// Subscribe registers a queue subscription.
func (s Server) Subscribe(subject, queue string, handler func(msg *natsgo.Msg)) error {
	if s.Conn == nil {
		return errors.New("nats connection is nil")
	}
	_, err := s.Conn.QueueSubscribe(subject, queue, handler)
	return err
}

// Respond helper marshals payload.
func Respond(msg *natsgo.Msg, payload any) {
	if msg == nil {
		return
	}
	data, _ := json.Marshal(payload)
	_ = msg.Respond(data)
}
