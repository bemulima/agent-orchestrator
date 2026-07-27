package workitem

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestDeterministicIssuesProduceCompleteRussianDrafts(t *testing.T) {
	task := domain.Task{ID: "task", ProjectID: "project", PlannerKey: "orders", Title: "Добавить проверку заказов",
		Description: "Реализовать проверку заказов в пределах выбранного репозитория.", RiskLevel: domain.RiskLevelMedium,
		WriteScope: []string{"internal/orders/**"}, AcceptanceCriteria: []string{"Все тесты проходят"}}
	bundle := domain.PlanBundle{Plan: domain.Plan{Summary: "Повысить надёжность обработки заказов."}, Tasks: []domain.Task{task}}
	contextValue := issueManagerContext{Projects: []issueManagerProjectContext{{Project: domain.Project{ID: "project", Name: "orders"}, Metadata: repository.ProjectIssueMetadata{
		Labels: []string{"тип::задача"}, Milestones: []string{"Ближайший релиз"}, Assignees: []string{"owner"},
	}}}}
	result, err := deterministicIssues(bundle, contextValue)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	require.True(t, hasRequiredIssueSections(result.Issues[0].Body))
}

func TestDeterministicPullRequestUsesVerifiedTaskContext(t *testing.T) {
	task := domain.Task{ID: "task", ProjectID: "project", Title: "Добавить проверку заказов",
		Description: "Реализована проверка заказов согласно утверждённым критериям.", RiskLevel: domain.RiskLevelLow,
		WriteScope: []string{"internal/orders/**"}, VerificationCommands: []string{"go test ./..."}}
	result, err := deterministicPullRequest(task, domain.Project{DefaultBranch: "main"}, domain.TaskAttempt{BranchName: "ai/task-orders"},
		domain.WorkItem{Title: "Связанная задача", ExternalURL: "https://github.example.test/issues/1"}, repository.ProjectIssueMetadata{
			Labels: []string{"тип::задача"}, Milestones: []string{"Ближайший релиз"}, Assignees: []string{"owner"}, Reviewers: []string{"reviewer"},
		})
	require.NoError(t, err)
	require.True(t, hasRequiredPullRequestSections(result.PullRequest.Body))
	require.Equal(t, "ai/task-orders", result.PullRequest.SourceBranch)
}
