package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	httpmiddleware "github.com/bemulima/agent-orchestrator/internal/adapters/http/middleware"
)

// RouterDependencies collects HTTP handlers and cross-cutting dependencies.
type RouterDependencies struct {
	HealthHandler     handlers.HealthHandler
	ProjectHandler    *handlers.ProjectHandler
	OnboardingHandler *handlers.OnboardingHandler
	Logger            *zap.Logger
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
	if deps.ProjectHandler != nil {
		root.Get("/api/v1/projects", deps.ProjectHandler.ListProjects)
		root.Post("/api/v1/projects/connect", deps.ProjectHandler.ConnectProject)
		root.Get("/api/v1/projects/{projectId}", deps.ProjectHandler.GetProject)
		root.Post("/api/v1/projects/{projectId}/scan", deps.ProjectHandler.ScanProject)
		root.Get("/api/v1/projects/{projectId}/reports/latest", deps.ProjectHandler.LatestDiscoveryReport)
	}
	if deps.OnboardingHandler != nil {
		root.Post("/api/v1/projects/{projectId}/onboard", deps.OnboardingHandler.PrepareProject)
		root.Get("/api/v1/onboarding-runs/{runId}", deps.OnboardingHandler.GetRun)
		root.Get("/api/v1/onboarding-runs/{runId}/diff", deps.OnboardingHandler.GetDiff)
		root.Post("/api/v1/onboarding-runs/{runId}/approve", deps.OnboardingHandler.ApproveRun)
		root.Post("/api/v1/onboarding-runs/{runId}/reject", deps.OnboardingHandler.RejectRun)
		root.Post("/api/v1/onboarding-runs/{runId}/apply", deps.OnboardingHandler.ApplyRun)
	}

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
