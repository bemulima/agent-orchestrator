package agentusage

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type TrackedRunner struct {
	Base         repository.AgentRunner
	Usage        repository.AgentUsageRepository
	Now          func() time.Time
	BudgetMode   string
	DeepModel    string
	DeepRunLimit int64
	XHighAllowed bool
}

func (r TrackedRunner) Run(ctx context.Context, request domain.AgentRunRequest, onThread repository.AgentThreadCallback) (domain.AgentRunResponse, error) {
	started := r.now()
	if request.ReasoningEffort == "xhigh" && !r.XHighAllowed {
		return domain.AgentRunResponse{}, fmt.Errorf("xhigh reasoning requires an explicit owner configuration: %w", domain.ErrApprovalNeeded)
	}
	value := domain.AgentRunUsage{
		ID: uuid.NewString(), Role: request.Role, Model: request.Model, ReasoningEffort: request.ReasoningEffort,
		RouteReason: "configured profile", Status: "running", StartedAt: started,
	}
	if request.UsageContext != nil {
		value.ResourceType = request.UsageContext.ResourceType
		value.ResourceID = request.UsageContext.ResourceID
		if request.UsageContext.RouteReason != "" {
			value.RouteReason = request.UsageContext.RouteReason
		}
	}
	if err := r.Usage.RecordAgentRun(context.WithoutCancel(ctx), value); err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("reserve agent usage: %w", err)
	}
	if r.BudgetMode == "enforce" && request.Model == r.DeepModel && r.DeepRunLimit > 0 {
		window, err := r.Usage.AgentUsageWindow(ctx, started.Add(-5*time.Hour))
		if err != nil {
			value.Status, value.CompletedAt = "failed", r.now()
			_ = r.Usage.RecordAgentRun(context.WithoutCancel(ctx), value)
			return domain.AgentRunResponse{}, fmt.Errorf("check deep-model budget: %w", err)
		}
		if runsFor(window.ByModel, r.DeepModel) > r.DeepRunLimit {
			value.Status, value.CompletedAt = "denied", r.now()
			_ = r.Usage.RecordAgentRun(context.WithoutCancel(ctx), value)
			return domain.AgentRunResponse{}, fmt.Errorf("deep-model five-hour budget is exhausted: %w", domain.ErrApprovalNeeded)
		}
	}
	response, runErr := r.Base.Run(ctx, request, onThread)
	completed := r.now()
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	value.ThreadID, value.Status = response.ThreadID, status
	value.InputTokens, value.CachedInputTokens = response.Usage.InputTokens, response.Usage.CachedInputTokens
	value.OutputTokens, value.ReasoningOutputTokens = response.Usage.OutputTokens, response.Usage.ReasoningOutputTokens
	value.DurationMilliseconds, value.CompletedAt = completed.Sub(started).Milliseconds(), completed
	// Usage persistence must never repeat an already consumed model turn.
	_ = r.Usage.RecordAgentRun(context.WithoutCancel(ctx), value)
	return response, runErr
}

func (r TrackedRunner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

var _ repository.AgentRunner = TrackedRunner{}
