package workitem

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestIssueManagerResultRequiresOneCompleteRussianIssuePerTask(t *testing.T) {
	task := domain.Task{
		ID: "task-id", PlannerKey: "orders", ProjectID: "project-id", RiskLevel: domain.RiskLevelHigh,
	}
	bundle := domain.PlanBundle{Tasks: []domain.Task{task}}
	contextValue := issueManagerContext{Projects: []issueManagerProjectContext{{
		Project:  domain.Project{ID: task.ProjectID},
		Metadata: repository.ProjectIssueMetadata{Assignees: []string{"marat"}},
	}}}
	raw, err := json.Marshal(domain.IssueManagerResult{
		Summary: "Подготовлена задача для сервиса заказов.",
		Issues: []domain.IssueDraft{{
			TaskKey: task.PlannerKey, ProjectID: task.ProjectID, IssueType: domain.IssueTypeTask,
			Title: "Добавить обработку заказов",
			Body:  russianIssueBody(), Labels: []string{"тип::задача", "приоритет::высокий"},
			Milestone: "Ближайший релиз", Assignees: []string{"marat"},
		}},
	})
	require.NoError(t, err)

	result, err := validateIssueManagerResult(raw, bundle, contextValue)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	require.Equal(t, domain.TaskComplexityHigh, result.Issues[0].Complexity)
	require.Equal(t, config.ModelProfileDeep, result.Issues[0].ModelProfile)
}

func TestIssueManagerResultRejectsUnavailableAssigneeAndIncompleteRussianBody(t *testing.T) {
	task := domain.Task{ID: "task-id", PlannerKey: "orders", ProjectID: "project-id", RiskLevel: domain.RiskLevelLow}
	bundle := domain.PlanBundle{Tasks: []domain.Task{task}}
	contextValue := issueManagerContext{Projects: []issueManagerProjectContext{{
		Project:  domain.Project{ID: task.ProjectID},
		Metadata: repository.ProjectIssueMetadata{Assignees: []string{"marat"}},
	}}}
	for name, draft := range map[string]domain.IssueDraft{
		"unavailable assignee": {
			TaskKey: task.PlannerKey, ProjectID: task.ProjectID, IssueType: domain.IssueTypeTask,
			Title: "Добавить обработку заказов", Body: russianIssueBody(), Labels: []string{"задача"},
			Milestone: "Релиз", Assignees: []string{"unknown"},
		},
		"missing sections": {
			TaskKey: task.PlannerKey, ProjectID: task.ProjectID, IssueType: domain.IssueTypeTask,
			Title: "Добавить обработку заказов", Body: "Описание задачи без обязательной структуры, которое остаётся достаточно длинным для отдельной проверки содержимого issue.",
			Labels: []string{"задача"}, Milestone: "Релиз", Assignees: []string{"marat"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(domain.IssueManagerResult{Summary: "Подготовлена задача.", Issues: []domain.IssueDraft{draft}})
			require.NoError(t, err)
			_, err = validateIssueManagerResult(raw, bundle, contextValue)
			require.ErrorIs(t, err, domain.ErrValidation)
		})
	}
}

func TestPullRequestManagerResultRequiresFullRussianMetadataAndExactBranches(t *testing.T) {
	task := domain.Task{ID: "task-id", ProjectID: "project-id", RiskLevel: domain.RiskLevelMedium}
	project := domain.Project{ID: task.ProjectID, DefaultBranch: "main"}
	attempt := domain.TaskAttempt{BranchName: "ai/task-orders"}
	metadata := repository.ProjectIssueMetadata{Assignees: []string{"marat"}, Reviewers: []string{"reviewer"}}
	raw, err := json.Marshal(domain.PullRequestManagerResult{
		Summary: "Подготовлен черновик изменений для проверки.",
		PullRequest: domain.PullRequestDraft{
			TaskID: task.ID, ProjectID: task.ProjectID, Title: "Реализовать обработку заказов",
			Body: russianPullRequestBody(), Labels: []string{"тип::задача"}, Milestone: "Ближайший релиз",
			Assignees: []string{"marat"}, Reviewers: []string{"reviewer"},
			SourceBranch: attempt.BranchName, TargetBranch: project.DefaultBranch,
		},
	})
	require.NoError(t, err)

	result, err := validatePullRequestManagerResult(raw, task, project, attempt, metadata)
	require.NoError(t, err)
	require.Equal(t, domain.TaskComplexityMedium, result.PullRequest.Complexity)
	require.Equal(t, config.ModelProfileStandard, result.PullRequest.ModelProfile)

	var invalid domain.PullRequestManagerResult
	require.NoError(t, json.Unmarshal(raw, &invalid))
	invalid.PullRequest.TargetBranch = "develop"
	raw, err = json.Marshal(invalid)
	require.NoError(t, err)
	_, err = validatePullRequestManagerResult(raw, task, project, attempt, metadata)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func russianIssueBody() string {
	return `## Контекст
Сервису заказов требуется согласованное изменение поведения.
## Цель
Обеспечить корректную обработку нового сценария.
## Ответственность репозитория
Репозиторий отвечает за доменную логику заказов.
## Объём работ
Реализовать код, тесты и документацию без выхода за утверждённый scope.
## Критерии приёмки
Все проверки проходят, а ожидаемое поведение подтверждено тестами.
## Зависимости
Зависимости перечислены в плане и исполняются в указанном порядке.
## Риски
Изменение может затронуть контракт, поэтому требуется независимая проверка.`
}

func russianPullRequestBody() string {
	return `## Связанная issue
Изменения реализуют опубликованную задачу проекта.
## Что сделано
Добавлена согласованная обработка сценария заказов и тесты.
## Изменённые компоненты
Обновлены доменная логика, проверки и документация.
## Проверки
Все команды из утверждённого плана выполнены успешно.
## Контракты и миграции
Изменения контрактов и миграций перечислены явно.
## Риски и ограничения
Оставшиеся риски ограничены утверждённым scope задачи.
## Проверка результата
Результат проверен отдельным reviewer-agent и готов к ручному просмотру.`
}
