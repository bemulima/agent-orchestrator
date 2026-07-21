package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const maxArtifactBytes = 10 << 20

type Verifier struct {
	Worktrees repository.TaskWorktree
	Now       func() time.Time
}

func (v Verifier) Verify(
	ctx context.Context,
	executionContext domain.TaskExecutionContext,
	workspace domain.TaskWorkspace,
	result domain.AgentResult,
) (domain.VerificationReport, domain.WorkspaceState, error) {
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	report := domain.VerificationReport{Status: "failed", VerifiedAt: now}
	if v.Worktrees == nil {
		return report, domain.WorkspaceState{}, fmt.Errorf("task worktree is not configured: %w", domain.ErrInvalidStatus)
	}
	state, err := v.Worktrees.Inspect(ctx, executionContext.Project, workspace)
	if err != nil {
		return report, domain.WorkspaceState{}, err
	}
	report.ChangedFiles = append([]string(nil), state.ChangedFiles...)
	var failures []string
	add := func(name, status, details string, exitCode *int) {
		report.Checks = append(report.Checks, domain.VerificationCheck{
			Name: name, Status: status, Details: boundedDetails(details), ExitCode: exitCode,
		})
		if status == "failed" {
			failures = append(failures, name+": "+details)
		}
	}

	if result.Status != domain.AgentResultCompleted {
		add("result_status", "failed", "only a completed coder result can be verified", nil)
	} else {
		add("result_status", "passed", string(result.Status), nil)
	}
	if len(state.ChangedFiles) == 0 {
		add("non_empty_diff", "failed", "completed task produced no changed files", nil)
	} else {
		add("non_empty_diff", "passed", fmt.Sprintf("%d changed files", len(state.ChangedFiles)), nil)
	}
	if sameFileSet(state.ChangedFiles, result.FilesChanged) {
		add("claimed_files", "passed", "agent claims match Git", nil)
	} else {
		add("claimed_files", "failed", fmt.Sprintf("Git=%v agent=%v", state.ChangedFiles, result.FilesChanged), nil)
	}

	outside := filesOutsideScope(state.ChangedFiles, executionContext.Task.WriteScope)
	if len(outside) == 0 {
		add("write_scope", "passed", strings.Join(executionContext.Task.WriteScope, ", "), nil)
	} else {
		add("write_scope", "failed", "outside scope: "+strings.Join(outside, ", "), nil)
	}

	if executionContext.Task.RequiresMigration {
		if migrationPairsPresent(state.ChangedFiles) {
			add("migration_pair", "passed", "matching up/down migration files found", nil)
		} else {
			add("migration_pair", "failed", "task requires a matching *.up.sql and *.down.sql pair", nil)
		}
	}
	if executionContext.Task.ChangesContracts {
		if contractChangePresent(state.ChangedFiles) {
			add("contract_change", "passed", "contract surface changed", nil)
		} else {
			add("contract_change", "failed", "task declares contract changes but no contract path changed", nil)
		}
	}

	executed := make(map[string]string)
	commands := append([]string{"git diff --check"}, executionContext.Task.VerificationCommands...)
	commands = uniqueStrings(commands)
	for _, command := range commands {
		check, checkErr := v.Worktrees.RunCheck(ctx, workspace, command)
		if checkErr != nil {
			add(command, "failed", checkErr.Error(), nil)
			continue
		}
		executed[command] = map[bool]string{true: "passed", false: "failed"}[check.ExitCode == 0]
		status := executed[command]
		exitCode := check.ExitCode
		add(command, status, check.Output, &exitCode)
	}
	for _, claim := range result.Checks {
		independentStatus, exists := executed[claim.Name]
		if claim.Status == domain.AgentCheckPassed && (!exists || independentStatus != "passed") {
			add("agent_claim:"+claim.Name, "failed", "passed claim is not supported by an independent check", nil)
		}
		if claim.Status == domain.AgentCheckFailed {
			add("agent_claim:"+claim.Name, "failed", "coder reported a failed check", nil)
		}
	}

	for _, artifact := range result.Artifacts {
		content, artifactErr := v.Worktrees.ReadArtifact(ctx, workspace, artifact.Path, maxArtifactBytes)
		if artifactErr != nil {
			add("artifact:"+artifact.Name, "failed", artifactErr.Error(), nil)
			continue
		}
		digest := sha256.Sum256(content)
		add("artifact:"+artifact.Name, "passed", hex.EncodeToString(digest[:]), nil)
	}

	if len(failures) > 0 {
		return report, state, fmt.Errorf("independent verification failed: %s: %w", strings.Join(failures, "; "), domain.ErrValidation)
	}
	report.Status = "passed"
	return report, state, nil
}

func filesOutsideScope(files, scopes []string) []string {
	var outside []string
	for _, file := range files {
		matched := false
		for _, scope := range scopes {
			if matchScope(scope, file) {
				matched = true
				break
			}
		}
		if !matched {
			outside = append(outside, file)
		}
	}
	sort.Strings(outside)
	return outside
}

func matchScope(pattern, name string) bool {
	pattern = path.Clean(strings.ReplaceAll(strings.TrimSpace(pattern), "\\", "/"))
	name = path.Clean(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if pattern == name {
		return true
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					expression.WriteString("(?:.*/)?")
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	expression.WriteByte('$')
	matched, err := regexp.MatchString(expression.String(), name)
	return err == nil && matched
}

func migrationPairsPresent(files []string) bool {
	set := make(map[string]struct{}, len(files))
	for _, file := range files {
		set[file] = struct{}{}
	}
	for _, file := range files {
		if strings.HasSuffix(file, ".up.sql") {
			if _, exists := set[strings.TrimSuffix(file, ".up.sql")+".down.sql"]; exists {
				return true
			}
		}
	}
	return false
}

func contractChangePresent(files []string) bool {
	for _, file := range files {
		lower := strings.ToLower(filepathSlash(file))
		if strings.HasPrefix(lower, ".ai/contracts/") || strings.HasPrefix(lower, "contracts/") ||
			strings.HasPrefix(lower, "api/") || strings.HasPrefix(lower, "proto/") ||
			strings.HasSuffix(lower, ".proto") || strings.Contains(path.Base(lower), "openapi") ||
			strings.Contains(path.Base(lower), "asyncapi") {
			return true
		}
	}
	return false
}

func sameFileSet(left, right []string) bool {
	left = normalizedFiles(left)
	right = normalizedFiles(right)
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func normalizedFiles(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = filepathSlash(path.Clean(strings.TrimSpace(value)))
	}
	sort.Strings(result)
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := seen[value]; value == "" || exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func filepathSlash(value string) string { return strings.ReplaceAll(value, "\\", "/") }

func boundedDetails(value string) string {
	const limit = 16 << 10
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}
