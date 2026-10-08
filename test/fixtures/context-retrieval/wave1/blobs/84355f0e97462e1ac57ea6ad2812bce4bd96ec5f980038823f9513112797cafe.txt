// Package filestorage provides the User service's FileStorage HTTP client.
package filestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"time"
)

// Client uploads files and requests short-lived download capabilities.
// DownloadURL intentionally returns no direct URL because FileStorage proxy
// routes require a service credential; callers should use SignedURL instead.
type Client interface {
	Upload(ctx context.Context, req UploadRequest) (*UploadResponse, error)
	SignedURL(ctx context.Context, id string, expiresMinutes int64) (string, error)
	DownloadURL(id string) string
}

// ProcessingDelegationClient mints a narrowly scoped capability for an
// authenticated owner and an existing FileStorage object.
type ProcessingDelegationClient interface {
	CreateProcessingDelegation(ctx context.Context, fileID, ownerID, delegateService, scope string, ttlSeconds int64) (string, error)
}

// UploadRequest contains the owner and metadata for a file upload.
type UploadRequest struct {
	OwnerID        string
	FileKind       string
	ProcessingMode string
	FileName       string
	ContentType    string
	Data           []byte
}

// UploadResponse identifies the stored FileStorage object.
type UploadResponse struct {
	ID string `json:"id"`
}

type httpClient struct {
	baseURL       string
	client        *http.Client
	internalToken string
}

type uploadResponse struct {
	ID string `json:"id"`
}

type signedURLResponse struct {
	URL string `json:"url"`
}

type processingDelegationRequest struct {
	OwnerID         string `json:"owner_id"`
	DelegateService string `json:"delegate_service"`
	Scope           string `json:"scope"`
	TTLSeconds      int64  `json:"ttl_seconds,omitempty"`
}

type processingDelegationResponse struct {
	Delegation string `json:"delegation"`
}

// NewHTTPClient creates a FileStorage client authenticated with the internal token.
func NewHTTPClient(baseURL, internalToken string, timeout time.Duration) Client {
	return &httpClient{
		baseURL:       baseURL,
		client:        &http.Client{Timeout: timeout},
		internalToken: internalToken,
	}
}

func (c *httpClient) Upload(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
	if req.OwnerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if req.FileKind == "" {
		return nil, fmt.Errorf("file_kind is required")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", path.Base(req.FileName))
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(req.Data); err != nil {
		return nil, err
	}
	if req.ContentType != "" {
		_ = writer.WriteField("content_type", req.ContentType)
	}
	_ = writer.WriteField("owner_id", req.OwnerID)
	_ = writer.WriteField("file_kind", req.FileKind)
	if req.ProcessingMode != "" {
		_ = writer.WriteField("image_processing_mode", req.ProcessingMode)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/internal/v1/files/upload", c.baseURL), body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	c.authorize(httpReq)

	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("filestorage error: status %d: %s", res.StatusCode, string(data))
	}

	var resp uploadResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, err
	}

	if resp.ID == "" {
		return nil, fmt.Errorf("filestorage response missing id")
	}

	return &UploadResponse{ID: resp.ID}, nil
}

func (c *httpClient) SignedURL(ctx context.Context, id string, expiresMinutes int64) (string, error) {
	if expiresMinutes <= 0 {
		expiresMinutes = 15
	}
	body := map[string]any{
		"purpose":         "download",
		"method":          "GET",
		"expires_minutes": expiresMinutes,
	}
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/internal/v1/files/%s/signed-url", c.baseURL, id), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.authorize(httpReq)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("filestorage error: status %d: %s", res.StatusCode, string(data))
	}
	var resp signedURLResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return "", err
	}
	if resp.URL == "" {
		return "", fmt.Errorf("filestorage response missing url")
	}
	return resp.URL, nil
}

func (c *httpClient) CreateProcessingDelegation(ctx context.Context, fileID, ownerID, delegateService, scope string, ttlSeconds int64) (string, error) {
	if fileID == "" || ownerID == "" || delegateService == "" || scope == "" {
		return "", fmt.Errorf("file_id, owner_id, delegate_service and scope are required")
	}
	if ttlSeconds < 0 || ttlSeconds > 900 {
		return "", fmt.Errorf("ttl_seconds must be between 0 and 900")
	}

	body := processingDelegationRequest{
		OwnerID:         ownerID,
		DelegateService: delegateService,
		Scope:           scope,
		TTLSeconds:      ttlSeconds,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode processing delegation request: %w", err)
	}
	requestURL := fmt.Sprintf("%s/internal/v1/files/%s/processing-delegations", c.baseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)

	res, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("filestorage error: status %d: %s", res.StatusCode, string(data))
	}

	var response processingDelegationResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return "", err
	}
	if response.Delegation == "" {
		return "", fmt.Errorf("filestorage response missing delegation")
	}
	return response.Delegation, nil
}

// DownloadURL preserves the profile URL resolver contract without exposing a
// private FileStorage proxy URL. Use SignedURL for an authorized download link.
func (c *httpClient) DownloadURL(_ string) string {
	return ""
}
func (c *httpClient) authorize(req *http.Request) {
	req.Header.Set("X-Internal-Token", c.internalToken)
}
