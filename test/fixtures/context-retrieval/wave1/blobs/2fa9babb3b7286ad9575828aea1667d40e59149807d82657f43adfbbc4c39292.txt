// Package rbac provides the User service's role and permission client.
package rbac

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// Client reads role and permission decisions and assigns roles for a user.
type Client interface {
	GetRoleByUserID(ctx context.Context, userID string) (string, error)
	GetPermissionsByUserID(ctx context.Context, userID string) ([]string, error)
	CheckPermission(ctx context.Context, userID, permission string) (bool, error)
	CheckRole(ctx context.Context, userID, role string) (bool, error)
	AssignRole(ctx context.Context, userID, role string) error
}

// rbacAPIRoot is the mount declared by ms-go-rbac internal/transport/http/router.go.
const rbacAPIRoot = "/api/v1"

type httpClient struct {
	baseURL   string
	client    *http.Client
	configErr error
}

type cacheEntry struct {
	value     interface{}
	expiresAt time.Time
}

type cachingClient struct {
	delegate Client
	ttl      time.Duration
	cache    map[string]cacheEntry
	mu       sync.RWMutex
}

// NewHTTPClient creates an HTTP client from an RBAC origin (scheme://host[:port]).
func NewHTTPClient(baseURL string, timeout time.Duration) Client {
	return &httpClient{baseURL: baseURL, client: &http.Client{Timeout: timeout}, configErr: validateOrigin(baseURL)}
}

// validateOrigin rejects configuration components that would alter operation URLs.
// The error intentionally omits the input, which could contain credentials.
func validateOrigin(origin string) error {
	invalid := fmt.Errorf("invalid MS_RBAC origin: expected http://host[:port] or https://host[:port] without path, credentials, query, or fragment")
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || origin != u.Scheme+"://"+u.Host {
		return invalid
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		if net.ParseIP(host) == nil {
			return invalid
		}
	} else if strings.Contains(host, ":") {
		return invalid
	}
	if strings.HasSuffix(u.Host, ":") {
		return invalid
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return invalid
		}
	}
	return nil
}

// NewCachingClient wraps an RBAC client with a time-limited in-memory cache.
func NewCachingClient(delegate Client, ttl time.Duration) Client {
	return &cachingClient{delegate: delegate, ttl: ttl, cache: map[string]cacheEntry{}}
}

func (c *httpClient) GetRoleByUserID(ctx context.Context, userID string) (string, error) {
	var resp struct {
		Role string `json:"role"`
	}
	if err := c.get(ctx, "/principal-role/get", url.Values{"user_id": {userID}}, &resp); err != nil {
		return "", err
	}
	return resp.Role, nil
}

func (c *httpClient) GetPermissionsByUserID(ctx context.Context, userID string) ([]string, error) {
	var resp struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.get(ctx, "/principal-permission/list", url.Values{"user_id": {userID}}, &resp); err != nil {
		return nil, err
	}
	return resp.Permissions, nil
}

func (c *httpClient) CheckPermission(ctx context.Context, userID, permission string) (bool, error) {
	var resp struct {
		Allowed bool `json:"allowed"`
	}
	if err := c.get(ctx, "/principal-permission/get-by-permission", url.Values{"user_id": {userID}, "permission": {permission}}, &resp); err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (c *httpClient) CheckRole(ctx context.Context, userID, role string) (bool, error) {
	var resp struct {
		Allowed bool `json:"allowed"`
	}
	if err := c.get(ctx, "/principal-role/get-by-role", url.Values{"user_id": {userID}, "role": {role}}, &resp); err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (c *httpClient) AssignRole(ctx context.Context, userID, role string) error {
	payload := map[string]interface{}{
		"value": map[string]string{
			"user_id": userID,
			"role":    role,
		},
	}
	return c.sendJSON(ctx, http.MethodPatch, "/principal-role/update", payload)
}

func (c *httpClient) get(ctx context.Context, path string, params url.Values, out interface{}) error {
	if c.configErr != nil {
		return c.configErr
	}
	op := func() error {
		endpoint := fmt.Sprintf("%s%s?%s", c.baseURL+rbacAPIRoot, path, params.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return backoff.Permanent(err)
		}
		res, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 400 {
			return fmt.Errorf("rbac error: status %d", res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return backoff.Permanent(err)
		}
		return nil
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 200 * time.Millisecond
	bo.MaxElapsedTime = 3 * time.Second

	return backoff.Retry(op, backoff.WithContext(bo, ctx))
}

func (c *httpClient) sendJSON(ctx context.Context, method, path string, payload interface{}) error {
	if c.configErr != nil {
		return c.configErr
	}
	op := func() error {
		body, err := json.Marshal(payload)
		if err != nil {
			return backoff.Permanent(err)
		}
		req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s%s", c.baseURL+rbacAPIRoot, path), bytes.NewReader(body))
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
			return fmt.Errorf("rbac error: status %d", res.StatusCode)
		}
		return nil
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 200 * time.Millisecond
	bo.MaxElapsedTime = 3 * time.Second

	return backoff.Retry(op, backoff.WithContext(bo, ctx))
}

func (c *cachingClient) cacheKey(parts ...string) string {
	return fmt.Sprintf("%v", parts)
}

func (c *cachingClient) withCache(key string, loader func() (interface{}, error)) (interface{}, error) {
	now := time.Now()
	c.mu.RLock()
	if entry, ok := c.cache[key]; ok && now.Before(entry.expiresAt) {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	value, err := loader()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[key] = cacheEntry{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return value, nil
}

func (c *cachingClient) GetRoleByUserID(ctx context.Context, userID string) (string, error) {
	key := c.cacheKey("role", userID)
	value, err := c.withCache(key, func() (interface{}, error) {
		return c.delegate.GetRoleByUserID(ctx, userID)
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (c *cachingClient) GetPermissionsByUserID(ctx context.Context, userID string) ([]string, error) {
	key := c.cacheKey("perms", userID)
	value, err := c.withCache(key, func() (interface{}, error) {
		return c.delegate.GetPermissionsByUserID(ctx, userID)
	})
	if err != nil {
		return nil, err
	}
	return value.([]string), nil
}

func (c *cachingClient) CheckPermission(ctx context.Context, userID, permission string) (bool, error) {
	key := c.cacheKey("perm", userID, permission)
	value, err := c.withCache(key, func() (interface{}, error) {
		return c.delegate.CheckPermission(ctx, userID, permission)
	})
	if err != nil {
		return false, err
	}
	return value.(bool), nil
}

func (c *cachingClient) CheckRole(ctx context.Context, userID, role string) (bool, error) {
	key := c.cacheKey("role-check", userID, role)
	value, err := c.withCache(key, func() (interface{}, error) {
		return c.delegate.CheckRole(ctx, userID, role)
	})
	if err != nil {
		return false, err
	}
	return value.(bool), nil
}

func (c *cachingClient) AssignRole(ctx context.Context, userID, role string) error {
	return c.delegate.AssignRole(ctx, userID, role)
}
