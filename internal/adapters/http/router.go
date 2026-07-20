package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/example/course-dev-orchestrator/internal/adapters/http/handlers"
	httpmiddleware "github.com/example/course-dev-orchestrator/internal/adapters/http/middleware"
)

// RouterDependencies collects HTTP handlers and cross-cutting dependencies.
type RouterDependencies struct {
	HealthHandler handlers.HealthHandler
	Logger        *zap.Logger
}

// NewRouter builds the chi router.
func NewRouter(deps RouterDependencies) http.Handler {
	root := chi.NewRouter()
	root.Use(chimiddleware.RequestID)
	root.Use(chimiddleware.RealIP)
	root.Use(chimiddleware.Recoverer)
	if deps.Logger != nil {
		root.Use(httpmiddleware.RequestLogger(deps.Logger))
	}

	root.Get("/health", deps.HealthHandler.Health)
	root.Get("/ready", deps.HealthHandler.Ready)

	root.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		handlers.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	root.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		handlers.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	return root
}

// DefaultHealthTimeout bounds readiness checks.
const DefaultHealthTimeout = 2 * time.Second
