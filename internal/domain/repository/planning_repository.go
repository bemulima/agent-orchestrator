package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type PlanningRepository interface {
	CreateCommand(context.Context, domain.Command) (domain.Command, error)
	GetCommand(context.Context, string) (domain.Command, error)
	CreatePlan(context.Context, domain.Command, domain.PlannerInput, domain.PlannerOutput) (domain.PlanBundle, error)
	GetPlan(context.Context, string) (domain.PlanBundle, error)
	ApprovePlan(context.Context, string, string, string) (domain.PlanBundle, error)
	RejectPlan(context.Context, string, string, string) (domain.PlanBundle, error)
	PrepareRun(context.Context, string, int) (domain.PlanRun, domain.PlanBundle, error)
	AttachTemporalRun(context.Context, string, string) (domain.PlanRun, error)
	GetRun(context.Context, string) (domain.PlanRun, error)
	UpdateRunStatus(context.Context, string, domain.PlanRunStatus, string) (domain.PlanRun, error)
	MarkTaskReady(context.Context, string, string) (domain.Task, error)
	RecordTaskResult(context.Context, string, domain.TaskResult) (domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	CancelTask(context.Context, string) (domain.Task, error)
}

type Planner interface {
	Build(context.Context, domain.Command, domain.TopologyCatalog, domain.PlanRequest) (domain.PlannerInput, domain.PlannerOutput, error)
}

type PlanValidator interface {
	Validate(context.Context, domain.PlannerOutput) error
}

type PlanRunner interface {
	Start(context.Context, domain.PlanRun, domain.PlanSchedule) (string, error)
	Control(context.Context, string, domain.RunControlAction) error
	ReportTaskResult(context.Context, string, domain.TaskResult) error
}
