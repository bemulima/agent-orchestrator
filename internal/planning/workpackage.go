package planning

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func BuildWorkPackage(shard domain.ArchitecturalShard, task domain.Task, baseline domain.ContractBaseline) (domain.WorkPackage, error) {
	if shard.ID == "" || shard.PlanID == "" || shard.TaskID != task.ID || shard.PlanID != task.PlanID ||
		shard.RepositoryProjectID != task.ProjectID || shard.Repository == "" ||
		baseline.PlanID != shard.PlanID || baseline.TaskID != task.ID || baseline.RepositoryProjectID != task.ProjectID ||
		baseline.ExecutionState != domain.ContractBaselineFrozen || !baseline.Validation.Passed ||
		shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline ||
		shard.ExecutionBase.Revision != baseline.ContractBaselineCommit ||
		shard.ExecutionBase.ContractBaselineID != baseline.ID ||
		shard.ProfileID != baseline.ProfileID || shard.ProfileFingerprint != baseline.ProfileFingerprint {
		return domain.WorkPackage{}, fmt.Errorf("shard, task, profile, or frozen baseline identity does not match: %w", domain.ErrConflict)
	}
	readonly := make([]string, 0, len(baseline.Files))
	for _, file := range baseline.Files {
		readonly = append(readonly, file.Path)
	}
	for _, contract := range append(append([]domain.ContractReference(nil), shard.Consumes...), shard.Implements...) {
		readonly = append(readonly, contract.Path)
	}
	readonly = uniqueSorted(readonly)
	commands := []string{}
	for _, command := range task.VerificationCommands {
		if strings.TrimSpace(command) != "" {
			commands = append(commands, strings.TrimSpace(command))
		}
	}
	commands = append(commands, "go test ./...", "git diff --check")
	commands = uniqueSorted(commands)
	boundaryKinds := make([]string, 0, len(shard.Verification))
	for _, item := range shard.Verification {
		boundaryKinds = append(boundaryKinds, item.Kind)
	}
	boundaryKinds = uniqueSorted(boundaryKinds)
	workPackage := domain.WorkPackage{
		SchemaVersion: 1, ShardID: shard.ID, PlanID: shard.PlanID, TaskID: shard.TaskID,
		ProjectID: shard.RepositoryProjectID, Repository: shard.Repository,
		ArchitectureProfile: domain.WorkPackageProfile{ID: shard.ProfileID, Fingerprint: shard.ProfileFingerprint},
		Route:               shard.RouteID, ExecutionBase: shard.ExecutionBase, LocalIntent: localShardIntent(shard),
		Contracts: domain.WorkPackageContracts{
			Consumes: sortedContractRefs(shard.Consumes), Implements: sortedContractRefs(shard.Implements),
			ReadOnlyPaths: readonly,
		},
		Targets: domain.WorkPackageTargets{
			Paths: uniqueSorted(shard.Targets.Paths), Symbols: uniqueSorted(shard.Targets.Symbols),
		},
		WriteScope: domain.ShardWriteScope{
			Allow: uniqueSorted(shard.WriteScope.Allow), Deny: uniqueSorted(shard.WriteScope.Deny),
			ReadOnlyContracts: uniqueSorted(shard.WriteScope.ReadOnlyContracts),
			CompositionOnly:   uniqueSorted(shard.WriteScope.CompositionOnly),
			TestPaths:         uniqueSorted(shard.WriteScope.TestPaths), MaxFiles: shard.WriteScope.MaxFiles,
		},
		Verification: domain.WorkPackageVerification{
			Boundary: strings.Join(boundaryKinds, ","), CandidateCommands: commands,
			TestPaths: uniqueSorted(shard.WriteScope.TestPaths),
		},
		SemanticInvariants: routeInvariants(shard.RouteID),
		BlockersAllowed:    append([]string(nil), domain.ShardExecutionBlockers...),
	}
	if err := ValidateWorkPackage(workPackage); err != nil {
		return domain.WorkPackage{}, err
	}
	return workPackage, nil
}

func ValidateWorkPackage(value domain.WorkPackage) error {
	if value.SchemaVersion != 1 || value.ShardID == "" || value.PlanID == "" || value.TaskID == "" ||
		value.ProjectID == "" || value.Repository == "" || value.ArchitectureProfile.ID == "" ||
		value.ArchitectureProfile.Fingerprint == "" || value.Route == "" || value.LocalIntent == "" ||
		value.ExecutionBase.Kind != domain.ShardBaseContractBaseline ||
		!fullCommitSHA(value.ExecutionBase.Revision) || value.ExecutionBase.ContractBaselineID == "" ||
		len(value.Targets.Paths) == 0 || len(value.WriteScope.Allow) == 0 || value.WriteScope.MaxFiles <= 0 ||
		len(value.SemanticInvariants) == 0 || len(value.BlockersAllowed) == 0 || value.Verification.Boundary == "" {
		return fmt.Errorf("work package is incomplete: %w", domain.ErrValidation)
	}
	allow := map[string]struct{}{}
	for _, candidate := range value.WriteScope.Allow {
		if !safeScopePattern(candidate) {
			return fmt.Errorf("work package has an unsafe allow path %q: %w", candidate, domain.ErrValidation)
		}
		allow[candidate] = struct{}{}
	}
	for _, denied := range value.WriteScope.Deny {
		if !safeScopePattern(strings.TrimSuffix(denied, "/")) {
			return fmt.Errorf("work package has an unsafe deny path %q: %w", denied, domain.ErrValidation)
		}
		if _, overlaps := allow[denied]; overlaps {
			return fmt.Errorf("work package path %q is both writable and denied: %w", denied, domain.ErrWriteScope)
		}
	}
	for _, pathValue := range value.Targets.Paths {
		if !safeScopePattern(pathValue) || !scopeContains(value.WriteScope.Allow, pathValue) || scopeContains(value.WriteScope.Deny, pathValue) ||
			scopeContains(value.WriteScope.CompositionOnly, pathValue) {
			return fmt.Errorf("work package target %q is outside its allow scope: %w", pathValue, domain.ErrWriteScope)
		}
	}
	for _, pathValue := range value.Contracts.ReadOnlyPaths {
		if !safeScopePattern(pathValue) || scopeContains(value.WriteScope.Allow, pathValue) ||
			!scopeContains(value.WriteScope.Deny, pathValue) {
			return fmt.Errorf("frozen contract path %q is not read-only: %w", pathValue, domain.ErrWriteScope)
		}
	}
	for _, pathValue := range value.Verification.TestPaths {
		if !safeScopePattern(pathValue) || !scopeContains(value.WriteScope.Allow, pathValue) || scopeContains(value.WriteScope.Deny, pathValue) ||
			scopeContains(value.WriteScope.CompositionOnly, pathValue) {
			return fmt.Errorf("work package test path %q is outside its allow scope: %w", pathValue, domain.ErrWriteScope)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("serialize work package: %w", err)
	}
	var roundTrip domain.WorkPackage
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.ShardID != value.ShardID || roundTrip.ExecutionBase != value.ExecutionBase {
		return fmt.Errorf("work package serialization is unstable: %w", domain.ErrValidation)
	}
	return nil
}

func ValidateDisjointWorkPackages(packages []domain.WorkPackage) error {
	owners := map[string]string{}
	for _, packageValue := range packages {
		for _, pattern := range packageValue.WriteScope.Allow {
			for existing, owner := range owners {
				if owner != packageValue.ShardID && scopePatternsOverlap(existing, pattern) {
					return fmt.Errorf("shards %s and %s have overlapping writable paths %q and %q: %w", owner, packageValue.ShardID, existing, pattern, domain.ErrWriteScope)
				}
			}
			owners[pattern] = packageValue.ShardID
		}
	}
	return nil
}

func localShardIntent(shard domain.ArchitecturalShard) string {
	switch shard.RouteID {
	case "backend.transport.http":
		return "Expose NewAvailabilityHandler taking the frozen application interface. Implement only the HTTP boundary for GET /availability: parse and validate resource_id/start/end, delegate with the frozen application query/result contract, and map invalid requests to HTTP 400. Do not implement application behavior, persistence, SQL, or route composition."
	case "backend.usecase":
		return "Expose NewAvailabilityUsecase taking the frozen domain RepositoryPort. Implement only availability application behavior against the frozen RepositoryPort and query/result contracts. Validate resource identity and the UTC half-open interval, then preserve ordered non-overlapping repository results. Do not implement HTTP parsing, SQL, PostgreSQL, or composition."
	case "backend.infrastructure.persistence":
		return "Expose NewAvailabilityStore taking the existing pgxpool.Pool. Implement only the PostgreSQL adapter for the frozen RepositoryPort, including query behavior for UTC half-open availability intervals. Do not modify application or HTTP behavior, schema migrations, or composition."
	default:
		return "Implement only the approved responsibility described by the persisted shard target and profile route purpose. Preserve frozen contracts and do not modify sibling or composition paths."
	}
}

func routeInvariants(route string) []string {
	switch route {
	case "backend.transport.http":
		return []string{"GET /availability parses resource_id, start, and end", "valid requests delegate through the frozen query/result boundary", "invalid query values map to HTTP 400"}
	case "backend.usecase":
		return []string{"resource_id is non-empty", "start precedes end in the UTC half-open interval [start,end)", "repository is called only after request validation", "result intervals remain ordered and non-overlapping"}
	case "backend.infrastructure.persistence":
		return []string{"query is scoped to the requested resource and UTC half-open interval", "results are ordered and non-overlapping", "the adapter implements the frozen repository port without schema changes"}
	default:
		return []string{}
	}
}

func sortedContractRefs(values []domain.ContractReference) []domain.ContractReference {
	result := append([]domain.ContractReference(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Symbol < result[j].Symbol
	})
	return result
}

func safeScopePattern(value string) bool {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "."
}

func scopeContains(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if pattern == target || strings.HasSuffix(pattern, "/") && strings.HasPrefix(target, pattern) ||
			strings.HasSuffix(pattern, "/**") && strings.HasPrefix(target, strings.TrimSuffix(pattern, "**")) {
			return true
		}
	}
	return false
}

func scopePatternsOverlap(left, right string) bool {
	if left == right || strings.HasSuffix(left, "/**") && strings.HasPrefix(right, strings.TrimSuffix(left, "**")) ||
		strings.HasSuffix(right, "/**") && strings.HasPrefix(left, strings.TrimSuffix(right, "**")) {
		return true
	}
	return false
}

func fullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
