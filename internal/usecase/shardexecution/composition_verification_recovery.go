package shardexecution

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"strconv"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// compositionStartupFallbacks recognizes only a top-level HTTP_ADDR default in
// main, with direct environment provenance and no extra statements or branch.
func compositionStartupFallbacks(fn *ast.FuncDecl) map[*ast.IfStmt]bool {
	allowed := map[*ast.IfStmt]bool{}
	if fn.Name.Name != "main" || fn.Recv != nil || fn.Body == nil {
		return allowed
	}
	environment := map[string]bool{}
	for _, statement := range fn.Body.List {
		if assignment, ok := statement.(*ast.AssignStmt); ok {
			for _, lhs := range assignment.Lhs {
				if name, ok := lhs.(*ast.Ident); ok {
					environment[name.Name] = false
				}
			}
			if len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
				name, nok := assignment.Lhs[0].(*ast.Ident)
				call, cok := assignment.Rhs[0].(*ast.CallExpr)
				if nok && cok && len(call.Args) == 1 {
					selector, sok := call.Fun.(*ast.SelectorExpr)
					literal, lok := call.Args[0].(*ast.BasicLit)
					if sok && lok && literal.Kind == token.STRING {
						owner, ok := selector.X.(*ast.Ident)
						value, _ := strconv.Unquote(literal.Value)
						if ok && owner.Name == "os" && selector.Sel.Name == "Getenv" && value == "HTTP_ADDR" {
							environment[name.Name] = true
						}
					}
				}
			}
		}
		conditional, ok := statement.(*ast.IfStmt)
		if !ok || conditional.Init != nil || conditional.Else != nil || len(conditional.Body.List) != 1 {
			continue
		}
		condition, ok := conditional.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.EQL {
			continue
		}
		variable, ok := condition.X.(*ast.Ident)
		if !ok || !environment[variable.Name] {
			continue
		}
		empty, ok := condition.Y.(*ast.BasicLit)
		if !ok || empty.Kind != token.STRING {
			continue
		}
		text, err := strconv.Unquote(empty.Value)
		if err != nil || text != "" {
			continue
		}
		fallback, ok := conditional.Body.List[0].(*ast.AssignStmt)
		if !ok || fallback.Tok != token.ASSIGN || len(fallback.Lhs) != 1 || len(fallback.Rhs) != 1 {
			continue
		}
		target, ok := fallback.Lhs[0].(*ast.Ident)
		if !ok || target.Name != variable.Name {
			continue
		}
		literal, ok := fallback.Rhs[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || value != ":8080" {
			continue
		}
		allowed[conditional] = true
		environment[variable.Name] = false
	}
	return allowed
}
func isCompositionFallbackCondition(expression *ast.BinaryExpr, allowed map[*ast.IfStmt]bool) bool {
	for conditional := range allowed {
		if conditional.Cond == expression {
			return true
		}
	}
	return false
}

func compositionWiringVerificationRecoveryAllowed(execution domain.ShardFanoutExecution) bool {
	attempt := execution.CompositionAttempt
	if attempt == nil || execution.State != "COMPOSITION_BLOCKED" || execution.Composition != "COMPOSITION_BLOCKED" || attempt.Status != domain.ShardAttemptBlocked || attempt.Phase != domain.ShardPhaseImplementing || attempt.ID == "" || attempt.WorkerThreadID == "" || attempt.Model == "" || attempt.ReasoningEffort == "" || attempt.StartedAt.IsZero() || attempt.FinishedAt == nil || len(attempt.RecoveryHistory) != 0 || attempt.BudgetBlocker != nil || attempt.Red == nil || !attempt.Red.Semantic || attempt.Red.RecordedAt.IsZero() || attempt.REDSetup == nil || attempt.REDSetup.MechanicalReview != "PASS" || attempt.FixtureReplay == nil || attempt.Green != nil || attempt.Implementation != nil || attempt.CommitSHA != "" || len(attempt.Verification) != 0 || len(attempt.ChangedFiles) != 0 {
		return false
	}
	if len(attempt.Blockers) != 1 || attempt.Blockers[0] != "OUT_OF_SCOPE_CHANGE_REQUIRED" || len(execution.BarrierReasons) != 1 || execution.BarrierReasons[0] != "OUT_OF_SCOPE_CHANGE_REQUIRED: composition only allows startup error checks" {
		return false
	}
	return samePaths(attempt.Red.TestPaths, attempt.WorkPackage.Verification.TestPaths) && attempt.Red.Command == "go test ./... -count=1"
}

func (s Service) validateCompositionVerificationRecoverySources(ctx context.Context, execution domain.ShardFanoutExecution, wp domain.CompositionWorkPackage, workspace domain.TaskWorkspace, paths []string) error {
	attempt := execution.CompositionAttempt
	if !samePaths(paths, wp.WriteScope.Allow) || attempt.FixtureReplay.NewWorkPackageID != wp.ID {
		return fmt.Errorf("composition verification recovery scope or package changed: %w", domain.ErrConflict)
	}
	for _, path := range attempt.Red.TestPaths {
		source, err := s.Worktrees.ReadArtifact(ctx, workspace, path, 1<<20)
		if err != nil {
			return err
		}
		if contentHash(source) != attempt.FixtureReplay.SourceHashes[path] {
			return fmt.Errorf("composition verification recovery RED test changed: %w", domain.ErrConflict)
		}
	}
	return ValidateCompositionWiring(workspace.Path, wp, paths, false)
}
