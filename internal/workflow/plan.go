package workflow

import (
	"fmt"
	"sort"
	"time"

	"go.temporal.io/sdk/temporal"
	temporalworkflow "go.temporal.io/sdk/workflow"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const (
	PlanPauseSignal      = "plan.pause"
	PlanResumeSignal     = "plan.resume"
	PlanCancelSignal     = "plan.cancel"
	PlanTaskResultSignal = "plan.task_result"
	PlanStateQuery       = "plan.state"
)

type PlanWorkflowState struct {
	RunID       string                       `json:"run_id"`
	PlanID      string                       `json:"plan_id"`
	Status      domain.PlanRunStatus         `json:"status"`
	TaskStatus  map[string]domain.TaskStatus `json:"task_status"`
	ActiveTasks []string                     `json:"active_tasks"`
	LastError   string                       `json:"last_error,omitempty"`
}

type PlanWorkflowOutput struct {
	RunID      string                       `json:"run_id"`
	PlanID     string                       `json:"plan_id"`
	Status     domain.PlanRunStatus         `json:"status"`
	TaskStatus map[string]domain.TaskStatus `json:"task_status"`
	Error      string                       `json:"error,omitempty"`
}

func PlanWorkflow(ctx temporalworkflow.Context, schedule domain.PlanSchedule) (PlanWorkflowOutput, error) {
	if err := validateSchedule(schedule); err != nil {
		return PlanWorkflowOutput{}, err
	}
	ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout:    30 * time.Second,
		HeartbeatTimeout:       10 * time.Second,
		ScheduleToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Second, BackoffCoefficient: 2,
			MaximumInterval: 10 * time.Second, MaximumAttempts: int32(schedule.MaxActivityAttempts),
		},
	})
	state := PlanWorkflowState{
		RunID: schedule.RunID, PlanID: schedule.PlanID, Status: domain.PlanRunStatusPending,
		TaskStatus: make(map[string]domain.TaskStatus, len(schedule.Tasks)), ActiveTasks: []string{},
	}
	for _, task := range schedule.Tasks {
		state.TaskStatus[task.TaskID] = domain.TaskStatusPlanned
	}
	if err := temporalworkflow.SetQueryHandler(ctx, PlanStateQuery, func() (PlanWorkflowState, error) {
		return copyPlanState(state), nil
	}); err != nil {
		return PlanWorkflowOutput{}, err
	}
	pauseChannel := temporalworkflow.GetSignalChannel(ctx, PlanPauseSignal)
	resumeChannel := temporalworkflow.GetSignalChannel(ctx, PlanResumeSignal)
	cancelChannel := temporalworkflow.GetSignalChannel(ctx, PlanCancelSignal)
	resultChannel := temporalworkflow.GetSignalChannel(ctx, PlanTaskResultSignal)

	if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusRunning, ""); err != nil {
		return PlanWorkflowOutput{}, err
	}
	state.Status = domain.PlanRunStatusRunning
	active := make(map[string]struct{})
	var pendingControl domain.RunControlAction
	var pendingResult *domain.TaskResult

	for {
		control, result := pendingControl, pendingResult
		pendingControl, pendingResult = "", nil
		if control == "" && result == nil {
			control, result = receivePendingSignals(pauseChannel, resumeChannel, cancelChannel, resultChannel)
		}
		if result != nil {
			current, exists := state.TaskStatus[result.TaskID]
			if exists && current != domain.TaskStatusCompleted && current != domain.TaskStatusFailed &&
				current != domain.TaskStatusCancelled && current != domain.TaskStatusBlocked {
				if err := recordTaskResult(ctx, schedule.RunID, *result); err != nil {
					state.LastError = err.Error()
					return failPlanWorkflow(ctx, schedule, state, active, err.Error())
				}
				state.TaskStatus[result.TaskID] = result.Status
				delete(active, result.TaskID)
				state.ActiveTasks = activeTaskIDs(active)
				if result.Status == domain.TaskStatusFailed || result.Status == domain.TaskStatusBlocked || result.Status == domain.TaskStatusCancelled {
					message := result.Error
					if message == "" {
						message = fmt.Sprintf("task %s finished with status %s", result.TaskID, result.Status)
					}
					return failPlanWorkflow(ctx, schedule, state, active, message)
				}
			}
		}
		switch control {
		case domain.RunControlCancel:
			if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusCancelled, ""); err != nil {
				return PlanWorkflowOutput{}, err
			}
			state.Status = domain.PlanRunStatusCancelled
			for taskID, status := range state.TaskStatus {
				if status != domain.TaskStatusCompleted {
					state.TaskStatus[taskID] = domain.TaskStatusCancelled
				}
			}
			return workflowOutput(state), nil
		case domain.RunControlPause:
			if state.Status == domain.PlanRunStatusRunning {
				if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusPaused, ""); err != nil {
					return PlanWorkflowOutput{}, err
				}
				state.Status = domain.PlanRunStatusPaused
			}
		case domain.RunControlResume:
			if state.Status == domain.PlanRunStatusPaused {
				if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusRunning, ""); err != nil {
					return PlanWorkflowOutput{}, err
				}
				state.Status = domain.PlanRunStatusRunning
			}
		}

		if allTasksCompleted(state.TaskStatus) {
			if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusCompleted, ""); err != nil {
				return PlanWorkflowOutput{}, err
			}
			state.Status = domain.PlanRunStatusCompleted
			return workflowOutput(state), nil
		}

		if state.Status == domain.PlanRunStatusRunning {
			ready := runnableTasks(schedule.Tasks, state.TaskStatus)
			slots := schedule.MaxParallelTasks - len(active)
			if slots > len(ready) {
				slots = len(ready)
			}
			if slots > 0 {
				selected := ready[:slots]
				futures := make([]temporalworkflow.Future, 0, len(selected))
				for _, task := range selected {
					state.TaskStatus[task.TaskID] = domain.TaskStatusReady
					active[task.TaskID] = struct{}{}
					futures = append(futures, temporalworkflow.ExecuteActivity(ctx, "DispatchPlanTask", activities.DispatchPlanTaskInput{
						RunID: schedule.RunID, PlanID: schedule.PlanID, TaskID: task.TaskID,
					}))
				}
				state.ActiveTasks = activeTaskIDs(active)
				for index, future := range futures {
					if err := future.Get(ctx, nil); err != nil {
						taskID := selected[index].TaskID
						state.TaskStatus[taskID] = domain.TaskStatusFailed
						delete(active, taskID)
						state.ActiveTasks = activeTaskIDs(active)
						_ = recordTaskResult(ctx, schedule.RunID, domain.TaskResult{TaskID: taskID, Status: domain.TaskStatusFailed, Error: err.Error()})
						return failPlanWorkflow(ctx, schedule, state, active, err.Error())
					}
				}
			}
		}

		if len(active) == 0 && state.Status == domain.PlanRunStatusRunning && len(runnableTasks(schedule.Tasks, state.TaskStatus)) == 0 {
			return failPlanWorkflow(ctx, schedule, state, active, "no runnable tasks remain before plan completion")
		}

		selector := temporalworkflow.NewSelector(ctx)
		selector.AddReceive(pauseChannel, func(channel temporalworkflow.ReceiveChannel, _ bool) {
			var value bool
			channel.Receive(ctx, &value)
			pendingControl = domain.RunControlPause
		})
		selector.AddReceive(resumeChannel, func(channel temporalworkflow.ReceiveChannel, _ bool) {
			var value bool
			channel.Receive(ctx, &value)
			pendingControl = domain.RunControlResume
		})
		selector.AddReceive(cancelChannel, func(channel temporalworkflow.ReceiveChannel, _ bool) {
			var value bool
			channel.Receive(ctx, &value)
			pendingControl = domain.RunControlCancel
		})
		selector.AddReceive(resultChannel, func(channel temporalworkflow.ReceiveChannel, _ bool) {
			var value domain.TaskResult
			channel.Receive(ctx, &value)
			pendingResult = &value
		})
		selector.Select(ctx)
	}
}

func receivePendingSignals(
	pause, resume, cancel, results temporalworkflow.ReceiveChannel,
) (domain.RunControlAction, *domain.TaskResult) {
	var marker bool
	if cancel.ReceiveAsync(&marker) {
		return domain.RunControlCancel, nil
	}
	if pause.ReceiveAsync(&marker) {
		return domain.RunControlPause, nil
	}
	if resume.ReceiveAsync(&marker) {
		return domain.RunControlResume, nil
	}
	var result domain.TaskResult
	if results.ReceiveAsync(&result) {
		return "", &result
	}
	return "", nil
}

func runnableTasks(tasks []domain.ScheduledTask, status map[string]domain.TaskStatus) []domain.ScheduledTask {
	result := make([]domain.ScheduledTask, 0)
	for _, task := range tasks {
		if status[task.TaskID] != domain.TaskStatusPlanned {
			continue
		}
		ready := true
		for _, dependency := range task.Dependencies {
			if status[dependency] != domain.TaskStatusCompleted {
				ready = false
				break
			}
		}
		if ready {
			result = append(result, task)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return result[i].TaskID < result[j].TaskID
	})
	return result
}

func setRunStatus(ctx temporalworkflow.Context, runID string, status domain.PlanRunStatus, message string) error {
	return temporalworkflow.ExecuteActivity(ctx, "SetPlanRunStatus", activities.SetPlanRunStatusInput{
		RunID: runID, Status: status, Error: message,
	}).Get(ctx, nil)
}

func recordTaskResult(ctx temporalworkflow.Context, runID string, result domain.TaskResult) error {
	return temporalworkflow.ExecuteActivity(ctx, "RecordPlanTaskResult", activities.RecordPlanTaskResultInput{
		RunID: runID, Result: result,
	}).Get(ctx, nil)
}

func failPlanWorkflow(
	ctx temporalworkflow.Context,
	schedule domain.PlanSchedule,
	state PlanWorkflowState,
	active map[string]struct{},
	message string,
) (PlanWorkflowOutput, error) {
	state.LastError = message
	state.ActiveTasks = activeTaskIDs(active)
	if err := setRunStatus(ctx, schedule.RunID, domain.PlanRunStatusFailed, message); err != nil {
		return PlanWorkflowOutput{}, err
	}
	state.Status = domain.PlanRunStatusFailed
	return workflowOutput(state), nil
}

func activeTaskIDs(active map[string]struct{}) []string {
	result := make([]string, 0, len(active))
	for taskID := range active {
		result = append(result, taskID)
	}
	sort.Strings(result)
	return result
}

func allTasksCompleted(status map[string]domain.TaskStatus) bool {
	for _, taskStatus := range status {
		if taskStatus != domain.TaskStatusCompleted {
			return false
		}
	}
	return true
}

func validateSchedule(schedule domain.PlanSchedule) error {
	if schedule.RunID == "" || schedule.PlanID == "" || len(schedule.Tasks) == 0 ||
		schedule.MaxParallelTasks < 1 || schedule.MaxParallelTasks > 3 || schedule.MaxActivityAttempts < 1 {
		return fmt.Errorf("invalid plan schedule: %w", domain.ErrValidation)
	}
	seen := make(map[string]struct{}, len(schedule.Tasks))
	for _, task := range schedule.Tasks {
		if task.TaskID == "" {
			return fmt.Errorf("scheduled task ID is required: %w", domain.ErrValidation)
		}
		if _, exists := seen[task.TaskID]; exists {
			return fmt.Errorf("duplicate scheduled task %q: %w", task.TaskID, domain.ErrConflict)
		}
		seen[task.TaskID] = struct{}{}
	}
	for _, task := range schedule.Tasks {
		for _, dependency := range task.Dependencies {
			if _, exists := seen[dependency]; !exists || dependency == task.TaskID {
				return fmt.Errorf("invalid scheduled dependency %q: %w", dependency, domain.ErrValidation)
			}
		}
	}
	return nil
}

func copyPlanState(state PlanWorkflowState) PlanWorkflowState {
	copy := state
	copy.TaskStatus = make(map[string]domain.TaskStatus, len(state.TaskStatus))
	for taskID, status := range state.TaskStatus {
		copy.TaskStatus[taskID] = status
	}
	copy.ActiveTasks = append([]string(nil), state.ActiveTasks...)
	return copy
}

func workflowOutput(state PlanWorkflowState) PlanWorkflowOutput {
	return PlanWorkflowOutput{
		RunID: state.RunID, PlanID: state.PlanID, Status: state.Status,
		TaskStatus: copyPlanState(state).TaskStatus, Error: state.LastError,
	}
}
