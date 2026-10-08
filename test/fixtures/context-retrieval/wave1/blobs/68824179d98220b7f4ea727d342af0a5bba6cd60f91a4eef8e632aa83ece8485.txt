package natsadapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/example/auth-service/internal/domain"
	nats "github.com/nats-io/nats.go"
)

type signupProofSigner struct {
	private ed25519.PrivateKey
}

type signupProofEnvelope struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type signupProofPayload struct {
	Version       int    `json:"version"`
	Issuer        string `json:"issuer"`
	Audience      string `json:"audience"`
	Purpose       string `json:"purpose"`
	Subject       string `json:"subject"`
	OperationID   string `json:"operation_id"`
	PrincipalID   string `json:"principal_id"`
	Role          string `json:"role"`
	PrincipalKind string `json:"principal_kind"`
	TenantID      string `json:"tenant_id"`
	ServiceID     string `json:"service_id"`
	ResourceKind  string `json:"resource_kind"`
	ResourceID    string `json:"resource_id"`
	IssuedAt      int64  `json:"issued_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

var signupUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func canonicalSignupUUID(value string) bool {
	return signupUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

// NewRBACClientWithSignupProof adds the dedicated signup facet without granting
// authority to generic AssignRole. Empty configuration leaves signup unavailable.
func NewRBACClientWithSignupProof(conn *nats.Conn, assignSubject, checkRoleSubject, encodedPrivateKey string) (domain.RoleClient, error) {
	client := &rbacClient{conn: conn, assignSubject: assignSubject, checkRoleSubject: checkRoleSubject}
	if encodedPrivateKey == "" {
		return client, nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encodedPrivateKey)
	if err != nil || len(key) != ed25519.PrivateKeySize || base64.StdEncoding.EncodeToString(key) != encodedPrivateKey {
		return nil, errors.New("invalid Auth signup signing key configuration")
	}
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !bytes.Equal(key, derived) {
		return nil, errors.New("invalid Auth signup signing key configuration")
	}
	if !concreteSignupSubject(assignSubject) {
		return nil, errors.New("invalid Auth signup assignment subject")
	}
	client.signupProof = &signupProofSigner{private: derived}
	return client, nil
}

func concreteSignupSubject(subject string) bool {
	if subject == "" || strings.ContainsAny(subject, "*> \t\r\n") {
		return false
	}
	for _, token := range strings.Split(subject, ".") {
		if token == "" {
			return false
		}
	}
	return true
}

func (s *signupProofSigner) envelope(principalID, operationID, subject string, now time.Time) (*signupProofEnvelope, error) {
	if s == nil || len(s.private) != ed25519.PrivateKeySize {
		return nil, errors.New("authenticated signup role provisioning unavailable")
	}
	if !canonicalSignupUUID(principalID) || !canonicalSignupUUID(operationID) || !concreteSignupSubject(subject) {
		return nil, errors.New("invalid signup role binding")
	}
	payload := signupProofPayload{
		Version: 1, Issuer: "ms-go-auth", Audience: "ms-go-rbac",
		Purpose: "signup-student-provisioning-v1", Subject: subject,
		OperationID: operationID, PrincipalID: principalID, Role: "student",
		PrincipalKind: "user", TenantID: "00000000-0000-0000-0000-000000000000",
		ServiceID: "00000000-0000-0000-0000-000000000100", ResourceKind: "global",
		ResourceID: "00000000-0000-0000-0000-000000000000",
		IssuedAt:   now.Unix(), ExpiresAt: now.Unix() + 60,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("encode signup role binding")
	}
	return &signupProofEnvelope{
		KeyID: "auth-signup-v1", Payload: base64.StdEncoding.EncodeToString(data),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(s.private, data)),
	}, nil
}

func (c *rbacClient) AssignSignupRole(ctx context.Context, principalID, operationID string) error {
	envelope, err := c.signupProof.envelope(principalID, operationID, c.assignSubject, time.Now().UTC())
	if err != nil {
		return err
	}
	if c.conn == nil {
		return errors.New("signup role transport unavailable")
	}
	return requestAck(ctx, c.conn, c.assignSubject, envelope)
}

var _ domain.SignupRoleProvisioner = (*rbacClient)(nil)
