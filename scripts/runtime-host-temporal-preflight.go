//go:build ignore

// Disposable Temporal SDK capability probe; no agent or model execution.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func capabilityWorkflow(ctx workflow.Context) error {
	return workflow.Sleep(ctx, time.Minute)
}

func certify(output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Only server startup connection is retried, before any execution exists.
	startup := time.Now().Add(10 * time.Second)
	var c client.Client
	var err error
	for {
		dialCtx, stop := context.WithTimeout(ctx, time.Second)
		c, err = client.DialContext(dialCtx, client.Options{HostPort: "127.0.0.1:17233"})
		stop()
		if err == nil {
			break
		}
		if status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("TEMPORAL_STARTUP_DENIED: %w", err)
		}
		if time.Now().After(startup) {
			return fmt.Errorf("TEMPORAL_STARTUP_UNAVAILABLE: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer c.Close()
	identity := fmt.Sprintf("cdo-host-capability-%d", time.Now().UnixNano())
	w := worker.New(c, identity, worker.Options{})
	w.RegisterWorkflow(capabilityWorkflow)
	if err = w.Start(); err != nil {
		return err
	}
	defer w.Stop()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: identity, TaskQueue: identity,
		WorkflowExecutionTimeout: 20 * time.Second}, capabilityWorkflow)
	if err != nil {
		return err
	}
	runID := run.GetRunID()
	if runID == "" {
		return errors.New("TEMPORAL_RUN_ID_MISSING")
	}
	if err = c.CancelWorkflow(ctx, identity, runID); err != nil {
		return err
	}
	err = run.Get(ctx, nil)
	var canceled *temporal.CanceledError
	if !errors.As(err, &canceled) {
		return fmt.Errorf("TEMPORAL_CANCELLATION_NOT_OBSERVED: %v", err)
	}
	iterator := c.GetWorkflowHistory(ctx, identity, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events := []string{}
	requested, terminal, started := false, false, false
	var last int64
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return err
		}
		if len(events) >= 256 || event.EventId <= last {
			return errors.New("TEMPORAL_HISTORY_INVALID")
		}
		last = event.EventId
		events = append(events, event.EventType.String())
		switch event.EventType {
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			started = true
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			requested = true
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
			terminal = true
		}
	}
	if !started || !requested || !terminal {
		return errors.New("TEMPORAL_EXACT_RUN_HISTORY_INCOMPLETE")
	}
	value := map[string]any{"pass": true, "workflow_id": identity, "run_id": runID,
		"history_api": "GetWorkflowHistory", "event_types": events, "cancel_requested": requested,
		"canceled": terminal, "real_model_calls": 0, "agent_execution": false}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func main() {
	output := flag.String("output", "", "fresh exact capability evidence file")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "explicit --output required")
		os.Exit(2)
	}
	if err := certify(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
