package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
)

// UserProfileRepository stores and retrieves user profile data.
type UserProfileRepository interface {
	Create(ctx context.Context, profile *domain.UserProfile) error
	Update(ctx context.Context, profile *domain.UserProfile) error
	FindByUserID(ctx context.Context, userID string) (*domain.UserProfile, error)
}

type gormUserProfileRepository struct {
	db *gorm.DB
}

// NewUserProfileRepository creates a PostgreSQL-backed profile repository.
func NewUserProfileRepository(db *gorm.DB) UserProfileRepository {
	return &gormUserProfileRepository{db: db}
}

func (r *gormUserProfileRepository) Create(ctx context.Context, profile *domain.UserProfile) error {
	return r.db.WithContext(ctx).Create(profile).Error
}

func (r *gormUserProfileRepository) Update(ctx context.Context, profile *domain.UserProfile) error {
	return r.db.WithContext(ctx).Save(profile).Error
}

func (r *gormUserProfileRepository) FindByUserID(ctx context.Context, userID string) (*domain.UserProfile, error) {
	var profile domain.UserProfile
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}
