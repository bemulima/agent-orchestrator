package domain

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound lets use cases express absent auth state without depending on a
// persistence implementation.
var ErrNotFound = errors.New("auth record not found")

type AuthUserRepository interface {
	Create(context.Context, *AuthUser) error
	FindByEmail(context.Context, string) (*AuthUser, error)
	FindByID(context.Context, string) (*AuthUser, error)
	Update(context.Context, *AuthUser) error
}

type VerificationIdentityRepository interface {
	CreateVerificationIfAbsent(context.Context, *AuthUser) (*AuthUser, bool, error)
}

// SignupCompletionRepository must use durable atomic writes. An adapter lacking
// this port cannot safely consume signup proofs or inspect pending principals.
type SignupCompletionRepository interface {
	BeginSignupCompletion(context.Context, string, string, string, string) (*SignupCompletion, error)
	StoreSignupReceipt(context.Context, string, *SignupProofReceipt) (*SignupCompletion, error)
	EnsureSignupPrincipal(context.Context, string) (*AuthUser, error)
	CompleteSignup(context.Context, string, time.Time, bool) (bool, error)
	SignupPrincipalPending(context.Context, string) (bool, error)
	FindSignupCompletionByPrincipal(context.Context, string) (*SignupCompletion, error)
}

type SignupProofConsumer interface {
	ConsumeSignupProof(context.Context, string, string, string) (*SignupProofReceipt, error)
}

type AuthIdentityRepository interface {
	FindByProvider(context.Context, string, string) (*AuthIdentity, error)
	Create(context.Context, *AuthIdentity) error
	ResolveUser(context.Context, *AuthIdentity) (*AuthUser, bool, error)
	ListByUser(context.Context, string) ([]AuthIdentity, error)
	Delete(context.Context, string, string, string) error
}

type OAuthTransactionRepository interface {
	Create(context.Context, *OAuthTransaction) error
	Consume(context.Context, string, string, time.Time) (*OAuthTransaction, error)
	DeleteExpired(context.Context, time.Time) error
}

type RefreshTokenRepository interface {
	Create(context.Context, *RefreshToken) error
	FindActive(context.Context, string) (*RefreshToken, error)
	RevokeByHash(context.Context, string) error
}

type VerificationClient interface {
	StartSignup(context.Context, string, string) error
	VerifySignup(context.Context, string, string) (string, error)
	StartEmailChange(context.Context, string, string) (string, error)
	VerifyEmailChange(context.Context, string) (string, string, error)
	StartPasswordReset(context.Context, string) (string, error)
	VerifyPasswordReset(context.Context, string, string) error
}

type UserProvisionRequest struct {
	ID           string        `json:"id"`
	Email        string        `json:"email"`
	Source       string        `json:"source"`
	Type         string        `json:"type"`
	OAuthProfile *OAuthProfile `json:"oauth_profile,omitempty"`
}

type OAuthProfile struct {
	Provider  string `json:"provider"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	BirthYear *int   `json:"birth_year,omitempty"`
	Gender    string `json:"gender,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type UserProvisioner interface {
	CreateUser(context.Context, UserProvisionRequest) error
}

// SignupRoleProvisioner binds the accepted signup grant to its persisted operation.
// Generic role mutation is deliberately a separate, non-authoritative port.
type SignupRoleProvisioner interface {
	AssignSignupRole(context.Context, string, string) error
}

type RoleClient interface {
	AssignRole(context.Context, string, string) error
	CheckRole(context.Context, string, string) (bool, error)
}

type OAuthProviderName string

type OAuthProfileData struct {
	ProviderUserID string
	Email          string
	EmailVerified  bool
	DisplayName    string
	FirstName      string
	LastName       string
	BirthYear      *int
	Gender         string
	AvatarURL      string
	RawProfile     map[string]interface{}
}

type OAuthProvider interface {
	Name() OAuthProviderName
	Validate() error
	AuthorizationURL(string, string) string
	Authenticate(context.Context, string, string) (*OAuthProfileData, error)
}

type OAuthRegistry interface {
	Get(string) (OAuthProvider, error)
}
