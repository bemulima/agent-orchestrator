// Package port defines application interfaces implemented by user service adapters.
package port

import (
	"context"

	"github.com/example/user-service/internal/domain"
)

// UserProfileWriter persists changes to a user's profile.
type UserProfileWriter interface {
	Update(ctx context.Context, profile *domain.UserProfile) error
}

// OAuthAvatar contains downloaded image bytes and their validated media metadata.
type OAuthAvatar struct {
	Data        []byte
	ContentType string
	FileName    string
}

// OAuthAvatarDownloader retrieves an avatar from a supported provider URL.
type OAuthAvatarDownloader interface {
	Download(ctx context.Context, provider, rawURL string) (*OAuthAvatar, error)
}

// OAuthAvatarUpload describes an avatar write owned by a user and file category.
type OAuthAvatarUpload struct {
	OwnerID  string
	FileKind string
	Avatar   *OAuthAvatar
}

// OAuthAvatarStorage stores a downloaded avatar and returns its opaque file ID.
type OAuthAvatarStorage interface {
	Store(ctx context.Context, upload OAuthAvatarUpload) (string, error)
}
