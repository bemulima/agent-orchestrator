package service

import (
	"context"
	"testing"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/port"
)

type oauthProfileRepoStub struct {
	updates int
}

func (r *oauthProfileRepoStub) Create(context.Context, *domain.UserProfile) error { return nil }
func (r *oauthProfileRepoStub) Update(_ context.Context, _ *domain.UserProfile) error {
	r.updates++
	return nil
}
func (r *oauthProfileRepoStub) FindByUserID(context.Context, string) (*domain.UserProfile, error) {
	return nil, nil
}

type oauthAvatarClientStub struct {
	calls int
}

func (c *oauthAvatarClientStub) Download(context.Context, string, string) (*port.OAuthAvatar, error) {
	c.calls++
	return &port.OAuthAvatar{Data: []byte("image"), ContentType: "image/png", FileName: "oauth-avatar.png"}, nil
}

type oauthAvatarStorageStub struct {
	calls int
}

func (s *oauthAvatarStorageStub) Store(_ context.Context, request port.OAuthAvatarUpload) (string, error) {
	s.calls++
	if request.OwnerID != "user-1" || request.FileKind != "USER_MEDIA" || request.Avatar == nil || request.Avatar.ContentType != "image/png" {
		return "", context.Canceled
	}
	return "file-1", nil
}

func TestOAuthProfileImporterFillsMissingFieldsAndStoresAvatar(t *testing.T) {
	repo := &oauthProfileRepoStub{}
	avatarClient := &oauthAvatarClientStub{}
	storage := &oauthAvatarStorageStub{}
	importer := NewOAuthProfileImporter(repo, storage, avatarClient, "USER_MEDIA")
	manualFirstName := "Manual"
	profile := &domain.UserProfile{UserID: "user-1", FirstName: &manualFirstName}
	birthYear := 1998

	err := importer.Import(context.Background(), profile, OAuthProfileInput{
		Provider: "google", FirstName: "Provider", LastName: "Lovelace",
		BirthYear: &birthYear, Gender: " FEMALE ", AvatarURL: "https://lh3.googleusercontent.com/a/avatar",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if profile.FirstName == nil || *profile.FirstName != manualFirstName {
		t.Fatalf("existing first name was overwritten: %+v", profile.FirstName)
	}
	if profile.LastName == nil || *profile.LastName != "Lovelace" || profile.BirthYear == nil || *profile.BirthYear != birthYear || profile.Gender == nil || *profile.Gender != "female" {
		t.Fatalf("profile fields were not imported: %+v", profile)
	}
	if profile.AvatarFileID == nil || *profile.AvatarFileID != "file-1" || avatarClient.calls != 1 || storage.calls != 1 || repo.updates != 2 {
		t.Fatalf("avatar was not persisted: profile=%+v downloads=%d uploads=%d updates=%d", profile, avatarClient.calls, storage.calls, repo.updates)
	}
}

func TestOAuthProfileImporterKeepsExistingAvatar(t *testing.T) {
	repo := &oauthProfileRepoStub{}
	avatarClient := &oauthAvatarClientStub{}
	storage := &oauthAvatarStorageStub{}
	importer := NewOAuthProfileImporter(repo, storage, avatarClient, "USER_MEDIA")
	avatarID := "manual-avatar"
	profile := &domain.UserProfile{UserID: "user-1", AvatarFileID: &avatarID}

	if err := importer.Import(context.Background(), profile, OAuthProfileInput{Provider: "github", AvatarURL: "https://avatars.githubusercontent.com/u/1"}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if avatarClient.calls != 0 || storage.calls != 0 || profile.AvatarFileID == nil || *profile.AvatarFileID != avatarID {
		t.Fatalf("existing avatar must be preserved")
	}
}
