package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/port"
)

// OAuthProfileInput contains provider-supplied fields eligible for missing-field import.
type OAuthProfileInput struct {
	Provider  string
	FirstName string
	LastName  string
	BirthYear *int
	Gender    string
	AvatarURL string
}

// OAuthProfileImporter applies eligible provider profile fields and optional avatar data.
type OAuthProfileImporter interface {
	Import(ctx context.Context, profile *domain.UserProfile, input OAuthProfileInput) error
}

type oauthProfileImporter struct {
	profiles     port.UserProfileWriter
	storage      port.OAuthAvatarStorage
	avatarClient port.OAuthAvatarDownloader
	avatarKind   string
}

// NewOAuthProfileImporter wires profile persistence, avatar download, and storage ports.
func NewOAuthProfileImporter(
	profiles port.UserProfileWriter,
	storage port.OAuthAvatarStorage,
	avatarClient port.OAuthAvatarDownloader,
	avatarKind string,
) OAuthProfileImporter {
	return &oauthProfileImporter{
		profiles: profiles, storage: storage, avatarClient: avatarClient, avatarKind: avatarKind,
	}
}

func (s *oauthProfileImporter) Import(ctx context.Context, profile *domain.UserProfile, input OAuthProfileInput) error {
	if profile == nil {
		return fmt.Errorf("user profile is required")
	}
	profile.FillOAuthData(input.FirstName, input.LastName, input.BirthYear, input.Gender, time.Now().UTC().Year())
	if err := s.profiles.Update(ctx, profile); err != nil {
		return fmt.Errorf("save OAuth profile fields: %w", err)
	}

	if profile.AvatarFileID != nil && strings.TrimSpace(*profile.AvatarFileID) != "" {
		return nil
	}
	if strings.TrimSpace(input.AvatarURL) == "" || s.avatarClient == nil || s.storage == nil {
		return nil
	}

	image, err := s.avatarClient.Download(ctx, input.Provider, input.AvatarURL)
	if err != nil {
		return err
	}
	fileID, err := s.storage.Store(ctx, port.OAuthAvatarUpload{
		OwnerID: profile.UserID, FileKind: s.avatarKind, Avatar: image,
	})
	if err != nil {
		return fmt.Errorf("store OAuth avatar: %w", err)
	}
	profile.Update(nil, &fileID)
	if err := s.profiles.Update(ctx, profile); err != nil {
		return fmt.Errorf("save OAuth avatar: %w", err)
	}
	return nil
}
