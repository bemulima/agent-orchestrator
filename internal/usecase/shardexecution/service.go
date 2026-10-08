package shardexecution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/agentpolicy"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

type ProjectReader interface {
	Get(context.Context, string) (domain.Project, error)
}

type RepositoryInspector interface {
	InspectWithAllowedChanges(context.Context, string, []string) (domain.RepositorySource, error)
}

type ManagedWorktrees interface {
	repository.TaskWorktree
	repository.TaskWorkspaceSnapshotter
	PrepareAtCommit(context.Context, domain.Project, domain.Task, string) (domain.TaskWorkspace, error)
}

type CommitVerifier interface {
	VerifyCommit(context.Context, domain.Project, domain.TaskWorkspace, string, string, []string) error
}

type PlanExecutionStore interface {
	repository.ArchitecturalShardRepository
	repository.ContractBaselineRepository
	repository.FanoutReadinessRepository
	repository.ShardExecutionRepository
}

type Service struct {
	Plans        repository.PlanningRepository
	Projects     ProjectReader
	Execution    PlanExecutionStore
	Repositories RepositoryInspector
	Worktrees    ManagedWorktrees
	Runner       repository.AgentRunner
	Router       agentpolicy.Router
	Catalog      agentcontrol.Catalog
	Now          func() time.Time
}

type PreparedShard struct {
	Remediation *ShardRemediation    `json:"remediation,omitempty"`
	WorkPackage domain.WorkPackage   `json:"work_package"`
	Workspace   domain.TaskWorkspace `json:"workspace"`
}

type PreparedFanout struct {
	CompositionHistory  []domain.CompositionAttempt `json:"composition_history,omitempty"`
	CompositionShard    *domain.ArchitecturalShard  `json:"composition_shard,omitempty"`
	Remediation         *RemediationRequest         `json:"remediation,omitempty"`
	PlanID              string                      `json:"plan_id"`
	BaselineID          string                      `json:"contract_baseline_id"`
	BaselineCommit      string                      `json:"baseline_commit"`
	ParallelismLimit    int                         `json:"parallelism_limit"`
	CompositionRequired bool                        `json:"composition_required"`
	Workers             []PreparedShard             `json:"workers"`
}

type WorkerClaim struct {
	Status                  string   `json:"status"`
	ShardID                 string   `json:"shard_id"`
	BaselineRevision        string   `json:"baseline_revision"`
	WorkerThread            string   `json:"worker_thread"`
	ChangedFiles            []string `json:"changed_files"`
	ImplementedContracts    []string `json:"implemented_contracts"`
	Blockers                []string `json:"blockers"`
	ContractChangeRequested bool     `json:"contract_change_requested"`
}

type ServiceError struct {
	Status  domain.ShardAttemptStatus
	Blocker string
	Err     error
}

func (e ServiceError) Error() string { return e.Err.Error() }
func (e ServiceError) Unwrap() error { return e.Err }

func (s Service) Prepare(ctx context.Context, planID string, parallelismLimit int) (PreparedFanout, error) {
	return s.prepare(ctx, planID, parallelismLimit, nil)
}

func (s Service) PrepareRemediation(ctx context.Context, planID string, parallelismLimit int, request RemediationRequest) (PreparedFanout, error) {
	return s.prepare(ctx, planID, parallelismLimit, &request)
}

func (s Service) prepare(ctx context.Context, planID string, parallelismLimit int, remediation *RemediationRequest) (PreparedFanout, error) {
	if s.Plans == nil || s.Projects == nil || s.Execution == nil || s.Repositories == nil || s.Worktrees == nil ||
		parallelismLimit < 2 {
		return PreparedFanout{}, fmt.Errorf("shard fan-out dependencies or parallelism limit are incomplete: %w", domain.ErrInvalidStatus)
	}
	inputs, err := s.loadApprovedInputs(ctx, planID)
	if err != nil {
		return PreparedFanout{}, err
	}
	var previous domain.ShardFanoutExecution
	if remediation != nil {
		previous, err = s.Execution.GetShardFanout(ctx, planID)
		if err != nil {
			return PreparedFanout{}, err
		}
		if err := validateRemediation(*remediation, inputs, previous); err != nil {
			return PreparedFanout{}, err
		}
	}
	workers := make([]PreparedShard, 0, len(inputs.shards))
	compositionRequired := false
	var compositionShard *domain.ArchitecturalShard
	for _, shard := range inputs.shards {
		if shard.Status == domain.ShardStatusComposition || shard.Phase == "after_workers" {
			if compositionShard != nil || shard.RouteID != "backend.composition" || shard.Phase != "after_workers" || shard.Parallel {
				return PreparedFanout{}, fmt.Errorf("composition shard is not a unique serialized approved route: %w", domain.ErrInvalidStatus)
			}
			selected := shard
			compositionShard = &selected
			compositionRequired = true
			continue
		}
		if shard.Status != domain.ShardStatusPlanned || !shard.Parallel || len(shard.DependsOn) > 0 {
			return PreparedFanout{}, fmt.Errorf("shard %s is not an independent ready worker: %w", shard.ID, domain.ErrInvalidStatus)
		}
		task := inputs.tasks[shard.TaskID]
		baseline := inputs.baselines[shard.TaskID]
		packageValue, buildErr := planning.BuildWorkPackage(shard, task, baseline)
		if buildErr != nil {
			return PreparedFanout{}, buildErr
		}
		project, ok := inputs.projects[shard.RepositoryProjectID]
		if !ok || project.LocalPath == nil {
			return PreparedFanout{}, fmt.Errorf("shard project has no connected checkout: %w", domain.ErrNotFound)
		}
		workers = append(workers, PreparedShard{WorkPackage: packageValue})
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].WorkPackage.Route != workers[j].WorkPackage.Route {
			return workers[i].WorkPackage.Route < workers[j].WorkPackage.Route
		}
		return workers[i].WorkPackage.ShardID < workers[j].WorkPackage.ShardID
	})
	if len(workers) < 2 {
		return PreparedFanout{}, fmt.Errorf("fan-out needs at least two independent worker shards: %w", domain.ErrInvalidStatus)
	}
	workPackages := make([]domain.WorkPackage, 0, len(workers))
	for _, worker := range workers {
		workPackages = append(workPackages, worker.WorkPackage)
	}
	if err := planning.ValidateDisjointWorkPackages(workPackages); err != nil {
		return PreparedFanout{}, err
	}
	for index := range workers {
		packageValue := workers[index].WorkPackage
		project := inputs.projects[packageValue.ProjectID]
		baseline := inputs.baselines[packageValue.TaskID]
		if remediation != nil {
			if _, affected := remediation.Findings[packageValue.Route]; !affected {
				for _, prior := range previous.Attempts {
					if prior.ShardID == packageValue.ShardID {
						workers[index].Workspace = prior.Workspace
					}
				}
				if workers[index].Workspace.BaseCommit != baseline.ContractBaselineCommit {
					return PreparedFanout{}, fmt.Errorf("unaffected shard lacks reusable baseline workspace: %w", domain.ErrConflict)
				}
				continue
			}
		}
		workspaceID, idErr := uuid.NewRandom()
		if idErr != nil {
			return PreparedFanout{}, idErr
		}
		workspaceTask := domain.Task{ID: workspaceID.String(), ProjectID: project.ID, Title: "Shard " + packageValue.Route}
		workspace, prepErr := s.Worktrees.PrepareAtCommit(ctx, project, workspaceTask, baseline.ContractBaselineCommit)
		if prepErr != nil {
			return PreparedFanout{}, prepErr
		}
		state, inspectErr := s.Worktrees.Inspect(ctx, project, workspace)
		if inspectErr != nil {
			return PreparedFanout{}, inspectErr
		}
		if state.HeadCommit != baseline.ContractBaselineCommit || len(state.ChangedFiles) != 0 {
			return PreparedFanout{}, fmt.Errorf("shard workspace is not clean at its frozen base: %w", domain.ErrConflict)
		}
		workers[index].Workspace = workspace
		if remediation != nil {
			if findings, affected := remediation.Findings[packageValue.Route]; affected {
				for _, prior := range previous.Attempts {
					if prior.ShardID == packageValue.ShardID {
						workers[index].Remediation = &ShardRemediation{RequestID: remediation.ID, PriorCommit: prior.CommitSHA, Findings: append([]string(nil), findings...)}
					}
				}
			}
		}
	}
	sourcePreflights, err := s.persistedSourcePreflightHistory(ctx, inputs.baseline, inputs.sourcePreflights)
	if err != nil {
		return PreparedFanout{}, err
	}
	compositionHistory, err := retainCompositionHistory(previous)
	if err != nil {
		return PreparedFanout{}, err
	}
	execution := domain.ShardFanoutExecution{
		CompositionHistory: compositionHistory,
		PlanID:             inputs.plan.ID, ContractBaselineID: inputs.baseline.ID,
		BaselineCommit: inputs.baseline.ContractBaselineCommit, State: "FANOUT_RUNNING",
		SourcePreflights: sourcePreflights,
		Barrier:          domain.ShardBarrierPending, Composition: "PENDING",
		CreatedAt: s.now(), UpdatedAt: s.now(), Attempts: []domain.ShardAttempt{},
	}
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return PreparedFanout{}, err
	}
	return PreparedFanout{
		PlanID: inputs.plan.ID, BaselineID: inputs.baseline.ID,
		BaselineCommit: inputs.baseline.ContractBaselineCommit, ParallelismLimit: parallelismLimit,
		CompositionRequired: compositionRequired, CompositionShard: compositionShard, Workers: workers, Remediation: remediation, CompositionHistory: compositionHistory,
	}, nil
}

func (s Service) Execute(ctx context.Context, prepared PreparedShard) (domain.ShardAttempt, error) {
	packageValue := prepared.WorkPackage
	history, err := s.Execution.ListShardAttempts(ctx, packageValue.PlanID)
	if err != nil {
		return domain.ShardAttempt{}, err
	}
	if prepared.Remediation == nil {
		if reusable, reuseErr := s.reusableVerifiedAttempt(ctx, prepared, history); reuseErr != nil {
			return domain.ShardAttempt{}, reuseErr
		} else if reusable != nil {
			return *reusable, nil
		}
	}
	recoveryCandidate, err := httpFormatRecoveryCandidate(prepared, history)
	if err != nil {
		return domain.ShardAttempt{}, err
	}
	task := domain.Task{ID: packageValue.TaskID, PlanID: packageValue.PlanID, ProjectID: packageValue.ProjectID, ModelProfile: config.ModelProfileFast, RiskLevel: domain.RiskLevelMedium}
	var attempt domain.ShardAttempt
	if recoveryCandidate != nil {
		attempt = *recoveryCandidate
	} else {
		attemptNumber, err := nextPreparedAttemptNumber(prepared, history)
		if err != nil {
			return domain.ShardAttempt{}, err
		}
		attemptID, err := uuid.NewRandom()
		if err != nil {
			return domain.ShardAttempt{}, fmt.Errorf("create shard attempt ID: %w", err)
		}
		now := s.now()
		decision := s.Router.Coder(task)
		attempt = domain.ShardAttempt{Phase: domain.ShardPhasePrepared, PhaseHistory: []domain.ShardPhaseEvent{{Phase: domain.ShardPhasePrepared, RecordedAt: now}}, ID: attemptID.String(), PlanID: packageValue.PlanID, TaskID: packageValue.TaskID, ShardID: packageValue.ShardID, AttemptNumber: attemptNumber, WorkPackage: packageValue, BaselineCommit: packageValue.ExecutionBase.Revision, Workspace: prepared.Workspace, Model: decision.Model, ReasoningEffort: decision.Reasoning, Status: domain.ShardAttemptRunning, StartedAt: now, WorkerStartedAt: now, CreatedAt: now, UpdatedAt: now, Blockers: []string{}, ChangedFiles: []string{}}
		if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
			return domain.ShardAttempt{}, err
		}
	}
	if s.Runner == nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("Codex AgentRunner is unavailable: %w", domain.ErrInvalidStatus))
	}
	inputs, err := s.loadApprovedInputs(ctx, packageValue.PlanID)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
	}
	canonical, ok := inputs.shardByID[packageValue.ShardID]
	if !ok {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", fmt.Errorf("approved shard is no longer present: %w", domain.ErrConflict))
	}
	canonicalPackage, err := planning.BuildWorkPackage(canonical, inputs.tasks[canonical.TaskID], inputs.baselines[canonical.TaskID])
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
	}
	if !sameJSON(canonicalPackage, packageValue) || prepared.Workspace.BaseCommit != packageValue.ExecutionBase.Revision {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", fmt.Errorf("WorkPackage or workspace no longer matches approved baseline: %w", domain.ErrConflict))
	}
	project := inputs.projects[packageValue.ProjectID]
	baseline := inputs.baselines[packageValue.TaskID]
	if err := s.verifyWorkspaceBase(ctx, project, prepared.Workspace, baseline); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
	}

	recoveryPaths := []string{}
	if recoveryCandidate != nil {
		recoveryPaths, err = s.validateHTTPFormatRecoveryWorkspace(ctx, prepared, attempt, project)
		if err != nil {
			return domain.ShardAttempt{}, err
		}
		hashes := map[string]string{}
		for _, path := range recoveryPaths {
			source, readErr := s.Worktrees.ReadArtifact(ctx, prepared.Workspace, path, 1<<20)
			if readErr != nil {
				return domain.ShardAttempt{}, readErr
			}
			hashes[path] = contentHash(source)
		}
		attempt.FormatRecoveryHistory = append(attempt.FormatRecoveryHistory, domain.ShardAttemptFormatRecovery{RecordedAt: s.now(), PriorStatus: attempt.Status, PriorBlockers: append([]string(nil), attempt.Blockers...), PriorFinishedAt: attempt.FinishedAt, CandidateTestHashes: hashes, Reason: "agent RED timestamp formatting only; adopt actual candidate, then independently verify critical delegation RED before implementation"})
		attempt.Status = domain.ShardAttemptRunning
		attempt.Blockers = nil
		attempt.FinishedAt = nil
		attempt.UpdatedAt = s.now()
		if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
			return domain.ShardAttempt{}, err
		}
	} else {
		preimage, err := s.materializeRemediationPreimage(ctx, prepared, project, history)
		if err != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
		}
		if preimage != nil {
			attempt.RemediationPreimage = preimage
			if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
				return domain.ShardAttempt{}, err
			}
		}

	}

	if packageValue.Route == "backend.infrastructure.persistence" && !persistenceBoundaryExists(prepared.Workspace.Path, packageValue) {
		phaseResult, response, runErr := s.runPhase(ctx, attempt, "boundary assessment", readOnlyBoundaryPrompt(packageValue), phaseSchema(), "")
		if response.ThreadID != "" {
			attempt.WorkerThreadID = response.ThreadID
		}
		state, inspectErr := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
		if inspectErr != nil || len(state.ChangedFiles) != 0 {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("persistence boundary assessment modified the workspace"))
		}
		attempt.Implementation = &domain.WorkerResult{
			Status: "blocked", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision,
			WorkerThread: attempt.WorkerThreadID, ChangedFiles: []string{}, Blockers: []string{"TEST_BOUNDARY_MISSING"},
			ContractChangeRequested: false,
		}
		if runErr != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "TEST_BOUNDARY_MISSING", runErr)
		}
		if phaseResult.Status == "completed" && len(phaseResult.ChangedFiles) > 0 {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("read-only test-boundary assessment claimed file changes: %w", domain.ErrWriteScope))
		}
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "TEST_BOUNDARY_MISSING",
			fmt.Errorf("no real PostgreSQL integration boundary is available inside the approved shard scope: %w", domain.ErrInvalidStatus))
	}

	if packageValue.Route == "backend.infrastructure.persistence" && !persistenceConcreteSeamExists(prepared.Workspace.Path, packageValue) || constructorSetupNeeded(prepared.Workspace.Path, packageValue) {
		if err := s.persistPhase(ctx, &attempt, domain.ShardPhaseREDSetup); err != nil {
			return domain.ShardAttempt{}, err
		}
		setupPreimage := map[string]string{}
		if prepared.Remediation != nil && packageValue.Route == "backend.transport.http" {
			state, inspectErr := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
			if inspectErr != nil {
				return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", inspectErr)
			}
			for _, path := range state.ChangedFiles {
				if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
					data, readErr := os.ReadFile(filepath.Join(prepared.Workspace.Path, filepath.FromSlash(path)))
					if readErr != nil {
						return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", readErr)
					}
					setupPreimage[path] = string(data)
				}
			}
		}
		report, response, runErr := s.runPhase(ctx, attempt, "RED_SETUP", redSetupPrompt(packageValue)+remediationPrompt(prepared.Remediation, packageValue.Route), phaseSchema(), attempt.WorkerThreadID)
		if response.ThreadID != "" {
			attempt.WorkerThreadID = response.ThreadID
		}
		if runErr != nil {
			return s.finishFailure(ctx, attempt, phaseBlocker(runErr), "", runErr)
		}
		if report.Status != "completed" || report.ContractChangeRequested {
			status, blocker := workerBlocker(report.Blockers, report.ContractChangeRequested)
			return s.finishFailure(ctx, attempt, status, blocker, fmt.Errorf("RED_SETUP worker blocked: %w", domain.ErrValidation))
		}
		state, err := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
		if err != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
		}
		if !samePaths(state.ChangedFiles, cleanSorted(report.ChangedFiles)) || len(state.ChangedFiles) > packageValue.WriteScope.MaxFiles {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", domain.ErrWriteScope)
		}
		if err := ValidateREDSetup(prepared.Workspace.Path, packageValue, state.ChangedFiles, setupPreimage); err != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", err)
		}
		if err := s.verifyWorkspaceBase(ctx, project, prepared.Workspace, baseline); err != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
		}
		snapshot, err := s.Worktrees.Snapshot(ctx, project, prepared.Workspace)
		if err != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
		}
		files := map[string]string{}
		sources := map[string]string{}
		totalBytes := 0
		for _, path := range state.ChangedFiles {
			source, readErr := os.ReadFile(filepath.Join(prepared.Workspace.Path, filepath.FromSlash(path)))
			totalBytes += len(source)
			if readErr != nil || totalBytes > 1<<20 || !strings.HasSuffix(snapshot.Files[path], ":"+contentHash(source)) {
				return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("RED_SETUP source evidence missing, oversized or changed: %w", domain.ErrValidation))
			}
			files[path] = snapshot.Files[path]
			sources[path] = string(source)
		}
		attempt.REDSetup = &domain.ShardREDSetupEvidence{ChangedFiles: cleanSorted(state.ChangedFiles), Files: files, Sources: sources, SnapshotSHA: snapshotDigest(snapshot), MechanicalReview: "PASS", RecordedAt: s.now()}
		if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
			return domain.ShardAttempt{}, err
		}
	}

	beforeRed, err := s.Worktrees.Snapshot(ctx, project, prepared.Workspace)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	var redReport domain.WorkerPhaseResult
	var response domain.AgentRunResponse
	if recoveryCandidate != nil {
		redReport = domain.WorkerPhaseResult{Status: "completed", ShardID: packageValue.ShardID, BaselineRevision: packageValue.ExecutionBase.Revision, ChangedFiles: append([]string(nil), recoveryPaths...), Red: domain.ShardREDEvidence{ExpectedFailure: "semantic RED: non-zero offset is currently delegated", TestPaths: append([]string(nil), recoveryPaths...)}}
	} else {
		redReport, response, err = s.runPhase(ctx, attempt, "semantic RED test-only", redPrompt(packageValue)+remediationPrompt(prepared.Remediation, packageValue.Route), phaseSchema(), attempt.WorkerThreadID)
		if response.ThreadID != "" {
			attempt.WorkerThreadID = response.ThreadID
		}
		if err != nil {
			return s.finishFailure(ctx, attempt, phaseBlocker(err), "", err)
		}
	}
	if redReport.Status != "completed" || redReport.ContractChangeRequested {
		status, blocker := workerBlocker(redReport.Blockers, redReport.ContractChangeRequested)
		return s.finishFailure(ctx, attempt, status, blocker, fmt.Errorf("worker did not produce an allowed test-first RED report: %w", domain.ErrValidation))
	}
	redState, err := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	claimedRedFiles := cleanSorted(redReport.ChangedFiles)
	afterRed, snapshotErr := s.Worktrees.Snapshot(ctx, project, prepared.Workspace)
	if snapshotErr != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", snapshotErr)
	}
	redDelta := snapshotChangedFiles(beforeRed, afterRed)
	if recoveryCandidate != nil {
		redDelta = append([]string(nil), recoveryPaths...)
	}
	if !scopeAllowsFiles(packageValue, redState.ChangedFiles) || len(redState.ChangedFiles) > packageValue.WriteScope.MaxFiles || !samePaths(redDelta, claimedRedFiles) || len(claimedRedFiles) == 0 ||
		!pathsWithin(claimedRedFiles, packageValue.Verification.TestPaths) {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("RED phase changed paths outside declared test ownership: %w", domain.ErrWriteScope))
	}
	if err := s.verifyWorkspaceBase(ctx, project, prepared.Workspace, baseline); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
	}
	redCommand := "go test ./..."
	if recoveryCandidate != nil {
		redCommand = "go test ./... -count=1"
	}
	redCheck, err := s.Worktrees.RunCheck(ctx, prepared.Workspace, redCommand)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	redValidation := redCheck
	redValidation.Command = "go test ./..."
	if err := ValidateSemanticRED(redReport.Red, claimedRedFiles, redValidation); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("RED was absent, non-semantic, or caused only by a build/environment error: %w", domain.ErrValidation))
	}
	if err := validateHTTPRemediationDelegationRED(prepared, redCheck); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "TEST_BOUNDARY_MISSING", err)
	}
	red := redReport.Red
	if !samePaths(red.TestPaths, claimedRedFiles) {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("RED evidence test paths differ from changed tests: %w", domain.ErrValidation))
	}
	red.Command = redCommand
	red.ObservedFailure = bounded(redCheck.Output, 4000)
	red.Semantic = true
	red.TestPaths = append([]string(nil), claimedRedFiles...)
	red.RecordedAt = s.now()
	red.PreImplementationSHA = snapshotDigest(beforeRed)
	attempt.Red = &red
	setShardPhase(&attempt, domain.ShardPhaseREDVerified, s.now())
	attempt.UpdatedAt = s.now()
	if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
		return domain.ShardAttempt{}, err
	}
	redSnapshot, err := s.Worktrees.Snapshot(ctx, project, prepared.Workspace)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}

	if err := s.persistPhase(ctx, &attempt, domain.ShardPhaseImplementing); err != nil {
		return domain.ShardAttempt{}, err
	}
	claim, response, err := s.runImplementation(ctx, attempt, packageValue, red, prepared.Remediation)
	if response.ThreadID != "" {
		attempt.WorkerThreadID = response.ThreadID
	}
	if err != nil {
		return s.finishFailure(ctx, attempt, phaseBlocker(err), "", err)
	}
	if claim.Status != "completed" || claim.ContractChangeRequested || claim.ShardID != packageValue.ShardID ||
		claim.BaselineRevision != packageValue.ExecutionBase.Revision || claim.WorkerThread != attempt.WorkerThreadID {
		status, blocker := workerBlocker(claim.Blockers, claim.ContractChangeRequested)
		return s.finishFailure(ctx, attempt, status, blocker, fmt.Errorf("implementation result identity or status is not accepted: %w", domain.ErrConflict))
	}
	state, err := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	claimedFiles := cleanSorted(claim.ChangedFiles)
	if !implementationClaimMatches(claimedFiles, state.ChangedFiles, red.TestPaths) || !scopeAllowsFiles(packageValue, state.ChangedFiles) ||
		len(state.ChangedFiles) > packageValue.WriteScope.MaxFiles {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("actual diff does not match the approved shard write scope: %w", domain.ErrWriteScope))
	}
	if !redTestsUnchanged(redSnapshot, red.TestPaths, state.ChangedFiles, prepared.Workspace.Path) {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "OUT_OF_SCOPE_CHANGE_REQUIRED", fmt.Errorf("worker modified or removed its semantic RED test: %w", domain.ErrWriteScope))
	}
	if err := s.verifyWorkspaceBase(ctx, project, prepared.Workspace, baseline); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptBlocked, "BASELINE_STALE", err)
	}
	greenResults := []domain.WorkspaceCheckResult{}
	commands := uniqueCommands(packageValue.Verification.CandidateCommands)
	for _, command := range commands {
		result, checkErr := s.Worktrees.RunCheck(ctx, prepared.Workspace, command)
		if checkErr != nil {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", checkErr)
		}
		result.Output = bounded(result.Output, 4000)
		greenResults = append(greenResults, result)
		if result.ExitCode != 0 {
			return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("GREEN verification failed: %s", command))
		}
	}
	green := domain.ShardGreenEvidence{Commands: greenResults, Passed: len(greenResults) > 0, RecordedAt: s.now()}
	attempt.Green = &green
	attempt.ChangedFiles = append([]string(nil), state.ChangedFiles...)
	workerResult := domain.WorkerResult{
		Status: "completed", ShardID: packageValue.ShardID,
		BaselineRevision: packageValue.ExecutionBase.Revision, WorkerThread: attempt.WorkerThreadID,
		Red: red, ChangedFiles: append([]string(nil), state.ChangedFiles...), Green: green,
		ImplementedContracts: cleanSorted(claim.ImplementedContracts), Blockers: []string{},
		ContractChangeRequested: false,
	}
	attempt.Implementation = &workerResult
	verification, _ := json.Marshal(map[string]any{
		"scope_passed": true, "baseline_passed": true, "frozen_contracts_unchanged": true,
		"red_passed": true, "green_passed": green.Passed, "changed_files": state.ChangedFiles,
		"checks": greenResults,
	})
	attempt.Verification = verification
	if !green.Passed {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("worker has no green verification evidence: %w", domain.ErrValidation))
	}
	if err := s.persistPhase(ctx, &attempt, domain.ShardPhaseGREENVerified); err != nil {
		return domain.ShardAttempt{}, err
	}
	if err := ctx.Err(); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "EXECUTION_CANCELLED", err)
	}
	accepted, acceptedErr := s.Execution.ListShardAttempts(ctx, attempt.PlanID)
	if acceptedErr != nil {
		return domain.ShardAttempt{}, acceptedErr
	}
	if !domain.ShardExecutionCurrent(attempt.ID, attempt.AttemptNumber, attempt.ShardID, accepted) {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "STALE_EXECUTION", domain.ErrConflict)
	}
	commit, err := s.Worktrees.Commit(ctx, project, domain.Task{ID: packageValue.ShardID, ProjectID: project.ID, Title: "Shard " + packageValue.Route}, prepared.Workspace, state.ChangedFiles)
	if err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	verifier, ok := s.Worktrees.(CommitVerifier)
	if !ok {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", fmt.Errorf("managed worktree subsystem cannot verify commits: %w", domain.ErrInvalidStatus))
	}
	if err := verifier.VerifyCommit(ctx, project, prepared.Workspace, commit, packageValue.ExecutionBase.Revision, state.ChangedFiles); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "", err)
	}
	if err := ctx.Err(); err != nil {
		return s.finishFailure(ctx, attempt, domain.ShardAttemptFailed, "EXECUTION_CANCELLED", err)
	}
	attempt.CommitSHA = commit
	setShardPhase(&attempt, domain.ShardPhaseVerified, s.now())
	attempt.Status = domain.ShardAttemptVerified
	finished := s.now()
	attempt.FinishedAt = &finished
	attempt.UpdatedAt = finished
	if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
		return domain.ShardAttempt{}, err
	}
	return attempt, nil
}

func nextShardAttemptNumber(value domain.WorkPackage, history []domain.ShardAttempt) (int, error) {
	next := 1
	for _, previous := range history {
		if previous.ShardID != value.ShardID {
			continue
		}
		if previous.AttemptNumber >= next {
			next = previous.AttemptNumber + 1
		}
		if previous.FinishedAt == nil {
			return 0, fmt.Errorf("unfinished shard attempt prevents retry: %w", domain.ErrConflict)
		}
		if previous.BaselineCommit != value.ExecutionBase.Revision {
			continue
		}
		if !sameJSON(previous.WorkPackage, value) || previous.Status != domain.ShardAttemptFailed {
			return 0, fmt.Errorf("shard retry requires unchanged package and a completed failed attempt: %w", domain.ErrConflict)
		}
	}
	return next, nil
}

func (s Service) Barrier(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	if _, err := s.loadApprovedInputs(ctx, prepared.PlanID); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	required := make([]string, 0, len(prepared.Workers))
	for _, worker := range prepared.Workers {
		required = append(required, worker.WorkPackage.ShardID)
	}
	result := EvaluateBarrier(required, execution.Attempts, prepared.BaselineCommit)
	execution.Barrier = result.State
	execution.BarrierReasons = result.Reasons
	execution.MaxSimultaneousWorkers = MaxSimultaneousWorkers(execution.Attempts)
	if result.State == domain.ShardBarrierReady {
		if execution.BarrierReadyAt == nil {
			at := s.now()
			execution.BarrierReadyAt = &at
		}
		execution.State = string(domain.ShardBarrierReady)
		if prepared.CompositionRequired {
			execution.Composition = "COMPOSITION_REQUIRED"
		} else {
			execution.Composition = "SKIPPED_NOT_REQUIRED"
		}
	} else if result.State == domain.ShardBarrierReplanRequired {
		execution.State = "REPLAN_REQUIRED"
	} else {
		execution.State = "BARRIER_BLOCKED"
	}
	execution.UpdatedAt = s.now()
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func (s Service) ResolveCompositionBoundary(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	if execution.Barrier != domain.ShardBarrierReady || !prepared.CompositionRequired {
		return domain.ShardFanoutExecution{}, fmt.Errorf("Composition boundary requires a passed worker barrier and approved composition shard: %w", domain.ErrInvalidStatus)
	}
	execution.State = "COMPOSITION_BLOCKED"
	execution.Composition = "COMPOSITION_REQUIRED"
	execution.BarrierReasons = appendUnique(execution.BarrierReasons, "serialized Composition phase is placed after the worker barrier; this Plan requires an explicitly scoped Composition worker")
	execution.UpdatedAt = s.now()
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func (s Service) BlockFanout(ctx context.Context, planID string, reasons []string) (domain.ShardFanoutExecution, error) {
	execution, err := s.Execution.GetShardFanout(ctx, planID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution.State = "BARRIER_BLOCKED"
	execution.Barrier = domain.ShardBarrierBlocked
	for _, reason := range reasons {
		execution.BarrierReasons = appendUnique(execution.BarrierReasons, reason)
	}
	execution.UpdatedAt = s.now()
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func (s Service) runPhase(ctx context.Context, attempt domain.ShardAttempt, phase, prompt string, schema map[string]any, thread string) (domain.WorkerPhaseResult, domain.AgentRunResponse, error) {
	request := domain.AgentRunRequest{
		Attempt: attempt.AttemptNumber, Role: domain.AgentRunCoder, ThreadID: thread, WorkingDirectory: attempt.Workspace.Path,
		Model: attempt.Model, ReasoningEffort: attempt.ReasoningEffort,
		Prompt: prompt, OutputSchema: schema,
		SandboxScope: &domain.AgentSandboxScope{Profile: "layer", WritePaths: sandboxLayerPaths(attempt.WorkPackage, phase)},
		UsageContext: &domain.AgentUsageContext{
			ResourceType: "architectural_shard", ResourceID: attempt.ShardID, RouteReason: phase,
		},
	}
	var observedThread string
	response, err := s.Runner.Run(ctx, request, func(callbackCtx context.Context, threadID string) error {
		observedThread = threadID
		attempt.WorkerThreadID = threadID
		attempt.UpdatedAt = s.now()
		return s.Execution.SaveShardAttempt(callbackCtx, attempt)
	})
	if err != nil {
		return domain.WorkerPhaseResult{}, response, fmt.Errorf("run %s phase: %w", phase, err)
	}
	result, decodeErr := decodeCompositionPhaseResult(response.Result)
	if decodeErr != nil {
		err := decodeErr
		return result, response, fmt.Errorf("decode %s WorkerResult: %w", phase, err)
	}
	if err := validateRunnerThread(thread, observedThread, response.ThreadID); err != nil {
		return result, response, err
	}
	result.WorkerThread = response.ThreadID
	if result.ShardID != attempt.ShardID || result.BaselineRevision != attempt.BaselineCommit {
		return result, response, fmt.Errorf("%s result identity mismatch: %w", phase, domain.ErrConflict)
	}
	return result, response, nil
}

func (s Service) runImplementation(ctx context.Context, attempt domain.ShardAttempt, packageValue domain.WorkPackage, red domain.ShardREDEvidence, remediation ...*ShardRemediation) (WorkerClaim, domain.AgentRunResponse, error) {
	prompt := implementationPrompt(packageValue, red)
	if len(remediation) > 0 {
		prompt += remediationPrompt(remediation[0], packageValue.Route)
	}
	request := domain.AgentRunRequest{
		Attempt: attempt.AttemptNumber, Role: domain.AgentRunCoder, ThreadID: attempt.WorkerThreadID, WorkingDirectory: attempt.Workspace.Path,
		Model: attempt.Model, ReasoningEffort: attempt.ReasoningEffort, Prompt: prompt, OutputSchema: claimSchema(),
		SandboxScope: &domain.AgentSandboxScope{Profile: "layer", WritePaths: sandboxLayerPaths(packageValue, "implementation")},
		UsageContext: &domain.AgentUsageContext{
			ResourceType: "architectural_shard", ResourceID: attempt.ShardID, RouteReason: "implementation after semantic RED",
		},
	}
	var observedThread string
	response, err := s.Runner.Run(ctx, request, func(callbackCtx context.Context, threadID string) error {
		observedThread = threadID
		attempt.WorkerThreadID = threadID
		attempt.UpdatedAt = s.now()
		return s.Execution.SaveShardAttempt(callbackCtx, attempt)
	})
	if err != nil {
		return WorkerClaim{}, response, fmt.Errorf("run implementation phase: %w", err)
	}
	var claim WorkerClaim
	if err := strictJSON(response.Result, &claim); err != nil {
		return claim, response, fmt.Errorf("decode implementation WorkerResult: %w", err)
	}
	if err := validateRunnerThread(request.ThreadID, observedThread, response.ThreadID); err != nil {
		return claim, response, err
	}
	claim.WorkerThread = response.ThreadID
	return claim, response, nil
}

func validateRunnerThread(requested, observed, returned string) error {
	if returned == "" || observed != returned || requested != "" && requested != returned {
		return fmt.Errorf("runner thread callback, response or resume identity mismatch: %w", domain.ErrConflict)
	}
	return nil
}

type approvedInputs struct {
	plan             domain.Plan
	shards           []domain.ArchitecturalShard
	shardByID        map[string]domain.ArchitecturalShard
	tasks            map[string]domain.Task
	projects         map[string]domain.Project
	baselines        map[string]domain.ContractBaseline
	baseline         domain.ContractBaseline
	sourcePreflights []domain.SourcePreflightEvidence
}

func (s Service) loadApprovedInputs(ctx context.Context, planID string) (approvedInputs, error) {
	if s.Plans == nil || s.Projects == nil || s.Execution == nil || s.Repositories == nil {
		return approvedInputs{}, fmt.Errorf("shard execution dependencies are incomplete: %w", domain.ErrInvalidStatus)
	}
	bundle, err := s.Plans.GetPlan(ctx, planID)
	if err != nil {
		return approvedInputs{}, err
	}
	if bundle.Plan.Status != domain.PlanStatusApproved || bundle.Plan.ApprovedFingerprint == nil ||
		bundle.Plan.Fingerprint == "" || *bundle.Plan.ApprovedFingerprint != bundle.Plan.Fingerprint {
		return approvedInputs{}, fmt.Errorf("plan approval fingerprint is missing or stale: %w", domain.ErrApprovalNeeded)
	}
	readiness, err := s.Execution.GetFanoutReadiness(ctx, planID)
	if err != nil {
		return approvedInputs{}, err
	}
	if readiness.State != domain.FanoutReadinessReady {
		return approvedInputs{}, fmt.Errorf("plan is not READY_FOR_FANOUT: %w", domain.ErrInvalidStatus)
	}
	shards, err := s.Execution.ListArchitecturalShards(ctx, planID)
	if err != nil {
		return approvedInputs{}, err
	}
	baselines, err := s.Execution.ListContractBaselines(ctx, planID)
	if err != nil {
		return approvedInputs{}, err
	}
	var output domain.PlannerOutput
	if err := json.Unmarshal(bundle.Plan.PlannerOutput, &output); err != nil || output.ContractPlan == nil {
		return approvedInputs{}, fmt.Errorf("approved contract plan is missing: %w", domain.ErrValidation)
	}
	contractPlanFingerprint, err := contractbaseline.PlanFingerprint(*output.ContractPlan)
	if err != nil {
		return approvedInputs{}, err
	}
	result := approvedInputs{
		plan: bundle.Plan, shards: shards, shardByID: map[string]domain.ArchitecturalShard{},
		tasks: map[string]domain.Task{}, projects: map[string]domain.Project{},
		baselines: map[string]domain.ContractBaseline{},
	}
	for _, task := range bundle.Tasks {
		result.tasks[task.ID] = task
	}
	for _, baseline := range baselines {
		result.baselines[baseline.TaskID] = baseline
	}
	baselineIDs := map[string]struct{}{}
	profileFingerprints := map[string]string{}
	for _, shard := range shards {
		result.shardByID[shard.ID] = shard
		task, exists := result.tasks[shard.TaskID]
		if !exists || task.PlanID != planID || task.ProjectID != shard.RepositoryProjectID {
			return approvedInputs{}, fmt.Errorf("shard no longer matches its approved Task: %w", domain.ErrConflict)
		}
		baseline, exists := result.baselines[shard.TaskID]
		if !exists {
			return approvedInputs{}, fmt.Errorf("frozen contract baseline is missing: %w", domain.ErrInvalidStatus)
		}
		project, exists := result.projects[shard.RepositoryProjectID]
		if !exists {
			project, err = s.Projects.Get(ctx, shard.RepositoryProjectID)
			if err != nil {
				evidence := sourcePreflightFailure(shard.RepositoryProjectID, "", baseline, "PROJECT_LOOKUP_FAILED", err.Error(), s.now())
				if recordErr := s.recordSourcePreflight(ctx, baseline, &readiness, evidence); recordErr != nil {
					return approvedInputs{}, recordErr
				}
				return approvedInputs{}, err
			}
			result.projects[project.ID] = project
		}
		if project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
			evidence := sourcePreflightFailure(project.ID, "", baseline, "REPOSITORY_PATH_UNAVAILABLE", "shard project has no canonical local checkout", s.now())
			if recordErr := s.recordSourcePreflight(ctx, baseline, &readiness, evidence); recordErr != nil {
				return approvedInputs{}, recordErr
			}
			return approvedInputs{}, fmt.Errorf("shard project has no canonical local checkout: %w", domain.ErrNotFound)
		}
		profile, exists := s.Catalog.Profiles[baseline.ProfileID]
		if !exists {
			if err := s.invalidateBaseline(ctx, baseline, &readiness, "current architecture profile is missing"); err != nil {
				return approvedInputs{}, err
			}
			return approvedInputs{}, fmt.Errorf("current architecture profile is missing: %w", domain.ErrValidation)
		}
		profileFingerprint, fingerprintErr := agentcontrol.ProfileFingerprint(s.Catalog, profile.ID)
		if fingerprintErr != nil || profileFingerprint != baseline.ProfileFingerprint ||
			shard.ProfileFingerprint != profileFingerprint || shard.ProfileID != baseline.ProfileID {
			if err := s.invalidateBaseline(ctx, baseline, &readiness, "architecture profile fingerprint changed"); err != nil {
				return approvedInputs{}, err
			}
			return approvedInputs{}, fmt.Errorf("architecture profile fingerprint changed: %w", domain.ErrConflict)
		}
		profileFingerprints[project.ID] = profileFingerprint
		if baseline.ExecutionState != domain.ContractBaselineFrozen || !baseline.Validation.Passed ||
			baseline.ApprovedPlanFingerprint != bundle.Plan.Fingerprint || baseline.ContractPlanFingerprint != contractPlanFingerprint ||
			baseline.ContractBaselineCommit == "" ||
			shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline ||
			shard.ExecutionBase.ContractBaselineID != baseline.ID || shard.ExecutionBase.Revision != baseline.ContractBaselineCommit {
			if err := s.invalidateBaseline(ctx, baseline, &readiness, "contract baseline, Plan fingerprint, source revision, or shard base changed"); err != nil {
				return approvedInputs{}, err
			}
			return approvedInputs{}, fmt.Errorf("contract baseline evidence is stale or mismatched: %w", domain.ErrConflict)
		}
		if _, recorded := readiness.ProfileFingerprints[project.ID]; !recorded || readiness.ProfileFingerprints[project.ID] != profileFingerprint {
			if err := s.invalidateBaseline(ctx, baseline, &readiness, "fan-out readiness profile fingerprint changed"); err != nil {
				return approvedInputs{}, err
			}
			return approvedInputs{}, fmt.Errorf("readiness profile fingerprint changed: %w", domain.ErrConflict)
		}
		inspection, inspectErr := s.Repositories.InspectWithAllowedChanges(ctx, *project.LocalPath, nil)
		sourceEvidence, sourceErr := connectedSourcePreflight(project, baseline, inspection, inspectErr, s.now())
		if sourceErr != nil {
			if err := s.recordSourcePreflight(ctx, baseline, &readiness, sourceEvidence); err != nil {
				return approvedInputs{}, err
			}
			return approvedInputs{}, sourceErr
		}
		verified := contractbaseline.VerifyBaseline(ctx, contractbaseline.FileMaterializer{}, *project.LocalPath,
			baseline, bundle.Plan.Fingerprint, *output.ContractPlan, profile, profileFingerprint, baseline.RepositoryRevision, s.now())
		if !verified.Validation.Passed || verified.ExecutionState != domain.ContractBaselineFrozen {
			sourceEvidence = contractBaselinePreflight(sourceEvidence, verified.Validation)
			if err := s.recordSourcePreflight(ctx, baseline, &readiness, sourceEvidence); err != nil {
				return approvedInputs{}, err
			}
			if verified.Validation.InspectionFailed {
				return approvedInputs{}, fmt.Errorf("frozen contract baseline inspection failed: %s: %w", sourceEvidence.Error, domain.ErrInvalidStatus)
			}
			return approvedInputs{}, fmt.Errorf("frozen contract baseline drift detected (%s): %s: %w", sourceEvidence.ReasonCode, sourceEvidence.Error, domain.ErrConflict)
		}
		sourceEvidence.InspectionStatus = domain.SourcePreflightVerified
		sourceEvidence.ReasonCode = "SOURCE_STATE_MATCHED"
		if err := s.recordSourcePreflight(ctx, baseline, &readiness, sourceEvidence); err != nil {
			return approvedInputs{}, err
		}
		result.sourcePreflights = append(result.sourcePreflights, sourceEvidence)
		baselineIDs[baseline.ID] = struct{}{}
		if result.baseline.ID == "" {
			result.baseline = baseline
		} else if result.baseline.ID != baseline.ID || result.baseline.ContractBaselineCommit != baseline.ContractBaselineCommit {
			return approvedInputs{}, fmt.Errorf("fan-out shards do not share one frozen baseline: %w", domain.ErrConflict)
		}
	}
	if len(result.shards) != len(readiness.ShardIDs) || len(baselineIDs) != len(readiness.BaselineIDs) ||
		!sameStrings(shardIDs(shards), readiness.ShardIDs) || !sameStrings(mapKeys(baselineIDs), readiness.BaselineIDs) {
		return approvedInputs{}, fmt.Errorf("ready shard or baseline set changed after approval: %w", domain.ErrConflict)
	}
	if err := agentcontrol.ValidateCatalog(s.Catalog); err != nil {
		return approvedInputs{}, err
	}
	_ = profileFingerprints
	return result, nil
}

func validateConnectedSource(inspection domain.RepositorySource, expectedHead string, inspectErr error) (string, error) {
	if inspectErr != nil {
		message := bounded(inspectErr.Error(), 500)
		return "connected source checkout inspection failed after readiness: " + message,
			fmt.Errorf("inspect connected source checkout after readiness: %w", inspectErr)
	}
	if inspection.IsDirty {
		return "connected source checkout has unapproved changes after readiness",
			fmt.Errorf("connected source checkout is dirty after readiness: %w", domain.ErrConflict)
	}
	if inspection.HeadCommit != expectedHead {
		return fmt.Sprintf("connected source checkout HEAD changed after readiness (expected %s, observed %s)", expectedHead, inspection.HeadCommit),
			fmt.Errorf("connected source checkout HEAD changed after readiness: %w", domain.ErrConflict)
	}
	return "", nil
}

func connectedSourcePreflight(project domain.Project, baseline domain.ContractBaseline, inspection domain.RepositorySource, inspectErr error, now time.Time) (domain.SourcePreflightEvidence, error) {
	path := ""
	if project.LocalPath != nil {
		path = *project.LocalPath
	}
	evidence := domain.SourcePreflightEvidence{
		RepositoryPath: path, ProjectID: project.ID, ExpectedRevision: baseline.RepositoryRevision,
		ActualRevision: inspection.HeadCommit, ExpectedClean: true, ActualStatus: "UNKNOWN",
		ContractBaselineCommit: baseline.ContractBaselineCommit, ExpectedIdentity: project.SourceIdentity,
		ActualIdentity: inspection.Identity, RecordedAt: now,
	}
	if inspection.LocalPath != "" {
		evidence.RepositoryPath = inspection.LocalPath
	}
	if inspectErr != nil {
		evidence.InspectionStatus = domain.SourcePreflightInspectionFailed
		evidence.ReasonCode = "REPOSITORY_INSPECTION_FAILED"
		evidence.Error = bounded(inspectErr.Error(), 1000)
		return evidence, fmt.Errorf("source preflight inspection failed (project=%s path=%s expected_revision=%s): %w", project.ID, path, baseline.RepositoryRevision, inspectErr)
	}
	if inspection.IsDirty {
		evidence.ActualStatus = "DIRTY"
		evidence.InspectionStatus = domain.SourcePreflightDrift
		evidence.ReasonCode = "WORKTREE_DIRTY"
		evidence.Error = "connected source checkout contains unapproved changes"
		return evidence, fmt.Errorf("source preflight drift (project=%s expected_revision=%s actual_revision=%s actual_status=DIRTY): %w", project.ID, evidence.ExpectedRevision, evidence.ActualRevision, domain.ErrConflict)
	}
	evidence.ActualStatus = "CLEAN"
	if inspection.Identity != project.SourceIdentity {
		evidence.InspectionStatus = domain.SourcePreflightDrift
		evidence.ReasonCode = "REPOSITORY_IDENTITY_MISMATCH"
		evidence.Error = "connected checkout identity differs from the connected project"
		return evidence, fmt.Errorf("source preflight repository identity drift (project=%s): %w", project.ID, domain.ErrConflict)
	}
	if project.HeadCommit != baseline.RepositoryRevision {
		evidence.InspectionStatus = domain.SourcePreflightDrift
		evidence.ReasonCode = "PROJECT_REVISION_MISMATCH"
		evidence.Error = fmt.Sprintf("connected project revision %s differs from frozen source revision %s", project.HeadCommit, baseline.RepositoryRevision)
		return evidence, fmt.Errorf("source preflight project revision drift (project=%s expected_revision=%s project_revision=%s actual_revision=%s): %w", project.ID, evidence.ExpectedRevision, project.HeadCommit, evidence.ActualRevision, domain.ErrConflict)
	}
	if inspection.HeadCommit != baseline.RepositoryRevision {
		evidence.InspectionStatus = domain.SourcePreflightDrift
		evidence.ReasonCode = "HEAD_REVISION_MISMATCH"
		evidence.Error = fmt.Sprintf("connected source HEAD %s differs from frozen source revision %s", inspection.HeadCommit, baseline.RepositoryRevision)
		return evidence, fmt.Errorf("source preflight HEAD drift (project=%s expected_revision=%s actual_revision=%s actual_status=CLEAN): %w", project.ID, evidence.ExpectedRevision, evidence.ActualRevision, domain.ErrConflict)
	}
	evidence.InspectionStatus = domain.SourcePreflightVerified
	evidence.ReasonCode = "SOURCE_STATE_MATCHED"
	return evidence, nil
}

func sourcePreflightFailure(projectID, path string, baseline domain.ContractBaseline, reason, message string, now time.Time) domain.SourcePreflightEvidence {
	return domain.SourcePreflightEvidence{
		RepositoryPath: path, ProjectID: projectID, ExpectedRevision: baseline.RepositoryRevision,
		ExpectedClean: true, ActualStatus: "UNKNOWN", ContractBaselineCommit: baseline.ContractBaselineCommit,
		InspectionStatus: domain.SourcePreflightInspectionFailed, ReasonCode: reason,
		Error: bounded(message, 1000), RecordedAt: now,
	}
}

type sourcePreflightStore interface {
	SaveContractBaseline(context.Context, domain.ContractBaseline) error
	SaveFanoutReadiness(context.Context, domain.FanoutReadinessEvidence) error
	GetShardFanout(context.Context, string) (domain.ShardFanoutExecution, error)
	SaveShardFanout(context.Context, domain.ShardFanoutExecution) error
}

func (s Service) recordSourcePreflight(ctx context.Context, baseline domain.ContractBaseline, readiness *domain.FanoutReadinessEvidence, evidence domain.SourcePreflightEvidence) error {
	store, ok := s.Execution.(sourcePreflightStore)
	if !ok {
		return fmt.Errorf("source preflight persistence is unavailable: %w", domain.ErrInvalidStatus)
	}
	return applySourcePreflight(ctx, store, baseline, readiness, evidence)
}

func (s Service) persistedSourcePreflightHistory(ctx context.Context, baseline domain.ContractBaseline, fallback []domain.SourcePreflightEvidence) ([]domain.SourcePreflightEvidence, error) {
	store, ok := s.Execution.(sourcePreflightStore)
	if !ok {
		return nil, fmt.Errorf("source preflight persistence is unavailable: %w", domain.ErrInvalidStatus)
	}
	return loadSourcePreflightHistory(ctx, store, baseline, fallback)
}

func loadSourcePreflightHistory(ctx context.Context, store sourcePreflightStore, baseline domain.ContractBaseline, fallback []domain.SourcePreflightEvidence) ([]domain.SourcePreflightEvidence, error) {
	prior, err := store.GetShardFanout(ctx, baseline.PlanID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fallback, nil
		}
		return nil, err
	}
	if prior.ContractBaselineID != baseline.ID || prior.BaselineCommit != baseline.ContractBaselineCommit {
		if baseline.ID != prior.ContractBaselineID && baseline.PredecessorBaselineID == prior.ContractBaselineID && prior.ContractBaselineID != "" && baseline.ContractBaselineCommit != "" && baseline.ContractBaselineCommit != prior.BaselineCommit {
			// Repository SaveShardFanout independently validates and audits replacement
			// lifecycle; preflight evidence is attributed only to the current baseline.
			return append([]domain.SourcePreflightEvidence(nil), fallback...), nil
		}
		return nil, fmt.Errorf("source preflight history belongs to a different frozen baseline: %w", domain.ErrConflict)
	}
	if len(prior.SourcePreflights) > 0 {
		return append([]domain.SourcePreflightEvidence(nil), prior.SourcePreflights...), nil
	}
	return fallback, nil
}

func applySourcePreflight(ctx context.Context, store sourcePreflightStore, baseline domain.ContractBaseline, readiness *domain.FanoutReadinessEvidence, evidence domain.SourcePreflightEvidence) error {
	blocked := evidence.InspectionStatus != domain.SourcePreflightVerified
	if evidence.InspectionStatus == domain.SourcePreflightDrift {
		reason := "source preflight drift [" + evidence.ReasonCode + "]: " + evidence.Error
		if baseline.ExecutionState == domain.ContractBaselineFrozen {
			baseline.ExecutionState = domain.ContractBaselineInvalidated
			baseline.Validation.Passed = false
			baseline.Validation.Reasons = appendUnique(baseline.Validation.Reasons, reason)
			baseline.UpdatedAt = evidence.RecordedAt
			if err := store.SaveContractBaseline(ctx, baseline); err != nil {
				return err
			}
		}
		readiness.State = domain.FanoutReadinessBlocked
		readiness.Reasons = appendUnique(readiness.Reasons, reason)
		readiness.RecordedAt = evidence.RecordedAt
		if err := store.SaveFanoutReadiness(ctx, *readiness); err != nil {
			return err
		}
	}
	execution, err := store.GetShardFanout(ctx, baseline.PlanID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if !blocked {
			return nil
		}
		now := evidence.RecordedAt
		execution = domain.ShardFanoutExecution{
			PlanID: baseline.PlanID, ContractBaselineID: baseline.ID, BaselineCommit: baseline.ContractBaselineCommit,
			State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked, Composition: "PENDING",
			CreatedAt: now, Attempts: []domain.ShardAttempt{},
		}
	}
	if execution.ContractBaselineID != baseline.ID || execution.BaselineCommit != baseline.ContractBaselineCommit {
		if !blocked && baseline.ID != execution.ContractBaselineID && baseline.PredecessorBaselineID == execution.ContractBaselineID && execution.ContractBaselineID != "" && baseline.ContractBaselineCommit != "" && baseline.ContractBaselineCommit != execution.BaselineCommit {
			// Keep the predecessor projection intact until approved fanout preparation
			// atomically audits and replaces it through the repository lifecycle gate.
			return nil
		}
		return fmt.Errorf("source preflight projection belongs to a different frozen baseline: %w", domain.ErrConflict)
	}
	if execution.SourcePreflights == nil {
		execution.SourcePreflights = []domain.SourcePreflightEvidence{}
	}
	execution.SourcePreflights = append(execution.SourcePreflights, evidence)
	if blocked {
		execution.State = "BARRIER_BLOCKED"
		execution.Barrier = domain.ShardBarrierBlocked
		reason := string(evidence.InspectionStatus) + " [" + evidence.ReasonCode + "]"
		if evidence.Error != "" {
			reason += ": " + evidence.Error
		}
		execution.BarrierReasons = appendUnique(execution.BarrierReasons, reason)
	}
	execution.UpdatedAt = evidence.RecordedAt
	return store.SaveShardFanout(ctx, execution)
}

func contractBaselineReasonCode(validation domain.ContractBaselineValidation) string {
	for _, reason := range validation.Reasons {
		if strings.Contains(strings.ToLower(reason), "hash") {
			return "CONTRACT_HASH_MISMATCH"
		}
	}
	return "CONTRACT_BASELINE_VALIDATION_FAILED"
}

func contractBaselinePreflight(evidence domain.SourcePreflightEvidence, validation domain.ContractBaselineValidation) domain.SourcePreflightEvidence {
	evidence.ActualStatus = "CLEAN"
	evidence.InspectionStatus = domain.SourcePreflightDrift
	evidence.ReasonCode = contractBaselineReasonCode(validation)
	evidence.Error = bounded(strings.Join(validation.Reasons, "; "), 1000)
	if validation.InspectionFailed {
		evidence.InspectionStatus = domain.SourcePreflightInspectionFailed
		evidence.ReasonCode = "CONTRACT_BASELINE_INSPECTION_FAILED"
	}
	return evidence
}

func (s Service) invalidateBaseline(ctx context.Context, baseline domain.ContractBaseline, readiness *domain.FanoutReadinessEvidence, reasons ...string) error {
	if baseline.ExecutionState == domain.ContractBaselineFrozen {
		baseline.ExecutionState = domain.ContractBaselineInvalidated
		baseline.Validation.Passed = false
		for _, reason := range reasons {
			baseline.Validation.Reasons = appendUnique(baseline.Validation.Reasons, reason)
		}
		baseline.UpdatedAt = s.now()
		if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
			return err
		}
	}
	readiness.State = domain.FanoutReadinessBlocked
	for _, reason := range reasons {
		readiness.Reasons = appendUnique(readiness.Reasons, reason)
	}
	readiness.RecordedAt = s.now()
	return s.Execution.SaveFanoutReadiness(ctx, *readiness)
}

func (s Service) verifyWorkspaceBase(ctx context.Context, project domain.Project, workspace domain.TaskWorkspace, baseline domain.ContractBaseline) error {
	if workspace.BaseCommit != baseline.ContractBaselineCommit {
		return fmt.Errorf("worker workspace does not match frozen baseline: %w", domain.ErrConflict)
	}
	state, err := s.Worktrees.Inspect(ctx, project, workspace)
	if err != nil {
		return err
	}
	if err := validateWorkerFrozenHEAD(workspace, state); err != nil {
		return err
	}
	if err := verifyWorkingFrozenFiles(ctx, s.Worktrees, workspace, baseline); err != nil {
		return err
	}
	validation := contractbaseline.VerifyCommittedBaseline(ctx, *project.LocalPath, baseline.RepositoryRevision,
		baseline.ContractBaselineCommit, s.Catalog.Profiles[baseline.ProfileID], baseline.Files, baseline.MaterializedContracts)
	if !validation.Passed {
		return fmt.Errorf("frozen contract hashes no longer validate: %w", domain.ErrConflict)
	}
	return nil
}

func persistenceBoundaryExists(root string, packageValue domain.WorkPackage) bool {
	hasIntegrationTest := false
	for _, testPath := range packageValue.Verification.TestPaths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(testPath)))
		if err != nil {
			continue
		}
		test := strings.ToLower(string(content))
		usesPostgres := strings.Contains(test, "pgx") || strings.Contains(test, "database/sql") || strings.Contains(test, "postgres")
		usesRealDatabase := strings.Contains(test, "test_database_url") || strings.Contains(test, "testcontainers") || strings.Contains(test, "database_url")
		usesFake := strings.Contains(test, "sqlmock") || strings.Contains(test, "mock")
		if usesPostgres && usesRealDatabase && !usesFake {
			hasIntegrationTest = true
		}
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}
	module := strings.ToLower(string(goMod))
	hasDriver := strings.Contains(module, "pgx") || strings.Contains(module, "lib/pq")
	hasSchema := false
	for _, directory := range []string{"db/migrations", "migrations", "schema"} {
		matches, _ := filepath.Glob(filepath.Join(root, directory, "*.sql"))
		if len(matches) > 0 {
			hasSchema = true
		}
	}
	return hasDriver && hasSchema && hasIntegrationTest
}

func (s Service) finishFailure(ctx context.Context, attempt domain.ShardAttempt, status domain.ShardAttemptStatus, blocker string, cause error) (domain.ShardAttempt, error) {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		blocker = "EXECUTION_CANCELLED"
	}

	if status == "" {
		status = domain.ShardAttemptFailed
	}
	attempt.Status = status
	if blocker != "" {
		attempt.Blockers = appendUnique(attempt.Blockers, blocker)
	}
	if cause != nil {
		attempt.Blockers = appendUnique(attempt.Blockers, bounded(cause.Error(), 2000))
	}
	finished := s.now()
	attempt.FinishedAt = &finished
	attempt.UpdatedAt = finished
	if err := s.Execution.SaveShardAttempt(ctx, attempt); err != nil {
		return domain.ShardAttempt{}, errors.Join(cause, err)
	}
	return attempt, nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func phaseSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"status", "shard_id", "baseline_revision", "red", "changed_files", "blockers", "contract_change_requested"},
		"properties": map[string]any{
			"status":   map[string]any{"type": "string", "enum": []string{"completed", "blocked", "replan_required"}},
			"shard_id": map[string]any{"type": "string"}, "baseline_revision": map[string]any{"type": "string"},
			"red": map[string]any{"type": "object", "additionalProperties": false,
				"required": []string{"command", "expected_failure", "observed_failure", "semantic", "test_paths", "recorded_at", "pre_implementation_sha"},
				"properties": map[string]any{
					"command": map[string]any{"type": "string"}, "expected_failure": map[string]any{"type": "string"},
					"observed_failure": map[string]any{"type": "string"}, "semantic": map[string]any{"type": "boolean"},
					"test_paths":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"recorded_at": map[string]any{"type": "string"}, "pre_implementation_sha": map[string]any{"type": "string"},
				},
			},
			"changed_files": stringArraySchema(), "blockers": stringArraySchema(),
			"contract_change_requested": map[string]any{"type": "boolean"},
		},
	}
}

func claimSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"status", "shard_id", "baseline_revision", "changed_files", "implemented_contracts", "blockers", "contract_change_requested"},
		"properties": map[string]any{
			"status":   map[string]any{"type": "string", "enum": []string{"completed", "blocked", "replan_required"}},
			"shard_id": map[string]any{"type": "string"}, "baseline_revision": map[string]any{"type": "string"},
			"changed_files":         stringArraySchema(),
			"implemented_contracts": stringArraySchema(), "blockers": stringArraySchema(),
			"contract_change_requested": map[string]any{"type": "boolean"},
		},
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}

func redPrompt(packageValue domain.WorkPackage) string {
	return "You are one generic architectural worker. This is preparation of a candidate behavioral RED test only. Do not run tests, Docker, initdb, database setup commands or verification; do not change the environment. The external orchestrator owns go test and the existing real PostgreSQL fixture environment. Agent sandbox database credentials and database tools are not prerequisites for preparing tests. Report completed when the candidate test is prepared, expected_failure describing the anticipated semantic assertion, semantic=false, observed_failure empty, and the exact changed test_paths. Only the orchestrator may persist verified semantic RED after observing the actual test result. Do not modify production files, frozen contracts, sibling scopes, production SQL, migrations, or composition. Test-only SQL for fixture seed and assertions is allowed. Create the cheapest meaningful behavioral test for this WorkPackage, only in its declared test paths. The test must fail with an assertion containing the exact marker `semantic RED:` because the target behavior is absent; syntax, import, dependency, fixture, or environment failures are invalid. Assert observable output for representative valid and invalid inputs through the frozen boundary. Testing interface existence alone is insufficient: runtime interface assertions may avoid compiler-only RED, but the same test must proceed to exercise behavior once the implementation exists. For persistence, write tests that open the existing real PostgreSQL fixture and seed meaningful resource/range data before the semantic assertion, then execute an actual production repository call/query and assert its query result for resource filtering, half-open boundaries, ordering and non-overlap. Check deterministic seed values against every expected interval: an available row wholly inside the requested half-open range must not be dropped, and a row starting exactly at End is outside. A RED caused by an absent interface or method is NOT valid persistence behavior RED. Do not add a fake failing query or repository implementation in a test to manufacture RED. An orchestrator-approved RED_SETUP shell may already exist. Write the candidate test to exercise its real production method through the PostgreSQL fixture. An explicit ErrNotImplemented result can establish absent target behavior only after the fixture is seeded and the production call is made. Preserve setup production files byte-for-byte; modify declared test paths only. For usecase, supply a fake frozen repository and assert validation plus domain-to-application mapping. UTC means numeric zero offset: cover time.UTC and distinct time.FixedZone with offset zero, plus rejection of positive/negative offsets; Location pointer identity is not a valid UTC rule. For HTTP, supply a fake application and assert parsed request, delegation and HTTP status/body. Keep test setup and future implementation construction within this shard. Do not implement behavior. Return only the required structured phase result and claim only paths you actually changed. WorkPackage:\n" + compactJSON(packageValue)
}

func implementationPrompt(packageValue domain.WorkPackage, red domain.ShardREDEvidence) string {
	return "Continue this generic architectural worker after orchestrator-verified semantic RED. Implement only the WorkPackage. Preserve the RED test files byte-for-byte. Frozen contracts are read-only. Do not change sibling or composition paths. If a contract change, out-of-scope change, missing test boundary, stale baseline, dependency, or architecture conflict is needed, stop and report the allowed blocker. Do not commit. Do not self-certify GREEN; the orchestrator reruns checks. Return only the required structured implementation claim. WorkPackage:\n" + compactJSON(packageValue) + "\nVerified RED evidence:\n" + compactJSON(red)
}

func readOnlyBoundaryPrompt(packageValue domain.WorkPackage) string {
	return "You are the persistence shard worker. This is a read-only test-boundary assessment, before implementation. Inspect only the bounded persistence source, approved test paths, go.mod, and existing schema files needed to determine whether a real disposable PostgreSQL integration test can be run without changing a sibling route or adding out-of-scope files. Do not modify anything. If no real SQL test boundary exists, report status blocked with TEST_BOUNDARY_MISSING. Do not propose a mock of SQL. Return only the structured phase result. WorkPackage:\n" + compactJSON(packageValue)
}

func strictJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("strict JSON decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("structured result contains trailing data: %w", domain.ErrValidation)
	}
	return nil
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func invalidRedOutput(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"undefined:", "syntax error", "no required module provides", "cannot find module", "build failed", "could not import", "permission denied", "test_database_url is required", "test_database_url required", "test_database_url must be set", "missing test_database_url", "connection refused", "fixture setup failed", "fixture initialization failed", "fixture seed failed", "migration failed", "migrations failed", "failed to apply migration", "failed to connect to postgres", "no such host"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func ValidateSemanticRED(evidence domain.ShardREDEvidence, changedTestPaths []string, result domain.WorkspaceCheckResult) error {
	if result.Command != "go test ./..." || result.ExitCode == 0 ||
		strings.TrimSpace(evidence.ExpectedFailure) == "" || !samePaths(evidence.TestPaths, changedTestPaths) ||
		!strings.Contains(strings.ToLower(result.Output), "semantic red:") || invalidRedOutput(result.Output) {
		return fmt.Errorf("test result is not a target-behavior semantic RED: %w", domain.ErrValidation)
	}
	return nil
}

func phaseBlocker(err error) domain.ShardAttemptStatus {
	var serviceErr ServiceError
	if errors.As(err, &serviceErr) && serviceErr.Status != "" {
		return serviceErr.Status
	}
	if errors.Is(err, domain.ErrWriteScope) {
		return domain.ShardAttemptFailed
	}
	return domain.ShardAttemptFailed
}

func workerBlocker(blockers []string, contractChange bool) (domain.ShardAttemptStatus, string) {
	if contractChange || contains(blockers, "CONTRACT_CHANGE_REQUIRED") {
		return domain.ShardAttemptContractChange, "CONTRACT_CHANGE_REQUIRED"
	}
	for _, blocker := range domain.ShardExecutionBlockers {
		if !contains(blockers, blocker) {
			continue
		}
		switch blocker {
		case "OUT_OF_SCOPE_CHANGE_REQUIRED":
			return domain.ShardAttemptFailed, blocker
		case "ARCHITECTURE_CONFLICT":
			return domain.ShardAttemptReplan, blocker
		default:
			return domain.ShardAttemptBlocked, blocker
		}
	}
	return domain.ShardAttemptFailed, ""
}

func scopeAllowsFiles(workPackage domain.WorkPackage, files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		allowed := false
		for _, pattern := range workPackage.WriteScope.Allow {
			if pathMatches(pattern, file) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
		for _, pattern := range append(append([]string(nil), workPackage.WriteScope.Deny...), workPackage.WriteScope.CompositionOnly...) {
			if pathMatches(pattern, file) {
				return false
			}
		}
		for _, readonly := range workPackage.Contracts.ReadOnlyPaths {
			if file == readonly {
				return false
			}
		}
	}
	return true
}

func pathsWithin(files, allowed []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		matched := false
		for _, candidate := range allowed {
			if pathMatches(candidate, file) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func pathMatches(pattern, file string) bool {
	pattern = filepath.ToSlash(pattern)
	file = filepath.ToSlash(file)
	return pattern == file || strings.HasSuffix(pattern, "/**") && strings.HasPrefix(file, strings.TrimSuffix(pattern, "**"))
}

func redTestsUnchanged(redSnapshot domain.WorkspaceSnapshot, redPaths, finalFiles []string, root string) bool {
	for _, file := range redPaths {
		redHash, exists := redSnapshot.Files[file]
		if !exists || !strings.HasSuffix(file, "_test.go") || !contains(finalFiles, file) {
			return false
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil || !strings.HasSuffix(redHash, ":"+contentHash(content)) {
			return false
		}
	}
	return true
}

func contentHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func snapshotDigest(snapshot domain.WorkspaceSnapshot) string {
	keys := make([]string, 0, len(snapshot.Files))
	for key := range snapshot.Files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(keys)
	hash := sha256.New()
	_, _ = hash.Write(encoded)
	for _, key := range keys {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(snapshot.Files[key]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cleanSorted(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
		if value == "" || value == "." {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func samePaths(left, right []string) bool {
	return strings.Join(cleanSorted(left), "\x00") == strings.Join(cleanSorted(right), "\x00")
}
func sameStrings(left, right []string) bool {
	return strings.Join(cleanSorted(left), "\x00") == strings.Join(cleanSorted(right), "\x00")
}
func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func appendUnique(values []string, value string) []string {
	if !contains(values, value) {
		values = append(values, value)
	}
	return values
}
func uniqueCommands(values []string) []string { return cleanSorted(values) }
func bounded(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}
func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func shardIDs(shards []domain.ArchitecturalShard) []string {
	result := make([]string, 0, len(shards))
	for _, shard := range shards {
		result = append(result, shard.ID)
	}
	sort.Strings(result)
	return result
}
func sameJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func implementationClaimMatches(claimed, actual, redPaths []string) bool {
	return samePaths(actual, cleanSorted(append(append([]string(nil), claimed...), redPaths...)))
}

func setShardPhase(attempt *domain.ShardAttempt, phase domain.ShardExecutionPhase, now time.Time) {
	attempt.Phase = phase
	attempt.PhaseHistory = append(attempt.PhaseHistory, domain.ShardPhaseEvent{Phase: phase, RecordedAt: now})
	attempt.UpdatedAt = now
}
func (s Service) persistPhase(ctx context.Context, attempt *domain.ShardAttempt, phase domain.ShardExecutionPhase) error {
	setShardPhase(attempt, phase, s.now())
	return s.Execution.SaveShardAttempt(ctx, *attempt)
}
func snapshotChangedFiles(before, after domain.WorkspaceSnapshot) []string {
	changed := []string{}
	for path, hash := range before.Files {
		if after.Files[path] != hash {
			changed = append(changed, path)
		}
	}
	for path := range after.Files {
		if _, ok := before.Files[path]; !ok {
			changed = append(changed, path)
		}
	}
	return cleanSorted(changed)
}
func redSetupPrompt(wp domain.WorkPackage) string {
	if wp.Route == "backend.transport.http" || wp.Route == "backend.usecase" {
		return "Constructor RED_SETUP only. Preserve the existing AvailabilityHandler (HTTP) or AvailabilityUsecase (usecase) implementation type name. Add exactly one dependency field: usecase.ApplicationCommandResult for HTTP, domain.RepositoryPort for usecase. Add exported NewAvailabilityHandler or NewAvailabilityUsecase taking that dependency and returning only &Type{field: parameter}. Import only its frozen dependency package. Do not add receiver methods, HTTP behavior, business logic, validation, SQL, tests, routes, composition, helpers, or contract edits. If existing verified HTTP business methods are present, preserve them and all original imports byte-for-byte; only add the allocation constructor (existing dependency field stays unchanged). Do not commit or run tests. Return completed structured phase result with semantic=false and no behavioral RED claim. The orchestrator will independently validate setup before candidate regression tests. WorkPackage:\n" + compactJSON(wp)
	}
	return "Persistence RED_SETUP only. Create only a concrete adapter struct with exactly one *pgxpool.Pool field, a New<Type> constructor returning &Type{pool: pool}, var ErrNotImplemented = errors.New(\"not implemented\"), and pointer receiver methods matching every frozen domain.RepositoryPort signature exactly. Each method returns nil, ErrNotImplemented. Imports only context, errors, frozen domain, pgxpool. No SQL, queries, scanning, mapping, filtering, sorting, interval handling, fallback, target behavior, test changes, contract or sibling changes. Do not commit. Return structured phase result with the required RED object populated using empty strings, semantic false, empty test_paths, a valid recorded_at timestamp, and no claim of behavioral RED. WorkPackage:\n" + compactJSON(wp)
}
func persistenceConcreteSeamExists(root string, wp domain.WorkPackage) bool {
	port, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal/domain/repository_port.go"), nil, 0)
	if err != nil {
		return false
	}
	methods := map[string]string{}
	for _, decl := range port.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typ, ok := spec.(*ast.TypeSpec)
			if !ok || typ.Name.Name != "RepositoryPort" {
				continue
			}
			iface, ok := typ.Type.(*ast.InterfaceType)
			if !ok {
				return false
			}
			for _, field := range iface.Methods.List {
				fn, ok := field.Type.(*ast.FuncType)
				if !ok || len(field.Names) != 1 {
					return false
				}
				methods[field.Names[0].Name] = setupSignature(fn, true)
			}
		}
	}
	if len(methods) == 0 {
		return false
	}
	receivers := map[string]map[string]bool{}
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !scopeAllowsFiles(wp, []string{filepath.ToSlash(rel)}) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
				continue
			}
			signature, ok := methods[fn.Name.Name]
			if !ok || signature != setupSignature(fn.Type, false) {
				continue
			}
			receiver := filepath.Dir(path) + ":" + strings.TrimPrefix(setupText(fn.Recv.List[0].Type), "*")
			if receivers[receiver] == nil {
				receivers[receiver] = map[string]bool{}
			}
			receivers[receiver][fn.Name.Name] = true
		}
		return nil
	})
	for _, implemented := range receivers {
		if len(implemented) == len(methods) {
			return true
		}
	}
	return false
}

func validateWorkerFrozenHEAD(workspace domain.TaskWorkspace, state domain.WorkspaceState) error {
	if workspace.BaseCommit == "" || state.HeadCommit != workspace.BaseCommit {
		return fmt.Errorf("worker workspace HEAD differs from frozen base: %w", domain.ErrConflict)
	}
	return nil
}

type frozenArtifactReader interface {
	ReadArtifact(context.Context, domain.TaskWorkspace, string, int64) ([]byte, error)
}

func verifyWorkingFrozenFiles(ctx context.Context, reader frozenArtifactReader, workspace domain.TaskWorkspace, baseline domain.ContractBaseline) error {
	for _, file := range baseline.Files {
		if file.Path == "" || filepath.IsAbs(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || strings.HasPrefix(file.Path, "../") {
			return fmt.Errorf("unsafe frozen contract path: %w", domain.ErrConflict)
		}
		var content []byte
		var err error
		if reader != nil {
			content, err = reader.ReadArtifact(ctx, workspace, file.Path, 1<<20)
		} else {
			full := workspace.Path
			for _, part := range strings.Split(file.Path, "/") {
				full = filepath.Join(full, part)
				info, statErr := os.Lstat(full)
				if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
					err = fmt.Errorf("missing or symbolic frozen contract path")
					break
				}
			}
			if err == nil {
				info, statErr := os.Lstat(full)
				if statErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
					err = fmt.Errorf("frozen contract is not a bounded regular file")
				} else {
					handle, openErr := os.Open(full)
					if openErr != nil {
						err = openErr
					} else {
						content, err = io.ReadAll(io.LimitReader(handle, (1<<20)+1))
						closeErr := handle.Close()
						if err == nil {
							err = closeErr
						}
					}
				}
			}
		}
		if err != nil || len(content) > 1<<20 || contentHash(content) != file.SHA256 {
			return fmt.Errorf("working frozen contract differs from baseline: %s: %w", file.Path, domain.ErrConflict)
		}
	}
	return nil
}

func constructorSetupNeeded(root string, wp domain.WorkPackage) bool {
	target := ""
	switch wp.Route {
	case "backend.transport.http":
		target = "NewAvailabilityHandler"
	case "backend.usecase":
		target = "NewAvailabilityUsecase"
	default:
		return false
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !scopeAllowsFiles(wp, []string{filepath.ToSlash(rel)}) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == target {
				found = true
			}
		}
		return nil
	})
	return !found
}

func remediationPrompt(remediation *ShardRemediation, route string) string {
	if remediation == nil {
		return ""
	}
	common := "\nThis is selective remediation of authoritative independent reviewer findings, not a new Plan or freeze. Remediation metadata: " + compactJSON(remediation) + ". Start from the existing frozen baseline. If the orchestrator has reproduced exact previously VERIFIED affected HTTP or Usecase production bytes for known-bug reproduction, preserve that audited preimage during RED setup/test phases; it is existing behavior, not permission to implement any change before semantic RED. Previous verified commit is read-only reference evidence; do not checkout, cherry-pick, commit, manage worktrees, or change HEAD. Write fresh regressions for the findings before implementing behavior. Tests must construct production components through ordinary public constructors. Do not use reflect or unsafe to populate private fields; no test-only substitute constructor or fake production query. No DI, route registration, container or composition wiring. Preserve prior approved behavior as well as the regression."
	switch route {
	case "backend.transport.http":
		return common + " HTTP UTC-boundary selective retry only. The orchestrator has reproduced the exact prior VERIFIED HTTP production source at frozen HEAD; its existing NewAvailabilityHandler constructor and ServeHTTP method are already present. Preserve every production byte and constructor signature during the test-only RED phase. No RED_SETUP or missing-interface test is needed. Use the real NewAvailabilityHandler with a spy ApplicationCommandResult that counts calls and returns a successful deterministic result for ANY request, including nonzero offsets; the spy must not validate timestamps or hide the delegation bug by returning ErrInvalidRequest. Build fresh regression table cases with net/url.Values encoding so literal plus offsets reach the parser correctly. Check UTC Z and +00:00 on BOTH start and end, including mixed zero-offset representations: HTTP200, application call count exactly one, and correctly parsed delegated values. Check +03:00, -05:00 and malformed timestamp independently on EACH of start and end with the other endpoint valid: HTTP400 and application call count exactly zero. Choose separated dates so all parseable nonzero-offset cases are increasing ranges; a reversed/equal range cannot establish the timezone regression. Preserve invalid resource and range behavior. For each nonzero case assert ZERO spy calls BEFORE checking status, so the critical semantic assertion exposes actual prior delegation, not an unrelated response-body or missing-method failure. Use the exact critical assertion prefix `semantic RED: non-zero offset is currently delegated`; expected_failure must identify this delegation assertion. Missing interface, constructor, compiler, environment and fixture failures do not qualify as RED. The orchestrator must persist the actual failed regression and untouched preimage evidence BEFORE implementation. Only after that persisted semantic RED may implementation add HTTP offset-zero checks using parsed Start.Zone() and End.Zone() BEFORE calling the application, retaining acceptance of Z/+00:00 and existing behavior. Do not change constructors, Usecase, Persistence, Composition, frozen contracts, ContractPlan, routing or approved scope. No worker commits, worktree lifecycle or self-certified GREEN. The orchestrator independently verifies GREEN, scope and frozen hashes, then creates the verified HTTP commit."

	case "backend.usecase":
		return common + " Usecase UTC-equivalence remediation: the orchestrator has reproduced the exact previously VERIFIED Usecase production source on frozen HEAD solely to reproduce its current Location identity bug. Do not change that source or existing public constructor during RED. Construct the real NewAvailabilityUsecase with a fake domain RepositoryPort. Add a table regression proving UTC Z, parsed RFC3339 +00:00, and a deterministic time.FixedZone(\"zero-offset-regression\", 0) value are ACCEPTED and actually delegate once with correct input and nonempty mapped result; nonzero positive +03:00 and negative -05:00 values are REJECTED with zero repository calls; reversed and equal ranges remain rejected. The critical assertion must fail specifically because current GetAvailability rejects valid zero-offset input through Location()==time.UTC identity, with semantic RED: in the assertion and precise expected_failure. A missing interface, constructor, compiler or environment failure does not qualify. No constructor setup or business change is needed before this regression; the existing constructor is already present. Only after orchestrator persists actual RED fix Usecase UTC equivalence using time.Time.Zone offset zero, retaining start<end and [start,end) semantics, resource validation, mapping, empty result and repository errors. Do not change HTTP, Persistence, frozen contracts, routing or composition."
	case "backend.infrastructure.persistence":
		return common + " Finding C: open the existing real PostgreSQL fixture, apply its existing migration/schema and deterministic seed BEFORE invoking the real production repository method. Use separately identifiable resources or disjoint windows to prove [start,end) left boundary: a row ending exactly at query Start is excluded; a row beginning exactly at Start is included; a row actually overlapping Start is included and clipped correctly. Ensure expected output distinguishes excluded row from included rows; do not merge all cases into a result that masks a wrong boundary predicate. Also retain resource filtering, right boundary, ordering/non-overlap and invalid query coverage. ErrNotImplemented after fixture seed and real production method call is a valid absent-behavior RED; missing interface, fixture or compiler failure is not. Preserve the already correct > left-boundary predicate if no behavior fix is needed, while adding explicit real database regression coverage."
	}
	return common
}
