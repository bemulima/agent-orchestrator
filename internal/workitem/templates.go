package workitem

import (
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func deterministicIssues(bundle domain.PlanBundle, value issueManagerContext) (domain.IssueManagerResult, error) {
	projects := map[string]issueManagerProjectContext{}
	for _, project := range value.Projects {
		projects[project.Project.ID] = project
	}
	tasks := map[string]domain.Task{}
	for _, task := range bundle.Tasks {
		tasks[task.ID] = task
	}
	result := domain.IssueManagerResult{Summary: "Подготовлены детерминированные предложения issue без расхода модели."}
	for _, task := range bundle.Tasks {
		project, ok := projects[task.ProjectID]
		if !ok {
			return domain.IssueManagerResult{}, fmt.Errorf("missing project metadata for template: %w", domain.ErrValidation)
		}
		labels, milestone, assignee, err := templateMetadata(project.Metadata, false)
		if err != nil {
			return domain.IssueManagerResult{}, err
		}
		dependencies := []string{"- Нет обязательных зависимостей."}
		for _, dependency := range bundle.Dependencies {
			if dependency.TaskID == task.ID {
				dependencies = append(dependencies[:0], "- "+tasks[dependency.DependsOnTaskID].Title)
			}
		}
		body := strings.Join([]string{
			"## Контекст", bundle.Plan.Summary,
			"## Цель", task.Description,
			"## Ответственность репозитория", "Изменения ограничены репозиторием «" + project.Project.Name + "».",
			"## Объём работ", bullets(task.WriteScope),
			"## Критерии приёмки", bullets(task.AcceptanceCriteria),
			"## Зависимости", strings.Join(dependencies, "\n"),
			"## Риски", riskText(task),
		}, "\n\n")
		draft := domain.IssueDraft{TaskKey: task.PlannerKey, ProjectID: task.ProjectID, IssueType: domain.IssueTypeTask,
			Title: draftTitle(task.Title), Body: body, Labels: labels, Milestone: milestone, Assignees: []string{assignee}}
		for _, source := range value.SourceIssues {
			if source.ProjectID == task.ProjectID {
				copy := source
				draft.Existing = &copy
				break
			}
		}
		result.Issues = append(result.Issues, draft)
	}
	return result, nil
}

func deterministicPullRequest(task domain.Task, project domain.Project, attempt domain.TaskAttempt, issue domain.WorkItem, metadata repository.ProjectIssueMetadata) (domain.PullRequestManagerResult, error) {
	labels, milestone, assignee, err := templateMetadata(metadata, true)
	if err != nil {
		return domain.PullRequestManagerResult{}, err
	}
	target := strings.TrimSpace(project.DefaultBranch)
	if target == "" {
		target = "main"
	}
	reviewer := metadata.Reviewers[0]
	issueLink := issue.ExternalURL
	if issueLink == "" {
		issueLink = issue.Title
	}
	body := strings.Join([]string{
		"## Связанная issue", issueLink,
		"## Что сделано", task.Description,
		"## Изменённые компоненты", bullets(task.WriteScope),
		"## Проверки", bullets(task.VerificationCommands),
		"## Контракты и миграции", contractText(task),
		"## Риски и ограничения", riskText(task),
		"## Проверка результата", "Изменения прошли независимую верификацию и reviewer-проверку оркестратора.",
	}, "\n\n")
	return domain.PullRequestManagerResult{Summary: "Подготовлен детерминированный черновик PR без расхода модели.", PullRequest: domain.PullRequestDraft{
		TaskID: task.ID, ProjectID: task.ProjectID, Title: draftTitle(task.Title), Body: body, Labels: labels, Milestone: milestone,
		Assignees: []string{assignee}, Reviewers: []string{reviewer}, SourceBranch: attempt.BranchName, TargetBranch: target,
	}}, nil
}

func templateMetadata(metadata repository.ProjectIssueMetadata, reviewers bool) ([]string, string, string, error) {
	if len(metadata.Labels) == 0 || len(metadata.Milestones) == 0 || len(metadata.Assignees) == 0 || reviewers && len(metadata.Reviewers) == 0 {
		return nil, "", "", fmt.Errorf("repository metadata is incomplete for deterministic draft: %w", domain.ErrValidation)
	}
	return []string{metadata.Labels[0]}, metadata.Milestones[0], metadata.Assignees[0], nil
}

func bullets(values []string) string {
	if len(values) == 0 {
		return "- Не требуется дополнительных изменений."
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, "- "+strings.TrimSpace(value))
	}
	return strings.Join(result, "\n")
}

func riskText(task domain.Task) string {
	values := []string{"- Уровень риска: " + string(task.RiskLevel) + "."}
	if task.RequiresMigration {
		values = append(values, "- Требуется проверить парность и обратимость миграций.")
	}
	if task.ChangesContracts {
		values = append(values, "- Изменение контрактов требует проверки совместимости потребителей.")
	}
	return strings.Join(values, "\n")
}

func contractText(task domain.Task) string {
	if !task.RequiresMigration && !task.ChangesContracts {
		return "Миграции и публичные контракты не изменяются."
	}
	return riskText(task)
}

func draftTitle(value string) string {
	value = strings.TrimSpace(value)
	if !hasCyrillic(value) || len([]rune(value)) < 10 {
		return "Выполнить задачу: " + value
	}
	return value
}
