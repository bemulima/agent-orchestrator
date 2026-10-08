// Package tarantool preserves only a legacy hermetic registration-client fixture.
// It is not wired by the User process; Auth owns production verification.
package tarantool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// Client starts or verifies registration and email-change requests.
type Client interface {
	StartRegistration(ctx context.Context, email, password string) (string, error)
	VerifyRegistration(ctx context.Context, uuid, code string) (*VerificationResult, error)
	StartEmailChange(ctx context.Context, userID, email string) (string, error)
	VerifyEmailChange(ctx context.Context, uuid, code string) (*VerificationResult, error)
}

// VerificationResult contains the values returned after a registration check.
type VerificationResult struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type httpClient struct {
	baseURL string
	client  *http.Client
}

type response struct {
	UUID string `json:"uuid"`
}

// NewHTTPClient creates an HTTP client for the registration service.
func NewHTTPClient(baseURL string, timeout time.Duration) Client {
	return &httpClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *httpClient) StartRegistration(ctx context.Context, email, password string) (string, error) {
	payload := map[string]interface{}{"value": map[string]string{"email": email, "password": password}}
	var resp response
	if err := c.postWithRetry(ctx, "/set-new-user", payload, &resp); err != nil {
		return "", err
	}
	return resp.UUID, nil
}

func (c *httpClient) VerifyRegistration(ctx context.Context, uuid, code string) (*VerificationResult, error) {
	payload := map[string]interface{}{"value": map[string]string{"uuid": uuid, "code": code}}
	var resp struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.postWithRetry(ctx, "/check-new-user-code", payload, &resp); err != nil {
		return nil, err
	}
	return &VerificationResult{Email: resp.Email, Password: resp.Password}, nil
}

func (c *httpClient) StartEmailChange(ctx context.Context, userID, email string) (string, error) {
	payload := map[string]interface{}{"value": map[string]string{"user_id": userID, "email": email}}
	var resp response
	if err := c.postWithRetry(ctx, "/start-email-change", payload, &resp); err != nil {
		return "", err
	}
	return resp.UUID, nil
}

func (c *httpClient) VerifyEmailChange(ctx context.Context, uuid, code string) (*VerificationResult, error) {
	payload := map[string]interface{}{"value": map[string]string{"uuid": uuid, "code": code}}
	var resp struct {
		Email string `json:"email"`
	}
	if err := c.postWithRetry(ctx, "/verify-email-change", payload, &resp); err != nil {
		return nil, err
	}
	return &VerificationResult{Email: resp.Email}, nil
}

func (c *httpClient) postWithRetry(ctx context.Context, path string, payload interface{}, out interface{}) error {
	op := func() error {
		reqBody, err := json.Marshal(payload)
		if err != nil {
			return backoff.Permanent(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", c.baseURL, path), bytes.NewReader(reqBody))
		if err != nil {
			return backoff.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 400 {
			return fmt.Errorf("tarantool error: status %d", res.StatusCode)
		}
		if out != nil {
			if err := json.NewDecoder(res.Body).Decode(out); err != nil {
				return backoff.Permanent(err)
			}
		}
		return nil
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 200 * time.Millisecond
	bo.MaxElapsedTime = 3 * time.Second

	return backoff.Retry(op, backoff.WithContext(bo, ctx))
}
