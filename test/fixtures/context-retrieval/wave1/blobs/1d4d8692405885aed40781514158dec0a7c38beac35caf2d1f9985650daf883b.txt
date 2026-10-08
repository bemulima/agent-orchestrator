package unit

import (
	"context"
	"errors"
	"testing"

	"github.com/example/auth-service/internal/domain"
	"github.com/example/auth-service/internal/usecase"
	pkglog "github.com/example/auth-service/pkg/log"
	"golang.org/x/crypto/bcrypt"
)

const (
	provisioningContractTraceID = "auth-user-rbac-provisioning-contract"
	provisioningContractEmail   = "provisioning-contract@example.test"
	provisioningContractCode    = "provisioning-contract-code"
	provisioningContractSecret  = "provisioning-contract-secret"
)

var (
	errContractUserProvisioning = errors.New("user provisioning unavailable")
	errContractRoleProvisioning = errors.New("role provisioning unavailable")
)

type provisioningContractUserClient struct {
	calls       []domain.UserProvisionRequest
	projections map[string]domain.UserProvisionRequest
	err         error
}

func (c *provisioningContractUserClient) CreateUser(_ context.Context, request domain.UserProvisionRequest) error {
	c.calls = append(c.calls, request)
	if c.err != nil {
		return c.err
	}
	if c.projections == nil {
		c.projections = make(map[string]domain.UserProvisionRequest)
	}
	c.projections[request.ID] = request
	return nil
}

type provisioningContractRoleBinding struct {
	userID string
	role   string
}

type provisioningContractRoleClient struct {
	assignCalls     []provisioningContractRoleBinding
	checkCalls      []provisioningContractRoleBinding
	assignments     map[provisioningContractRoleBinding]struct{}
	assignErr       error
	checkErr        error
	forceUnreadable bool
}

// Unit-only signup facet; no service credential is fabricated here.
func (c *provisioningContractRoleClient) AssignSignupRole(ctx context.Context, userID, operationID string) error {
	if operationID == "" {
		return errors.New("missing signup operation")
	}
	return c.AssignRole(ctx, userID, "student")
}

func (c *provisioningContractRoleClient) AssignRole(_ context.Context, userID, role string) error {
	binding := provisioningContractRoleBinding{userID: userID, role: role}
	c.assignCalls = append(c.assignCalls, binding)
	if c.assignErr != nil {
		return c.assignErr
	}
	if c.assignments == nil {
		c.assignments = make(map[provisioningContractRoleBinding]struct{})
	}
	c.assignments[binding] = struct{}{}
	return nil
}

func (c *provisioningContractRoleClient) CheckRole(_ context.Context, userID, role string) (bool, error) {
	binding := provisioningContractRoleBinding{userID: userID, role: role}
	c.checkCalls = append(c.checkCalls, binding)
	if c.checkErr != nil {
		return false, c.checkErr
	}
	if c.forceUnreadable {
		return false, nil
	}
	_, ok := c.assignments[binding]
	return ok, nil
}

func (c *provisioningContractRoleClient) seedAssignment(userID, role string) {
	if c.assignments == nil {
		c.assignments = make(map[provisioningContractRoleBinding]struct{})
	}
	c.assignments[provisioningContractRoleBinding{userID: userID, role: role}] = struct{}{}
}

func (c *provisioningContractRoleClient) assignmentCount() int {
	return len(c.assignments)
}

func newProvisioningContractService(t *testing.T, userClient domain.UserProvisioner, roleClient domain.RoleClient) (usecase.Service, *testDeps) {
	t.Helper()

	svc, deps := newTestServiceWithClients(t, userClient, roleClient)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(provisioningContractSecret), bcrypt.MinCost)
	if err != nil {
		t.Fatal("create signup password hash")
	}
	deps.tara.verifySignupPassword = string(passwordHash)
	return svc, deps
}

func verifyProvisioningContractSignup(svc usecase.Service) (*domain.AuthUser, *usecase.Tokens, error) {
	return svc.VerifySignup(context.Background(), provisioningContractTraceID, provisioningContractEmail, provisioningContractCode)
}

func requireIncompleteProvisioningSignup(t *testing.T, deps *testDeps, tokens *usecase.Tokens, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a retryable provisioning failure")
	}
	if tokens != nil {
		t.Fatal("signup returned tokens before provisioning completed")
	}
	if len(deps.refresh.tokens) != 0 {
		t.Fatal("signup persisted a refresh session before provisioning completed")
	}
}

func requireCompletedProvisioningSignup(t *testing.T, user *domain.AuthUser, tokens *usecase.Tokens, err error) {
	t.Helper()
	if err != nil {
		t.Fatal("expected provisioning completion")
	}
	if user == nil || tokens == nil {
		t.Fatal("expected completed signup identity and tokens")
	}
}

func TestVerifySignupProvisioningContractRequiresReadyUserAndRole(t *testing.T) {
	t.Run("ready-user-and-role", func(t *testing.T) {
		userClient := &provisioningContractUserClient{}
		roleClient := &provisioningContractRoleClient{}
		svc, deps := newProvisioningContractService(t, userClient, roleClient)

		user, tokens, err := verifyProvisioningContractSignup(svc)
		requireCompletedProvisioningSignup(t, user, tokens, err)
		if len(userClient.projections) != 1 {
			t.Fatal("expected one user projection")
		}
		if roleClient.assignmentCount() != 1 {
			t.Fatal("expected one role assignment")
		}
		if len(roleClient.assignCalls) != 1 || roleClient.assignCalls[0].role != "student" {
			t.Fatal("expected the canonical signup role")
		}
		if len(roleClient.checkCalls) == 0 {
			t.Fatal("expected role readiness confirmation before token issuance")
		}
		if len(deps.refresh.tokens) != 1 {
			t.Fatal("expected one refresh session after provisioning completion")
		}
	})

	t.Run("role-not-readable", func(t *testing.T) {
		userClient := &provisioningContractUserClient{}
		roleClient := &provisioningContractRoleClient{forceUnreadable: true}
		svc, deps := newProvisioningContractService(t, userClient, roleClient)

		_, tokens, err := verifyProvisioningContractSignup(svc)
		requireIncompleteProvisioningSignup(t, deps, tokens, err)
	})

	t.Run("role read fails", func(t *testing.T) {
		userClient := &provisioningContractUserClient{}
		roleClient := &provisioningContractRoleClient{checkErr: errContractRoleProvisioning}
		svc, deps := newProvisioningContractService(t, userClient, roleClient)

		_, tokens, err := verifyProvisioningContractSignup(svc)
		requireIncompleteProvisioningSignup(t, deps, tokens, err)
	})
}

func TestVerifySignupProvisioningContractRejectsUserProvisionFailure(t *testing.T) {
	userClient := &provisioningContractUserClient{err: errContractUserProvisioning}
	roleClient := &provisioningContractRoleClient{}
	svc, deps := newProvisioningContractService(t, userClient, roleClient)

	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
}

func TestVerifySignupProvisioningContractRejectsRoleProvisionFailure(t *testing.T) {
	userClient := &provisioningContractUserClient{}
	roleClient := &provisioningContractRoleClient{assignErr: errContractRoleProvisioning}
	svc, deps := newProvisioningContractService(t, userClient, roleClient)

	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
}

func TestVerifySignupProvisioningContractRequiresProvisioningDependencies(t *testing.T) {
	t.Run("missing user provisioner", func(t *testing.T) {
		svc, deps := newProvisioningContractService(t, nil, &provisioningContractRoleClient{})

		_, tokens, err := verifyProvisioningContractSignup(svc)
		requireIncompleteProvisioningSignup(t, deps, tokens, err)
	})

	t.Run("missing role provisioner", func(t *testing.T) {
		svc, deps := newProvisioningContractService(t, &provisioningContractUserClient{}, nil)

		_, tokens, err := verifyProvisioningContractSignup(svc)
		requireIncompleteProvisioningSignup(t, deps, tokens, err)
	})
}

func TestVerifySignupProvisioningContractRetriesUserReadyRoleMissing(t *testing.T) {
	userClient := &provisioningContractUserClient{}
	roleClient := &provisioningContractRoleClient{assignErr: errContractRoleProvisioning}
	svc, deps := newProvisioningContractService(t, userClient, roleClient)

	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
	if len(userClient.projections) != 1 {
		t.Fatal("expected the partial user projection")
	}

	roleClient.assignErr = nil
	user, retryTokens, retryErr := verifyProvisioningContractSignup(svc)
	requireCompletedProvisioningSignup(t, user, retryTokens, retryErr)
	if len(deps.users.users) != 1 {
		t.Fatal("expected one stable auth identity after retry")
	}
	if len(userClient.projections) != 1 {
		t.Fatal("expected one stable user projection after retry")
	}
	if roleClient.assignmentCount() != 1 {
		t.Fatal("expected one repaired role assignment after retry")
	}
}

func TestVerifySignupProvisioningContractRetriesRoleReadyUserMissing(t *testing.T) {
	userClient := &provisioningContractUserClient{err: errContractUserProvisioning}
	roleClient := &provisioningContractRoleClient{}
	svc, deps := newProvisioningContractService(t, userClient, roleClient)

	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
	if len(userClient.calls) != 1 {
		t.Fatal("expected one partial user provisioning attempt")
	}
	partialPrincipalID := userClient.calls[0].ID
	roleClient.seedAssignment(partialPrincipalID, "student")

	userClient.err = nil
	user, retryTokens, retryErr := verifyProvisioningContractSignup(svc)
	requireCompletedProvisioningSignup(t, user, retryTokens, retryErr)
	if user.ID != partialPrincipalID {
		t.Fatal("expected retry to retain the canonical principal")
	}
	if len(deps.users.users) != 1 {
		t.Fatal("expected one stable auth identity after retry")
	}
	if len(userClient.projections) != 1 {
		t.Fatal("expected one repaired user projection after retry")
	}
	if roleClient.assignmentCount() != 1 {
		t.Fatal("expected one stable role assignment after retry")
	}
}

func TestVerifySignupProvisioningContractCompletedRetryIsStable(t *testing.T) {
	userClient := &provisioningContractUserClient{}
	roleClient := &provisioningContractRoleClient{}
	svc, deps := newProvisioningContractService(t, userClient, roleClient)

	firstUser, firstTokens, firstErr := verifyProvisioningContractSignup(svc)
	requireCompletedProvisioningSignup(t, firstUser, firstTokens, firstErr)
	_, secondTokens, secondErr := verifyProvisioningContractSignup(svc)
	if secondErr == nil || secondTokens != nil {
		t.Fatal("completed signup code replay must be denied")
	}
	if firstUser == nil || firstTokens == nil || len(deps.refresh.tokens) != 1 {
		t.Fatal("expected exactly one signup token attempt")
	}

	if len(deps.users.users) != 1 {
		t.Fatal("expected one auth identity after completed retry")
	}
	if len(userClient.projections) != 1 {
		t.Fatal("expected one user projection after completed retry")
	}
	if roleClient.assignmentCount() != 1 {
		t.Fatal("expected one role assignment after completed retry")
	}
}

// t16OneShotProof preserves consumption across service reconstruction. Legacy
// verification never returns an already-consumed proof a second time.
type t16OneShotProof struct {
	*mockTarantool
	consumed bool
}

func (p *t16OneShotProof) VerifySignup(ctx context.Context, email, code string) (string, error) {
	if p.consumed {
		return "", domain.ErrNotFound
	}
	p.consumed = true
	return p.mockTarantool.VerifySignup(ctx, email, code)
}
func t16RebuildService(deps *testDeps, proof domain.VerificationClient) usecase.Service {
	return usecase.NewAuthService(deps.cfg, pkglog.New("test"), deps.users, mockIdentityRepo{}, nil, nil, deps.refresh, proof, deps.userClient, deps.rbacClient, deps.signer)
}
func TestT16OriginalPostConsumeRecovery(t *testing.T) {
	users := &provisioningContractUserClient{}
	roles := &provisioningContractRoleClient{assignErr: errContractRoleProvisioning}
	_, deps := newProvisioningContractService(t, users, roles)
	proof := &t16OneShotProof{mockTarantool: deps.tara}
	svc := t16RebuildService(deps, proof)
	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
	if len(users.calls) != 1 {
		t.Fatal("expected first attempt to reach downstream outage after consuming proof")
	}
	originalID := users.calls[0].ID
	roles.assignErr = nil
	svc = t16RebuildService(deps, proof)
	user, tokens, err := verifyProvisioningContractSignup(svc)
	if err != nil || user == nil || tokens == nil {
		t.Fatalf("durable recovery after restart failed: %v", err)
	}
	if user.ID != originalID {
		t.Fatal("recovery changed canonical principal")
	}
}
func TestT16OriginalPendingSigninDenied(t *testing.T) {
	users := &provisioningContractUserClient{}
	roles := &provisioningContractRoleClient{assignErr: errContractRoleProvisioning}
	_, deps := newProvisioningContractService(t, users, roles)
	proof := &t16OneShotProof{mockTarantool: deps.tara}
	svc := t16RebuildService(deps, proof)
	_, tokens, err := verifyProvisioningContractSignup(svc)
	requireIncompleteProvisioningSignup(t, deps, tokens, err)
	svc = t16RebuildService(deps, proof)
	_, tokens, err = svc.SignIn(context.Background(), provisioningContractTraceID, provisioningContractEmail, provisioningContractSecret)
	if err == nil || tokens != nil || len(deps.refresh.tokens) != 0 {
		t.Fatal("pending signup bypassed provisioning through password signin")
	}
}

func TestT16OriginalExistingEmailCannotBeAdopted(t *testing.T) {
	users := &provisioningContractUserClient{}
	roles := &provisioningContractRoleClient{}
	_, deps := newProvisioningContractService(t, users, roles)
	otherHash, err := bcrypt.GenerateFromPassword([]byte("other-existing-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal("create existing credential")
	}
	existing := &domain.AuthUser{ID: "existing-unrelated-principal", Email: provisioningContractEmail, PasswordHash: stringPtr(string(otherHash))}
	if err := deps.users.Create(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	proof := &t16OneShotProof{mockTarantool: deps.tara}
	svc := t16RebuildService(deps, proof)
	_, tokens, err := verifyProvisioningContractSignup(svc)
	if err == nil || tokens != nil || len(deps.refresh.tokens) != 0 {
		t.Fatal("signup proof adopted an unrelated existing-email account")
	}
}
