package shardexecution

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/google/uuid"
)

// ExecuteComposition executes a separately approved assembly package only after
// all layer commits have passed the barrier and their input tree is assembled.
func (s Service) ExecuteComposition(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	inputs, err := s.loadApprovedInputs(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return execution, err
	}
	if !prepared.CompositionRequired || prepared.CompositionShard == nil || execution.Barrier != domain.ShardBarrierReady || execution.BarrierReadyAt == nil || s.now().Before(*execution.BarrierReadyAt) || (execution.Composition != "COMPOSITION_REQUIRED" && execution.Composition != "COMPOSITION_BLOCKED") || execution.AssemblyCommit == "" || execution.AssemblyWorkspace.Path == "" {
		return execution, fmt.Errorf("composition requires approved route, passed barrier and verified worker assembly: %w", domain.ErrInvalidStatus)
	}
	verificationRecovery := compositionWiringVerificationRecoveryAllowed(execution)
	recovering := execution.CompositionAttempt != nil
	if !recovering && execution.Composition != "COMPOSITION_REQUIRED" {
		return execution, fmt.Errorf("composition blocked without recoverable attempt: %w", domain.ErrConflict)
	}
	if recovering && !verificationRecovery && !compositionFormattingRecoveryAllowed(execution) {
		return execution, fmt.Errorf("composition already has an attempt; explicit owner retry required: %w", domain.ErrConflict)
	}
	canonical, ok := inputs.shardByID[prepared.CompositionShard.ID]
	if !ok || !sameJSON(canonical, *prepared.CompositionShard) {
		return execution, fmt.Errorf("composition shard changed: %w", domain.ErrConflict)
	}
	baseline := inputs.baselines[canonical.TaskID]
	project := inputs.projects[canonical.RepositoryProjectID]
	packageValue, err := BuildCompositionWorkPackage(execution.AssemblyWorkspace.Path, canonical, baseline, execution)
	if err != nil {
		return execution, err
	}
	workspace := execution.AssemblyWorkspace
	workspace.BaseCommit = execution.AssemblyCommit // composition-only diff and commit parent
	state, err := s.Worktrees.Inspect(ctx, project, workspace)
	if err != nil || state.HeadCommit != execution.AssemblyCommit || (!recovering && len(state.ChangedFiles) != 0) {
		return execution, fmt.Errorf("composition input tree is not clean at assembled worker tip: %w", domain.ErrConflict)
	}
	if recovering {
		if !sameJSON(execution.CompositionAttempt.WorkPackage, packageValue) || !sameJSON(execution.CompositionAttempt.Workspace, workspace) || execution.CompositionAttempt.BaselineCommit != baseline.ContractBaselineCommit {
			return execution, fmt.Errorf("composition recovery package/workspace/baseline drift: %w", domain.ErrConflict)
		}
		if verificationRecovery {
			if err := s.validateCompositionVerificationRecoverySources(ctx, execution, packageValue, workspace, state.ChangedFiles); err != nil {
				return execution, err
			}
		} else if err := validateCompositionRecoveryScaffold(workspace.Path, packageValue, state.ChangedFiles); err != nil {
			return execution, err
		}
	}
	if err := verifyWorkingFrozenFiles(ctx, s.Worktrees, workspace, baseline); err != nil {
		return execution, err
	}
	before, err := s.Worktrees.Snapshot(ctx, project, workspace)
	if err != nil {
		return execution, err
	}
	task := domain.Task{ID: packageValue.TaskID, PlanID: packageValue.PlanID, ProjectID: project.ID, ModelProfile: config.ModelProfileFast, RiskLevel: domain.RiskLevelMedium}
	var attempt *domain.CompositionAttempt
	if recovering {
		attempt = execution.CompositionAttempt
	} else {
		decision := s.Router.Coder(task)
		now := s.now()
		attempt = &domain.CompositionAttempt{ID: uuid.NewString(), WorkPackage: packageValue, Workspace: workspace, BaselineCommit: packageValue.ExecutionBase.Revision, Phase: domain.ShardPhasePrepared, PhaseHistory: []domain.ShardPhaseEvent{{Phase: domain.ShardPhasePrepared, RecordedAt: now}}, Model: decision.Model, ReasoningEffort: decision.Reasoning, Status: domain.ShardAttemptRunning, StartedAt: now, UpdatedAt: now}
	}
	if recovering {
		recoveryReason := "strict wire timestamp formatting failure; verified empty-mux setup adopted without another agent call"
		if verificationRecovery {
			recoveryReason = "bounded HTTP_ADDR startup fallback mechanical guard correction; same completed wiring attempt resumes verification only, no agent call"
		}
		attempt.RecoveryHistory = append(attempt.RecoveryHistory, domain.CompositionRecoveryEvidence{RecordedAt: s.now(), PriorState: execution.State, PriorStatus: attempt.Status, PriorBlockers: append([]string(nil), attempt.Blockers...), PriorBarrierReasons: append([]string(nil), execution.BarrierReasons...), PriorFinishedAt: attempt.FinishedAt, Reason: recoveryReason})
		attempt.Status = domain.ShardAttemptRunning
		attempt.FinishedAt = nil
		attempt.Blockers = nil
		execution.BarrierReasons = nil
	}
	execution.Composition = "COMPOSITION_REQUIRED"
	if len(execution.CompositionHistory) > 0 {
		prior := execution.CompositionHistory[len(execution.CompositionHistory)-1]
		attempt.PriorCompositionAttemptID = prior.ID
		if !sameJSON(prior.WorkPackage, packageValue) {
			attempt.ReuseDecision = "NEW_ATTEMPT_REQUIRED: CompositionWorkPackage/input effective commits changed"
		} else {
			attempt.ReuseDecision = "NEW_ATTEMPT_REQUIRED: clean patch reuse has not been independently proven"
		}
	}
	execution.CompositionAttempt = attempt
	execution.State = "COMPOSITION_RUNNING"
	save := func() error {
		attempt.UpdatedAt = s.now()
		execution.UpdatedAt = s.now()
		return s.Execution.SaveShardFanout(ctx, execution)
	}
	fail := func(blocker string, cause error) (domain.ShardFanoutExecution, error) {
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			blocker = "EXECUTION_CANCELLED"
		}

		attempt.Status = domain.ShardAttemptBlocked
		if attempt.BudgetBlocker != nil {
			blocker = "BUDGET_EXHAUSTED"
		}
		if blocker == "CONTRACT_CHANGE_REQUIRED" {
			attempt.Status = domain.ShardAttemptContractChange
		}
		if blocker == "REPLAN_REQUIRED" {
			attempt.Status = domain.ShardAttemptReplan
		}
		attempt.Blockers = appendUnique(attempt.Blockers, blocker)
		done := s.now()
		attempt.FinishedAt = &done
		execution.State = "COMPOSITION_BLOCKED"
		execution.Composition = "COMPOSITION_BLOCKED"
		execution.BarrierReasons = appendUnique(execution.BarrierReasons, blocker+": "+bounded(cause.Error(), 1000))
		if err := save(); err != nil {
			return execution, err
		}
		return execution, cause
	}
	phase := func(value domain.ShardExecutionPhase) error {
		attempt.Phase = value
		attempt.PhaseHistory = append(attempt.PhaseHistory, domain.ShardPhaseEvent{Phase: value, RecordedAt: s.now()})
		return save()
	}
	validate := func() (domain.WorkspaceState, domain.WorkspaceSnapshot, error) {
		state, err := s.Worktrees.Inspect(ctx, project, workspace)
		if err != nil {
			return state, domain.WorkspaceSnapshot{}, err
		}
		if state.HeadCommit != packageValue.AssemblyCommit {
			return state, domain.WorkspaceSnapshot{}, fmt.Errorf("composition worker changed HEAD: %w", domain.ErrConflict)
		}
		if !compositionScopeAllows(packageValue, state.ChangedFiles) {
			return state, domain.WorkspaceSnapshot{}, domain.ErrWriteScope
		}
		snapshot, err := s.Worktrees.Snapshot(ctx, project, workspace)
		if err != nil {
			return state, snapshot, err
		}
		for _, path := range snapshotChangedFiles(before, snapshot) {
			if !compositionScopeAllows(packageValue, []string{path}) {
				return state, snapshot, fmt.Errorf("read-only assembled source changed: %s: %w", path, domain.ErrWriteScope)
			}
		}
		if err := verifyWorkingFrozenFiles(ctx, s.Worktrees, workspace, baseline); err != nil {
			return state, snapshot, err
		}
		return state, snapshot, nil
	}
	if err := save(); err != nil {
		return execution, err
	}
	if verificationRecovery {
		state, snapshot, err := validate()
		if err != nil {
			return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
		}
		if !samePaths(state.ChangedFiles, packageValue.WriteScope.Allow) {
			return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("verification resume diff scope changed"))
		}
		return s.completeCompositionVerification(ctx, &execution, project, task, workspace, packageValue, before, snapshot, before, attempt.Red.TestPaths, *attempt.Red, validate, fail, phase)
	}
	if s.Runner == nil {
		return fail("DEPENDENCY_NOT_READY", fmt.Errorf("composition runner unavailable"))
	}
	replayed := false
	replayProduction := []string{}
	if !recovering {
		if prior := compositionReplayCandidate(packageValue, execution.CompositionHistory); prior != nil {
			if err := phase(domain.ShardPhaseREDSetup); err != nil {
				return execution, err
			}
			replayProduction, err = s.replayCompositionRegression(ctx, project, attempt, *prior)
			if err != nil {
				return fail("TEST_BOUNDARY_MISSING", err)
			}
			replayed = true
		}
	}
	// A callable empty mux produces an observable 404 without implementing wiring.
	var setup domain.WorkerPhaseResult
	if replayed {
		setup = domain.WorkerPhaseResult{Status: "completed", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision, ChangedFiles: append([]string(nil), replayProduction...)}
	} else if recovering {
		setup = domain.WorkerPhaseResult{Status: "completed", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision, ChangedFiles: append([]string(nil), state.ChangedFiles...)}
	} else {
		if err := phase(domain.ShardPhaseREDSetup); err != nil {
			return execution, err
		}
		setup, err = s.runCompositionPhase(ctx, &execution, "RED_SETUP", compositionPrompt(packageValue, "setup", nil), phaseSchema())
		if err != nil {
			return fail("TEST_BOUNDARY_MISSING", err)
		}
		if setup.Status != "completed" || setup.ContractChangeRequested {
			return fail(compositionBlocker(setup.Blockers, setup.ContractChangeRequested), fmt.Errorf("composition setup blocked"))
		}
	}
	setupState, setupSnapshot, err := validate()
	if err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	setupPaths := setupState.ChangedFiles
	if replayed {
		setupPaths = replayProduction
	}
	if !samePaths(setupPaths, setup.ChangedFiles) || len(setupPaths) == 0 {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("composition setup claimed paths differ from actual diff"))
	}
	if err := ValidateCompositionWiring(workspace.Path, packageValue, setupPaths, true); err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	setupSources := map[string]string{}
	setupHashes := map[string]string{}
	for _, path := range setupPaths {
		data, err := s.Worktrees.ReadArtifact(ctx, workspace, path, 1<<20)
		if err != nil {
			return fail("TEST_BOUNDARY_MISSING", err)
		}
		setupSources[path] = string(data)
		setupHashes[path] = contentHash(data)
	}
	attempt.REDSetup = &domain.ShardREDSetupEvidence{Sources: setupSources, ChangedFiles: cleanSorted(setupPaths), Files: setupHashes, SnapshotSHA: snapshotDigest(setupSnapshot), MechanicalReview: "PASS", RecordedAt: s.now()}
	if err := save(); err != nil {
		return execution, err
	}
	var redResult domain.WorkerPhaseResult
	if replayed {
		redResult = domain.WorkerPhaseResult{Status: "completed", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision, ChangedFiles: append([]string(nil), packageValue.Verification.TestPaths...), Red: domain.ShardREDEvidence{ExpectedFailure: "TestCompositionAvailabilityRoute: semantic RED: current empty router returns404 for expected reachable availability response", TestPaths: append([]string(nil), packageValue.Verification.TestPaths...)}}
	} else {
		redResult, err := s.runCompositionPhase(ctx, &execution, "semantic RED test-only", compositionPrompt(packageValue, "red", nil), phaseSchema())
		if err != nil {
			return fail("TEST_BOUNDARY_MISSING", err)
		}
		if redResult.Status != "completed" || redResult.ContractChangeRequested {
			return fail(compositionBlocker(redResult.Blockers, redResult.ContractChangeRequested), fmt.Errorf("composition RED preparation blocked"))
		}
	}
	_, redSnapshot, err := validate()
	if err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	delta := snapshotChangedFiles(setupSnapshot, redSnapshot)
	if replayed {
		delta = append([]string(nil), packageValue.Verification.TestPaths...)
	}
	if len(delta) == 0 || !samePaths(delta, redResult.ChangedFiles) || !pathsWithin(delta, packageValue.Verification.TestPaths) {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("composition RED phase must change declared tests only"))
	}
	for _, path := range delta {
		test, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(workspace.Path, path), nil, 0)
		if parseErr != nil {
			return fail("TEST_BOUNDARY_MISSING", parseErr)
		}
		if err := validateCompositionBehaviorTest(test); err != nil {
			return fail("TEST_BOUNDARY_MISSING", err)
		}
	}
	redCheck, err := s.Worktrees.RunCheck(ctx, workspace, "go test ./... -count=1")
	if err != nil {
		return fail("TEST_BOUNDARY_MISSING", err)
	}
	redValidation := redCheck
	redValidation.Command = "go test ./..." // shared validator recognizes the suite; execution is explicitly uncached
	if err := ValidateSemanticRED(redResult.Red, delta, redValidation); err != nil {
		return fail("TEST_BOUNDARY_MISSING", err)
	}
	if !strings.Contains(redCheck.Output, "TestCompositionAvailabilityRoute") || !strings.Contains(redCheck.Output, "404") {
		return fail("TEST_BOUNDARY_MISSING", fmt.Errorf("composition RED must prove actual unreachable route HTTP404"))
	}
	red := redResult.Red
	red.Command = "go test ./... -count=1"
	red.TestPaths = delta
	red.ObservedFailure = bounded(redCheck.Output, 4000)
	red.Semantic = true
	red.RecordedAt = s.now()
	red.PreImplementationSHA = snapshotDigest(setupSnapshot)
	attempt.Red = &red
	if err := phase(domain.ShardPhaseREDVerified); err != nil {
		return execution, err
	}
	if err := phase(domain.ShardPhaseImplementing); err != nil {
		return execution, err
	}
	result, err := s.runCompositionPhase(ctx, &execution, "wiring implementation after semantic RED", compositionPrompt(packageValue, "implementation", &red), phaseSchema())
	if err != nil {
		return fail("DEPENDENCY_NOT_READY", err)
	}
	if result.Status != "completed" || result.ContractChangeRequested {
		return fail(compositionBlocker(result.Blockers, result.ContractChangeRequested), fmt.Errorf("composition implementation blocked"))
	}
	finalState, beforeGreen, err := validate()
	if err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	if !implementationClaimMatches(result.ChangedFiles, finalState.ChangedFiles, delta) {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("composition claim differs from full actual diff"))
	}
	if !redTestsUnchanged(redSnapshot, delta, finalState.ChangedFiles, workspace.Path) {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("composition semantic RED test changed during implementation"))
	}
	if err := ValidateCompositionWiring(workspace.Path, packageValue, finalState.ChangedFiles, false); err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	return s.completeCompositionVerification(ctx, &execution, project, task, workspace, packageValue, before, beforeGreen, redSnapshot, delta, red, validate, fail, phase)
}

func (s Service) completeCompositionVerification(ctx context.Context, execution *domain.ShardFanoutExecution, project domain.Project, task domain.Task, workspace domain.TaskWorkspace, packageValue domain.CompositionWorkPackage, before, beforeGreen, redSnapshot domain.WorkspaceSnapshot, delta []string, red domain.ShardREDEvidence, validate func() (domain.WorkspaceState, domain.WorkspaceSnapshot, error), fail func(string, error) (domain.ShardFanoutExecution, error), phase func(domain.ShardExecutionPhase) error) (domain.ShardFanoutExecution, error) {
	attempt := execution.CompositionAttempt
	green := &domain.ShardGreenEvidence{Passed: true, RecordedAt: s.now()}
	candidateCommands := append([]string{}, packageValue.Verification.CandidateCommands...)
	for index, command := range candidateCommands {
		if command == "go test ./..." {
			candidateCommands[index] = "go test ./... -count=1"
		}
	}
	commands := uniqueCommands(append(candidateCommands, "go test ./... -count=1", "git diff --check"))
	for _, command := range commands {
		check, err := s.Worktrees.RunCheck(ctx, workspace, command)
		if err != nil || check.ExitCode != 0 {
			if err == nil {
				err = fmt.Errorf("composition GREEN failed: %s", command)
			}
			return fail("DEPENDENCY_NOT_READY", err)
		}
		green.Commands = append(green.Commands, check)
	}
	finalState, finalSnapshot, err := validate()
	if err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	if len(snapshotChangedFiles(beforeGreen, finalSnapshot)) != 0 || !redTestsUnchanged(redSnapshot, delta, finalState.ChangedFiles, workspace.Path) {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("verification modified composition source or immutable RED tests"))
	}
	attempt.Green = green
	attempt.Implementation = &domain.WorkerResult{Status: "completed", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision, WorkerThread: attempt.WorkerThreadID, ChangedFiles: finalState.ChangedFiles, Red: red, Green: *green}
	attempt.ChangedFiles = cleanSorted(finalState.ChangedFiles)
	readonly := map[string]string{}
	for path, hash := range before.Files {
		if !compositionScopeAllows(packageValue, []string{path}) {
			readonly[path] = hash
		}
	}
	attempt.Verification, _ = json.Marshal(map[string]any{"scope": "PASS", "frozen_contracts": "PASS", "wiring_ast": "PASS", "readonly_input_hashes": readonly, "input_assembly_commit": packageValue.AssemblyCommit, "effective_commits": packageValue.EffectiveCommits, "final_snapshot_sha": snapshotDigest(finalSnapshot)})
	if err := phase(domain.ShardPhaseGREENVerified); err != nil {
		return *execution, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	accepted, acceptedErr := s.Execution.GetShardFanout(ctx, packageValue.PlanID)
	if acceptedErr != nil {
		return domain.ShardFanoutExecution{}, acceptedErr
	}
	if accepted.CompositionAttempt == nil || accepted.CompositionAttempt.ID != attempt.ID || accepted.CompositionAttempt.Status != domain.ShardAttemptRunning {
		return fail("STALE_EXECUTION", domain.ErrConflict)
	}
	commit, err := s.Worktrees.Commit(ctx, project, task, workspace, attempt.ChangedFiles)
	if err != nil {
		return fail("DEPENDENCY_NOT_READY", err)
	}
	verifier, ok := s.Worktrees.(CommitVerifier)
	if !ok {
		return fail("DEPENDENCY_NOT_READY", fmt.Errorf("managed commit verifier unavailable"))
	}
	if err := verifier.VerifyCommit(ctx, project, workspace, commit, packageValue.AssemblyCommit, attempt.ChangedFiles); err != nil {
		return fail("OUT_OF_SCOPE_CHANGE_REQUIRED", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("EXECUTION_CANCELLED", err)
	}
	attempt.CommitSHA = commit
	attempt.Status = domain.ShardAttemptVerified
	done := s.now()
	attempt.FinishedAt = &done
	execution.Composition = "VERIFIED"
	execution.State = "COMPOSITION_VERIFIED"
	if err := phase(domain.ShardPhaseVerified); err != nil {
		return *execution, err
	}
	return *execution, nil
}

func (s Service) runCompositionPhase(ctx context.Context, execution *domain.ShardFanoutExecution, name, prompt string, schema map[string]any) (domain.WorkerPhaseResult, error) {
	attempt := execution.CompositionAttempt
	request := domain.AgentRunRequest{Role: domain.AgentRunCoder, ThreadID: attempt.WorkerThreadID, WorkingDirectory: attempt.Workspace.Path, Model: attempt.Model, ReasoningEffort: attempt.ReasoningEffort, Prompt: prompt, OutputSchema: schema, SandboxScope: &domain.AgentSandboxScope{Profile: "composition", WritePaths: sandboxCompositionPaths(attempt.WorkPackage, name)}, UsageContext: &domain.AgentUsageContext{ResourceType: "composition", ResourceID: attempt.ID, RouteReason: name}}
	var observed string
	response, err := s.Runner.Run(ctx, request, func(callbackCtx context.Context, thread string) error {
		observed = thread
		attempt.WorkerThreadID = thread
		attempt.UpdatedAt = s.now()
		execution.UpdatedAt = s.now()
		return s.Execution.SaveShardFanout(callbackCtx, *execution)
	})
	if err != nil {
		s.recordCompositionBudgetDenial(ctx, attempt, name, err)
		return domain.WorkerPhaseResult{}, err
	}
	if err := validateRunnerThread(request.ThreadID, observed, response.ThreadID); err != nil {
		return domain.WorkerPhaseResult{}, err
	}
	result, err := decodeCompositionPhaseResult(response.Result)
	if err != nil {
		return result, err
	}
	if result.ShardID != attempt.WorkPackage.ShardID || result.BaselineRevision != attempt.WorkPackage.ExecutionBase.Revision {
		return result, fmt.Errorf("composition phase identity mismatch: %w", domain.ErrConflict)
	}
	result.WorkerThread = response.ThreadID
	return result, nil
}

func compositionBlocker(blockers []string, contract bool) string {
	if contract {
		return "CONTRACT_CHANGE_REQUIRED"
	}
	for _, blocker := range blockers {
		if blocker == "CONTRACT_CHANGE_REQUIRED" || blocker == "REPLAN_REQUIRED" || blocker == "DEPENDENCY_NOT_READY" || blocker == "TEST_BOUNDARY_MISSING" {
			return blocker
		}
	}
	return "REPLAN_REQUIRED"
}

func compositionScopeAllows(wp domain.CompositionWorkPackage, paths []string) bool {
	if len(paths) > wp.WriteScope.MaxFiles {
		return false
	}
	temporary := domain.WorkPackage{WriteScope: wp.WriteScope, Contracts: domain.WorkPackageContracts{ReadOnlyPaths: wp.ReadOnlyPaths}}
	temporary.WriteScope.CompositionOnly = nil // this role alone owns the approved composition paths
	return scopeAllowsFiles(temporary, paths)
}

func compositionPrompt(wp domain.CompositionWorkPackage, phase string, red *domain.ShardREDEvidence) string {
	common := "You are the serialized Composition worker after BARRIER_READY. Only the typed CompositionWorkPackage below is authorized. Exact public APIs are parsed from the actually verified worker assembly. Every frozen contract, domain/usecase/HTTP/persistence implementation, migration and sibling test is READ-ONLY. No business logic, validation, interval filtering, SQL, layer repairs, mocks replacing production components, reflect/unsafe, new contracts, worktrees, branches, commits or environment changes. If an upstream worker bug or missing API/test boundary prevents correct wiring, STOP with DEPENDENCY_NOT_READY or REPLAN_REQUIRED; never repair a layer. Do not run tests or Docker; orchestrator owns existing real PostgreSQL fixture and verification. All phase reports use shard_id from package and baseline_revision from execution_base.revision, never invent threadID. ChangedFiles must exactly reflect phase writes. Packet:\n" + compactJSON(wp) + "\n"
	switch phase {
	case "setup":
		return common + "RED_SETUP only: create the approved production main.go with package main, empty func main(), and callable exported func BuildAvailabilityRouter(pool *pgxpool.Pool) http.Handler returning only http.NewServeMux(). This is a real unwired empty router; no registration, constructors, queries or behavior. No tests yet. Return completed phase report with empty RED claim. Do not change upstream code."
	case "red":
		return common + "Semantic RED test-only: preserve production bytes. In the declared main_test.go create TestCompositionAvailabilityRoute with existing real PostgreSQL fixture/migration/deterministic available rows. Call the real BuildAvailabilityRouter(pool), httptest requests GET /availability?resource_id=...&start=...&end=... using URL-encoded +00:00 and Z cases; expect success and correct nonempty intervals. Use existing postgresfixture.Open fixture and pool.Exec deterministic seed; exercise BuildAvailabilityRouter with httptest requests. Current empty mux must yield actual HTTP404, assertion containing semantic RED:. This proves an unreachable route before wiring, not fake failure. Also retain non-zero offset rejection and half-open interval result checks. No setup/production edits. Return expected_failure precise test name and 404 assertion, semantic=false and observed_failure empty; orchestrator executes and persists RED."
	default:
		return common + "Implement wiring only after persisted semantic RED. Keep RED tests byte-for-byte. BuildAvailabilityRouter(pool) must assemble real Persistence → real Usecase → real HTTP through exact provided constructors, register /availability on http.NewServeMux, return the mux. main may read DATABASE_URL/HTTP_ADDR, initialize pgxpool, close it, and run http.ListenAndServe. No fake handlers/businesslogic/SQL. Report all production paths you changed; orchestrator reruns GREEN and commits. Verified RED:\n" + compactJSON(red)
	}
}

// ValidateCompositionWiring excludes computation and business/query code from
// the composition root, while permitting constructors, router registration and
// ordinary process startup. Tests remain independently executed behavioral proof.
func ValidateCompositionWiring(root string, wp domain.CompositionWorkPackage, paths []string, setup bool) error {
	if !compositionScopeAllows(wp, paths) {
		return domain.ErrWriteScope
	}
	public := map[string]bool{}
	constructorPackages := map[string]string{}
	for _, api := range wp.APIs {
		if strings.HasPrefix(api.Symbol, "New") {
			public[api.Symbol] = true
			constructorPackages[api.Symbol] = filepath.ToSlash(filepath.Dir(api.Path))
		}
	}
	constructorCalls := map[string]bool{}
	registered := false
	production := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			test, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, path), nil, 0)
			if err != nil {
				return err
			}
			for _, imp := range test.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				if value == "reflect" || value == "unsafe" {
					return fmt.Errorf("composition tests may not use reflect/unsafe")
				}
			}
			for _, decl := range test.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name.Name == "BuildAvailabilityRouter" || strings.HasPrefix(fn.Name.Name, "NewAvailability")) {
					return fmt.Errorf("composition tests may not replace production constructors/router")
				}
			}
			if setup {
				return fmt.Errorf("setup may not write tests: %w", domain.ErrWriteScope)
			}
			if err := validateCompositionBehaviorTest(test); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(path, ".go") {
			return domain.ErrWriteScope
		}
		production++
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			return err
		}
		if file.Name.Name != "main" {
			return fmt.Errorf("composition source must be package main")
		}
		aliases := map[string]string{}
		for _, item := range file.Imports {
			importPath, err := strconv.Unquote(item.Path.Value)
			if err != nil {
				return err
			}
			alias := filepath.Base(importPath)
			if item.Name != nil {
				alias = item.Name.Name
			}
			if alias == "_" || alias == "." || importPath == "unsafe" || importPath == "reflect" || strings.Contains(importPath, "database/sql") {
				return fmt.Errorf("forbidden composition import")
			}
			aliases[alias] = importPath
		}
		allowed := map[string]bool{"http.NewServeMux": true}
		if !setup {
			for _, call := range []string{"context.Background", "pgxpool.New", "os.Getenv", "http.ListenAndServe", "log.Fatal", "log.Printf"} {
				allowed[call] = true
			}
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok {
				if gen.Tok != token.IMPORT {
					return fmt.Errorf("composition cannot define global business state or types")
				}
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				return fmt.Errorf("invalid composition declaration")
			}
			if fn.Name.Name == "BuildAvailabilityRouter" && setup {
				if err := validateEmptyCompositionRouter(fn); err != nil {
					return err
				}
			}
			if fn.Name.Name != "main" && fn.Name.Name != "BuildAvailabilityRouter" {
				return fmt.Errorf("composition helper outside wiring API")
			}
			if setup && fn.Name.Name == "main" && len(fn.Body.List) > 0 {
				return fmt.Errorf("setup main must remain empty")
			}
			boundedFallbacks := compositionStartupFallbacks(fn)
			var violation error
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if violation != nil {
					return false
				}
				switch value := node.(type) {
				case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.GoStmt, *ast.FuncLit, *ast.IndexExpr, *ast.SliceExpr:
					violation = fmt.Errorf("composition includes business/control computation")
				case *ast.IfStmt:
					if !setup && boundedFallbacks[value] {
						break
					}
					condition, ok := value.Cond.(*ast.BinaryExpr)
					if setup || !ok || condition.Op != token.NEQ {
						violation = fmt.Errorf("composition only allows startup error checks")
						break
					}
					left, lok := condition.X.(*ast.Ident)
					right, rok := condition.Y.(*ast.Ident)
					if !lok || !rok || left.Name != "err" || right.Name != "nil" {
						violation = fmt.Errorf("composition conditional is not startup error check")
					}
				case *ast.BinaryExpr:
					if value.Op != token.NEQ && !isCompositionFallbackCondition(value, boundedFallbacks) {
						violation = fmt.Errorf("composition arithmetic/comparison forbidden")
					}
				case *ast.CallExpr:
					if local, ok := value.Fun.(*ast.Ident); ok && !setup && fn.Name.Name == "main" && local.Name == "BuildAvailabilityRouter" && len(value.Args) == 1 {
						break
					}
					selector, ok := value.Fun.(*ast.SelectorExpr)
					if !ok {
						violation = fmt.Errorf("composition calls must be provided constructors or startup APIs")
						break
					}
					owner, ok := selector.X.(*ast.Ident)
					if !ok {
						violation = fmt.Errorf("unsupported chained composition call")
						break
					}
					symbol := selector.Sel.Name
					if !setup && public[symbol] && strings.HasSuffix(aliases[owner.Name], "/"+constructorPackages[symbol]) {
						constructorCalls[symbol] = true
						break
					}
					if allowed[owner.Name+"."+symbol] {
						correctImport := map[string]string{"http": "net/http", "context": "context", "os": "os", "log": "log", "pgxpool": "github.com/jackc/pgx/v5/pgxpool"}
						if aliases[owner.Name] != correctImport[owner.Name] {
							violation = fmt.Errorf("startup API imported from wrong package")
							break
						}
						break
					}
					if !setup && (symbol == "Handle" || symbol == "Close") {
						if symbol == "Handle" {
							if len(value.Args) != 2 {
								violation = fmt.Errorf("route registration requires pattern and real handler")
								break
							}
							literal, ok := value.Args[0].(*ast.BasicLit)
							if !ok || literal.Kind != token.STRING {
								violation = fmt.Errorf("dynamic route registration forbidden")
								break
							}
							route, err := strconv.Unquote(literal.Value)
							if err != nil || (route != "/availability" && route != "GET /availability") {
								violation = fmt.Errorf("route outside approved availability boundary")
								break
							}
							registered = true
						}
						break
					}
					violation = fmt.Errorf("unapproved composition call %s.%s", owner.Name, symbol)
				case *ast.CompositeLit:
					violation = fmt.Errorf("composition cannot synthesize business objects")
				}
				return violation == nil
			})
			if violation != nil {
				return violation
			}
		}
	}
	if production == 0 {
		return fmt.Errorf("composition must contain owned production root")
	}
	if !setup {
		if !registered {
			return fmt.Errorf("composition never registers a real handler")
		}
		for symbol := range public {
			if !constructorCalls[symbol] {
				return fmt.Errorf("provided constructor %s was not used", symbol)
			}
		}
	}
	return nil
}

// validateCompositionBehaviorTest binds the candidate regression to the real
// router and an actual PostgreSQL fixture/seed, rather than mocked layers.
func validateCompositionBehaviorTest(file *ast.File) error {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "reflect" || path == "unsafe" {
			return fmt.Errorf("composition regression cannot bypass public APIs with reflect/unsafe")
		}
		alias := filepath.Base(path)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = path
	}
	router, request, serve, fixture, seed, assertion := false, false, false, false, false, false
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			return fmt.Errorf("composition integration test may not introduce fake layer types")
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "BuildAvailabilityRouter" {
			router = true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		owner, _ := selector.X.(*ast.Ident)
		symbol := selector.Sel.Name
		if owner != nil {
			imported := aliases[owner.Name]
			if imported == "net/http/httptest" && (symbol == "NewRequest" || symbol == "NewServer") {
				request = true
			}
			if imported == "net/http" && symbol == "NewRequest" {
				request = true
			}
			if imported == "net/http/httptest" && symbol == "NewServer" {
				serve = true
			}
			if strings.HasSuffix(imported, "/internal/testsupport/postgresfixture") && symbol == "Open" {
				fixture = true
			}
		}
		if symbol == "ServeHTTP" {
			serve = true
		}
		if symbol == "Exec" {
			seed = true
		}
		if symbol == "Fatalf" || symbol == "Errorf" {
			assertion = true
		}
		return true
	})
	if !router || !request || !serve || !fixture || !seed || !assertion {
		return fmt.Errorf("composition regression must use real router, HTTP request, PostgreSQL fixture and seed, with behavioral assertions")
	}
	return nil
}

func validateEmptyCompositionRouter(fn *ast.FuncDecl) error {
	fail := func() error { return fmt.Errorf("composition RED_SETUP must be exactly a callable empty mux") }
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 || len(fn.Body.List) != 1 {
		return fail()
	}
	pointer, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return fail()
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return fail()
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != "pgxpool" || selector.Sel.Name != "Pool" {
		return fail()
	}
	result, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return fail()
	}
	owner, ok = result.X.(*ast.Ident)
	if !ok || owner.Name != "http" || result.Sel.Name != "Handler" {
		return fail()
	}
	returned, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return fail()
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return fail()
	}
	selector, ok = call.Fun.(*ast.SelectorExpr)
	if !ok {
		return fail()
	}
	owner, ok = selector.X.(*ast.Ident)
	if !ok || owner.Name != "http" || selector.Sel.Name != "NewServeMux" {
		return fail()
	}
	return nil
}

// The agent timestamp is a wire string, not evidence. Only the orchestrator may
// assign authoritative RecordedAt after executing the behavioral verification.
func decodeCompositionPhaseResult(encoded []byte) (domain.WorkerPhaseResult, error) {
	type redWire struct {
		Command              string   `json:"command"`
		ExpectedFailure      string   `json:"expected_failure"`
		ObservedFailure      string   `json:"observed_failure"`
		Semantic             bool     `json:"semantic"`
		TestPaths            []string `json:"test_paths"`
		RecordedAt           string   `json:"recorded_at"`
		PreImplementationSHA string   `json:"pre_implementation_sha,omitempty"`
	}
	var wire struct {
		Status                  string   `json:"status"`
		ShardID                 string   `json:"shard_id"`
		BaselineRevision        string   `json:"baseline_revision"`
		WorkerThread            string   `json:"worker_thread"`
		Red                     redWire  `json:"red"`
		ChangedFiles            []string `json:"changed_files"`
		Blockers                []string `json:"blockers"`
		ContractChangeRequested bool     `json:"contract_change_requested"`
	}
	if err := strictJSON(encoded, &wire); err != nil {
		return domain.WorkerPhaseResult{}, err
	}
	return domain.WorkerPhaseResult{Status: wire.Status, ShardID: wire.ShardID, BaselineRevision: wire.BaselineRevision, WorkerThread: wire.WorkerThread, ChangedFiles: wire.ChangedFiles, Blockers: wire.Blockers, ContractChangeRequested: wire.ContractChangeRequested, Red: domain.ShardREDEvidence{Command: wire.Red.Command, ExpectedFailure: wire.Red.ExpectedFailure, ObservedFailure: wire.Red.ObservedFailure, Semantic: wire.Red.Semantic, TestPaths: wire.Red.TestPaths, PreImplementationSHA: wire.Red.PreImplementationSHA}}, nil
}

func compositionFormattingRecoveryAllowed(execution domain.ShardFanoutExecution) bool {
	attempt := execution.CompositionAttempt
	if execution.State != "COMPOSITION_BLOCKED" || execution.Composition != "COMPOSITION_BLOCKED" || attempt == nil || attempt.Status != domain.ShardAttemptBlocked || attempt.Phase != domain.ShardPhaseREDSetup || attempt.FinishedAt == nil || attempt.Red != nil || attempt.Green != nil || attempt.Implementation != nil || attempt.REDSetup != nil || attempt.CommitSHA != "" || len(attempt.RecoveryHistory) != 0 || attempt.ID == "" || attempt.WorkerThreadID == "" || attempt.Model == "" || attempt.ReasoningEffort == "" || attempt.StartedAt.IsZero() || len(attempt.ChangedFiles) != 0 || len(attempt.Verification) != 0 || len(attempt.Blockers) != 1 || attempt.Blockers[0] != "TEST_BOUNDARY_MISSING" || len(execution.BarrierReasons) != 1 {
		return false
	}
	setupPhaseFound := false
	for _, event := range attempt.PhaseHistory {
		if event.Phase != domain.ShardPhasePrepared && event.Phase != domain.ShardPhaseREDSetup {
			return false
		}
		if event.Phase == domain.ShardPhaseREDSetup {
			setupPhaseFound = true
		}
	}
	if !setupPhaseFound {
		return false
	}
	reason := execution.BarrierReasons[0]
	return strings.Contains(reason, "strict JSON decode:") && strings.Contains(reason, `parsing time ""`)
}

func validateCompositionRecoveryScaffold(root string, wp domain.CompositionWorkPackage, changedFiles []string) error {
	production := []string{}
	for _, path := range wp.WriteScope.Allow {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			production = append(production, path)
		}
	}
	if len(production) != 1 || !samePaths(production, changedFiles) {
		return fmt.Errorf("formatting recovery requires only the exact approved production setup path: %w", domain.ErrWriteScope)
	}
	return ValidateCompositionWiring(root, wp, changedFiles, true)
}
