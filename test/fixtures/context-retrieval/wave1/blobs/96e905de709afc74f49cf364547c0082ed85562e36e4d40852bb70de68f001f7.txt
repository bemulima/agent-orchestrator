// Package handlers implements private service-to-service HTTP endpoints.
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/transport/http/middleware"
	service "github.com/example/user-service/internal/usecase"
	res "github.com/example/user-service/pkg/http"
)

const maxResolveBodyBytes = 64 << 10

// Handler exposes private service-to-service user contracts.
type Handler struct {
	activeUsers service.ActiveUserResolver
}

// NewHandler creates the internal HTTP handler.
func NewHandler(activeUsers service.ActiveUserResolver) *Handler {
	return &Handler{activeUsers: activeUsers}
}

// ResolveActiveUsers validates and resolves one bounded UUID batch.
func (h *Handler) ResolveActiveUsers(c echo.Context) error {
	if h == nil || h.activeUsers == nil {
		return res.ErrorJSON(c, http.StatusServiceUnavailable, "service_unavailable", "active user resolver is unavailable", middleware.RequestIDFromCtx(c), nil)
	}
	request := struct {
		UserIDs []string `json:"user_ids"`
	}{}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxResolveBodyBytes)
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return invalidRequest(c)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalidRequest(c)
	}
	result, err := h.activeUsers.ResolveActiveUsers(c.Request().Context(), request.UserIDs)
	if err != nil {
		if errors.Is(err, service.ErrInvalidActiveUserBatch) {
			return invalidRequest(c)
		}
		return res.ErrorJSON(c, http.StatusInternalServerError, "resolve_failed", "active users could not be resolved", middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, result)
}

func invalidRequest(c echo.Context) error {
	return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "user_ids must contain 1 to 1000 unique UUIDs", middleware.RequestIDFromCtx(c), nil)
}
