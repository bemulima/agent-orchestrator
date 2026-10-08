// Package v1 registers administrative user management routes.
package v1

import (
	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/transport/http/admin/v1/handlers"
)

// RegisterRoutes attaches admin user management endpoints.
func RegisterRoutes(g *echo.Group, h *handlers.Handler) {
	h.RegisterRoutes(g)
}
