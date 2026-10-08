package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
)

// UserProviderRepository stores and retrieves linked provider accounts.
type UserProviderRepository interface {
	Create(ctx context.Context, provider *domain.UserProvider) error
	Update(ctx context.Context, provider *domain.UserProvider) error
	Delete(ctx context.Context, id string) error
	FindByProvider(ctx context.Context, providerType, providerUserID string) (*domain.UserProvider, error)
	FindByUserID(ctx context.Context, userID string) ([]domain.UserProvider, error)
}

type gormUserProviderRepository struct {
	db *gorm.DB
}

// NewUserProviderRepository creates a PostgreSQL-backed provider repository.
func NewUserProviderRepository(db *gorm.DB) UserProviderRepository {
	return &gormUserProviderRepository{db: db}
}

func (r *gormUserProviderRepository) Create(ctx context.Context, provider *domain.UserProvider) error {
	return r.db.WithContext(ctx).Create(provider).Error
}

func (r *gormUserProviderRepository) Update(ctx context.Context, provider *domain.UserProvider) error {
	return r.db.WithContext(ctx).Save(provider).Error
}

func (r *gormUserProviderRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&domain.UserProvider{}, "id = ?", id).Error
}

func (r *gormUserProviderRepository) FindByProvider(ctx context.Context, providerType, providerUserID string) (*domain.UserProvider, error) {
	var provider domain.UserProvider
	if err := r.db.WithContext(ctx).Where("provider_type = ? AND provider_user_id = ?", providerType, providerUserID).First(&provider).Error; err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *gormUserProviderRepository) FindByUserID(ctx context.Context, userID string) ([]domain.UserProvider, error) {
	var providers []domain.UserProvider
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}
