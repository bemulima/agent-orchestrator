// Package oauthavatar downloads provider avatars from allowlisted HTTPS hosts.
package oauthavatar

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/user-service/internal/port"
)

// DefaultMaxSize is the maximum accepted avatar payload when no limit is set.
const DefaultMaxSize int64 = 5 * 1024 * 1024

// Client downloads a provider avatar after validating its source URL.
type Client interface {
	Download(ctx context.Context, provider, rawURL string) (*port.OAuthAvatar, error)
}

// HTTPClient downloads avatars only from provider-specific allowlisted hosts.
type HTTPClient struct {
	client       *http.Client
	maxSize      int64
	allowedHosts map[string][]string
}

// NewHTTPClient creates a provider avatar client with bounded timeout and payload size.
func NewHTTPClient(timeout time.Duration, maxSize int64) *HTTPClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	return &HTTPClient{
		client:  &http.Client{Timeout: timeout},
		maxSize: maxSize,
		allowedHosts: map[string][]string{
			"google": {"googleusercontent.com"},
			"github": {"avatars.githubusercontent.com"},
		},
	}
}

// Download fetches an avatar after validating the provider host and redirects.
func (c *HTTPClient) Download(ctx context.Context, provider, rawURL string) (*port.OAuthAvatar, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	parsed, err := c.validateURL(provider, rawURL)
	if err != nil {
		return nil, err
	}

	httpClient := *c.client
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many avatar redirects")
		}
		_, err := c.validateURL(provider, req.URL.String())
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg")
	req.Header.Set("User-Agent", "ms-go-user")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download OAuth avatar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("download OAuth avatar: %s", resp.Status)
	}
	if resp.ContentLength > c.maxSize {
		return nil, fmt.Errorf("OAuth avatar exceeds %d bytes", c.maxSize)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read OAuth avatar: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("OAuth avatar is empty")
	}
	if int64(len(data)) > c.maxSize {
		return nil, fmt.Errorf("OAuth avatar exceeds %d bytes", c.maxSize)
	}

	contentType := http.DetectContentType(data)
	fileName := ""
	switch contentType {
	case "image/jpeg":
		fileName = "oauth-avatar.jpg"
	case "image/png":
		fileName = "oauth-avatar.png"
	case "image/webp":
		fileName = "oauth-avatar.webp"
	default:
		return nil, fmt.Errorf("unsupported OAuth avatar content type %q", contentType)
	}
	return &port.OAuthAvatar{Data: data, ContentType: contentType, FileName: fileName}, nil
}

func (c *HTTPClient) validateURL(provider, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid OAuth avatar URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, fmt.Errorf("OAuth avatar URL port is not allowed")
	}
	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range c.allowedHosts[provider] {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("OAuth avatar host is not allowed for %s", provider)
}
