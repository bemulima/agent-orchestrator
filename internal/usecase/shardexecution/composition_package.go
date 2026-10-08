package shardexecution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// BuildCompositionWorkPackage derives an immutable, bounded composition input
// from the approved serialized shard and the actual verified assembled APIs.
func BuildCompositionWorkPackage(root string, shard domain.ArchitecturalShard, baseline domain.ContractBaseline, execution domain.ShardFanoutExecution) (domain.CompositionWorkPackage, error) {
	fail := func(reason string) (domain.CompositionWorkPackage, error) {
		return domain.CompositionWorkPackage{}, fmt.Errorf("composition package %s: %w", reason, domain.ErrConflict)
	}
	if shard.RouteID != "backend.composition" || shard.Phase != "after_workers" || shard.Parallel || shard.Status != domain.ShardStatusComposition || shard.ID == "" || shard.PlanID != baseline.PlanID || shard.TaskID != baseline.TaskID || shard.RepositoryProjectID != baseline.RepositoryProjectID || shard.ProfileID != baseline.ProfileID || shard.ProfileFingerprint != baseline.ProfileFingerprint {
		return fail("is not the approved serialized shard")
	}
	if baseline.ExecutionState != domain.ContractBaselineFrozen || !baseline.Validation.Passed || baseline.ApprovedPlanFingerprint == "" || baseline.ID == "" || baseline.ContractBaselineCommit == "" || shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline || shard.ExecutionBase.Revision != baseline.ContractBaselineCommit || shard.ExecutionBase.ContractBaselineID != baseline.ID {
		return fail("frozen baseline identity differs")
	}
	if execution.PlanID != shard.PlanID || execution.ContractBaselineID != baseline.ID || execution.BaselineCommit != baseline.ContractBaselineCommit || execution.Barrier != domain.ShardBarrierReady || execution.AssemblyCommit == "" || execution.AssemblyWorkspace.Path == "" {
		return fail("requires a verified assembled barrier")
	}
	rootPath, rootErr := filepath.EvalSymlinks(root)
	assemblyPath, assemblyErr := filepath.EvalSymlinks(execution.AssemblyWorkspace.Path)
	if rootErr != nil || assemblyErr != nil || rootPath != assemblyPath {
		return fail("API source is not the assembled workspace")
	}
	allowed := cleanSorted(shard.WriteScope.Allow)
	narrow := []string{"cmd/availability-service/main.go", "cmd/availability-service/main_test.go"}
	if !samePaths(allowed, narrow) || shard.WriteScope.MaxFiles < 2 || !samePaths(cleanSorted(shard.WriteScope.TestPaths), []string{narrow[1]}) {
		return fail("write scope exceeds approved composition files")
	}
	// A materialized composition shard retains cmd/ in the generic worker
	// exclusion list. The serialized package grants only its two exact approved
	// files; cmd/ remains read-only for ordinary workers and every other cmd file
	// remains outside this package's allow list.
	effectiveDeny := []string{}
	for _, deny := range shard.WriteScope.Deny {
		if deny == "cmd/" && slices.Contains(shard.WriteScope.CompositionOnly, "cmd/") {
			continue
		}
		effectiveDeny = append(effectiveDeny, deny)
	}
	for _, path := range narrow {
		for _, deny := range effectiveDeny {
			if pathMatches(deny, path) {
				return fail("approved path is denied")
			}
		}
	}
	wp := domain.CompositionWorkPackage{SchemaVersion: 1, ShardID: shard.ID, PlanID: shard.PlanID, PlanFingerprint: baseline.ApprovedPlanFingerprint, TaskID: shard.TaskID, ProjectID: shard.RepositoryProjectID, Repository: shard.Repository, Route: shard.RouteID, ExecutionBase: shard.ExecutionBase, AssemblyCommit: execution.AssemblyCommit, WriteScope: shard.WriteScope, LocalIntent: shard.LocalIntent}
	wp.WriteScope.Allow = allowed
	wp.WriteScope.Deny = cleanSorted(effectiveDeny)
	wp.WriteScope.CompositionOnly = cleanSorted(shard.WriteScope.CompositionOnly)
	wp.WriteScope.ReadOnlyContracts = cleanSorted(shard.WriteScope.ReadOnlyContracts)
	wp.WriteScope.TestPaths = cleanSorted(shard.WriteScope.TestPaths)
	sources := []string{}
	for _, file := range baseline.Files {
		wp.ReadOnlyPaths = append(wp.ReadOnlyPaths, file.Path)
		sources = append(sources, file.Path)
	}
	routes := map[string]bool{}
	for _, attempt := range AssemblyOrder(execution.Attempts) {
		route := attempt.WorkPackage.Route
		if route != "backend.transport.http" && route != "backend.usecase" && route != "backend.infrastructure.persistence" || routes[route] || attempt.Status != domain.ShardAttemptVerified || attempt.CommitSHA == "" || attempt.BaselineCommit != baseline.ContractBaselineCommit || attempt.WorkPackage.ExecutionBase.Revision != baseline.ContractBaselineCommit || attempt.WorkPackage.ExecutionBase.ContractBaselineID != baseline.ID || attempt.WorkPackage.ProjectID != shard.RepositoryProjectID || attempt.Green == nil || !attempt.Green.Passed {
			return fail("effective worker set is not verified on one frozen baseline")
		}
		routes[route] = true
		wp.EffectiveCommits = append(wp.EffectiveCommits, domain.CompositionShardCommit{ShardID: attempt.ShardID, Route: route, CommitSHA: attempt.CommitSHA, BaselineCommit: attempt.BaselineCommit})
		wp.ReadOnlyPaths = append(wp.ReadOnlyPaths, attempt.WorkPackage.WriteScope.Allow...)
		wp.ReadOnlyPaths = append(wp.ReadOnlyPaths, attempt.ChangedFiles...)
		sources = append(sources, attempt.ChangedFiles...)
	}
	if len(routes) != 3 {
		return fail("requires HTTP/usecase/persistence verified commits")
	}
	wp.ReadOnlyPaths = cleanSorted(wp.ReadOnlyPaths)
	for _, readonly := range wp.ReadOnlyPaths {
		for _, path := range narrow {
			if pathMatches(readonly, path) {
				return fail("composition scope overlaps frozen or sibling source")
			}
		}
	}
	for _, source := range cleanSorted(sources) {
		if !strings.HasSuffix(source, ".go") || strings.HasSuffix(source, "_test.go") {
			continue
		}
		apis, err := readCompositionAPIs(root, source)
		if err != nil {
			return fail("cannot parse verified API: " + err.Error())
		}
		wp.APIs = append(wp.APIs, apis...)
	}
	sort.Slice(wp.APIs, func(i, j int) bool {
		a, b := wp.APIs[i], wp.APIs[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Symbol < b.Symbol
	})
	constructors, interfaces := 0, 0
	for _, api := range wp.APIs {
		if api.Kind == "constructor" {
			constructors++
		}
		if api.Kind == "interface" {
			interfaces++
		}
	}
	if constructors < 3 || interfaces < 2 {
		return fail("actual exported constructors/interfaces are missing")
	}
	commands := []string{"go test ./... -count=1", "git diff --check"}
	kinds := []string{}
	for _, item := range shard.Verification {
		commands = append(commands, item.Commands...)
		kinds = append(kinds, item.Kind)
	}
	wp.Verification = domain.WorkPackageVerification{Boundary: strings.Join(cleanSorted(kinds), ","), CandidateCommands: uniqueCommands(commands), TestPaths: append([]string(nil), wp.WriteScope.TestPaths...)}
	if wp.Verification.Boundary == "" {
		wp.Verification.Boundary = "composition_integration"
	}
	bytes, err := json.Marshal(wp)
	if err != nil {
		return fail("cannot encode immutable package")
	}
	digest := sha256.Sum256(bytes)
	wp.ID = "sha256:" + hex.EncodeToString(digest[:])
	return wp, nil
}

func readCompositionAPIs(root, path string) ([]domain.CompositionAPI, error) {
	if filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.HasPrefix(path, "../") {
		return nil, fmt.Errorf("unsafe API path")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(canonical, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("API path escapes assembled workspace")
	}
	file, err := parser.ParseFile(token.NewFileSet(), full, nil, 0)
	if err != nil {
		return nil, err
	}
	result := []domain.CompositionAPI{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && ast.IsExported(fn.Name.Name) && strings.HasPrefix(fn.Name.Name, "New") {
			result = append(result, domain.CompositionAPI{Path: path, Package: file.Name.Name, Kind: "constructor", Symbol: fn.Name.Name, Signature: setupText(fn.Type)})
		}
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok && ast.IsExported(typ.Name.Name) {
					if _, ok := typ.Type.(*ast.InterfaceType); ok {
						result = append(result, domain.CompositionAPI{Path: path, Package: file.Name.Name, Kind: "interface", Symbol: typ.Name.Name, Signature: setupText(typ.Type)})
					}
				}
			}
		}
	}
	return result, nil
}
