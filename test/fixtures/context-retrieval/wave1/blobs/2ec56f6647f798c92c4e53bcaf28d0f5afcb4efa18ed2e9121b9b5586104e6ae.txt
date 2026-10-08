package internalhttp

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Register attaches internal/health endpoints under provided group.
func Register(g *echo.Group) {
	g.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
}
