package agentpolicy

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func testRouter() Router {
	return Router{FastModel: "luna", StandardModel: "terra", DeepModel: "sol", CoderModel: "spark", ReviewModel: "terra",
		FastReasoning: "low", StandardReasoning: "medium", DeepReasoning: "high", ReviewReasoning: "medium"}
}

func TestRouterKeepsCodingOnSparkAndEscalatesOnlyReview(t *testing.T) {
	router := testRouter()
	critical := domain.Task{ID: "task", ModelProfile: config.ModelProfileDeep, RiskLevel: domain.RiskLevelHigh, ChangesContracts: true}
	require.Equal(t, Decision{Model: "spark", Reasoning: "medium", Reason: "Spark writes all code; complex work is bounded by planning and review"}, router.Coder(critical))
	require.Equal(t, "sol", router.Reviewer(critical).Model)
	require.Equal(t, "high", router.Reviewer(critical).Reasoning)
	routine := domain.Task{ModelProfile: config.ModelProfileStandard, RiskLevel: domain.RiskLevelMedium}
	require.Equal(t, "terra", router.Reviewer(routine).Model)
}

func TestRouterUsesCheapOperatorLookupsAndExplicitDeepAnalysis(t *testing.T) {
	router := testRouter()
	require.Equal(t, "luna", router.Operator("Покажи статус текущего плана").Model)
	require.Equal(t, "terra", router.Operator("Обсудим новую задачу").Model)
	require.Equal(t, "sol", router.Operator("Нужен глубокий анализ сложной архитектуры").Model)
}

func TestRouterSelectsPlannerByRiskAndBreadth(t *testing.T) {
	router := testRouter()
	require.Equal(t, "luna", router.Planner([]domain.PlannedTask{{RiskLevel: domain.RiskLevelLow}}).Model)
	require.Equal(t, "terra", router.Planner([]domain.PlannedTask{{RiskLevel: domain.RiskLevelMedium}}).Model)
	require.Equal(t, "sol", router.Planner([]domain.PlannedTask{{RiskLevel: domain.RiskLevelHigh}}).Model)
}
