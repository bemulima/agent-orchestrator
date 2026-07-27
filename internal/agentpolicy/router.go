package agentpolicy

import (
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type Decision struct {
	Model     string
	Reasoning string
	Reason    string
}

type Router struct {
	FastModel, StandardModel, DeepModel, CoderModel, ReviewModel     string
	FastReasoning, StandardReasoning, DeepReasoning, ReviewReasoning string
}

func FromConfig(cfg config.Config) Router {
	return Router{
		FastModel: cfg.CodexModelFast, StandardModel: cfg.CodexModelStandard,
		DeepModel: cfg.CodexModelDeep, CoderModel: cfg.CodexModelCoder, ReviewModel: cfg.CodexModelReview,
		FastReasoning: cfg.CodexReasoningFast, StandardReasoning: cfg.CodexReasoningStandard,
		DeepReasoning: cfg.CodexReasoningDeep, ReviewReasoning: cfg.CodexReasoningReview,
	}
}

func (r Router) Coder(task domain.Task) Decision {
	effort := r.FastReasoning
	if task.ModelProfile != config.ModelProfileFast {
		effort = "medium"
	}
	return Decision{Model: r.CoderModel, Reasoning: effort, Reason: "Spark writes all code; complex work is bounded by planning and review"}
}

func (r Router) Reviewer(task domain.Task) Decision {
	if task.RiskLevel == domain.RiskLevelHigh || task.RiskLevel == domain.RiskLevelCritical || task.RequiresMigration || task.ChangesContracts {
		return Decision{Model: r.DeepModel, Reasoning: r.DeepReasoning, Reason: "critical review: high risk, migration, or contract change"}
	}
	effort := "low"
	if task.RiskLevel == domain.RiskLevelMedium {
		effort = r.ReviewReasoning
	}
	return Decision{Model: r.ReviewModel, Reasoning: effort, Reason: "independent Terra review for routine code"}
}

func (r Router) Planner(tasks []domain.PlannedTask) Decision {
	critical := false
	for _, task := range tasks {
		if task.RiskLevel == domain.RiskLevelHigh || task.RiskLevel == domain.RiskLevelCritical || task.RequiresMigration || task.ChangesContracts {
			critical = true
			break
		}
	}
	if critical || len(tasks) > 3 {
		return Decision{Model: r.DeepModel, Reasoning: r.DeepReasoning, Reason: "complex planner: critical signals or broad multi-repository scope"}
	}
	if len(tasks) == 1 && tasks[0].RiskLevel == domain.RiskLevelLow {
		return Decision{Model: r.FastModel, Reasoning: r.FastReasoning, Reason: "lightweight single-project plan"}
	}
	return Decision{Model: r.StandardModel, Reasoning: r.StandardReasoning, Reason: "balanced planning for normal scope"}
}

func (r Router) Manager() Decision {
	return Decision{Model: r.FastModel, Reasoning: r.FastReasoning, Reason: "bounded issue/PR drafting uses the high-volume profile"}
}

func (r Router) Analyst() Decision {
	return Decision{Model: r.StandardModel, Reasoning: r.StandardReasoning, Reason: "semantic onboarding uses balanced analysis by default"}
}

func (r Router) Operator(message string) Decision {
	value := strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{"глубокий анализ", "глубоко проанализ", "критический анализ", "сложная архитектура", "deep analysis", "critical analysis"} {
		if strings.Contains(value, marker) {
			return Decision{Model: r.DeepModel, Reasoning: r.DeepReasoning, Reason: "owner explicitly requested difficult analysis"}
		}
	}
	for _, marker := range []string{"статус", "что выполняется", "что сейчас", "покажи", "список", "status", "show", "list"} {
		if strings.Contains(value, marker) {
			return Decision{Model: r.FastModel, Reasoning: r.FastReasoning, Reason: "read-only status or lookup request"}
		}
	}
	return Decision{Model: r.StandardModel, Reasoning: r.StandardReasoning, Reason: "normal owner discussion"}
}
