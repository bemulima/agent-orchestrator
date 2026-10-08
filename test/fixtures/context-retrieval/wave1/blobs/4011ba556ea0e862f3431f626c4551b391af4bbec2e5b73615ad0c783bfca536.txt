package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/port"
	"golang.org/x/crypto/bcrypt"
)

type (
	// UserManageService exposes administrative operations over users.
	UserManageService interface {
		GetUser(ctx context.Context, userID string) (*domain.User, error)
		CreateUser(ctx context.Context, req CreateUserRequest) (*domain.User, error)
		UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*domain.User, error)
		ChangeStatus(ctx context.Context, userID string, status domain.UserStatus) (*domain.User, error)
		ChangeRole(ctx context.Context, userID, role string) error
		ListUsers(ctx context.Context, offset, limit int) ([]domain.User, int64, error)
	}

	// CreateUserRequest carries legacy administrative creation fields. Credential
	// ownership is ms-go-auth; the current User compatibility method does not persist Password.
	CreateUserRequest struct {
		Email        string
		Password     string
		DisplayName  *string
		AvatarFileID *string
		Role         string
		Status       domain.UserStatus
	}

	// UpdateUserRequest carries legacy administrative update fields. Credential
	// ownership is ms-go-auth; the current User compatibility method does not persist Password.
	UpdateUserRequest struct {
		Email        *string
		Password     *string
		DisplayName  *string
		AvatarFileID *string
	}
)

type userManageService struct {
	users    port.UserRepository
	profiles port.UserProfileRepository
	rbac     port.RBACClient
}

// NewUserManageService constructs administrative user operations over persistence and RBAC ports.
func NewUserManageService(users port.UserRepository, profiles port.UserProfileRepository, rbacClient port.RBACClient) UserManageService {
	return &userManageService{users: users, profiles: profiles, rbac: rbacClient}
}

func (s *userManageService) GetUser(ctx context.Context, userID string) (*domain.User, error) {
	return s.users.FindByID(ctx, userID)
}

func (s *userManageService) CreateUser(ctx context.Context, req CreateUserRequest) (*domain.User, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}
	status := req.Status
	if status == "" {
		status = domain.UserStatusActive
	}
	if !status.IsValid() {
		return nil, fmt.Errorf("invalid status")
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	if _, err := s.users.FindByEmail(ctx, email); err == nil {
		return nil, fmt.Errorf("user already exists")
	} else if !port.IsNotFound(err) {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user := &domain.User{Email: email}
	if err := user.SetStatus(status); err != nil {
		return nil, err
	}
	user.SetPasswordHash(string(hash))
	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}

	profile := &domain.UserProfile{
		UserID:       user.ID,
		DisplayName:  req.DisplayName,
		AvatarFileID: req.AvatarFileID,
	}
	if err := s.profiles.Create(ctx, profile); err != nil {
		_ = s.users.Delete(ctx, user.ID)
		return nil, err
	}

	if s.rbac == nil {
		_ = s.users.Delete(ctx, user.ID)
		return nil, fmt.Errorf("rbac client not configured")
	}
	if err := s.rbac.AssignRole(ctx, user.ID, role); err != nil {
		_ = s.users.Delete(ctx, user.ID)
		return nil, err
	}

	user.Profile = profile
	return user, nil
}

func (s *userManageService) UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*domain.User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if err := validateEmail(email); err != nil {
			return nil, err
		}
		existing, err := s.users.FindByEmail(ctx, email)
		if err != nil && !port.IsNotFound(err) {
			return nil, err
		}
		if existing != nil && existing.ID != userID {
			return nil, fmt.Errorf("email already in use")
		}
		user.Email = email
	}

	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		user.SetPasswordHash(string(hash))
	}

	if req.DisplayName != nil || req.AvatarFileID != nil {
		profile, err := s.profiles.FindByUserID(ctx, userID)
		if err != nil {
			return nil, err
		}
		profile.Update(req.DisplayName, req.AvatarFileID)
		if err := s.profiles.Update(ctx, profile); err != nil {
			return nil, err
		}
		user.Profile = profile
	}

	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userManageService) ChangeStatus(ctx context.Context, userID string, status domain.UserStatus) (*domain.User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := user.SetStatus(status); err != nil {
		return nil, err
	}
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userManageService) ChangeRole(ctx context.Context, userID, role string) error {
	role = strings.TrimSpace(role)
	if role == "" {
		return fmt.Errorf("role is required")
	}
	if s.rbac == nil {
		return fmt.Errorf("rbac client not configured")
	}
	if _, err := s.users.FindByID(ctx, userID); err != nil {
		return err
	}
	return s.rbac.AssignRole(ctx, userID, role)
}

func (s *userManageService) ListUsers(ctx context.Context, offset, limit int) ([]domain.User, int64, error) {
	return s.users.List(ctx, offset, limit)
}

func validateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid email")
	}
	return nil
}

func validatePassword(pwd string) error {
	if len(pwd) < 6 {
		return fmt.Errorf("password too short")
	}
	return nil
}
