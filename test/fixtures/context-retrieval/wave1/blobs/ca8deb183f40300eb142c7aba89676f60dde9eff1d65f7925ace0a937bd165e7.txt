package avatarstorage

import (
	"context"
	"testing"

	"github.com/example/user-service/internal/infrastructure/filestorage"
	"github.com/example/user-service/internal/port"
)

type fileStorageStub struct {
	request filestorage.UploadRequest
}

func (s *fileStorageStub) Upload(_ context.Context, request filestorage.UploadRequest) (*filestorage.UploadResponse, error) {
	s.request = request
	return &filestorage.UploadResponse{ID: "file-1"}, nil
}
func (*fileStorageStub) SignedURL(context.Context, string, int64) (string, error) { return "", nil }
func (*fileStorageStub) DownloadURL(string) string                                { return "" }

func TestClientStoresOAuthAvatarInFileStorage(t *testing.T) {
	storage := &fileStorageStub{}
	client := NewClient(storage)
	id, err := client.Store(context.Background(), port.OAuthAvatarUpload{
		OwnerID: "user-1", FileKind: "USER_MEDIA",
		Avatar: &port.OAuthAvatar{Data: []byte("image"), ContentType: "image/png", FileName: "avatar.png"},
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if id != "file-1" || storage.request.OwnerID != "user-1" || storage.request.FileName != "avatar.png" || storage.request.ContentType != "image/png" {
		t.Fatalf("unexpected upload: id=%s request=%+v", id, storage.request)
	}
}
