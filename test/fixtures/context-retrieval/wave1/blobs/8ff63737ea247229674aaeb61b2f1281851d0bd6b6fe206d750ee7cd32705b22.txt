package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/infrastructure/filestorage"
	v1 "github.com/example/user-service/internal/transport/http/api/v1/handlers"
)

func TestUploadAvatar_Success(t *testing.T) {
	t.Parallel()

	fs := &stubFilestorage{}
	us := &stubUserService{
		setAvatarFileIDFn: func(_ context.Context, userID, avatarFileID string) (*domain.UserProfile, error) {
			require.Equal(t, "user-1", userID)
			require.Equal(t, "file-123", avatarFileID)
			return &domain.UserProfile{UserID: userID, AvatarFileID: &avatarFileID}, nil
		},
	}
	handler := v1.NewHandler(us, fs, nil, "avatar", "USER_MEDIA")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	require.NoError(t, err)
	_, _ = part.Write([]byte("img"))
	require.NoError(t, writer.Close())

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("user_id", "user-1")

	require.NoError(t, handler.UploadAvatar(c))
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "file-123", resp.Data["file_id"])
	require.Equal(t, "http://filestorage/files/file-123/signed", resp.Data["download_url"])

	require.Equal(t, "USER_MEDIA", fs.uploadReq.FileKind)
	require.Equal(t, "user-1", fs.uploadReq.OwnerID)
}

func TestUploadAvatar_TooLarge(t *testing.T) {
	t.Parallel()

	fs := &stubFilestorage{}
	us := &stubUserService{}
	handler := v1.NewHandler(us, fs, nil, "avatar", "USER_MEDIA")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	require.NoError(t, err)
	_, _ = part.Write(bytes.Repeat([]byte("a"), 5*1024*1024+1))
	require.NoError(t, writer.Close())

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("user_id", "user-1")

	require.NoError(t, handler.UploadAvatar(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUploadAvatar_EagerTriggersImageProcessor(t *testing.T) {
	t.Parallel()

	fs := &stubFilestorage{}
	proc := &stubImageProc{}
	us := &stubUserService{}
	handler := v1.NewHandler(us, fs, proc, "avatar", "USER_MEDIA")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	require.NoError(t, err)
	_, _ = part.Write([]byte("img"))
	_ = writer.WriteField("processing_mode", "EAGER")
	require.NoError(t, writer.Close())

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("user_id", "user-1")

	require.NoError(t, handler.UploadAvatar(c))
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "file-123", fs.delegationFileID)
	require.Equal(t, "user-1", fs.delegationOwnerID)
	require.Equal(t, "image_processor", fs.delegationService)
	require.Equal(t, "read_source", fs.delegationScope)
	require.Zero(t, fs.delegationTTLSeconds, "zero TTL selects FileStorage's 300 second default")
	require.Equal(t, "file-123", proc.lastOriginal)
	require.Equal(t, "user-1", proc.lastOwner)
	require.Equal(t, "USER_MEDIA", proc.lastKind)
	require.Equal(t, "avatar", proc.lastPreset)
	require.Equal(t, "opaque-processing-delegation", proc.lastDelegation)
	require.NotContains(t, rec.Body.String(), "opaque-processing-delegation")
	require.NotContains(t, rec.Body.String(), `"processing_delegation"`)
	require.NotContains(t, rec.Body.String(), `"delegation"`)
}

func TestUploadAvatar_DoesNotExposeDownstreamErrors(t *testing.T) {
	const downstreamError = "filestorage rejected request: internal_token=synthetic-secret"

	for _, tc := range []struct {
		name          string
		uploadErr     error
		delegationErr error
		imageErr      error
		wantStatus    int
		wantPublic    string
	}{
		{
			name:       "upload",
			uploadErr:  errors.New(downstreamError),
			wantStatus: http.StatusBadRequest,
			wantPublic: "avatar upload failed",
		},
		{
			name:          "processing delegation",
			delegationErr: errors.New(downstreamError),
			wantStatus:    http.StatusInternalServerError,
			wantPublic:    "avatar processing failed",
		},
		{
			name:       "image processing",
			imageErr:   errors.New(downstreamError),
			wantStatus: http.StatusInternalServerError,
			wantPublic: "avatar processing failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &stubFilestorage{uploadErr: tc.uploadErr, delegationErr: tc.delegationErr}
			proc := &stubImageProc{}
			proc.err = tc.imageErr
			handler := v1.NewHandler(&stubUserService{}, fs, proc, "avatar", "USER_MEDIA")

			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, err := writer.CreateFormFile("file", "avatar.png")
			require.NoError(t, err)
			_, _ = part.Write([]byte("img"))
			_ = writer.WriteField("processing_mode", "EAGER")
			require.NoError(t, writer.Close())

			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", body)
			req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.Set("user_id", "user-1")

			require.NoError(t, handler.UploadAvatar(c))
			require.Equal(t, tc.wantStatus, rec.Code)
			require.Contains(t, rec.Body.String(), tc.wantPublic)
			require.NotContains(t, rec.Body.String(), "synthetic-secret")
			require.NotContains(t, rec.Body.String(), downstreamError)
		})
	}
}

type stubFilestorage struct {
	uploadReq            filestorage.UploadRequest
	uploadErr            error
	delegationErr        error
	delegationFileID     string
	delegationOwnerID    string
	delegationService    string
	delegationScope      string
	delegationTTLSeconds int64
}

func (s *stubFilestorage) Upload(_ context.Context, req filestorage.UploadRequest) (*filestorage.UploadResponse, error) {
	s.uploadReq = req
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return &filestorage.UploadResponse{ID: "file-123"}, nil
}

func (s *stubFilestorage) SignedURL(_ context.Context, id string, _ int64) (string, error) {
	return "http://filestorage/files/" + id + "/signed", nil
}

func (s *stubFilestorage) CreateProcessingDelegation(_ context.Context, fileID, ownerID, delegateService, scope string, ttlSeconds int64) (string, error) {
	s.delegationFileID = fileID
	s.delegationOwnerID = ownerID
	s.delegationService = delegateService
	s.delegationScope = scope
	s.delegationTTLSeconds = ttlSeconds
	if s.delegationErr != nil {
		return "", s.delegationErr
	}
	return "opaque-processing-delegation", nil
}

func (s *stubFilestorage) DownloadURL(id string) string {
	return "http://filestorage/files/" + id + "/download"
}

type stubImageProc struct {
	lastOriginal   string
	lastOwner      string
	lastKind       string
	lastPreset     string
	lastDelegation string
	err            error
}

func (s *stubImageProc) GenerateWithDelegation(_ context.Context, originalID, ownerID, fileKind, presetGroup string, _ []string, processingDelegation string) error {
	s.lastOriginal = originalID
	s.lastOwner = ownerID
	s.lastKind = fileKind
	s.lastPreset = presetGroup
	s.lastDelegation = processingDelegation
	return s.err
}

type stubUserService struct {
	updateProfileFn   func(ctx context.Context, userID string, displayName *string) (*domain.UserProfile, error)
	setAvatarFileIDFn func(ctx context.Context, userID, avatarFileID string) (*domain.UserProfile, error)
}

func (s *stubUserService) GetMe(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}
func (s *stubUserService) GetByID(_ context.Context, _, _ string) (*domain.User, error) {
	return nil, nil
}
func (s *stubUserService) UpdateProfile(ctx context.Context, userID string, displayName *string) (*domain.UserProfile, error) {
	if s.updateProfileFn != nil {
		return s.updateProfileFn(ctx, userID, displayName)
	}
	return &domain.UserProfile{UserID: userID}, nil
}
func (s *stubUserService) SetAvatarFileID(ctx context.Context, userID, avatarFileID string) (*domain.UserProfile, error) {
	if s.setAvatarFileIDFn != nil {
		return s.setAvatarFileIDFn(ctx, userID, avatarFileID)
	}
	return &domain.UserProfile{UserID: userID, AvatarFileID: &avatarFileID}, nil
}
func (s *stubUserService) StartEmailChange(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (s *stubUserService) VerifyEmailChange(_ context.Context, _, _, _ string) (*domain.User, error) {
	return nil, nil
}
func (s *stubUserService) AttachIdentity(_ context.Context, _ string, _ domain.IdentityProvider, _, _ string, _, _ *string) (*domain.UserIdentity, *domain.UserProfile, error) {
	return nil, nil, nil
}
func (s *stubUserService) RemoveIdentity(_ context.Context, _ string, _ domain.IdentityProvider, _ string) error {
	return nil
}
func (s *stubUserService) ListIdentities(_ context.Context, _ string) ([]domain.UserIdentity, error) {
	return nil, nil
}
