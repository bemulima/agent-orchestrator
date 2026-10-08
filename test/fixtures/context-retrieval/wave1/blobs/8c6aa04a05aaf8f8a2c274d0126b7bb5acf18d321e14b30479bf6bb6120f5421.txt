package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/port"
)

// UserService exposes user profile and linked-identity operations to transports.
type UserService interface {
	GetMe(ctx context.Context, userID string) (*domain.User, error)
	GetByID(ctx context.Context, requesterID, targetID string) (*domain.User, error)
	UpdateProfile(ctx context.Context, userID string, displayName *string) (*domain.UserProfile, error)
	SetAvatarFileID(ctx context.Context, userID, avatarFileID string) (*domain.UserProfile, error)
	AttachIdentity(ctx context.Context, userID string, provider domain.IdentityProvider, providerUserID, email string, displayName, avatarURL *string) (*domain.UserIdentity, *domain.UserProfile, error)
	ListIdentities(ctx context.Context, userID string) ([]domain.UserIdentity, error)
	RemoveIdentity(ctx context.Context, userID string, provider domain.IdentityProvider, providerUserID string) error
}

type userService struct {
	users      port.UserRepository
	profiles   port.UserProfileRepository
	identities port.UserIdentityRepository
}

// NewUserService constructs the application service over its persistence ports.
func NewUserService(users port.UserRepository, profiles port.UserProfileRepository, identities port.UserIdentityRepository) UserService {
	return &userService{users: users, profiles: profiles, identities: identities}
}

func (s *userService) GetMe(ctx context.Context, userID string) (*domain.User, error) {
	return s.users.FindByID(ctx, userID)
}

func (s *userService) GetByID(ctx context.Context, _ string, targetID string) (*domain.User, error) {
	return s.users.FindByID(ctx, targetID)
}

func (s *userService) UpdateProfile(ctx context.Context, userID string, displayName *string) (*domain.UserProfile, error) {
	profile, err := s.profiles.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	profile.Update(displayName, nil)
	if err := s.profiles.Update(ctx, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *userService) SetAvatarFileID(ctx context.Context, userID, avatarFileID string) (*domain.UserProfile, error) {
	profile, err := s.profiles.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	profile.Update(nil, &avatarFileID)
	if err := s.profiles.Update(ctx, profile); err != nil {
		return nil, err
	}

	return profile, nil
}

func (s *userService) AttachIdentity(ctx context.Context, userID string, provider domain.IdentityProvider, providerUserID, email string, displayName, avatarURL *string) (*domain.UserIdentity, *domain.UserProfile, error) {
	if !provider.IsValid() {
		return nil, nil, fmt.Errorf("unsupported provider")
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if !strings.EqualFold(user.Email, email) {
		return nil, nil, errors.New("email does not match user")
	}
	if existing, err := s.identities.FindByProviderUserID(ctx, provider, providerUserID); err == nil {
		if existing.UserID != userID {
			return nil, nil, errors.New("identity already linked to another user")
		}
		return existing, user.Profile, nil
	}

	identity := &domain.UserIdentity{
		UserID:         userID,
		Provider:       provider,
		ProviderUserID: providerUserID,
		Email:          strings.ToLower(email),
		DisplayName:    displayName,
		AvatarURL:      avatarURL,
	}
	if err := s.identities.Create(ctx, identity); err != nil {
		return nil, nil, err
	}

	profile, err := s.profiles.FindByUserID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	profile.Update(displayName, nil)
	if err := s.profiles.Update(ctx, profile); err != nil {
		return nil, nil, err
	}

	return identity, profile, nil
}

func (s *userService) RemoveIdentity(ctx context.Context, userID string, provider domain.IdentityProvider, providerUserID string) error {
	if !provider.IsValid() {
		return fmt.Errorf("unsupported provider")
	}
	identity, err := s.identities.FindByProviderUserID(ctx, provider, providerUserID)
	if err != nil {
		return err
	}
	if identity.UserID != userID {
		return errors.New("identity does not belong to user")
	}
	return s.identities.Delete(ctx, identity)
}

func (s *userService) ListIdentities(ctx context.Context, userID string) ([]domain.UserIdentity, error) {
	return s.identities.ListByUser(ctx, userID)
}
