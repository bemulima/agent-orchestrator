package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
)

// UserRepository stores and retrieves user lifecycle records.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	Update(ctx context.Context, user *domain.User) error
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
	FindByID(ctx context.Context, id string) (*domain.User, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, offset, limit int) ([]domain.User, int64, error)
}

type gormUserRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a PostgreSQL-backed user repository.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &gormUserRepository{db: db}
}

func (r *gormUserRepository) Create(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *gormUserRepository) Update(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

func (r *gormUserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).Preload("Profile").Where("email = ?", email).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *gormUserRepository) FindByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).Preload("Profile").Where("id = ?", id).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *gormUserRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&domain.User{}, "id = ?", id).Error
}

func (r *gormUserRepository) List(ctx context.Context, offset, limit int) ([]domain.User, int64, error) {
	var users []domain.User
	var count int64
	query := r.db.WithContext(ctx).Model(&domain.User{})
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Preload("Profile").Offset(offset).Limit(limit).Order("created_at DESC").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, count, nil
}
