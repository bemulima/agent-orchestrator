package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	maxRunnerInput  = 1 << 20
	maxRunnerOutput = 1 << 20
	maxRunnerResult = 512 << 10
)

type ProcessRunner struct {
	command []string
}

func NewProcessRunner(command string) (*ProcessRunner, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return nil, fmt.Errorf("Codex runner command is empty: %w", domain.ErrValidation)
	}
	for _, field := range fields {
		if strings.ContainsAny(field, "\x00\r\n;&|`$<>") {
			return nil, fmt.Errorf("Codex runner command contains shell syntax: %w", domain.ErrValidation)
		}
	}
	return &ProcessRunner{command: fields}, nil
}

func (r *ProcessRunner) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	onThread repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	if len(r.command) == 0 || !supportedAgentRole(request.Role) ||
		!supportedReasoningEffort(request.ReasoningEffort) || len(request.Model) > 255 ||
		request.WorkingDirectory == "" || request.Prompt == "" || len(request.OutputSchema) == 0 {
		return domain.AgentRunResponse{}, fmt.Errorf("incomplete Codex runner request: %w", domain.ErrValidation)
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	// Each process invocation gets a fresh trusted identity, including retries.
	request.ExecutionID = uuid.NewString()
	if request.Attempt < 1 {
		request.Attempt = 1
	}
	if deadline, ok := runCtx.Deadline(); ok {
		request.ExecutionDeadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	input, err := json.Marshal(request)
	if err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("encode Codex runner request: %w", err)
	}
	if len(input) > maxRunnerInput {
		return domain.AgentRunResponse{}, fmt.Errorf("Codex runner request exceeds size limit: %w", domain.ErrValidation)
	}

	command := exec.CommandContext(runCtx, r.command[0], r.command[1:]...)
	stopEscalation := configureRunnerProcess(command)
	defer stopEscalation()
	command.Stdin = bytes.NewReader(input)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("open Codex runner stdout: %w", err)
	}
	var stderr boundedBuffer
	stderr.limit = maxRunnerOutput
	stderr.onOverflow = cancel
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("start Codex runner: %w", err)
	}

	response, readErr := readProtocol(runCtx, stdout, request.ThreadID, request.ExecutionID, request.Attempt, onThread)
	if readErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return domain.AgentRunResponse{}, err
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return domain.AgentRunResponse{}, context.DeadlineExceeded
	}
	if stderr.exceeded {
		return response, fmt.Errorf("Codex runner stderr exceeds output limit: %w", domain.ErrValidation)
	}
	if readErr != nil {
		if message := reportedRunnerError(stderr.String()); message != "" {
			if isTransientRunnerError(message) {
				return response, fmt.Errorf("%w: %w: runner reported: %s", readErr, domain.ErrTransient, message)
			}
			return response, fmt.Errorf("%w: runner reported: %s", readErr, message)
		}
		return response, readErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return domain.AgentRunResponse{}, fmt.Errorf("Codex runner failed: %s", message)
	}
	return response, nil
}

func supportedReasoningEffort(value string) bool {
	switch value {
	case "", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func supportedAgentRole(role domain.AgentRunRole) bool {
	switch role {
	case domain.AgentRunCoder, domain.AgentRunReviewer, domain.AgentRunAnalyst, domain.AgentRunPlanner,
		domain.AgentRunIssueManager, domain.AgentRunPullRequestManager, domain.AgentRunOperator:
		return true
	default:
		return false
	}
}

func isTransientRunnerError(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"stream disconnected", "reconnecting", "unexpected-eof", "unexpected eof",
		"tls close_notify", "connection reset", "connection closed", "temporarily unavailable",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func reportedRunnerError(value string) string {
	for _, line := range strings.Split(value, "\n") {
		var event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "error" {
			message := strings.TrimSpace(event.Message)
			if len(message) > 2000 {
				message = message[:2000]
			}
			return message
		}
	}
	return ""
}

func readProtocol(
	ctx context.Context,
	reader io.Reader,
	expectedThreadID string,
	expectedExecutionID string,
	expectedAttempt int,
	onThread repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxRunnerOutput+1))
	scanner.Buffer(make([]byte, 4096), maxRunnerOutput)
	var response domain.AgentRunResponse
	threadSeen := false
	resultSeen := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return domain.AgentRunResponse{}, err
		}
		var event struct {
			ExecutionID string                 `json:"execution_id"`
			Attempt     int                    `json:"attempt"`
			Type        string                 `json:"type"`
			ThreadID    string                 `json:"thread_id"`
			Result      json.RawMessage        `json:"result"`
			Usage       domain.AgentTokenUsage `json:"usage"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return domain.AgentRunResponse{}, fmt.Errorf("invalid Codex runner protocol event: %w", domain.ErrValidation)
		}
		switch event.Type {
		case "thread_started":
			if threadSeen || event.ThreadID == "" || expectedThreadID != "" && event.ThreadID != expectedThreadID {
				return domain.AgentRunResponse{}, fmt.Errorf("invalid Codex thread event: %w", domain.ErrConflict)
			}
			threadSeen = true
			response.ThreadID = event.ThreadID
			if onThread != nil {
				if err := onThread(ctx, event.ThreadID); err != nil {
					return domain.AgentRunResponse{}, fmt.Errorf("persist Codex thread: %w", err)
				}
			}
		case "result":
			if event.ExecutionID != expectedExecutionID || event.Attempt != expectedAttempt {
				return domain.AgentRunResponse{}, fmt.Errorf("stale runner execution identity: %w", domain.ErrConflict)
			}
			response.ExecutionID, response.Attempt = event.ExecutionID, event.Attempt
			if !threadSeen || resultSeen || event.ThreadID != response.ThreadID ||
				len(event.Result) == 0 || len(event.Result) > maxRunnerResult || !json.Valid(event.Result) || !validTokenUsage(event.Usage) {
				return domain.AgentRunResponse{}, fmt.Errorf("invalid Codex result event: %w", domain.ErrValidation)
			}
			resultSeen = true
			response.Result = append(json.RawMessage(nil), event.Result...)
			response.Usage = event.Usage
		default:
			return domain.AgentRunResponse{}, fmt.Errorf("unknown Codex runner event %q: %w", event.Type, domain.ErrValidation)
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("read Codex runner protocol: %w", err)
	}
	if !threadSeen || !resultSeen {
		return response, fmt.Errorf("incomplete Codex runner protocol: %w", domain.ErrValidation)
	}
	return response, nil
}

func validTokenUsage(value domain.AgentTokenUsage) bool {
	return value.InputTokens >= 0 && value.CachedInputTokens >= 0 && value.OutputTokens >= 0 && value.ReasoningOutputTokens >= 0 &&
		value.CachedInputTokens <= value.InputTokens
}

type boundedBuffer struct {
	buffer     bytes.Buffer
	limit      int
	exceeded   bool
	onOverflow context.CancelFunc
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if original > b.limit-b.buffer.Len() {
		if !b.exceeded && b.onOverflow != nil {
			b.onOverflow()
		}
		b.exceeded = true
	}
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

var _ repository.AgentRunner = (*ProcessRunner)(nil)
