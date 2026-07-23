package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
	planninguc "github.com/bemulima/agent-orchestrator/internal/usecase/planning"
)

func TestRouterPlanningAPI(t *testing.T) {
	command := domain.Command{ID: "command-id", Status: domain.CommandStatusReceived}
	plan := domain.Plan{ID: "plan-id", CommandID: command.ID, Status: domain.PlanStatusAwaitingApproval}
	task := domain.Task{ID: "task-id", PlanID: plan.ID, Status: domain.TaskStatusPlanned}
	run := domain.PlanRun{ID: "run-id", PlanID: plan.ID, Status: domain.PlanRunStatusRunning, WorkflowID: "workflow-id"}
	bundle := domain.PlanBundle{Plan: plan, Tasks: []domain.Task{task}, Run: &run}
	handler := &handlers.PlanningHandler{
		CreateCommand: planningCreateCommandFake{command: command}, GetCommand: planningGetCommandFake{command: command},
		CreatePlan: planningCreatePlanFake{bundle: bundle}, GetPlan: planningGetPlanFake{bundle: bundle},
		CommentPlan: planningDecidePlanFake{bundle: bundle}, SubmitPlan: planningDecidePlanFake{bundle: bundle},
		ApprovePlan: planningDecidePlanFake{bundle: bundle}, RejectPlan: planningDecidePlanFake{bundle: bundle},
		PrepareIssues: planningIssuesFake{}, PublishIssues: planningIssuesFake{},
		PreparePR: planningPullRequestFake{}, PublishPR: planningPullRequestFake{},
		StartPlan: planningStartPlanFake{run: run}, GetRun: planningGetRunFake{run: run},
		ControlRun: planningControlRunFake{run: run}, GetTask: planningGetTaskFake{task: task},
		CancelTask: planningCancelTaskFake{task: task}, RetryTask: planningRetryTaskFake{task: task},
		GetAttempts: planningGetAttemptsFake{}, GetArtifacts: planningGetArtifactsFake{},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}}, PlanningHandler: handler,
	})
	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodPost, "/api/v1/commands", `{"text":"change orders"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/commands/command-id", "", http.StatusOK},
		{http.MethodPost, "/api/v1/commands/command-id/plan", `{}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/plans/plan-id", "", http.StatusOK},
		{http.MethodGet, "/api/v1/plans/plan-id/tasks", "", http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/comments", `{"actor":"owner","comment":"Уточнение"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/issues/prepare", "", http.StatusCreated},
		{http.MethodPost, "/api/v1/plans/plan-id/submit", `{"actor":"owner"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/approve", `{}`, http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/issues/publish", "", http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/reject", `{}`, http.StatusOK},
		{http.MethodPost, "/api/v1/plans/plan-id/run", "", http.StatusAccepted},
		{http.MethodGet, "/api/v1/runs/run-id", "", http.StatusOK},
		{http.MethodPost, "/api/v1/runs/run-id/pause", "", http.StatusAccepted},
		{http.MethodPost, "/api/v1/runs/run-id/resume", "", http.StatusAccepted},
		{http.MethodPost, "/api/v1/runs/run-id/cancel", "", http.StatusAccepted},
		{http.MethodGet, "/api/v1/tasks/task-id", "", http.StatusOK},
		{http.MethodGet, "/api/v1/tasks/task-id/attempts", "", http.StatusOK},
		{http.MethodGet, "/api/v1/tasks/task-id/artifacts", "", http.StatusOK},
		{http.MethodPost, "/api/v1/tasks/task-id/retry", "", http.StatusAccepted},
		{http.MethodPost, "/api/v1/tasks/task-id/cancel", "", http.StatusAccepted},
		{http.MethodPost, "/api/v1/tasks/task-id/pull-request/prepare", "", http.StatusCreated},
		{http.MethodPost, "/api/v1/work-items/work-item-id/publish", "", http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestRouterPlanningRejectsUnknownCommandField(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler:   handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		PlanningHandler: &handlers.PlanningHandler{CreateCommand: planningCreateCommandFake{}},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/commands", bytes.NewBufferString(`{"text":"fixture","secret":"no"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}

type planningCreateCommandFake struct{ command domain.Command }

func (f planningCreateCommandFake) Handle(context.Context, planninguc.CreateCommandInput) (domain.Command, error) {
	return f.command, nil
}

type planningGetCommandFake struct{ command domain.Command }

func (f planningGetCommandFake) Handle(context.Context, string) (domain.Command, error) {
	return f.command, nil
}

type planningCreatePlanFake struct{ bundle domain.PlanBundle }

func (f planningCreatePlanFake) Handle(context.Context, string, domain.PlanRequest) (domain.PlanBundle, error) {
	return f.bundle, nil
}

type planningGetPlanFake struct{ bundle domain.PlanBundle }

func (f planningGetPlanFake) Handle(context.Context, string) (domain.PlanBundle, error) {
	return f.bundle, nil
}

type planningDecidePlanFake struct{ bundle domain.PlanBundle }

func (f planningDecidePlanFake) Handle(context.Context, planninguc.DecidePlanInput) (domain.PlanBundle, error) {
	return f.bundle, nil
}

type planningIssuesFake struct{}

func (planningIssuesFake) Prepare(context.Context, string) ([]domain.WorkItem, error) {
	return []domain.WorkItem{}, nil
}

func (planningIssuesFake) Publish(context.Context, string) ([]domain.WorkItem, error) {
	return []domain.WorkItem{}, nil
}

type planningPullRequestFake struct{}

func (planningPullRequestFake) Prepare(context.Context, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, nil
}

func (planningPullRequestFake) Publish(context.Context, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, nil
}

type planningStartPlanFake struct{ run domain.PlanRun }

func (f planningStartPlanFake) Handle(context.Context, string) (domain.PlanRun, error) {
	return f.run, nil
}

type planningGetRunFake struct{ run domain.PlanRun }

func (f planningGetRunFake) Handle(context.Context, string) (domain.PlanRun, error) {
	return f.run, nil
}

type planningControlRunFake struct{ run domain.PlanRun }

func (f planningControlRunFake) Handle(context.Context, planninguc.ControlRunInput) (domain.PlanRun, error) {
	return f.run, nil
}

type planningGetTaskFake struct{ task domain.Task }

func (f planningGetTaskFake) Handle(context.Context, string) (domain.Task, error) { return f.task, nil }

type planningCancelTaskFake struct{ task domain.Task }

func (f planningCancelTaskFake) Handle(context.Context, string) (domain.Task, error) {
	return f.task, nil
}

type planningRetryTaskFake struct{ task domain.Task }

func (f planningRetryTaskFake) Handle(context.Context, string) (domain.Task, error) {
	return f.task, nil
}

type planningGetAttemptsFake struct{}

func (planningGetAttemptsFake) Handle(context.Context, string) ([]domain.TaskAttempt, error) {
	return []domain.TaskAttempt{}, nil
}

type planningGetArtifactsFake struct{}

func (planningGetArtifactsFake) Handle(context.Context, string) ([]domain.Artifact, error) {
	return []domain.Artifact{}, nil
}
