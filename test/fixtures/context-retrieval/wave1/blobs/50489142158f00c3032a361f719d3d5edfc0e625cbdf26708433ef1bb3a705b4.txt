package natsadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"

	"github.com/example/auth-service/internal/domain"
)

type userClient struct {
	conn    *nats.Conn
	subject string
}

type rbacClient struct {
	conn             *nats.Conn
	assignSubject    string
	checkRoleSubject string
	signupProof      *signupProofSigner
}

func NewUserClient(conn *nats.Conn, subject string) domain.UserProvisioner {
	return &userClient{conn: conn, subject: subject}
}

func NewRBACClient(conn *nats.Conn, assignSubject, checkRoleSubject string) domain.RoleClient {
	return &rbacClient{conn: conn, assignSubject: assignSubject, checkRoleSubject: checkRoleSubject}
}

func (c *userClient) CreateUser(ctx context.Context, request domain.UserProvisionRequest) error {
	return requestAckWithTimeout(ctx, c.conn, c.subject, request, 12*time.Second)
}

func (c *rbacClient) AssignRole(ctx context.Context, userID, role string) error {
	payload := map[string]interface{}{"user_id": userID, "role": role}
	return requestAck(ctx, c.conn, c.assignSubject, payload)
}

func (c *rbacClient) CheckRole(ctx context.Context, userID, role string) (bool, error) {
	payload := map[string]interface{}{"user_id": userID, "role": role}
	return requestBool(ctx, c.conn, c.checkRoleSubject, payload)
}

func requestAck(ctx context.Context, conn *nats.Conn, subject string, payload interface{}) error {
	return requestAckWithTimeout(ctx, conn, subject, payload, 3*time.Second)
}

func requestAckWithTimeout(ctx context.Context, conn *nats.Conn, subject string, payload interface{}, timeout time.Duration) error {
	data, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg, err := conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("empty response from %s", subject)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}
		return fmt.Errorf("request to %s failed", subject)
	}
	return nil
}

func requestBool(ctx context.Context, conn *nats.Conn, subject string, payload interface{}) (bool, error) {
	data, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	msg, err := conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return false, err
	}
	if msg == nil {
		return false, fmt.Errorf("empty response from %s", subject)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return false, err
	}
	if resp.Error != "" {
		return false, fmt.Errorf(resp.Error)
	}
	return resp.OK, nil
}
