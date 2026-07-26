package ui

import (
	"context"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type readsFake struct {
	plans []domain.PlanSummary
}

func (f readsFake) Dashboard(context.Context) (domain.Dashboard, error) {
	return domain.Dashboard{}, nil
}
func (f readsFake) ListPlans(context.Context, domain.PageQuery) ([]domain.PlanSummary, bool, error) {
	return f.plans, false, nil
}
func (f readsFake) ListRuns(context.Context, domain.PageQuery) ([]domain.RunSummary, bool, error) {
	return nil, false, nil
}
func (f readsFake) ListTasks(context.Context, domain.PageQuery) ([]domain.TaskSummary, bool, error) {
	return nil, false, nil
}
func (f readsFake) ListApprovals(context.Context, domain.PageQuery) ([]domain.ApprovalSummary, bool, error) {
	return nil, false, nil
}
func (f readsFake) ListActivity(context.Context, domain.PageQuery) ([]domain.ActivityEvent, bool, error) {
	return nil, false, nil
}

func TestPlansExposeBackendAllowedActions(t *testing.T) {
	service := QueryService{Reads: readsFake{plans: []domain.PlanSummary{
		{ID: "00000000-0000-4000-8000-000000000001", Status: domain.PlanStatusAwaitingApproval, UpdatedAt: time.Now()},
	}}}
	page, err := service.Plans(context.Background(), ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].AllowedActions) != 2 || page.Items[0].AllowedActions[0].Action != "approve" || !page.Items[0].AllowedActions[0].RequiresFingerprint {
		t.Fatalf("allowed actions = %#v", page.Items[0].AllowedActions)
	}
}

func TestPageQueryRejectsInvalidCursor(t *testing.T) {
	if _, err := pageQuery(ListInput{Cursor: "not-base64"}); err == nil {
		t.Fatal("expected invalid cursor error")
	}
}
