package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/infrastructure/filestorage"
	v1 "github.com/example/user-service/internal/transport/http/api/v1/handlers"
)

const (
	avatarDelegationTestToken = "opaque-processing-delegation-test"
	avatarDelegationTestFile  = "uploaded-avatar-file"
)

type avatarDelegationRecorder struct {
	events          []string
	uploadOwner     string
	mintOwner       string
	mintFileID      string
	processorOwner  string
	processorFileID string
	delegateService string
	scope           string
	ttlSeconds      int64
	grant           string
	mintCalls       int
}

type avatarDelegationStorageStub struct {
	recorder *avatarDelegationRecorder
}

func (s *avatarDelegationStorageStub) Upload(_ context.Context, req filestorage.UploadRequest) (*filestorage.UploadResponse, error) {
	s.recorder.events = append(s.recorder.events, "upload")
	s.recorder.uploadOwner = req.OwnerID
	return &filestorage.UploadResponse{ID: avatarDelegationTestFile}, nil
}

func (s *avatarDelegationStorageStub) SignedURL(context.Context, string, int64) (string, error) {
	s.recorder.events = append(s.recorder.events, "signed-url")
	return "https://files.example.test/signed/avatar", nil
}

func (s *avatarDelegationStorageStub) DownloadURL(string) string { return "" }

func (s *avatarDelegationStorageStub) CreateProcessingDelegation(_ context.Context, fileID, ownerID, delegateService, scope string, ttlSeconds int64) (string, error) {
	s.recorder.events = append(s.recorder.events, "mint-delegation")
	s.recorder.mintFileID = fileID
	s.recorder.mintOwner = ownerID
	s.recorder.delegateService = delegateService
	s.recorder.scope = scope
	s.recorder.ttlSeconds = ttlSeconds
	s.recorder.grant = avatarDelegationTestToken
	s.recorder.mintCalls++
	return avatarDelegationTestToken, nil
}

type avatarDelegationImageProcessorStub struct {
	recorder     *avatarDelegationRecorder
	legacyFileID string
	legacyOwner  string
}

func (s *avatarDelegationImageProcessorStub) Generate(_ context.Context, originalID, ownerID, _, _ string, _ []string) error {
	s.recorder.events = append(s.recorder.events, "legacy-imageprocessor")
	s.legacyFileID = originalID
	s.legacyOwner = ownerID
	return nil
}

func (s *avatarDelegationImageProcessorStub) GenerateWithDelegation(_ context.Context, originalID, ownerID, _, _ string, _ []string, processingDelegation string) error {
	s.recorder.events = append(s.recorder.events, "imageprocessor-with-delegation")
	s.recorder.processorFileID = originalID
	s.recorder.processorOwner = ownerID
	s.recorder.grant = processingDelegation
	return nil
}

func TestUploadAvatar_MintsAndPassesBoundProcessingDelegation(t *testing.T) {
	recorder := &avatarDelegationRecorder{}
	storage := &avatarDelegationStorageStub{recorder: recorder}
	imageProcessor := &avatarDelegationImageProcessorStub{recorder: recorder}
	users := &stubUserService{setAvatarFileIDFn: func(_ context.Context, userID, fileID string) (*domain.UserProfile, error) {
		recorder.events = append(recorder.events, "save-avatar")
		require.Equal(t, "authenticated-user-17", userID)
		require.Equal(t, avatarDelegationTestFile, fileID)
		return &domain.UserProfile{UserID: userID, AvatarFileID: &fileID}, nil
	}}
	handler := v1.NewHandler(users, storage, imageProcessor, "avatar", "USER_MEDIA")

	requestBody, contentType := avatarRequestWithUntrustedIdentityFields(t)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", requestBody)
	req.Header.Set(echo.HeaderContentType, contentType)
	response := httptest.NewRecorder()
	c := e.NewContext(req, response)
	c.Set("user_id", "authenticated-user-17")

	require.NoError(t, handler.UploadAvatar(c))
	require.Equal(t, http.StatusCreated, response.Code)

	require.Equal(t, []string{
		"upload",
		"save-avatar",
		"mint-delegation",
		"imageprocessor-with-delegation",
		"signed-url",
	}, recorder.events, "User must mint the scoped grant after upload and pass it to Image Processor")
	require.Equal(t, "authenticated-user-17", recorder.uploadOwner)
	require.Equal(t, 1, recorder.mintCalls)
	require.Equal(t, avatarDelegationTestFile, recorder.mintFileID, "the grant must target the FileStorage upload result")
	require.Equal(t, "authenticated-user-17", recorder.mintOwner, "the grant must be bound to the authenticated actor")
	require.Equal(t, avatarDelegationTestFile, recorder.processorFileID, "Image Processor must receive the exact uploaded file ID")
	require.Equal(t, "authenticated-user-17", recorder.processorOwner)
	require.Equal(t, "image_processor", recorder.delegateService)
	require.Equal(t, "read_source", recorder.scope)
	require.True(t, recorder.ttlSeconds == 0 || recorder.ttlSeconds == 300, "request may omit ttl_seconds for the FileStorage default or explicitly request 300 seconds")

	var publicResponse map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &publicResponse))
	require.NotContains(t, response.Body.String(), avatarDelegationTestToken, "internal processing delegation must never appear in the public User response")
	require.NotContains(t, publicResponse, "processing_delegation")
	require.NotContains(t, publicResponse, "delegation")
}

func TestUploadAvatar_CallerCannotSubstituteOwnerOrSourceFile(t *testing.T) {
	t.Parallel()

	fs := &stubFilestorage{}
	proc := &stubImageProc{}
	handler := v1.NewHandler(&stubUserService{}, fs, proc, "avatar", "USER_MEDIA")
	requestBody, contentType := avatarRequestWithUntrustedIdentityFields(t)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", requestBody)
	req.Header.Set(echo.HeaderContentType, contentType)
	response := httptest.NewRecorder()
	c := e.NewContext(req, response)
	c.Set("user_id", "authenticated-user-17")

	require.NoError(t, handler.UploadAvatar(c))
	require.Equal(t, http.StatusCreated, response.Code)
	require.Equal(t, "authenticated-user-17", fs.uploadReq.OwnerID)
	require.Equal(t, "file-123", proc.lastOriginal, "processing must use FileStorage's uploaded file ID, not a caller-supplied ID")
	require.Equal(t, "authenticated-user-17", proc.lastOwner)
}

func avatarRequestWithUntrustedIdentityFields(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("avatar-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("processing_mode", "EAGER"))
	require.NoError(t, writer.WriteField("owner_id", "attacker-selected-owner"))
	require.NoError(t, writer.WriteField("file_id", "attacker-selected-source"))
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

func TestUploadAvatar_ProcessingGrantIsNotReturnedEvenWhenMinted(t *testing.T) {
	// The success-path contract test above drives a fake FileStorage issuer that
	// returns avatarDelegationTestToken. Keep this assertion separate so a future
	// handler cannot accidentally expose the grant while otherwise returning 201.
	t.Run("public response contains no delegation field", func(t *testing.T) {
		recorder := &avatarDelegationRecorder{}
		storage := &avatarDelegationStorageStub{recorder: recorder}
		processor := &avatarDelegationImageProcessorStub{recorder: recorder}
		handler := v1.NewHandler(&stubUserService{}, storage, processor, "avatar", "USER_MEDIA")
		body, contentType := avatarRequestWithUntrustedIdentityFields(t)
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/users/me/avatar", body)
		req.Header.Set(echo.HeaderContentType, contentType)
		response := httptest.NewRecorder()
		c := e.NewContext(req, response)
		c.Set("user_id", "authenticated-user-17")
		require.NoError(t, handler.UploadAvatar(c))
		require.Equal(t, http.StatusCreated, response.Code)
		assert.Equal(t, 1, recorder.mintCalls, "the public response check must exercise a minted delegation")
		assert.Equal(t, avatarDelegationTestToken, recorder.grant, "the minted grant must be handed to Image Processor")
		require.NotContains(t, response.Body.String(), avatarDelegationTestToken)
		require.NotContains(t, response.Body.String(), `"processing_delegation"`)
		require.NotContains(t, response.Body.String(), `"delegation"`)
		require.True(t, strings.Contains(response.Body.String(), "signed_url"), "normal avatar response fields remain available")
	})
}
