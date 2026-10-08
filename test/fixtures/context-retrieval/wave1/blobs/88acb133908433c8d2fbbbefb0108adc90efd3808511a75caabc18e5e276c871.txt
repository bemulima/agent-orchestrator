package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
)

// UserIdentityRepository stores and retrieves linked user identities.
type UserIdentityRepository interface {
	Create(ctx context.Context, identity *domain.UserIdentity) error
	FindByProviderUserID(ctx context.Context, provider domain.IdentityProvider, providerUserID string) (*domain.UserIdentity, error)
	FindByUserAndProvider(ctx context.Context, userID string, provider domain.IdentityProvider) (*domain.UserIdentity, error)
	ListByUser(ctx context.Context, userID string) ([]domain.UserIdentity, error)
	Delete(ctx context.Context, identity *domain.UserIdentity) error
}

type gormUserIdentityRepository struct {
	db *gorm.DB
}

// NewUserIdentityRepository creates a PostgreSQL-backed identity repository.
func NewUserIdentityRepository(db *gorm.DB) UserIdentityRepository {
	return &gormUserIdentityRepository{db: db}
}

func (r *gormUserIdentityRepository) Create(ctx context.Context, identity *domain.UserIdentity) error {
	return r.db.WithContext(ctx).Create(identity).Error
}

func (r *gormUserIdentityRepository) FindByProviderUserID(ctx context.Context, provider domain.IdentityProvider, providerUserID string) (*domain.UserIdentity, error) {
	var identity domain.UserIdentity
	if err := r.db.WithContext(ctx).Where("provider = ? AND provider_user_id = ?", provider, providerUserID).First(&identity).Error; err != nil {
		return nil, err
	}
	return &identity, nil
}

func (r *gormUserIdentityRepository) FindByUserAndProvider(ctx context.Context, userID string, provider domain.IdentityProvider) (*domain.UserIdentity, error) {
	var identity domain.UserIdentity
	if err := r.db.WithContext(ctx).Where("user_id = ? AND provider = ?", userID, provider).First(&identity).Error; err != nil {
		return nil, err
	}
	return &identity, nil
}

func (r *gormUserIdentityRepository) ListByUser(ctx context.Context, userID string) ([]domain.UserIdentity, error) {
	var identities []domain.UserIdentity
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&identities).Error; err != nil {
		return nil, err
	}
	return identities, nil
}

func (r *gormUserIdentityRepository) Delete(ctx context.Context, identity *domain.UserIdentity) error {
	return r.db.WithContext(ctx).Delete(identity).Error
}
