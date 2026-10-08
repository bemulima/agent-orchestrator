// Package v1 registers the public user API routes.
package v1

import (
	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/transport/http/api/v1/handlers"
)

// RegisterRoutes attaches user-facing endpoints under provided group.
func RegisterRoutes(g *echo.Group, h *handlers.Handler) {
	h.RegisterRoutes(g)
}
