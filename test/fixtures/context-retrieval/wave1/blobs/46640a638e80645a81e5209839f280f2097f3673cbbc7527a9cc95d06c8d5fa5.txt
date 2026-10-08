// Package avatarstorage stores provider avatars through the FileStorage client.
package avatarstorage

import (
	"context"
	"fmt"

	"github.com/example/user-service/internal/infrastructure/filestorage"
	"github.com/example/user-service/internal/port"
)

// Client adapts FileStorage uploads to the OAuth avatar storage port.
type Client struct {
	storage filestorage.Client
}

// NewClient creates an OAuth avatar storage adapter.
func NewClient(storage filestorage.Client) *Client {
	return &Client{storage: storage}
}

// Store uploads the imported avatar and returns its FileStorage ID.
func (c *Client) Store(ctx context.Context, upload port.OAuthAvatarUpload) (string, error) {
	if c.storage == nil || upload.Avatar == nil {
		return "", fmt.Errorf("avatar storage is not configured")
	}
	result, err := c.storage.Upload(ctx, filestorage.UploadRequest{
		OwnerID: upload.OwnerID, FileKind: upload.FileKind,
		FileName: upload.Avatar.FileName, ContentType: upload.Avatar.ContentType, Data: upload.Avatar.Data,
	})
	if err != nil {
		return "", err
	}
	return result.ID, nil
}
