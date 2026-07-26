package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type ListInput struct {
	Limit     int
	Cursor    string
	Statuses  []string
	ProjectID string
	PlanID    string
}

type QueryService struct {
	Reads repository.UIReadRepository
}

func (s QueryService) Dashboard(ctx context.Context) (domain.Dashboard, error) {
	result, err := s.Reads.Dashboard(ctx)
	if err != nil {
		return domain.Dashboard{}, err
	}
	for index := range result.ActiveRuns {
		result.ActiveRuns[index].AllowedActions = runActions(result.ActiveRuns[index].Status)
	}
	return result, nil
}

func (s QueryService) Plans(ctx context.Context, input ListInput) (domain.PlanSummaryPage, error) {
	query, err := pageQuery(input)
	if err != nil {
		return domain.PlanSummaryPage{}, err
	}
	items, more, err := s.Reads.ListPlans(ctx, query)
	if err != nil {
		return domain.PlanSummaryPage{}, err
	}
	for index := range items {
		items[index].AllowedActions = planActions(items[index])
	}
	return domain.PlanSummaryPage{Items: items, PageInfo: pageInfo(more, lastPlan(items))}, nil
}

func (s QueryService) Runs(ctx context.Context, input ListInput) (domain.RunSummaryPage, error) {
	query, err := pageQuery(input)
	if err != nil {
		return domain.RunSummaryPage{}, err
	}
	items, more, err := s.Reads.ListRuns(ctx, query)
	if err != nil {
		return domain.RunSummaryPage{}, err
	}
	for index := range items {
		items[index].AllowedActions = runActions(items[index].Status)
	}
	return domain.RunSummaryPage{Items: items, PageInfo: pageInfo(more, lastRun(items))}, nil
}

func (s QueryService) Tasks(ctx context.Context, input ListInput) (domain.TaskSummaryPage, error) {
	query, err := pageQuery(input)
	if err != nil {
		return domain.TaskSummaryPage{}, err
	}
	items, more, err := s.Reads.ListTasks(ctx, query)
	if err != nil {
		return domain.TaskSummaryPage{}, err
	}
	for index := range items {
		items[index].AllowedActions = taskActions(items[index].Status)
	}
	return domain.TaskSummaryPage{Items: items, PageInfo: pageInfo(more, lastTask(items))}, nil
}

func (s QueryService) Approvals(ctx context.Context, input ListInput) (domain.ApprovalSummaryPage, error) {
	query, err := pageQuery(input)
	if err != nil {
		return domain.ApprovalSummaryPage{}, err
	}
	items, more, err := s.Reads.ListApprovals(ctx, query)
	if err != nil {
		return domain.ApprovalSummaryPage{}, err
	}
	return domain.ApprovalSummaryPage{Items: items, PageInfo: pageInfo(more, lastApproval(items))}, nil
}

func (s QueryService) Activity(ctx context.Context, input ListInput) (domain.ActivityPage, error) {
	query, err := pageQuery(input)
	if err != nil {
		return domain.ActivityPage{}, err
	}
	items, more, err := s.Reads.ListActivity(ctx, query)
	if err != nil {
		return domain.ActivityPage{}, err
	}
	return domain.ActivityPage{Items: items, PageInfo: pageInfo(more, lastActivity(items))}, nil
}

type cursorEnvelope struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func pageQuery(input ListInput) (domain.PageQuery, error) {
	query := domain.PageQuery{
		Limit: input.Limit, Statuses: compact(input.Statuses),
		ProjectID: strings.TrimSpace(input.ProjectID), PlanID: strings.TrimSpace(input.PlanID),
	}
	if query.Limit < 0 || query.Limit > 100 {
		return domain.PageQuery{}, fmt.Errorf("limit must be between 1 and 100: %w", domain.ErrValidation)
	}
	for _, id := range []string{query.ProjectID, query.PlanID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return domain.PageQuery{}, fmt.Errorf("invalid resource ID: %w", domain.ErrValidation)
			}
		}
	}
	if strings.TrimSpace(input.Cursor) == "" {
		return query, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(input.Cursor))
	if err != nil {
		return domain.PageQuery{}, fmt.Errorf("invalid cursor: %w", domain.ErrValidation)
	}
	var decoded cursorEnvelope
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.At.IsZero() {
		return domain.PageQuery{}, fmt.Errorf("invalid cursor: %w", domain.ErrValidation)
	}
	if _, err := uuid.Parse(decoded.ID); err != nil {
		return domain.PageQuery{}, fmt.Errorf("invalid cursor: %w", domain.ErrValidation)
	}
	query.Cursor = &domain.PageCursor{At: decoded.At, ID: decoded.ID}
	return query, nil
}

func compact(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func pageInfo(more bool, cursor *domain.PageCursor) domain.PageInfo {
	info := domain.PageInfo{HasMore: more}
	if !more || cursor == nil {
		return info
	}
	raw, _ := json.Marshal(cursorEnvelope{At: cursor.At, ID: cursor.ID})
	info.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	return info
}

func lastPlan(items []domain.PlanSummary) *domain.PageCursor {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &domain.PageCursor{At: item.UpdatedAt, ID: item.ID}
}

func lastRun(items []domain.RunSummary) *domain.PageCursor {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &domain.PageCursor{At: item.UpdatedAt, ID: item.ID}
}

func lastTask(items []domain.TaskSummary) *domain.PageCursor {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &domain.PageCursor{At: item.UpdatedAt, ID: item.ID}
}

func lastApproval(items []domain.ApprovalSummary) *domain.PageCursor {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &domain.PageCursor{At: item.RequestedAt, ID: item.ID}
}

func lastActivity(items []domain.ActivityEvent) *domain.PageCursor {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &domain.PageCursor{At: item.CreatedAt, ID: item.ID}
}

func action(name string, fingerprint bool) domain.ResourceAction {
	return domain.ResourceAction{Action: name, RequiresConfirmation: true, RequiresFingerprint: fingerprint}
}

func planActions(plan domain.PlanSummary) []domain.ResourceAction {
	switch plan.Status {
	case domain.PlanStatusDiscussion:
		if plan.IssueCount < plan.TaskCount {
			return []domain.ResourceAction{action("prepare_issues", false)}
		}
		return []domain.ResourceAction{action("submit", false)}
	case domain.PlanStatusReadyForApproval:
		return []domain.ResourceAction{action("submit", false)}
	case domain.PlanStatusAwaitingApproval:
		return []domain.ResourceAction{action("approve", true), action("reject", false)}
	case domain.PlanStatusApproved:
		if plan.PublishedIssues < plan.TaskCount {
			return []domain.ResourceAction{action("publish_issues", false)}
		}
		return []domain.ResourceAction{action("run", false)}
	default:
		return []domain.ResourceAction{}
	}
}

func runActions(status domain.PlanRunStatus) []domain.ResourceAction {
	switch status {
	case domain.PlanRunStatusRunning:
		return []domain.ResourceAction{action("pause", false), action("cancel", false)}
	case domain.PlanRunStatusPaused:
		return []domain.ResourceAction{action("resume", false), action("cancel", false)}
	default:
		return []domain.ResourceAction{}
	}
}

func taskActions(status domain.TaskStatus) []domain.ResourceAction {
	switch status {
	case domain.TaskStatusBlocked, domain.TaskStatusChangesRequested:
		return []domain.ResourceAction{action("retry", false), action("cancel", false)}
	case domain.TaskStatusDraft, domain.TaskStatusPlanned, domain.TaskStatusReady,
		domain.TaskStatusRunning, domain.TaskStatusVerification:
		return []domain.ResourceAction{action("cancel", false)}
	default:
		return []domain.ResourceAction{}
	}
}
