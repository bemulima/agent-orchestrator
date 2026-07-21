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
	TopologyHandler   *handlers.TopologyHandler
	PlanningHandler   *handlers.PlanningHandler
	GitLabHandler     *handlers.GitLabHandler
	TelegramHandler   *handlers.TelegramHandler
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
	if deps.TopologyHandler != nil {
		root.Post("/api/v1/topology/rebuild", deps.TopologyHandler.RebuildTopology)
		root.Get("/api/v1/topology", deps.TopologyHandler.GetTopology)
		root.Get("/api/v1/topology/services", deps.TopologyHandler.ListServices)
		root.Get("/api/v1/topology/contracts", deps.TopologyHandler.ListContracts)
		root.Get("/api/v1/topology/contract-drift", deps.TopologyHandler.ListContractDrift)
		root.Get("/api/v1/projects/{projectId}/dependencies", deps.TopologyHandler.ProjectDependencies)
		root.Get("/api/v1/projects/{projectId}/contracts", deps.TopologyHandler.ProjectContracts)
		root.Get("/api/v1/projects/{projectId}/consumers", deps.TopologyHandler.ProjectConsumers)
	}
	if deps.PlanningHandler != nil {
		root.Post("/api/v1/commands", deps.PlanningHandler.CreateCommandRequest)
		root.Get("/api/v1/commands/{commandId}", deps.PlanningHandler.GetCommandRequest)
		root.Post("/api/v1/commands/{commandId}/plan", deps.PlanningHandler.PlanCommand)
		root.Get("/api/v1/plans/{planId}", deps.PlanningHandler.GetPlanRequest)
		root.Get("/api/v1/plans/{planId}/tasks", deps.PlanningHandler.GetPlanTasks)
		root.Post("/api/v1/plans/{planId}/approve", deps.PlanningHandler.ApprovePlanRequest)
		root.Post("/api/v1/plans/{planId}/reject", deps.PlanningHandler.RejectPlanRequest)
		root.Post("/api/v1/plans/{planId}/run", deps.PlanningHandler.RunPlan)
		root.Get("/api/v1/runs/{runId}", deps.PlanningHandler.GetRunRequest)
		root.Post("/api/v1/runs/{runId}/pause", deps.PlanningHandler.PauseRun)
		root.Post("/api/v1/runs/{runId}/resume", deps.PlanningHandler.ResumeRun)
		root.Post("/api/v1/runs/{runId}/cancel", deps.PlanningHandler.CancelRun)
		root.Get("/api/v1/tasks/{taskId}", deps.PlanningHandler.GetTaskRequest)
		root.Get("/api/v1/tasks/{taskId}/attempts", deps.PlanningHandler.GetTaskAttempts)
		root.Get("/api/v1/tasks/{taskId}/artifacts", deps.PlanningHandler.GetTaskArtifacts)
		root.Post("/api/v1/tasks/{taskId}/retry", deps.PlanningHandler.RetryTaskRequest)
		root.Post("/api/v1/tasks/{taskId}/cancel", deps.PlanningHandler.CancelTaskRequest)
	}
	if deps.GitLabHandler != nil {
		root.Post("/api/v1/plans/{planId}/gitlab/sync", deps.GitLabHandler.SyncPlan)
		root.Get("/api/v1/plans/{planId}/gitlab", deps.GitLabHandler.ListPlanLinks)
		root.Post("/api/v1/integrations/gitlab/webhook", deps.GitLabHandler.ReceiveWebhook)
	}
	if deps.TelegramHandler != nil {
		root.Post("/api/v1/integrations/telegram/webhook", deps.TelegramHandler.ReceiveWebhook)
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
