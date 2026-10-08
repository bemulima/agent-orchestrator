package usecase

import (
	"context"
	"errors"
	"testing"
)

// Unit-only spy: tests producer call order and persisted binding, not crypto or NATS.
type signupBindingSpy struct {
	fixture    *t16Fixture
	operations []string
	principals []string
	loseACK    bool
}

func (s *signupBindingSpy) AssignRole(context.Context, string, string) error {
	return errors.New("generic role assignment must not provision signup")
}
func (s *signupBindingSpy) CheckRole(ctx context.Context, id, role string) (bool, error) {
	return s.fixture.roles.CheckRole(ctx, id, role)
}
func (s *signupBindingSpy) AssignSignupRole(ctx context.Context, id, operation string) error {
	if !s.fixture.users.ids[id] {
		return errors.New("role provisioning preceded User acknowledgement")
	}
	op := s.fixture.store.ops[operation]
	if op == nil || op.PrincipalID != id {
		return errors.New("role provisioning not bound to reserved operation")
	}
	s.operations = append(s.operations, operation)
	s.principals = append(s.principals, id)
	if err := s.fixture.roles.AssignRole(ctx, id, signupRole); err != nil {
		return err
	}
	if s.loseACK {
		s.loseACK = false
		return errT16Failure
	}
	return nil
}

func TestAuthRBACStudentProvisioningV1RetriesCommittedAssignment(t *testing.T) {
	f := newT16(t)
	spy := &signupBindingSpy{fixture: f, loseACK: true}
	first := f.service()
	first.rbacClient = spy
	_, tokens, err := first.VerifySignup(context.Background(), "unit", "t16@example.test", "private-test-proof")
	t16NoTokens(t, f, tokens, err)
	if len(spy.operations) != 1 || len(f.roles.ids) != 1 {
		t.Fatal("named ACK loss must follow the committed student assignment")
	}
	// Reconstruct Auth using retained provisioning state and repeat its own flow.
	retry := f.service()
	retry.rbacClient = spy
	user, tokens, err := retry.VerifySignup(context.Background(), "unit", "t16@example.test", "private-test-proof")
	if err != nil || user == nil || tokens == nil {
		t.Fatal("signup did not recover the committed downstream assignment")
	}
	if len(spy.operations) != 2 || spy.operations[0] != spy.operations[1] ||
		spy.principals[0] != user.ID || spy.principals[1] != user.ID || len(f.roles.ids) != 1 {
		t.Fatal("signup retry changed canonical binding or duplicated assignment")
	}
}

func TestSignupAndCredentialRepairUsePersistedRoleBinding(t *testing.T) {
	f := newT16(t)
	spy := &signupBindingSpy{fixture: f}
	svc := f.service()
	svc.rbacClient = spy
	f.store.casErr = errT16Failure
	_, tokens, err := svc.VerifySignup(context.Background(), "unit", "t16@example.test", "private-test-proof")
	t16NoTokens(t, f, tokens, err)
	if len(spy.operations) != 1 {
		t.Fatal("signup did not use signup-only role port")
	}
	f.store.casErr = nil
	user, tokens, err := svc.SignIn(context.Background(), "unit", "t16@example.test", "t16-private-test-password")
	if err != nil || user == nil || tokens == nil {
		t.Fatal("credential-owned repair did not complete")
	}
	if len(spy.operations) != 2 || spy.operations[0] != spy.operations[1] || spy.principals[0] != user.ID || spy.principals[1] != user.ID {
		t.Fatal("repair changed persisted operation or canonical principal")
	}
}

type noSignupFacet struct{ delegate *t16Roles }

func (s noSignupFacet) AssignRole(context.Context, string, string) error {
	return errors.New("generic fallback forbidden")
}
func (s noSignupFacet) CheckRole(ctx context.Context, id, role string) (bool, error) {
	return s.delegate.CheckRole(ctx, id, role)
}

func TestSignupMissingAuthenticatedFacetCannotIssueTokens(t *testing.T) {
	f := newT16(t)
	svc := f.service()
	svc.rbacClient = noSignupFacet{delegate: f.roles}
	_, tokens, err := svc.VerifySignup(context.Background(), "unit", "t16@example.test", "private-test-proof")
	t16NoTokens(t, f, tokens, err)
	if len(f.roles.ids) != 0 {
		t.Fatal("missing facet changed role state")
	}
}
