// Package imageprocessor provides the User service's image processing client.
package imageprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"time"
)

// Client asks Image Processor to generate variants for an owned file.
type Client interface {
	GenerateWithDelegation(ctx context.Context, originalID, ownerID, fileKind, presetGroup string, variants []string, processingDelegation string) error
}

type httpClient struct {
	baseURL       string
	client        *http.Client
	internalToken string
}

type generateRequest struct {
	PresetGroup          string   `json:"preset_group"`
	Variants             []string `json:"variants,omitempty"`
	Force                bool     `json:"force_regenerate"`
	OwnerID              string   `json:"owner_id"`
	FileKind             string   `json:"file_kind"`
	ProcessingDelegation string   `json:"processing_delegation"`
}

// NewHTTPClient creates an Image Processor client authenticated with an optional internal token.
func NewHTTPClient(baseURL string, timeout time.Duration, internalToken ...string) Client {
	var token string
	if len(internalToken) > 0 {
		token = internalToken[0]
	}
	return &httpClient{baseURL: baseURL, client: &http.Client{Timeout: timeout}, internalToken: token}
}

func (c *httpClient) GenerateWithDelegation(ctx context.Context, originalID, ownerID, fileKind, presetGroup string, variants []string, processingDelegation string) error {
	if c.baseURL == "" {
		return fmt.Errorf("image processor url is not configured")
	}
	if originalID == "" || ownerID == "" || processingDelegation == "" {
		return fmt.Errorf("original file, owner and processing delegation are required")
	}
	body := generateRequest{
		PresetGroup:          presetGroup,
		Variants:             variants,
		Force:                false,
		OwnerID:              ownerID,
		FileKind:             fileKind,
		ProcessingDelegation: processingDelegation,
	}
	payload, _ := json.Marshal(body)
	url := c.baseURL + path.Join("/admin/v1/images", originalID, "variants/generate")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", c.internalToken)

	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode >= 400 {
		return fmt.Errorf("image processor responded %d", res.StatusCode)
	}
	return nil
}
