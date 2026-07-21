package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

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
	if len(r.command) == 0 || request.Role != domain.AgentRunCoder && request.Role != domain.AgentRunReviewer ||
		request.WorkingDirectory == "" || request.Prompt == "" || len(request.OutputSchema) == 0 {
		return domain.AgentRunResponse{}, fmt.Errorf("incomplete Codex runner request: %w", domain.ErrValidation)
	}
	input, err := json.Marshal(request)
	if err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("encode Codex runner request: %w", err)
	}
	if len(input) > maxRunnerInput {
		return domain.AgentRunResponse{}, fmt.Errorf("Codex runner request exceeds size limit: %w", domain.ErrValidation)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(runCtx, r.command[0], r.command[1:]...)
	command.Stdin = bytes.NewReader(input)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("open Codex runner stdout: %w", err)
	}
	var stderr boundedBuffer
	stderr.limit = maxRunnerOutput
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("start Codex runner: %w", err)
	}

	response, readErr := readProtocol(runCtx, stdout, request.ThreadID, onThread)
	if readErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return domain.AgentRunResponse{}, readErr
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

func readProtocol(
	ctx context.Context,
	reader io.Reader,
	expectedThreadID string,
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
			Type     string          `json:"type"`
			ThreadID string          `json:"thread_id"`
			Result   json.RawMessage `json:"result"`
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
			if !threadSeen || resultSeen || event.ThreadID != response.ThreadID ||
				len(event.Result) == 0 || len(event.Result) > maxRunnerResult || !json.Valid(event.Result) {
				return domain.AgentRunResponse{}, fmt.Errorf("invalid Codex result event: %w", domain.ErrValidation)
			}
			resultSeen = true
			response.Result = append(json.RawMessage(nil), event.Result...)
		default:
			return domain.AgentRunResponse{}, fmt.Errorf("unknown Codex runner event %q: %w", event.Type, domain.ErrValidation)
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.AgentRunResponse{}, fmt.Errorf("read Codex runner protocol: %w", err)
	}
	if !threadSeen || !resultSeen {
		return domain.AgentRunResponse{}, fmt.Errorf("incomplete Codex runner protocol: %w", domain.ErrValidation)
	}
	return response, nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
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
