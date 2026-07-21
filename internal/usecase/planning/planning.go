package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type CreateCommandInput struct {
	Source         domain.CommandSource `json:"source"`
	SourceUserID   *string              `json:"source_user_id,omitempty"`
	Text           string               `json:"text"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
}

type CreateCommand struct {
	Plans repository.PlanningRepository
}

func (uc CreateCommand) Handle(ctx context.Context, input CreateCommandInput) (domain.Command, error) {
	input.Text = strings.TrimSpace(input.Text)
	if input.Source == "" {
		input.Source = domain.CommandSourceAPI
	}
	if input.Text == "" {
		return domain.Command{}, fmt.Errorf("command text is required: %w", domain.ErrValidation)
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		user := ""
		if input.SourceUserID != nil {
			user = strings.TrimSpace(*input.SourceUserID)
		}
		input.IdempotencyKey = commandKey(input.Source, user, input.Text)
	}
	return uc.Plans.CreateCommand(ctx, domain.Command{
		Source: input.Source, SourceUserID: input.SourceUserID, Text: input.Text,
		Status: domain.CommandStatusReceived, IdempotencyKey: input.IdempotencyKey,
	})
}

type GetCommand struct {
	Plans repository.PlanningRepository
}

func (uc GetCommand) Handle(ctx context.Context, id string) (domain.Command, error) {
	return uc.Plans.GetCommand(ctx, strings.TrimSpace(id))
}

type CreatePlan struct {
	Plans     repository.PlanningRepository
	Topology  repository.TopologyRepository
	Planner   repository.Planner
	Validator repository.PlanValidator
}

func (uc CreatePlan) Handle(ctx context.Context, commandID string, request domain.PlanRequest) (domain.PlanBundle, error) {
	command, err := uc.Plans.GetCommand(ctx, strings.TrimSpace(commandID))
	if err != nil {
		return domain.PlanBundle{}, err
	}
	catalog, err := uc.Topology.Get(ctx)
	if err != nil {
		return domain.PlanBundle{}, err
	}
	input, output, err := uc.Planner.Build(ctx, command, catalog, request)
	if err != nil {
		return domain.PlanBundle{}, err
	}
	if err := uc.Validator.Validate(ctx, output); err != nil {
		return domain.PlanBundle{}, err
	}
	return uc.Plans.CreatePlan(ctx, command, input, output)
}

type GetPlan struct {
	Plans repository.PlanningRepository
}

func (uc GetPlan) Handle(ctx context.Context, id string) (domain.PlanBundle, error) {
	return uc.Plans.GetPlan(ctx, strings.TrimSpace(id))
}

type DecidePlanInput struct {
	PlanID  string `json:"-"`
	Actor   string `json:"actor"`
	Comment string `json:"comment,omitempty"`
}

type ApprovePlan struct {
	Plans repository.PlanningRepository
}

func (uc ApprovePlan) Handle(ctx context.Context, input DecidePlanInput) (domain.PlanBundle, error) {
	return uc.Plans.ApprovePlan(ctx, strings.TrimSpace(input.PlanID), strings.TrimSpace(input.Actor), strings.TrimSpace(input.Comment))
}

type RejectPlan struct {
	Plans repository.PlanningRepository
}

func (uc RejectPlan) Handle(ctx context.Context, input DecidePlanInput) (domain.PlanBundle, error) {
	return uc.Plans.RejectPlan(ctx, strings.TrimSpace(input.PlanID), strings.TrimSpace(input.Actor), strings.TrimSpace(input.Comment))
}

type StartPlan struct {
	Plans               repository.PlanningRepository
	Runner              repository.PlanRunner
	MaxParallelTasks    int
	MaxActivityAttempts int
}

func (uc StartPlan) Handle(ctx context.Context, planID string) (domain.PlanRun, error) {
	run, bundle, err := uc.Plans.PrepareRun(ctx, strings.TrimSpace(planID), uc.MaxParallelTasks)
	if err != nil {
		return domain.PlanRun{}, err
	}
	if run.TemporalRunID != nil {
		return run, nil
	}
	schedule := buildSchedule(run, bundle, uc.MaxActivityAttempts)
	temporalRunID, err := uc.Runner.Start(ctx, run, schedule)
	if err != nil {
		return domain.PlanRun{}, err
	}
	return uc.Plans.AttachTemporalRun(ctx, run.ID, temporalRunID)
}

type GetRun struct {
	Plans repository.PlanningRepository
}

func (uc GetRun) Handle(ctx context.Context, id string) (domain.PlanRun, error) {
	return uc.Plans.GetRun(ctx, strings.TrimSpace(id))
}

type ControlRunInput struct {
	RunID  string
	Action domain.RunControlAction
}

type ControlRun struct {
	Plans  repository.PlanningRepository
	Runner repository.PlanRunner
}

func (uc ControlRun) Handle(ctx context.Context, input ControlRunInput) (domain.PlanRun, error) {
	run, err := uc.Plans.GetRun(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return domain.PlanRun{}, err
	}
	if !allowedControl(run.Status, input.Action) {
		return domain.PlanRun{}, fmt.Errorf("run %s cannot %s: %w", run.Status, input.Action, domain.ErrInvalidStatus)
	}
	if err := uc.Runner.Control(ctx, run.WorkflowID, input.Action); err != nil {
		return domain.PlanRun{}, err
	}
	return run, nil
}

type GetTask struct {
	Plans repository.PlanningRepository
}

func (uc GetTask) Handle(ctx context.Context, id string) (domain.Task, error) {
	return uc.Plans.GetTask(ctx, strings.TrimSpace(id))
}

type CancelTask struct {
	Plans  repository.PlanningRepository
	Runner repository.PlanRunner
}

func (uc CancelTask) Handle(ctx context.Context, id string) (domain.Task, error) {
	task, err := uc.Plans.GetTask(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Task{}, err
	}
	if task.Status == domain.TaskStatusCompleted || task.Status == domain.TaskStatusFailed || task.Status == domain.TaskStatusCancelled {
		return domain.Task{}, fmt.Errorf("task is already terminal: %w", domain.ErrInvalidStatus)
	}
	bundle, err := uc.Plans.GetPlan(ctx, task.PlanID)
	if err != nil {
		return domain.Task{}, err
	}
	if bundle.Run == nil {
		return domain.Task{}, fmt.Errorf("task plan has no active run: %w", domain.ErrInvalidStatus)
	}
	if err := uc.Runner.ReportTaskResult(ctx, bundle.Run.WorkflowID, domain.TaskResult{
		TaskID: task.ID, Status: domain.TaskStatusCancelled, Error: "cancelled by owner",
	}); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func buildSchedule(run domain.PlanRun, bundle domain.PlanBundle, maxAttempts int) domain.PlanSchedule {
	dependencies := make(map[string][]string)
	for _, dependency := range bundle.Dependencies {
		dependencies[dependency.TaskID] = append(dependencies[dependency.TaskID], dependency.DependsOnTaskID)
	}
	tasks := make([]domain.ScheduledTask, 0, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		tasks = append(tasks, domain.ScheduledTask{
			TaskID: task.ID, Priority: task.Priority,
			Dependencies: append([]string(nil), dependencies[task.ID]...),
		})
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return domain.PlanSchedule{
		RunID: run.ID, PlanID: run.PlanID, MaxParallelTasks: run.MaxParallelTasks,
		MaxActivityAttempts: maxAttempts, Tasks: tasks,
	}
}

func allowedControl(status domain.PlanRunStatus, action domain.RunControlAction) bool {
	switch action {
	case domain.RunControlPause:
		return status == domain.PlanRunStatusPending || status == domain.PlanRunStatusRunning
	case domain.RunControlResume:
		return status == domain.PlanRunStatusPaused
	case domain.RunControlCancel:
		return status == domain.PlanRunStatusPending || status == domain.PlanRunStatusRunning || status == domain.PlanRunStatusPaused
	default:
		return false
	}
}

func commandKey(source domain.CommandSource, user, text string) string {
	hash := sha256.Sum256([]byte(string(source) + "\x00" + user + "\x00" + text))
	return "sha256:" + hex.EncodeToString(hash[:])
}
