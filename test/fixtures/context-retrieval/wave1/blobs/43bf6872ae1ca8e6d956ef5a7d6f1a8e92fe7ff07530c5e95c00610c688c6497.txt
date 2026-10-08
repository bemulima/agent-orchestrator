package rbac

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPClient_AssignRole(t *testing.T) {
	t.Parallel()

	var payload map[string]interface{}
	client := &httpClient{
		baseURL: "http://rbac-service",
		client: newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPatch, req.Method)
			require.Equal(t, "/api/v1/principal-role/update", req.URL.Path)
			require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}

	require.NoError(t, client.AssignRole(context.Background(), "user-123", "role-admin"))

	value, ok := payload["value"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "user-123", value["user_id"])
	require.Equal(t, "role-admin", value["role"])
}

func TestHTTPClient_GetRoleByUserID(t *testing.T) {
	t.Parallel()

	const expectedRole = "role-captain"
	client := &httpClient{
		baseURL: "http://rbac-service",
		client: newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, "/api/v1/principal-role/get", req.URL.Path)
			require.Equal(t, "user-789", req.URL.Query().Get("user_id"))
			body := `{"role":"` + expectedRole + `"}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	}

	role, err := client.GetRoleByUserID(context.Background(), "user-789")
	require.NoError(t, err)
	require.Equal(t, expectedRole, role)
}

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func newMockHTTPClient(tripper mockRoundTripper) *http.Client {
	return &http.Client{Transport: tripper}
}

func TestNewHTTPClient_ComposesProviderAPIRootFromOrigin(t *testing.T) {
	for _, origin := range []string{"http://rbac-service", "https://rbac-service:8082", "http://127.0.0.1:18080", "http://[::1]:18080"} {
		t.Run(origin, func(t *testing.T) {
			client := NewHTTPClient(origin, time.Second).(*httpClient)
			calls := 0
			client.client = newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, origin+"/api/v1/principal-role/get", req.URL.Scheme+"://"+req.URL.Host+req.URL.Path)
				require.Equal(t, "user-123", req.URL.Query().Get("user_id"))
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"role":"role-user"}`))}, nil
			})
			role, err := client.GetRoleByUserID(context.Background(), "user-123")
			require.NoError(t, err)
			require.Equal(t, "role-user", role)
			require.Equal(t, 1, calls)
		})
	}
}

func TestNewHTTPClient_RejectsNonOriginWithoutHTTPCalls(t *testing.T) {
	origins := []string{
		"", "rbac-service:8082", "ftp://rbac-service", "http:///", "http://:8082",
		"http://rbac-service/", "http://rbac-service/api/v1", "http://rbac-service/other",
		"http://rbac-service?key=value", "http://rbac-service?", "http://rbac-service#fragment", "http://rbac-service#",
		"http://user:password@rbac-service", "http://rbac-service:", "http://rbac-service:bad",
		"http://rbac-service:0", "http://rbac-service:65536", "http://::1", "http://[invalid]",
	}
	for _, origin := range origins {
		t.Run(origin, func(t *testing.T) {
			client := NewHTTPClient(origin, time.Second).(*httpClient)
			calls := 0
			client.client = newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			ctx := context.Background()
			_, err := client.GetRoleByUserID(ctx, "user-123")
			require.ErrorContains(t, err, "invalid MS_RBAC origin")
			_, err = client.GetPermissionsByUserID(ctx, "user-123")
			require.ErrorContains(t, err, "invalid MS_RBAC origin")
			_, err = client.CheckPermission(ctx, "user-123", "read:profile")
			require.ErrorContains(t, err, "invalid MS_RBAC origin")
			_, err = client.CheckRole(ctx, "user-123", "role-user")
			require.ErrorContains(t, err, "invalid MS_RBAC origin")
			err = client.AssignRole(ctx, "user-123", "role-user")
			require.ErrorContains(t, err, "invalid MS_RBAC origin")
			require.NotContains(t, err.Error(), "password")
			require.Zero(t, calls)
		})
	}
}
