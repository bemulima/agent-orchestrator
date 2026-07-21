package planning

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Validator struct {
	MaxParallelTasks     int
	MaxRequiredTaskDepth int
}

func (v Validator) Validate(ctx context.Context, output domain.PlannerOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(output.Summary) == "" || len(output.Tasks) == 0 {
		return fmt.Errorf("plan summary and tasks are required: %w", domain.ErrValidation)
	}
	if !validRisk(output.RiskLevel) {
		return fmt.Errorf("invalid plan risk level %q: %w", output.RiskLevel, domain.ErrValidation)
	}
	tasks := make(map[string]domain.PlannedTask, len(output.Tasks))
	projects := make(map[string]struct{}, len(output.Tasks))
	for _, task := range output.Tasks {
		if task.Key == "" || task.ProjectID == "" || task.Role == "" || strings.TrimSpace(task.Title) == "" ||
			strings.TrimSpace(task.Description) == "" || len(task.AcceptanceCriteria) == 0 || len(task.WriteScope) == 0 ||
			len(task.VerificationCommands) == 0 || !validRisk(task.RiskLevel) || task.Depth < 0 ||
			task.Depth > v.maxDepth() || !validModelProfile(task.ModelProfile) ||
			!allNonEmpty(task.AcceptanceCriteria) || !validWriteScopes(task.WriteScope) ||
			!allNonEmpty(task.VerificationCommands) {
			return fmt.Errorf("task %q is incomplete: %w", task.Key, domain.ErrValidation)
		}
		if _, exists := tasks[task.Key]; exists {
			return fmt.Errorf("duplicate task key %q: %w", task.Key, domain.ErrConflict)
		}
		if _, exists := projects[task.ProjectID]; exists {
			return fmt.Errorf("project %q is assigned to more than one task: %w", task.ProjectID, domain.ErrValidation)
		}
		tasks[task.Key] = task
		projects[task.ProjectID] = struct{}{}
	}
	indegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))
	edges := make(map[string]struct{}, len(output.Dependencies))
	for key := range tasks {
		indegree[key] = 0
	}
	for _, dependency := range output.Dependencies {
		if dependency.TaskKey == dependency.DependsOnTaskKey || dependency.DependencyType == "" {
			return fmt.Errorf("invalid self or untyped dependency for %q: %w", dependency.TaskKey, domain.ErrValidation)
		}
		if _, exists := tasks[dependency.TaskKey]; !exists {
			return fmt.Errorf("dependency references missing task %q: %w", dependency.TaskKey, domain.ErrValidation)
		}
		if _, exists := tasks[dependency.DependsOnTaskKey]; !exists {
			return fmt.Errorf("dependency references missing prerequisite %q: %w", dependency.DependsOnTaskKey, domain.ErrValidation)
		}
		edge := dependency.TaskKey + "\x00" + dependency.DependsOnTaskKey
		if _, exists := edges[edge]; exists {
			return fmt.Errorf("duplicate dependency for %q: %w", dependency.TaskKey, domain.ErrConflict)
		}
		edges[edge] = struct{}{}
		indegree[dependency.TaskKey]++
		dependents[dependency.DependsOnTaskKey] = append(dependents[dependency.DependsOnTaskKey], dependency.TaskKey)
	}
	ready := make([]string, 0)
	for key, degree := range indegree {
		if degree == 0 {
			ready = append(ready, key)
		}
	}
	processed := 0
	for len(ready) > 0 {
		if len(ready) > v.maxParallel() {
			return fmt.Errorf("plan exposes %d parallel tasks, maximum is %d: %w", len(ready), v.maxParallel(), domain.ErrValidation)
		}
		wave := ready
		ready = nil
		processed += len(wave)
		for _, key := range wave {
			for _, dependent := range dependents[key] {
				indegree[dependent]--
				if indegree[dependent] == 0 {
					ready = append(ready, dependent)
				}
			}
		}
	}
	if processed != len(tasks) {
		return fmt.Errorf("plan task graph contains a cycle: %w", domain.ErrValidation)
	}
	return nil
}

func allNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func validWriteScopes(scopes []string) bool {
	for _, scope := range scopes {
		scope = strings.ReplaceAll(strings.TrimSpace(scope), "\\", "/")
		if scope == "" || path.IsAbs(scope) || scope == ".." || strings.HasPrefix(scope, "../") ||
			strings.Contains(scope, "/../") || strings.HasPrefix(scope, "~/") || strings.ContainsRune(scope, '\x00') ||
			(len(scope) >= 2 && scope[1] == ':') {
			return false
		}
	}
	return true
}

func (v Validator) maxParallel() int {
	if v.MaxParallelTasks < 1 {
		return 1
	}
	if v.MaxParallelTasks > 3 {
		return 3
	}
	return v.MaxParallelTasks
}

func (v Validator) maxDepth() int {
	if v.MaxRequiredTaskDepth < 1 {
		return 1
	}
	return v.MaxRequiredTaskDepth
}

func validRisk(value domain.RiskLevel) bool {
	switch value {
	case domain.RiskLevelLow, domain.RiskLevelMedium, domain.RiskLevelHigh, domain.RiskLevelCritical:
		return true
	default:
		return false
	}
}

func validModelProfile(value string) bool {
	switch value {
	case config.ModelProfileFast, config.ModelProfileStandard, config.ModelProfileDeep, config.ModelProfileReview:
		return true
	default:
		return false
	}
}

var _ repository.PlanValidator = Validator{}
