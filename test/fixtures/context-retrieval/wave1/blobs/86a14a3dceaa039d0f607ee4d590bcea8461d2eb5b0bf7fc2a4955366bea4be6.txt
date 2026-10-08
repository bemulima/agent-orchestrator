package port

import (
	"context"

	"github.com/example/user-service/internal/domain"
)

// UserRepository is the application persistence port for user lifecycle data.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	Update(ctx context.Context, user *domain.User) error
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
	FindByID(ctx context.Context, id string) (*domain.User, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, offset, limit int) ([]domain.User, int64, error)
}

// UserProfileRepository is the application persistence port for profiles.
type UserProfileRepository interface {
	Create(ctx context.Context, profile *domain.UserProfile) error
	Update(ctx context.Context, profile *domain.UserProfile) error
	FindByUserID(ctx context.Context, userID string) (*domain.UserProfile, error)
}

// UserIdentityRepository is the application persistence port for identities.
type UserIdentityRepository interface {
	Create(ctx context.Context, identity *domain.UserIdentity) error
	FindByProviderUserID(ctx context.Context, provider domain.IdentityProvider, providerUserID string) (*domain.UserIdentity, error)
	FindByUserAndProvider(ctx context.Context, userID string, provider domain.IdentityProvider) (*domain.UserIdentity, error)
	ListByUser(ctx context.Context, userID string) ([]domain.UserIdentity, error)
	Delete(ctx context.Context, identity *domain.UserIdentity) error
}

// ActiveUserRepository resolves active user IDs with one bounded query.
type ActiveUserRepository interface {
	ListActiveIDs(ctx context.Context, userIDs []string) ([]string, error)
}

// RBACClient is the application port for role and permission management.
type RBACClient interface {
	GetRoleByUserID(ctx context.Context, userID string) (string, error)
	GetPermissionsByUserID(ctx context.Context, userID string) ([]string, error)
	CheckPermission(ctx context.Context, userID, permission string) (bool, error)
	CheckRole(ctx context.Context, userID, role string) (bool, error)
	AssignRole(ctx context.Context, userID, role string) error
}

// IsNotFound recognizes the stable error returned by the current persistence
// implementation without exposing that implementation to application code.
func IsNotFound(err error) bool {
	return err != nil && err.Error() == "record not found"
}
