//go:build ignore

// Disposable complete-transport driver. It runs the production Temporal workflow,
// activity and ProcessRunner in a socket-free backend; it never controls Docker.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
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

type evidence struct {
	root string
	mu   sync.Mutex
}

func (e *evidence) save(name string, value any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	if len(data) > 1<<20 {
		panic("evidence record limit")
	}
	file, err := os.CreateTemp(e.root, ".probe-*")
	if err != nil {
		panic(err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		panic(err)
	}
	if closeErr != nil {
		panic(closeErr)
	}
	if err = os.Rename(tmp, filepath.Join(e.root, name)); err != nil {
		panic(err)
	}
}

func (e *evidence) append(name string, value any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	file, err := os.OpenFile(filepath.Join(e.root, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size()+int64(len(data))+1 > 1<<20 {
		panic("evidence stream limit")
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		panic(err)
	}
	if err = file.Sync(); err != nil {
		panic(err)
	}
}

func (e *evidence) exists(name string) bool {
	_, err := os.Stat(filepath.Join(e.root, name))
	return err == nil
}

type fixturePlans struct {
	repository.PlanningRepository
	evidence *evidence
}

func (p fixturePlans) UpdateRunStatus(_ context.Context, id string, status domain.PlanRunStatus, message string) (domain.PlanRun, error) {
	p.evidence.append("plan-records.jsonl", map[string]any{"time": time.Now().UTC(), "id": id, "status": status, "error": message})
	return domain.PlanRun{ID: id, Status: status}, nil
}

func (fixturePlans) MarkTaskReady(_ context.Context, _, id string) (domain.Task, error) {
	return domain.Task{ID: id, Status: domain.TaskStatusReady}, nil
}

func (p fixturePlans) RecordTaskResult(_ context.Context, _ string, result domain.TaskResult) (domain.Task, error) {
	p.evidence.append("task-records.jsonl", map[string]any{"time": time.Now().UTC(), "result": result})
	return domain.Task{ID: result.TaskID, Status: result.Status}, nil
}

type executor struct {
	request  domain.AgentRunRequest
	runner   *codex.ProcessRunner
	evidence *evidence
}

func (e executor) Execute(ctx context.Context, taskID, _ string) (domain.TaskExecutionOutcome, error) {
	info := activity.GetInfo(ctx)
	identity, bound := domain.AgentRuntimeContextFromContext(ctx)
	e.evidence.save("activity-start.json", map[string]any{
		"time": time.Now().UTC(), "activity_id": info.ActivityID, "attempt": info.Attempt,
		"runtime_context": identity, "trusted_runtime_context_bound": bound,
	})
	if !bound || identity.WorkflowID != info.WorkflowExecution.ID ||
		identity.WorkflowRunID != info.WorkflowExecution.RunID ||
		identity.ActivityID != info.ActivityID || identity.ActivityAttempt != info.Attempt {
		return domain.TaskExecutionOutcome{}, errors.New("RUNTIME_CONTEXT_MISMATCH")
	}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			e.evidence.save("activity-context-cancel.json", map[string]any{"time": time.Now().UTC(), "error": ctx.Err().Error(), "runtime_context": identity})
		case <-finished:
		}
	}()
	response, err := e.runner.Run(ctx, e.request, nil)
	// A fast teardown can win the finished-channel race before the observer
	// goroutine runs. Record the independently observed context at acknowledgment
	// as well; never infer cancellation only from the requested control signal.
	if ctx.Err() != nil {
		e.evidence.save("activity-context-cancel.json", map[string]any{"time": time.Now().UTC(), "error": ctx.Err().Error(), "runtime_context": identity})
	}
	e.evidence.save("runner-return.json", map[string]any{
		"time": time.Now().UTC(), "result": response, "error": fmt.Sprint(err),
		"cancelled": ctx.Err() != nil, "runtime_context": identity,
	})
	if ctx.Err() != nil {
		return domain.TaskExecutionOutcome{}, ctx.Err()
	}
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	if len(response.Result) == 0 || response.ExecutionID == "" {
		return domain.TaskExecutionOutcome{}, errors.New("RUNTIME_RESULT_MISSING")
	}
	return domain.TaskExecutionOutcome{Result: domain.TaskResult{TaskID: taskID, Status: domain.TaskStatusCompleted}}, nil
}

func requireSocketFreeBackend(e *evidence) {
	if path, err := exec.LookPath("docker"); err == nil {
		panic("backend contains Docker CLI: " + path)
	}
	for _, path := range []string{"/var/run/docker.sock", "/run/docker.sock"} {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			panic("backend socket absence is not established: " + path)
		}
	}
	e.save("backend-boundary.json", map[string]any{
		"time": time.Now().UTC(), "docker_cli_absent": true, "docker_socket_absent": true,
		"claim_scope": "PATH_AND_CONVENTIONAL_SOCKET_PATHS_ONLY",
	})
}

func readRequest(path string) domain.AgentRunRequest {
	file, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		panic("invalid trusted fixture request")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var request domain.AgentRunRequest
	if err = decoder.Decode(&request); err != nil {
		panic(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		panic("trusted fixture request has trailing JSON")
	}
	return request
}

func recordHistory(c client.Client, workflowID, runID string, e *evidence) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	history := c.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	file, err := os.CreateTemp(e.root, ".history-*")
	if err != nil {
		panic(err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	var written int
	digest := sha256.New()
	for history.HasNext() {
		event, err := history.Next()
		if err != nil {
			file.Close()
			panic(err)
		}
		data, err := protojson.Marshal(event)
		if err != nil || written+len(data)+1 > 8<<20 {
			file.Close()
			panic("actual history limit or encoding failure")
		}
		if _, err = file.Write(append(data, '\n')); err != nil {
			file.Close()
			panic(err)
		}
		digest.Write(append(data, '\n'))
		written += len(data) + 1
	}
	if err = file.Sync(); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
	if err = os.Rename(tmp, filepath.Join(e.root, "history.jsonl")); err != nil {
		panic(err)
	}
	e.save("history-binding.json", map[string]any{"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": runID, "api": "GetWorkflowHistory", "bytes": written, "sha256": hex.EncodeToString(digest.Sum(nil))})
}

func observeStaleSignal(c client.Client, workflowID, runID string, e *evidence) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	description, err := c.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		panic(err)
	}
	query, queryErr := c.QueryWorkflow(ctx, workflowID, runID, production.PlanStateQuery)
	var state production.PlanWorkflowState
	if queryErr == nil {
		queryErr = query.Get(&state)
	}
	history := c.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var signalEventID int64
	for history.HasNext() {
		event, err := history.Next()
		if err != nil {
			panic(err)
		}
		if attributes := event.GetWorkflowExecutionSignaledEventAttributes(); attributes != nil && attributes.SignalName == production.PlanTaskResultSignal {
			signalEventID = event.EventId
		}
	}
	e.save("stale-observation.json", map[string]any{
		"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": runID,
		"signal_event_id": signalEventID, "workflow_status": description.WorkflowExecutionInfo.Status.String(),
		"task_status": state.TaskStatus["fixture"], "query_error": fmt.Sprint(queryErr),
		"observation_source": "DescribeWorkflowExecution_QueryWorkflow_GetWorkflowHistory",
	})
}

func boundedSeconds(name string, fallback, minimum, maximum int64) time.Duration {
	value := fallback
	if raw := os.Getenv(name); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < minimum || parsed > maximum {
			panic("bounded certification timeout invalid: " + name)
		}
		value = parsed
	}
	return time.Duration(value) * time.Second
}

func main() {
	collectOnly := flag.Bool("collect-only", false, "observe an existing workflow without starting an activity")
	observeTerminal := flag.Bool("observe-terminal", false, "finish only workflow bookkeeping after backend loss; no ExecutePlanTask registration")
	flag.Parse()
	if *collectOnly && *observeTerminal {
		panic("choose exactly one observer mode")
	}
	output := os.Getenv("CDO_LIFECYCLE_OUTPUT")
	workflowID := os.Getenv("CDO_WORKFLOW_ID")
	address := os.Getenv("CDO_TEMPORAL_ADDRESS")
	command := os.Getenv("CDO_RUNNER_COMMAND")
	if output == "" || workflowID == "" || address == "" || (!*collectOnly && !*observeTerminal && command == "") {
		panic("explicit output, workflow identity, Temporal address and runner command required")
	}
	e := &evidence{root: output}
	defaultActivitySeconds := int64(120)
	if os.Getenv("CDO_CANCEL_KIND") == "timeout" {
		defaultActivitySeconds = 6
	}
	executionTimeout := boundedSeconds("CDO_ACTIVITY_TIMEOUT_SECONDS", defaultActivitySeconds, 1, 120)
	observerTimeout := boundedSeconds("CDO_OBSERVER_TIMEOUT_SECONDS", 180, 150, 300)
	requireSocketFreeBackend(e)
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if *collectOnly || *observeTerminal {
		data, err := os.ReadFile(filepath.Join(output, "workflow-start.json"))
		if err != nil || len(data) > 1<<20 {
			panic("exact original workflow identity required for observer")
		}
		var identity struct {
			WorkflowID    string `json:"workflow_id"`
			WorkflowRunID string `json:"workflow_run_id"`
		}
		if json.Unmarshal(data, &identity) != nil || identity.WorkflowID != workflowID || identity.WorkflowRunID == "" {
			panic("original workflow identity mismatch")
		}
		mode := "HISTORY_ONLY"
		registered := []string{}
		if *observeTerminal {
			mode = "BOOKKEEPING_ONLY"
			// This observer cannot rerun the lost agent. Register only production
			// workflow and metadata activities needed to persist its failure.
			bookkeeping := activities.PlanActivities{Plans: fixturePlans{evidence: e}}
			observer := worker.New(c, workflowID, worker.Options{})
			observer.RegisterWorkflow(production.PlanWorkflow)
			observer.RegisterActivityWithOptions(bookkeeping.SetPlanRunStatus, activity.RegisterOptions{Name: "SetPlanRunStatus"})
			observer.RegisterActivityWithOptions(bookkeeping.RecordPlanTaskResult, activity.RegisterOptions{Name: "RecordPlanTaskResult"})
			registered = []string{"SetPlanRunStatus", "RecordPlanTaskResult"}
			if err = observer.Start(); err != nil {
				panic(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), observerTimeout)
			var result production.PlanWorkflowOutput
			err = c.GetWorkflow(ctx, workflowID, identity.WorkflowRunID).Get(ctx, &result)
			cancel()
			observer.Stop()
			e.save("workflow-result.json", map[string]any{"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": identity.WorkflowRunID, "output": result, "error": fmt.Sprint(err), "bookkeeping_observer": true, "observer_timeout_seconds": int64(observerTimeout / time.Second)})
		}
		recordHistory(c, workflowID, identity.WorkflowRunID, e)
		e.save("observer-complete.json", map[string]any{"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": identity.WorkflowRunID, "started_execution": false, "mode": mode, "registered_activity_names": registered, "observer_timeout_seconds": int64(observerTimeout / time.Second)})
		return
	}
	request := readRequest(filepath.Join(output, "request.json"))
	runner, err := codex.NewProcessRunner(command)
	if err != nil {
		panic(err)
	}
	w := worker.New(c, workflowID, worker.Options{MaxHeartbeatThrottleInterval: time.Second, DefaultHeartbeatThrottleInterval: time.Second})
	w.RegisterWorkflow(production.PlanWorkflow)
	w.RegisterActivity(&activities.PlanActivities{Plans: fixturePlans{evidence: e}, Executor: executor{request, runner, e}})
	if err = w.Start(); err != nil {
		panic(err)
	}
	defer w.Stop()
	adapter := temporaladapter.PlanRunner{Client: c, TaskQueue: workflowID}
	runID, err := adapter.Start(context.Background(), domain.PlanRun{WorkflowID: workflowID}, domain.PlanSchedule{
		RunID: workflowID, PlanID: workflowID, WorkflowID: workflowID,
		MaxParallelTasks: 1, MaxActivityAttempts: 3, ExecutionTimeout: executionTimeout,
		ExecuteTasks: true, Tasks: []domain.ScheduledTask{{TaskID: "fixture"}},
	})
	if err != nil {
		panic(err)
	}
	e.save("workflow-start.json", map[string]any{"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": runID, "temporal_address": address, "activity_timeout_seconds": int64(executionTimeout / time.Second)})
	staleSent := false
	controlDeadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(controlDeadline) {
		if e.exists("stale") && !staleSent {
			err = c.SignalWorkflow(context.Background(), workflowID, runID, production.PlanTaskResultSignal, domain.TaskResult{TaskID: "fixture", Status: domain.TaskStatusCompleted})
			if err != nil {
				panic(err)
			}
			staleSent = true
			observeStaleSignal(c, workflowID, runID, e)
			e.save("stale-sent.json", map[string]any{"time": time.Now().UTC(), "unbound_external_green": true})
		}
		if e.exists("cancel") {
			kind := os.Getenv("CDO_CANCEL_KIND")
			e.save("cancel-request.json", map[string]any{"time": time.Now().UTC(), "kind": kind})
			if kind == "workflow" {
				err = c.CancelWorkflow(context.Background(), workflowID, runID)
			} else {
				err = c.SignalWorkflow(context.Background(), workflowID, runID, production.PlanCancelSignal, true)
			}
			if err != nil {
				panic(err)
			}
			break
		}
		if e.exists("runner-return.json") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	var result production.PlanWorkflowOutput
	err = c.GetWorkflow(ctx, workflowID, runID).Get(ctx, &result)
	cancel()
	e.save("workflow-result.json", map[string]any{"time": time.Now().UTC(), "workflow_id": workflowID, "workflow_run_id": runID, "output": result, "error": fmt.Sprint(err)})
	// Workflow cancellation can close the parent before the activity acknowledges.
	// Capture actual teardown separately; a closed workflow is not cleanup evidence.
	ackDeadline := time.Now().Add(60 * time.Second)
	for !e.exists("runner-return.json") && time.Now().Before(ackDeadline) {
		time.Sleep(100 * time.Millisecond)
	}
	recordHistory(c, workflowID, runID, e)
}
