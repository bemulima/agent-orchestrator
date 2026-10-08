package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	rbacclient "github.com/example/user-service/internal/infrastructure/rbac"
	res "github.com/example/user-service/pkg/http"
)

// RBACMiddleware checks user roles and permissions before route execution.
type RBACMiddleware struct {
	client rbacclient.Client
}

// NewRBACMiddleware creates role and permission middleware backed by client.
func NewRBACMiddleware(client rbacclient.Client) *RBACMiddleware {
	return &RBACMiddleware{client: client}
}

// RequireRole permits a request only when the user has role.
func (m *RBACMiddleware) RequireRole(role string) echo.MiddlewareFunc {
	return m.requireRoles([]string{role})
}

// RequireAnyRole permits a request when the user has any listed role.
func (m *RBACMiddleware) RequireAnyRole(roles ...string) echo.MiddlewareFunc {
	return m.requireRoles(roles)
}

func (m *RBACMiddleware) requireRoles(roles []string) echo.MiddlewareFunc {
	validRoles := make([]string, 0, len(roles))
	for _, role := range roles {
		if trimmed := strings.TrimSpace(role); trimmed != "" {
			validRoles = append(validRoles, trimmed)
		}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			userID, _ := c.Get("user_id").(string)
			if userID == "" {
				return res.ErrorJSON(c, http.StatusForbidden, "forbidden", "missing user", RequestIDFromCtx(c), nil)
			}
			allowed := false
			if cached, ok := c.Get("role").(string); ok {
				for _, r := range validRoles {
					if cached == r {
						allowed = true
						break
					}
				}
			}
			if !allowed && m.client != nil {
				if cachedRole, err := m.client.GetRoleByUserID(c.Request().Context(), userID); err == nil {
					c.Set("role", cachedRole)
					for _, r := range validRoles {
						if cachedRole == r {
							allowed = true
							break
						}
					}
				}
				if !allowed {
					for _, r := range validRoles {
						ok, err := m.client.CheckRole(c.Request().Context(), userID, r)
						if err == nil && ok {
							allowed = true
							break
						}
					}
				}
			}
			if !allowed {
				return res.ErrorJSON(c, http.StatusForbidden, "forbidden", "role required", RequestIDFromCtx(c), nil)
			}
			return next(c)
		}
	}
}

// RequirePermission permits a request only when the user has permission.
func (m *RBACMiddleware) RequirePermission(permission string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			userID, _ := c.Get("user_id").(string)
			if userID == "" {
				return res.ErrorJSON(c, http.StatusForbidden, "forbidden", "missing user", RequestIDFromCtx(c), nil)
			}
			allowed := false
			if cached, ok := c.Get("permissions").([]string); ok {
				for _, p := range cached {
					if p == permission {
						allowed = true
						break
					}
				}
			}
			if !allowed && m.client != nil {
				ok, err := m.client.CheckPermission(c.Request().Context(), userID, permission)
				if err == nil {
					allowed = ok
				}
			}
			if !allowed {
				return res.ErrorJSON(c, http.StatusForbidden, "forbidden", "permission required", RequestIDFromCtx(c), nil)
			}
			return next(c)
		}
	}
}
