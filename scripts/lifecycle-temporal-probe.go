//go:build ignore

// Disposable integration driver: production workflow, activity and ProcessRunner.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/adapters/codex"
	temporaladapter "github.com/bemulima/agent-orchestrator/internal/adapters/temporal"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	production "github.com/bemulima/agent-orchestrator/internal/workflow"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"
)

type fixturePlans struct{ repository.PlanningRepository }

func (fixturePlans) UpdateRunStatus(_ context.Context, id string, s domain.PlanRunStatus, _ string) (domain.PlanRun, error) {
	return domain.PlanRun{ID: id, Status: s}, nil
}
func (fixturePlans) MarkTaskReady(_ context.Context, _, id string) (domain.Task, error) {
	return domain.Task{ID: id, Status: domain.TaskStatusReady}, nil
}
func (fixturePlans) RecordTaskResult(_ context.Context, _ string, r domain.TaskResult) (domain.Task, error) {
	return domain.Task{ID: r.TaskID, Status: r.Status}, nil
}

type executor struct {
	request domain.AgentRunRequest
	runner  *codex.ProcessRunner
	output  string
}

func (e executor) Execute(ctx context.Context, taskID, _ string) (domain.TaskExecutionOutcome, error) {
	info := activity.GetInfo(ctx)
	save(filepath.Join(e.output, "activity-start.json"), map[string]any{"time": time.Now().UTC(), "attempt": info.Attempt, "activity_id": info.ActivityID})
	go func() {
		<-ctx.Done()
		save(filepath.Join(e.output, "activity-context-cancel.json"), map[string]any{"time": time.Now().UTC(), "error": ctx.Err().Error()})
	}()
	response, err := e.runner.Run(ctx, e.request, nil)
	save(filepath.Join(e.output, "runner-return.json"), map[string]any{"time": time.Now().UTC(), "result": response, "error": fmt.Sprint(err), "cancelled": ctx.Err() != nil})
	if ctx.Err() != nil {
		return domain.TaskExecutionOutcome{}, ctx.Err()
	}
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	return domain.TaskExecutionOutcome{Result: domain.TaskResult{TaskID: taskID, Status: domain.TaskStatusCompleted}}, nil
}
func save(path string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0600); err != nil {
		panic(err)
	}
}
func timeout() time.Duration {
	if os.Getenv("CDO_CANCEL_KIND") == "timeout" {
		return 6 * time.Second
	}
	return 0
}
func main() {
	output := os.Getenv("CDO_LIFECYCLE_OUTPUT")
	if output == "" {
		panic("output required")
	}
	b, err := os.ReadFile(filepath.Join(output, "request.json"))
	if err != nil {
		panic(err)
	}
	var req domain.AgentRunRequest
	if err = json.Unmarshal(b, &req); err != nil {
		panic(err)
	}
	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:17233"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	id := "cdo-lifecycle-" + filepath.Base(output) + "-" + fmt.Sprint(time.Now().UnixNano())
	resume := os.Getenv("CDO_RESUME_WORKFLOW_ID")
	if resume != "" {
		id = resume
	}
	w := worker.New(c, id, worker.Options{MaxHeartbeatThrottleInterval: time.Second, DefaultHeartbeatThrottleInterval: time.Second})
	runner, err := codex.NewProcessRunner("node " + os.Getenv("CDO_RUNNER_SCRIPT"))
	if err != nil {
		panic(err)
	}
	w.RegisterWorkflow(production.PlanWorkflow)
	w.RegisterActivity(&activities.PlanActivities{Plans: fixturePlans{}, Executor: executor{req, runner, output}})
	if err = w.Start(); err != nil {
		panic(err)
	}
	defer w.Stop()
	adapter := temporaladapter.PlanRunner{Client: c, TaskQueue: id}
	if resume == "" {
		_, err = adapter.Start(context.Background(), domain.PlanRun{WorkflowID: id}, domain.PlanSchedule{RunID: id, PlanID: id, WorkflowID: id, MaxParallelTasks: 1, MaxActivityAttempts: 3, ExecutionTimeout: timeout(), ExecuteTasks: true, Tasks: []domain.ScheduledTask{{TaskID: "fixture"}}})
		if err != nil {
			panic(err)
		}
		save(filepath.Join(output, "workflow-start.json"), map[string]any{"workflow_id": id, "time": time.Now().UTC()})
	} else {
		save(filepath.Join(output, "worker-resume.json"), map[string]any{"workflow_id": id, "at": time.Now().UTC()})
	}
	staleSent := false
	deadline := time.Now().Add(150 * time.Second)
	for resume == "" && time.Now().Before(deadline) {
		if _, e := os.Stat(filepath.Join(output, "stale")); e == nil && !staleSent {
			err = adapter.ReportTaskResult(context.Background(), id, domain.TaskResult{TaskID: "fixture", Status: domain.TaskStatusCompleted})
			if err != nil {
				panic(err)
			}
			staleSent = true
			save(filepath.Join(output, "stale-sent.json"), map[string]any{"time": time.Now().UTC()})
		}
		if _, err = os.Stat(filepath.Join(output, "cancel")); err == nil {
			save(filepath.Join(output, "cancel-request.json"), map[string]any{"time": time.Now().UTC(), "kind": os.Getenv("CDO_CANCEL_KIND")})
			if os.Getenv("CDO_CANCEL_KIND") == "workflow" {
				err = c.CancelWorkflow(context.Background(), id, "")
			} else {
				err = adapter.Control(context.Background(), id, domain.RunControlCancel)
			}
			if err != nil {
				panic(err)
			}
			break
		}
		if _, err = os.Stat(filepath.Join(output, "runner-return.json")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	var out production.PlanWorkflowOutput
	err = c.GetWorkflow(ctx, id, "").Get(ctx, &out)
	save(filepath.Join(output, "workflow-result.json"), map[string]any{"time": time.Now().UTC(), "output": out, "error": fmt.Sprint(err)})
	// Wait for activity acknowledgment, independently of the parent close event.
	for i := 0; resume == "" && i < 600; i++ {
		if _, e := os.Stat(filepath.Join(output, "runner-return.json")); e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	history := c.GetWorkflowHistory(context.Background(), id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	f, e := os.Create(filepath.Join(output, "history.jsonl"))
	if e != nil {
		panic(e)
	}
	defer f.Close()
	for history.HasNext() {
		event, e := history.Next()
		if e != nil {
			panic(e)
		}
		j, e := protojson.Marshal(event)
		if e != nil {
			panic(e)
		}
		fmt.Fprintln(f, string(j))
	}
}
