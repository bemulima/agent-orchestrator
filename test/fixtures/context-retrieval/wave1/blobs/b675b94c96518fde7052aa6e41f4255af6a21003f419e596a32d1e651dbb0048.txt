package usecase

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/example/auth-service/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

var (
	verificationRunIDPattern = regexp.MustCompile(`^[a-z0-9]{8,20}$`)
	verificationUUIDPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const verificationLearnerRole = "student"

// VerificationFixtureInput deliberately has no role or email field: this
// closed fixture can only provision the run-derived synthetic student.
type VerificationFixtureInput struct {
	RunID    string
	UserID   string
	Password string
}

type VerificationFixtureResult struct {
	Status     string `json:"status"`
	RunID      string `json:"run_id"`
	IdentityID string `json:"identity_id"`
	Role       string `json:"role"`
}

// VerificationFixtureError exposes only safe phase metadata. The underlying
// database or NATS error is intentionally not included in Error().
type VerificationFixtureError struct {
	Status     string
	Stage      string
	RunID      string
	IdentityID string
}

func (e *VerificationFixtureError) Error() string {
	return fmt.Sprintf("verification fixture %s at %s", e.Status, e.Stage)
}

type VerificationFixture struct {
	users domain.VerificationIdentityRepository
	roles domain.RoleClient
}

func NewVerificationFixture(users domain.VerificationIdentityRepository, roles domain.RoleClient) *VerificationFixture {
	return &VerificationFixture{users: users, roles: roles}
}

func (f *VerificationFixture) Provision(ctx context.Context, input VerificationFixtureInput) (*VerificationFixtureResult, error) {
	if f == nil || f.users == nil || f.roles == nil {
		return nil, fixtureError("failed", "owner_dependencies", input)
	}
	if !verificationRunIDPattern.MatchString(input.RunID) || !verificationUUIDPattern.MatchString(input.UserID) {
		return nil, fixtureError("failed", "input", input)
	}
	if input.Password == "" || len(input.Password) < 8 || len(input.Password) > 72 || strings.ContainsAny(input.Password, "\x00\r\n") {
		return nil, fixtureError("failed", "input", input)
	}
	email := "v1+" + input.RunID + "@verification.invalid"
	if err := validateEmail(email); err != nil {
		return nil, fixtureError("failed", "input", input)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fixtureError("failed", "password_hash", input)
	}
	now := time.Now().UTC()
	newUser := &domain.AuthUser{ID: input.UserID, Email: email, PasswordHash: ptr(string(hash)), PasswordUpdatedAt: &now}
	user, created, err := f.users.CreateVerificationIfAbsent(ctx, newUser)
	if err != nil || user == nil {
		return nil, fixtureError("failed", "auth_identity", input)
	}

	if !created {
		if user.ID != input.UserID || user.Email != email || user.PasswordHash == nil ||
			bcrypt.CompareHashAndPassword([]byte(deref(user.PasswordHash)), []byte(input.Password)) != nil {
			return nil, fixtureError("conflict", "existing_identity", input)
		}
		assigned, checkErr := f.roles.CheckRole(ctx, user.ID, verificationLearnerRole)
		if checkErr != nil || !assigned {
			return nil, fixtureError("incomplete", "existing_role_readback", input)
		}
		return fixtureResult("already_present", input), nil
	}

	assignErr := f.roles.AssignRole(ctx, user.ID, verificationLearnerRole)
	assigned, checkErr := f.roles.CheckRole(ctx, user.ID, verificationLearnerRole)
	if checkErr == nil && assigned {
		return fixtureResult("created", input), nil
	}
	if assignErr != nil || checkErr != nil || !assigned {
		return nil, fixtureError("incomplete", "rbac_assignment", input)
	}
	return nil, errors.New("unreachable verification fixture state")
}

func fixtureResult(status string, input VerificationFixtureInput) *VerificationFixtureResult {
	return &VerificationFixtureResult{Status: status, RunID: input.RunID, IdentityID: input.UserID, Role: verificationLearnerRole}
}

func fixtureError(status, stage string, input VerificationFixtureInput) *VerificationFixtureError {
	identityID := ""
	if verificationUUIDPattern.MatchString(input.UserID) {
		identityID = input.UserID
	}
	return &VerificationFixtureError{Status: status, Stage: stage, RunID: input.RunID, IdentityID: identityID}
}

func ptr[T any](value T) *T { return &value }

func deref[T any](value *T) T { return *value }
