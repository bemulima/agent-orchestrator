package handlers

import (
	"context"
	"net/http"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type agentUsageService interface {
	Dashboard(context.Context) (domain.AgentUsageDashboard, error)
}

type AgentUsageHandler struct{ Service agentUsageService }

func (h AgentUsageHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	result, err := h.Service.Dashboard(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
