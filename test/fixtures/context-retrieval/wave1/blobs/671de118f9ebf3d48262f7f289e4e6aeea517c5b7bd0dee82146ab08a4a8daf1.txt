// Package postgres contains PostgreSQL persistence adapters for user data.
package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
)

// ActiveUserRepository resolves active user IDs with one bounded database query.
type ActiveUserRepository struct {
	db *gorm.DB
}

// NewActiveUserRepository creates the active-user query adapter.
func NewActiveUserRepository(db *gorm.DB) *ActiveUserRepository {
	return &ActiveUserRepository{db: db}
}

// ListActiveIDs returns the subset that is currently allowed to participate.
func (r *ActiveUserRepository) ListActiveIDs(ctx context.Context, userIDs []string) ([]string, error) {
	result := make([]string, 0, len(userIDs))
	err := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id IN ?", userIDs).
		Where("is_active = ?", true).
		Where("status IN ?", []domain.UserStatus{domain.UserStatusNew, domain.UserStatusActive}).
		Pluck("id", &result).Error
	return result, err
}
