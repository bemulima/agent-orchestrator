package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/infrastructure/rbac"
)

const (
	testUserID   = "user-123"
	testRoleKey  = "role-user"
	testPerm     = "read:profile"
	testPermBool = true
)

func TestRBACClientContract(t *testing.T) {
	handler := newMockRBACHandler(t)
	server := startServerOrSkip(t, handler)
	if server == nil {
		return
	}
	defer server.Close()

	client := rbac.NewHTTPClient(server.URL, 2*time.Second)

	ctx := context.Background()

	require.NoError(t, client.AssignRole(ctx, testUserID, testRoleKey))
	require.True(t, handler.assignCalled)

	role, err := client.GetRoleByUserID(ctx, testUserID)
	require.NoError(t, err)
	require.Equal(t, testRoleKey, role)

	perms, err := client.GetPermissionsByUserID(ctx, testUserID)
	require.NoError(t, err)
	require.Equal(t, []string{testPerm}, perms)

	allowed, err := client.CheckRole(ctx, testUserID, testRoleKey)
	require.NoError(t, err)
	require.True(t, allowed)

	permitted, err := client.CheckPermission(ctx, testUserID, testPerm)
	require.NoError(t, err)
	require.Equal(t, testPermBool, permitted)
}

func startServerOrSkip(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("skipping contract test: unable to start test server (%v)", r)
		}
	}()
	return httptest.NewServer(handler)
}

type mockRBACHandler struct {
	t            *testing.T
	assignCalled bool
}

func newMockRBACHandler(t *testing.T) *mockRBACHandler {
	return &mockRBACHandler{t: t}
}

func (h *mockRBACHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/principal-role/update":
		require.Equal(h.t, http.MethodPatch, r.Method)
		h.assignCalled = true
		var payload struct {
			Value struct {
				UserID string `json:"user_id"`
				Role   string `json:"role"`
			} `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		require.Equal(h.t, testUserID, payload.Value.UserID)
		require.Equal(h.t, testRoleKey, payload.Value.Role)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "/api/v1/principal-role/get":
		require.Equal(h.t, testUserID, r.URL.Query().Get("user_id"))
		writeJSON(w, http.StatusOK, map[string]string{"role": testRoleKey})
	case "/api/v1/principal-permission/list":
		require.Equal(h.t, testUserID, r.URL.Query().Get("user_id"))
		writeJSON(w, http.StatusOK, map[string][]string{"permissions": {testPerm}})
	case "/api/v1/principal-role/get-by-role":
		require.Equal(h.t, testUserID, r.URL.Query().Get("user_id"))
		require.Equal(h.t, testRoleKey, r.URL.Query().Get("role"))
		writeJSON(w, http.StatusOK, map[string]bool{"allowed": true})
	case "/api/v1/principal-permission/get-by-permission":
		require.Equal(h.t, testUserID, r.URL.Query().Get("user_id"))
		require.Equal(h.t, testPerm, r.URL.Query().Get("permission"))
		writeJSON(w, http.StatusOK, map[string]bool{"allowed": testPermBool})
	default:
		http.NotFound(w, r)
	}
}
