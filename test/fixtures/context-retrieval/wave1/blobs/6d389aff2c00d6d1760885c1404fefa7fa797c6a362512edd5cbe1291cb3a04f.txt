// Package private registers authenticated internal HTTP routes.
package private

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/transport/http/middleware"
	"github.com/example/user-service/internal/transport/http/private/handlers"
	res "github.com/example/user-service/pkg/http"
)

// Register mounts private service-to-service routes beneath the supplied group.
func Register(g *echo.Group, handler *handlers.Handler, internalToken string) {
	g.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	protected := g.Group("/v1", requireInternalToken(internalToken))
	protected.POST("/users/active/resolve", handler.ResolveActiveUsers)
}

func requireInternalToken(expected string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			provided := c.Request().Header.Get("X-Internal-Token")
			if !secureEqual(strings.TrimSpace(expected), provided) {
				return res.ErrorJSON(c, http.StatusUnauthorized, "internal_authentication_required", "valid internal credentials are required", middleware.RequestIDFromCtx(c), nil)
			}
			return next(c)
		}
	}
}

func secureEqual(expected, provided string) bool {
	if expected == "" || len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
