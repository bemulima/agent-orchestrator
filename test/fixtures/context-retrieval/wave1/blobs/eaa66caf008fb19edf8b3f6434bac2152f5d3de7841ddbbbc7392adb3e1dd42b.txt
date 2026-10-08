package natsadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/example/auth-service/internal/domain"
)

func TestSignupProofProducerBinding(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate disposable signing key")
	}
	client, err := NewRBACClientWithSignupProof(nil, "owned.rbac.assign-role", "owned.rbac.checkRole", base64.StdEncoding.EncodeToString(private))
	if err != nil {
		t.Fatal("construct signup signer")
	}
	principal := "11111111-1111-4111-8111-111111111111"
	operation := "22222222-2222-4222-8222-222222222222"
	now := time.Unix(1800000000, 0)
	envelope, err := client.(*rbacClient).signupProof.envelope(principal, operation, "owned.rbac.assign-role", now)
	if err != nil {
		t.Fatal("sign accepted binding")
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		t.Fatal("decode payload")
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || envelope.KeyID != "auth-signup-v1" || !ed25519.Verify(public, data, signature) {
		t.Fatal("proof signature invalid")
	}
	var payload signupProofPayload
	if json.Unmarshal(data, &payload) != nil {
		t.Fatal("decode signed binding")
	}
	expected := signupProofPayload{Version: 1, Issuer: "ms-go-auth", Audience: "ms-go-rbac", Purpose: "signup-student-provisioning-v1", Subject: "owned.rbac.assign-role", OperationID: operation, PrincipalID: principal, Role: "student", PrincipalKind: "user", TenantID: "00000000-0000-0000-0000-000000000000", ServiceID: "00000000-0000-0000-0000-000000000100", ResourceKind: "global", ResourceID: "00000000-0000-0000-0000-000000000000", IssuedAt: now.Unix(), ExpiresAt: now.Unix() + 60}
	if payload != expected {
		t.Fatal("producer changed frozen operation/principal/role/scope/time binding")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 15 {
		t.Fatal("unexpected payload schema")
	}
	encoded, _ := json.Marshal(envelope)
	if len(data) > 8*1024 || len(encoded) > 16*1024 {
		t.Fatal("producer exceeds consumer limits")
	}
	// Signing is a byte-bound proof; no proof or key is emitted to test output.
	data[0] ^= 1
	if ed25519.Verify(public, data, signature) {
		t.Fatal("signature is not bound to payload bytes")
	}
}

func TestSignupProofUnavailableAndInvalidConfiguration(t *testing.T) {
	for _, key := range []string{"not-base64", base64.StdEncoding.EncodeToString(make([]byte, 32)), base64.StdEncoding.EncodeToString(make([]byte, 64))} {
		if _, err := NewRBACClientWithSignupProof(nil, "rbac.assign-role", "rbac.checkRole", key); err == nil {
			t.Fatal("malformed nonempty key accepted")
		}
	}
	client, err := NewRBACClientWithSignupProof(nil, "rbac.assign-role", "rbac.checkRole", "")
	if err != nil {
		t.Fatal("missing key should leave provisioning unavailable")
	}
	if err := client.(domain.SignupRoleProvisioner).AssignSignupRole(context.Background(), "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("missing key allowed unsigned fallback")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate disposable key")
	}
	signer := &signupProofSigner{private: private}
	for _, id := range []string{"", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", " 11111111-1111-4111-8111-111111111111"} {
		if _, err := signer.envelope(id, "22222222-2222-4222-8222-222222222222", "rbac.assign-role", time.Now()); err == nil {
			t.Fatal("noncanonical principal accepted")
		}
		if _, err := signer.envelope("11111111-1111-4111-8111-111111111111", id, "rbac.assign-role", time.Now()); err == nil {
			t.Fatal("noncanonical operation accepted")
		}
	}
	for _, subject := range []string{"", "rbac.*", "rbac.>", "rbac..assign-role", "rbac assign-role"} {
		if _, err := NewRBACClientWithSignupProof(nil, subject, "rbac.checkRole", base64.StdEncoding.EncodeToString(private)); err == nil {
			t.Fatal("nonconcrete subject accepted")
		}
	}
}
