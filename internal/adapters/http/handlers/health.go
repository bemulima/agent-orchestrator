package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	healthuc "github.com/example/course-dev-orchestrator/internal/usecase/health"
)

type HealthHandler struct {
	Readiness healthuc.CheckReadiness
	Timeout   time.Duration
}

// Health handles GET /health.
func (h HealthHandler) Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": healthuc.StatusOK})
}

// Ready handles GET /ready.
func (h HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.Timeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	result := h.Readiness.Handle(ctx)
	status := http.StatusOK
	if result.Status != healthuc.StatusOK {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
