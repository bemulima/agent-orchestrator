package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
)

type agentUsageFake struct{}

func (agentUsageFake) Dashboard(context.Context) (domain.AgentUsageDashboard, error) {
	return domain.AgentUsageDashboard{Budget: domain.AgentBudgetState{Mode: "enforce", DeepModel: "sol", DeepRunLimit: 20}}, nil
}

func TestRouterAgentUsageAPI(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler:     handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		AgentUsageHandler: &handlers.AgentUsageHandler{Service: agentUsageFake{}},
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/agent-usage", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}
