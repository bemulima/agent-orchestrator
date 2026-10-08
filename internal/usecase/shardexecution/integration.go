package shardexecution

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type VerifierRegressionEvidence struct {
	Source             string `json:"source"`
	SHA256             string `json:"sha256"`
	Ephemeral          bool   `json:"ephemeral"`
	WorkspaceUnchanged bool   `json:"workspace_unchanged"`
}

type IntegrationCheckReport struct {
	HTTPDelegation             *VerifierRegressionEvidence   `json:"http_delegation,omitempty"`
	UTCCompatibility           *VerifierRegressionEvidence   `json:"utc_compatibility,omitempty"`
	Passed                     bool                          `json:"passed"`
	ChangedFiles               []string                      `json:"changed_files"`
	Checks                     []domain.WorkspaceCheckResult `json:"checks"`
	FrozenHashesPassed         bool                          `json:"frozen_hashes_passed"`
	ArchitectureChecksPassed   bool                          `json:"architecture_checks_passed"`
	NoUnsafeConstructionPassed bool                          `json:"no_unsafe_construction_passed"`
	VerifiedAt                 time.Time                     `json:"verified_at"`
}

type IntegrationReviewerDecision struct {
	Verdict  string   `json:"verdict"`
	Findings []string `json:"findings"`
}

type IntegrationReviewPacket struct {
	Plan struct {
		ID                  string            `json:"id"`
		Status              domain.PlanStatus `json:"status"`
		Fingerprint         string            `json:"fingerprint"`
		ApprovedFingerprint string            `json:"approved_fingerprint"`
		Summary             string            `json:"summary"`
	} `json:"approved_plan"`
	Tasks          []IntegrationReviewTask     `json:"tasks"`
	Routing        domain.RoutingResult        `json:"routing_result"`
	ContractPlan   domain.ContractPlan         `json:"contract_plan"`
	Baseline       IntegrationBaselineEvidence `json:"contract_baseline"`
	WorkPackages   []domain.WorkPackage        `json:"work_packages"`
	Workers        []domain.ShardAttempt       `json:"worker_results_and_commits"`
	Composition    *domain.CompositionAttempt  `json:"composition,omitempty"`
	BarrierReadyAt *time.Time                  `json:"barrier_ready_at,omitempty"`
	AssembledDiff  string                      `json:"assembled_diff"`
	Mechanical     json.RawMessage             `json:"mechanical_verification"`
}

type IntegrationReviewTask struct {
	ID                 string   `json:"id"`
	ProjectID          string   `json:"project_id"`
	Title              string   `json:"title"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	WriteScope         []string `json:"write_scope"`
}

type IntegrationBaselineEvidence struct {
	ID                  string                        `json:"id"`
	State               domain.ContractBaselineState  `json:"state"`
	Commit              string                        `json:"commit"`
	ApprovedFingerprint string                        `json:"approved_plan_fingerprint"`
	ProfileFingerprint  string                        `json:"profile_fingerprint"`
	Files               []domain.ContractBaselineFile `json:"files"`
}

func AssemblyOrder(attempts []domain.ShardAttempt) []domain.ShardAttempt {
	result := append([]domain.ShardAttempt(nil), attempts...)
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].WorkPackage, result[j].WorkPackage
		if left.Route != right.Route {
			return left.Route < right.Route
		}
		return left.ShardID < right.ShardID
	})
	return result
}

func (s Service) Assemble(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	return s.assemble(ctx, prepared, false)
}

func (s Service) AssembleWorkersForComposition(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	return s.assemble(ctx, prepared, true)
}

func (s Service) assemble(ctx context.Context, prepared PreparedFanout, compositionInput bool) (domain.ShardFanoutExecution, error) {
	inputs, err := s.loadApprovedInputs(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	validationPrepared := prepared
	validationExecution := execution
	if compositionInput {
		if !prepared.CompositionRequired || prepared.CompositionShard == nil || execution.BarrierReadyAt == nil || execution.Composition != "COMPOSITION_REQUIRED" {
			return domain.ShardFanoutExecution{}, fmt.Errorf("composition input assembly requires approved serial shard and persisted barrier: %w", domain.ErrInvalidStatus)
		}
		validationPrepared.CompositionRequired = false
		validationExecution.Composition = "SKIPPED_NOT_REQUIRED"
	}
	if err := validateAssemblyPreconditions(validationPrepared, validationExecution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	if prepared.CompositionRequired && !compositionInput {
		if err := validateCompositionAssemblyEvidence(prepared, execution, inputs.plan.Fingerprint); err != nil {
			return domain.ShardFanoutExecution{}, err
		}
	}

	ordered := AssemblyOrder(execution.Attempts)
	if len(ordered) != len(prepared.Workers) {
		return domain.ShardFanoutExecution{}, fmt.Errorf("attempt set does not match required worker set: %w", domain.ErrConflict)
	}
	projectID := ordered[0].WorkPackage.ProjectID
	project, exists := inputs.projects[projectID]
	if !exists {
		return domain.ShardFanoutExecution{}, fmt.Errorf("integration project disappeared: %w", domain.ErrNotFound)
	}
	for _, attempt := range ordered {
		if attempt.WorkPackage.ProjectID != projectID || attempt.Status != domain.ShardAttemptVerified || attempt.CommitSHA == "" {
			return domain.ShardFanoutExecution{}, fmt.Errorf("integration attempts do not share one verified repository: %w", domain.ErrConflict)
		}
		verifier, ok := s.Worktrees.(CommitVerifier)
		if !ok {
			return domain.ShardFanoutExecution{}, fmt.Errorf("managed worktree subsystem cannot verify shard commits: %w", domain.ErrInvalidStatus)
		}
		if err := verifier.VerifyCommit(ctx, project, attempt.Workspace, attempt.CommitSHA, prepared.BaselineCommit, attempt.ChangedFiles); err != nil {
			return domain.ShardFanoutExecution{}, err
		}
	}
	integrationIdentity := "agent-control-plane-integration:" + prepared.PlanID + ":" + projectID
	if compositionInput {
		integrationIdentity += ":composition-input:" + uuid.NewString()
	}
	if prepared.Remediation != nil {
		integrationIdentity += ":remediation:" + prepared.Remediation.ID
	}
	integrationTaskID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(integrationIdentity)).String()
	workspaceTask := domain.Task{ID: integrationTaskID, ProjectID: project.ID, Title: "Integration " + prepared.PlanID}
	workspace, err := s.Worktrees.PrepareAtCommit(ctx, project, workspaceTask, prepared.BaselineCommit)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	state, err := s.Worktrees.Inspect(ctx, project, workspace)
	if err != nil || state.HeadCommit != prepared.BaselineCommit || len(state.ChangedFiles) != 0 {
		return domain.ShardFanoutExecution{}, fmt.Errorf("integration workspace is not clean at the frozen baseline: %w", domain.ErrConflict)
	}
	applicator, ok := s.Worktrees.(interface {
		ApplyVerifiedCommit(context.Context, domain.Project, domain.TaskWorkspace, string, string, []string) (string, error)
	})
	if !ok {
		return domain.ShardFanoutExecution{}, fmt.Errorf("managed worktree subsystem cannot apply verified commits: %w", domain.ErrInvalidStatus)
	}
	tip := prepared.BaselineCommit
	for _, attempt := range ordered {
		tip, err = applicator.ApplyVerifiedCommit(ctx, project, workspace, attempt.CommitSHA, prepared.BaselineCommit, attempt.ChangedFiles)
		if err != nil {
			execution.State = "INTEGRATION_CONFLICT"
			execution.BarrierReasons = []string{attempt.WorkPackage.Route + ": " + err.Error()}
			execution.UpdatedAt = s.now()
			_ = s.Execution.SaveShardFanout(ctx, execution)
			return execution, nil
		}
	}
	if prepared.CompositionRequired && !compositionInput {
		attempt := execution.CompositionAttempt
		if attempt == nil || attempt.Status != domain.ShardAttemptVerified || attempt.CommitSHA == "" {
			return domain.ShardFanoutExecution{}, fmt.Errorf("composition is not verified: %w", domain.ErrInvalidStatus)
		}
		verifier, ok := s.Worktrees.(CommitVerifier)
		if !ok {
			return domain.ShardFanoutExecution{}, domain.ErrInvalidStatus
		}
		if err := verifier.VerifyCommit(ctx, project, attempt.Workspace, attempt.CommitSHA, attempt.WorkPackage.AssemblyCommit, attempt.ChangedFiles); err != nil {
			return domain.ShardFanoutExecution{}, err
		}
		tip, err = applicator.ApplyVerifiedCommit(ctx, project, workspace, attempt.CommitSHA, attempt.WorkPackage.AssemblyCommit, attempt.ChangedFiles)
		if err != nil {
			return domain.ShardFanoutExecution{}, err
		}
	}
	state, err = s.Worktrees.Inspect(ctx, project, workspace)
	if err != nil || state.HeadCommit != tip {
		return domain.ShardFanoutExecution{}, fmt.Errorf("integration tip failed managed workspace inspection: %w", domain.ErrConflict)
	}
	execution.AssemblyWorkspace = workspace
	execution.AssemblyCommit = tip
	execution.State = "ASSEMBLED"
	if compositionInput {
		execution.State = "WORKERS_ASSEMBLED"
	}
	execution.UpdatedAt = s.now()
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func validateAssemblyPreconditions(prepared PreparedFanout, execution domain.ShardFanoutExecution) error {
	if prepared.CompositionRequired && (execution.Composition != "VERIFIED" || execution.CompositionAttempt == nil || execution.CompositionAttempt.Status != domain.ShardAttemptVerified) {
		return fmt.Errorf("serialized Composition phase is required before integration assembly: %w", domain.ErrInvalidStatus)
	}
	if execution.Barrier != domain.ShardBarrierReady || (!prepared.CompositionRequired && execution.Composition != "SKIPPED_NOT_REQUIRED") {
		return fmt.Errorf("integration assembly cannot start before the worker barrier: %w", domain.ErrInvalidStatus)
	}
	barrier := EvaluateBarrier(preparedShardIDs(prepared), execution.Attempts, prepared.BaselineCommit)
	if barrier.State != domain.ShardBarrierReady {
		return barrierError(barrier)
	}
	if len(execution.Attempts) != len(prepared.Workers) {
		return fmt.Errorf("attempt set does not match required worker set: %w", domain.ErrConflict)
	}
	return nil
}

func (s Service) VerifyIntegration(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	inputs, err := s.loadApprovedInputs(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	if !integrationVerificationCanResume(execution) {
		return domain.ShardFanoutExecution{}, fmt.Errorf("combined integration tree is not assembled: %w", domain.ErrInvalidStatus)
	}
	project := inputs.projects[prepared.Workers[0].WorkPackage.ProjectID]
	workspace := execution.AssemblyWorkspace
	state, err := s.Worktrees.Inspect(ctx, project, workspace)
	if err != nil || state.HeadCommit != execution.AssemblyCommit {
		return domain.ShardFanoutExecution{}, fmt.Errorf("assembled integration workspace changed: %w", domain.ErrConflict)
	}
	checkedBaseline := contractbaseline.VerifyBaseline(ctx, contractbaseline.FileMaterializer{}, *project.LocalPath,
		inputs.baseline, inputs.plan.Fingerprint, mustContractPlan(inputs.plan), s.Catalog.Profiles[inputs.baseline.ProfileID],
		inputs.baseline.ProfileFingerprint, project.HeadCommit, s.now())
	checks := []domain.WorkspaceCheckResult{}
	commands := []string{"go test ./...", "go test ./... -count=1", "go vet ./...", "git diff --check"}
	for _, worker := range prepared.Workers {
		commands = append(commands, worker.WorkPackage.Verification.CandidateCommands...)
	}
	commands = uniqueCommands(commands)
	reader, _ := s.Worktrees.(frozenArtifactReader)
	workingFrozenErr := verifyWorkingFrozenFiles(ctx, reader, workspace, inputs.baseline)
	sourceWorkers := append([]PreparedShard(nil), prepared.Workers...)
	if prepared.CompositionRequired {
		if execution.CompositionAttempt == nil || execution.Composition != "VERIFIED" {
			return domain.ShardFanoutExecution{}, domain.ErrInvalidStatus
		}
		wp := execution.CompositionAttempt.WorkPackage
		sourceWorkers = append(sourceWorkers, PreparedShard{WorkPackage: domain.WorkPackage{Route: "backend.composition", WriteScope: wp.WriteScope}})
		commands = uniqueCommands(append(commands, wp.Verification.CandidateCommands...))
	}
	sourceCheck := checkIntegrationSourceBoundaries(workspace.Path, sourceWorkers)
	checks = append(checks, sourceCheck)
	passed := checkedBaseline.Validation.Passed && checkedBaseline.ExecutionState == domain.ContractBaselineFrozen && workingFrozenErr == nil && sourceCheck.ExitCode == 0
	for _, command := range commands {
		result, checkErr := s.Worktrees.RunCheck(ctx, workspace, command)
		if checkErr != nil {
			return domain.ShardFanoutExecution{}, checkErr
		}
		result.Output = bounded(result.Output, 4000)
		checks = append(checks, result)
		if result.ExitCode != 0 {
			passed = false
		}
	}
	var utcEvidence *VerifierRegressionEvidence
	if requiresUTCIntegration(prepared) {
		result, checkErr := s.verifyUTCIntegration(ctx, workspace)
		if checkErr != nil {
			return domain.ShardFanoutExecution{}, checkErr
		}
		result.Output = bounded(result.Output, 6000)
		checks = append(checks, result)
		if result.ExitCode != 0 {
			passed = false
		}
		utcEvidence = &VerifierRegressionEvidence{Source: utcIntegrationRegression, SHA256: contentHash([]byte(utcIntegrationRegression)), Ephemeral: true}
		afterCheck, inspectErr := s.Worktrees.Inspect(ctx, project, workspace)
		if inspectErr != nil || afterCheck.HeadCommit != state.HeadCommit || !samePaths(afterCheck.ChangedFiles, state.ChangedFiles) || afterCheck.Diff != state.Diff {
			return domain.ShardFanoutExecution{}, fmt.Errorf("verifier overlay changed assembled workspace: %w", domain.ErrConflict)
		}
		utcEvidence.WorkspaceUnchanged = true
	}
	report := IntegrationCheckReport{
		HTTPDelegation:   utcEvidence,
		UTCCompatibility: utcEvidence,
		Passed:           passed, ChangedFiles: append([]string(nil), state.ChangedFiles...), Checks: checks,
		FrozenHashesPassed:       checkedBaseline.Validation.Passed && workingFrozenErr == nil,
		ArchitectureChecksPassed: sourceCheck.ExitCode == 0, NoUnsafeConstructionPassed: sourceCheck.ExitCode == 0, VerifiedAt: s.now(),
	}
	encoded, _ := json.Marshal(report)
	execution.IntegrationVerification = encoded
	execution.UpdatedAt = report.VerifiedAt
	if passed {
		execution.State = "INTEGRATION_MECHANICALLY_VERIFIED"
	} else {
		execution.State = "INTEGRATION_VERIFICATION_FAILED"
	}
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func (s Service) ReviewIntegration(ctx context.Context, prepared PreparedFanout) (domain.ShardFanoutExecution, error) {
	inputs, err := s.loadApprovedInputs(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution, err := s.Execution.GetShardFanout(ctx, prepared.PlanID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	if execution.State != "INTEGRATION_MECHANICALLY_VERIFIED" || execution.IntegrationVerification == nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("independent review cannot start before mechanical integration verification: %w", domain.ErrInvalidStatus)
	}
	if s.Runner == nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("independent reviewer AgentRunner is unavailable: %w", domain.ErrInvalidStatus)
	}
	projectID := prepared.Workers[0].WorkPackage.ProjectID
	project := inputs.projects[projectID]
	state, err := s.Worktrees.Inspect(ctx, project, execution.AssemblyWorkspace)
	if err != nil || state.HeadCommit != execution.AssemblyCommit {
		return domain.ShardFanoutExecution{}, fmt.Errorf("assembled tree changed before reviewer start: %w", domain.ErrConflict)
	}
	var plannerOutput domain.PlannerOutput
	if err := json.Unmarshal(inputs.plan.PlannerOutput, &plannerOutput); err != nil || plannerOutput.Routing == nil || plannerOutput.ContractPlan == nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("approved routing or contract plan is unavailable for independent review: %w", domain.ErrValidation)
	}
	packet := makeReviewPacket(inputs, execution, *plannerOutput.Routing, *plannerOutput.ContractPlan, state.Diff)
	// The previous verdict remains authoritative evidence for the new independent review.
	remediationContext, _ := json.Marshal(prepared)
	task := inputs.tasks[prepared.Workers[0].WorkPackage.TaskID]
	decision := s.Router.Reviewer(task)
	request := domain.AgentRunRequest{
		Role: domain.AgentRunReviewer, WorkingDirectory: execution.AssemblyWorkspace.Path,
		Model: decision.Model, ReasoningEffort: decision.Reasoning,
		UsageContext: &domain.AgentUsageContext{
			ResourceType: "plan_shard_integration", ResourceID: prepared.PlanID, RouteReason: "independent integration review",
		},
		Prompt:       "Independently review the complete canary integration in read-only mode. Do not edit files. Check task acceptance within approved scope, every shard's local responsibility, frozen contract hashes, cross-layer boundaries, side effects, integration consistency, and real RED/GREEN and mechanical evidence. Explicitly verify public constructors without reflect/unsafe creation, HTTP nonzero UTC offsets rejected before delegation (Z and +00:00 allowed), and real PostgreSQL left half-open boundary cases. Explicitly verify zero-offset numeric +00:00 works with the real Usecase (not Location object identity) and the verifier-owned real HTTP/usecase regression; no fake ApplicationCommandResult can establish compatibility. Verify both independent ephemeral verifier levels: HTTP handler with counting application spy for zero calls before nonzero-offset rejection, and real HTTP handler -> real Usecase -> test domain RepositoryPort for Z/+00:00 compatibility and invalid ranges. These verifier-owned sources must be removed after checks and remain independent of worker/composition tests. For a composition-required Plan, verify typed composition package and upstream commits, composition started strictly after persisted BARRIER_READY, workers never wrote cmd, composition changed only approved wiring paths with no business/SQL/HTTP feature behavior, frozen hashes stayed identical, and real application/router through real usecase and real persistence into disposable PostgreSQL passes the uncached same RED/GREEN test. Fakes cannot establish the primary application success path. For a composition-excluded Plan, no wiring is required. Return only verdict PASS, REJECT, or REPLAN_REQUIRED with concise findings. Packet:\n" + compactJSON(packet) + "\nPrepared remediation context:\n" + string(remediationContext),
		OutputSchema: integrationReviewSchema(),
		SandboxScope: &domain.AgentSandboxScope{Profile: "reviewer", WritePaths: []string{}},
	}
	response, err := s.Runner.Run(ctx, request, func(callbackCtx context.Context, threadID string) error {
		execution.ReviewerThreadID = threadID
		execution.ReviewerModel = decision.Model
		execution.ReviewerReasoningEffort = decision.Reasoning
		execution.UpdatedAt = s.now()
		return s.Execution.SaveShardFanout(callbackCtx, execution)
	})
	if err != nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("run independent integration reviewer: %w", err)
	}
	afterReview, inspectErr := s.Worktrees.Inspect(ctx, project, execution.AssemblyWorkspace)
	if inspectErr != nil || afterReview.HeadCommit != state.HeadCommit || !samePaths(afterReview.ChangedFiles, state.ChangedFiles) || afterReview.Diff != state.Diff {
		execution.State = "INTEGRATION_REJECTED"
		execution.ReviewerVerdict = "REJECT"
		execution.ReviewerFindings = []string{"read-only integration reviewer changed the assembled workspace"}
		execution.UpdatedAt = s.now()
		if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
			return domain.ShardFanoutExecution{}, err
		}
		return execution, nil
	}
	var review IntegrationReviewerDecision
	if err := strictJSON(response.Result, &review); err != nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("decode integration reviewer verdict: %w", err)
	}
	if response.ThreadID == "" || review.Verdict != "PASS" && review.Verdict != "REJECT" && review.Verdict != "REPLAN_REQUIRED" {
		return domain.ShardFanoutExecution{}, fmt.Errorf("integration reviewer result is invalid: %w", domain.ErrValidation)
	}
	execution.ReviewerThreadID = response.ThreadID
	execution.ReviewerModel = decision.Model
	execution.ReviewerReasoningEffort = decision.Reasoning
	execution, err = applyIntegrationReviewerDecision(execution, review)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	execution.UpdatedAt = s.now()
	if err := s.Execution.SaveShardFanout(ctx, execution); err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	return execution, nil
}

func applyIntegrationReviewerDecision(execution domain.ShardFanoutExecution, review IntegrationReviewerDecision) (domain.ShardFanoutExecution, error) {
	execution.ReviewerVerdict = review.Verdict
	execution.ReviewerFindings = cleanSorted(review.Findings)
	switch review.Verdict {
	case "PASS":
		if execution.State != "INTEGRATION_MECHANICALLY_VERIFIED" || execution.IntegrationVerification == nil {
			return execution, fmt.Errorf("review cannot mark an integration verified before mechanical checks: %w", domain.ErrInvalidStatus)
		}
		execution.State = "INTEGRATION_VERIFIED"
	case "REJECT":
		execution.State = "INTEGRATION_REJECTED"
	case "REPLAN_REQUIRED":
		execution.State = "REPLAN_REQUIRED"
	default:
		return execution, fmt.Errorf("unknown independent integration verdict %q: %w", review.Verdict, domain.ErrValidation)
	}
	return execution, nil
}

func makeReviewPacket(inputs approvedInputs, execution domain.ShardFanoutExecution, routing domain.RoutingResult, contractPlan domain.ContractPlan, diff string) IntegrationReviewPacket {
	var packet IntegrationReviewPacket
	packet.Plan.ID = inputs.plan.ID
	packet.Plan.Status = inputs.plan.Status
	packet.Plan.Fingerprint = inputs.plan.Fingerprint
	if inputs.plan.ApprovedFingerprint != nil {
		packet.Plan.ApprovedFingerprint = *inputs.plan.ApprovedFingerprint
	}
	packet.Plan.Summary = inputs.plan.Summary
	packet.Tasks = []IntegrationReviewTask{}
	for _, task := range inputs.tasks {
		packet.Tasks = append(packet.Tasks, IntegrationReviewTask{
			ID: task.ID, ProjectID: task.ProjectID, Title: task.Title,
			AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...), WriteScope: append([]string(nil), task.WriteScope...),
		})
	}
	sort.Slice(packet.Tasks, func(i, j int) bool { return packet.Tasks[i].ID < packet.Tasks[j].ID })
	packet.Routing = routing
	packet.ContractPlan = contractPlan
	packet.Baseline = IntegrationBaselineEvidence{
		ID: inputs.baseline.ID, State: inputs.baseline.ExecutionState,
		Commit:              inputs.baseline.ContractBaselineCommit,
		ApprovedFingerprint: inputs.baseline.ApprovedPlanFingerprint,
		ProfileFingerprint:  inputs.baseline.ProfileFingerprint,
		Files:               append([]domain.ContractBaselineFile(nil), inputs.baseline.Files...),
	}
	packet.WorkPackages = []domain.WorkPackage{}
	for _, attempt := range execution.Attempts {
		packet.WorkPackages = append(packet.WorkPackages, attempt.WorkPackage)
	}
	packet.Workers = append([]domain.ShardAttempt(nil), execution.Attempts...)
	packet.Composition = execution.CompositionAttempt
	packet.BarrierReadyAt = execution.BarrierReadyAt
	packet.AssembledDiff = bounded(diff, 24_000)
	packet.Mechanical = append(json.RawMessage(nil), execution.IntegrationVerification...)
	return packet
}

func integrationReviewSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"verdict", "findings"},
		"properties": map[string]any{
			"verdict":  map[string]any{"type": "string", "enum": []string{"PASS", "REJECT", "REPLAN_REQUIRED"}},
			"findings": stringArraySchema(),
		},
	}
}

func mustContractPlan(plan domain.Plan) domain.ContractPlan {
	var output domain.PlannerOutput
	_ = json.Unmarshal(plan.PlannerOutput, &output)
	if output.ContractPlan == nil {
		return domain.ContractPlan{}
	}
	return *output.ContractPlan
}

func preparedShardIDs(prepared PreparedFanout) []string {
	result := make([]string, 0, len(prepared.Workers))
	for _, worker := range prepared.Workers {
		result = append(result, worker.WorkPackage.ShardID)
	}
	sort.Strings(result)
	return result
}

func validateCompositionAssemblyEvidence(prepared PreparedFanout, execution domain.ShardFanoutExecution, fingerprint string) error {
	a := execution.CompositionAttempt
	if a == nil || a.Status != domain.ShardAttemptVerified || a.Red == nil || !a.Red.Semantic || a.Green == nil || !a.Green.Passed || a.CommitSHA == "" || execution.BarrierReadyAt == nil || a.StartedAt.Before(*execution.BarrierReadyAt) {
		return fmt.Errorf("composition lacks verified serialized RED/GREEN evidence: %w", domain.ErrConflict)
	}
	wp := a.WorkPackage
	if wp.PlanID != prepared.PlanID || wp.PlanFingerprint != fingerprint || wp.ExecutionBase.Revision != prepared.BaselineCommit || wp.ExecutionBase.ContractBaselineID != prepared.BaselineID || a.Workspace.BaseCommit != wp.AssemblyCommit || !compositionScopeAllows(wp, a.ChangedFiles) {
		return fmt.Errorf("composition baseline, approval or scope differs: %w", domain.ErrConflict)
	}
	id := wp.ID
	wp.ID = ""
	bytes, err := json.Marshal(wp)
	if err != nil || id != "sha256:"+contentHash(bytes) {
		return fmt.Errorf("composition package identity changed: %w", domain.ErrConflict)
	}
	ordered := AssemblyOrder(execution.Attempts)
	if len(wp.EffectiveCommits) != len(ordered) {
		return domain.ErrConflict
	}
	for i, worker := range ordered {
		entry := wp.EffectiveCommits[i]
		if entry.ShardID != worker.ShardID || entry.Route != worker.WorkPackage.Route || entry.CommitSHA != worker.CommitSHA || entry.BaselineCommit != prepared.BaselineCommit {
			return fmt.Errorf("composition upstream verified commits changed: %w", domain.ErrConflict)
		}
	}
	return nil
}
