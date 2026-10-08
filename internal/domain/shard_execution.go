package domain

import (
	"encoding/json"
	"time"
)

// WorkPackage is the bounded, approved input for one architectural worker.
// It intentionally excludes the original business prompt and unrelated plan context.
type WorkPackage struct {
	SchemaVersion       int                     `json:"schema_version"`
	ShardID             string                  `json:"shard_id"`
	PlanID              string                  `json:"plan_id"`
	TaskID              string                  `json:"task_id"`
	ProjectID           string                  `json:"project_id"`
	Repository          string                  `json:"repository"`
	ArchitectureProfile WorkPackageProfile      `json:"architecture_profile"`
	Route               string                  `json:"route"`
	ExecutionBase       ShardExecutionBase      `json:"execution_base"`
	LocalIntent         string                  `json:"local_intent"`
	Contracts           WorkPackageContracts    `json:"contracts"`
	Targets             WorkPackageTargets      `json:"targets"`
	WriteScope          ShardWriteScope         `json:"write_scope"`
	Verification        WorkPackageVerification `json:"verification"`
	SemanticInvariants  []string                `json:"semantic_invariants"`
	BlockersAllowed     []string                `json:"blockers_allowed"`
}

type WorkPackageProfile struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

type WorkPackageContracts struct {
	Consumes      []ContractReference `json:"consumes,omitempty"`
	Implements    []ContractReference `json:"implements,omitempty"`
	ReadOnlyPaths []string            `json:"readonly_paths"`
}

type WorkPackageTargets struct {
	Paths   []string `json:"paths"`
	Symbols []string `json:"symbols,omitempty"`
}

type WorkPackageVerification struct {
	Boundary          string   `json:"boundary"`
	CandidateCommands []string `json:"candidate_commands"`
	TestPaths         []string `json:"test_paths"`
}

type ShardAttemptStatus string

const (
	ShardAttemptRunning        ShardAttemptStatus = "RUNNING"
	ShardAttemptVerified       ShardAttemptStatus = "VERIFIED"
	ShardAttemptBlocked        ShardAttemptStatus = "SHARD_BLOCKED"
	ShardAttemptFailed         ShardAttemptStatus = "SHARD_FAILED"
	ShardAttemptReplan         ShardAttemptStatus = "REPLAN_REQUIRED"
	ShardAttemptContractChange ShardAttemptStatus = "CONTRACT_CHANGE_REQUIRED"
)

type ShardREDEvidence struct {
	Command              string    `json:"command"`
	ExpectedFailure      string    `json:"expected_failure"`
	ObservedFailure      string    `json:"observed_failure"`
	Semantic             bool      `json:"semantic"`
	TestPaths            []string  `json:"test_paths"`
	RecordedAt           time.Time `json:"recorded_at"`
	PreImplementationSHA string    `json:"pre_implementation_sha,omitempty"`
}

type ShardGreenEvidence struct {
	Commands   []WorkspaceCheckResult `json:"commands"`
	Passed     bool                   `json:"passed"`
	RecordedAt time.Time              `json:"recorded_at"`
}

// WorkerResult is the strict, structured report returned by one worker phase.
// Orchestrator verification remains authoritative for completion.
type WorkerResult struct {
	Status                  string             `json:"status"`
	ShardID                 string             `json:"shard_id"`
	BaselineRevision        string             `json:"baseline_revision"`
	WorkerThread            string             `json:"worker_thread"`
	Red                     ShardREDEvidence   `json:"red"`
	ChangedFiles            []string           `json:"changed_files"`
	Green                   ShardGreenEvidence `json:"green"`
	ImplementedContracts    []string           `json:"implemented_contracts"`
	Blockers                []string           `json:"blockers"`
	ContractChangeRequested bool               `json:"contract_change_requested"`
}

type WorkerPhaseResult struct {
	Status                  string           `json:"status"`
	ShardID                 string           `json:"shard_id"`
	BaselineRevision        string           `json:"baseline_revision"`
	WorkerThread            string           `json:"worker_thread"`
	Red                     ShardREDEvidence `json:"red"`
	ChangedFiles            []string         `json:"changed_files"`
	Blockers                []string         `json:"blockers"`
	ContractChangeRequested bool             `json:"contract_change_requested"`
}

type ShardExecutionPhase string

const (
	ShardPhasePrepared      ShardExecutionPhase = "PREPARED"
	ShardPhaseREDSetup      ShardExecutionPhase = "RED_SETUP"
	ShardPhaseREDVerified   ShardExecutionPhase = "RED_VERIFIED"
	ShardPhaseImplementing  ShardExecutionPhase = "IMPLEMENTING"
	ShardPhaseGREENVerified ShardExecutionPhase = "GREEN_VERIFIED"
	ShardPhaseVerified      ShardExecutionPhase = "VERIFIED"
)

type ShardPhaseEvent struct {
	Phase      ShardExecutionPhase `json:"phase"`
	RecordedAt time.Time           `json:"recorded_at"`
}

type ShardREDSetupEvidence struct {
	Sources          map[string]string `json:"sources"`
	ChangedFiles     []string          `json:"changed_files"`
	Files            map[string]string `json:"files"`
	SnapshotSHA      string            `json:"snapshot_sha"`
	MechanicalReview string            `json:"mechanical_review"`
	RecordedAt       time.Time         `json:"recorded_at"`
}

type ShardRemediationPreimage struct {
	RequestID      string            `json:"request_id"`
	PriorCommit    string            `json:"prior_commit"`
	BaselineCommit string            `json:"baseline_commit"`
	Sources        map[string]string `json:"sources"`
	Hashes         map[string]string `json:"hashes"`
	RecordedAt     time.Time         `json:"recorded_at"`
}

type ShardAttemptFormatRecovery struct {
	RecordedAt          time.Time          `json:"recorded_at"`
	PriorStatus         ShardAttemptStatus `json:"prior_status"`
	PriorBlockers       []string           `json:"prior_blockers"`
	PriorFinishedAt     *time.Time         `json:"prior_finished_at"`
	CandidateTestHashes map[string]string  `json:"candidate_test_hashes"`
	Reason              string             `json:"reason"`
}

type ShardAttempt struct {
	FormatRecoveryHistory []ShardAttemptFormatRecovery `json:"format_recovery_history,omitempty"`
	RemediationPreimage   *ShardRemediationPreimage    `json:"remediation_preimage,omitempty"`
	Phase                 ShardExecutionPhase          `json:"phase,omitempty"`
	PhaseHistory          []ShardPhaseEvent            `json:"phase_history,omitempty"`
	REDSetup              *ShardREDSetupEvidence       `json:"red_setup,omitempty"`
	ID                    string                       `json:"id"`
	PlanID                string                       `json:"plan_id"`
	TaskID                string                       `json:"task_id"`
	ShardID               string                       `json:"shard_id"`
	AttemptNumber         int                          `json:"attempt_number"`
	WorkPackage           WorkPackage                  `json:"work_package"`
	BaselineCommit        string                       `json:"baseline_commit"`
	Workspace             TaskWorkspace                `json:"workspace"`
	WorkerThreadID        string                       `json:"worker_thread_id,omitempty"`
	Model                 string                       `json:"model,omitempty"`
	ReasoningEffort       string                       `json:"reasoning_effort,omitempty"`
	Red                   *ShardREDEvidence            `json:"red,omitempty"`
	Implementation        *WorkerResult                `json:"implementation_result,omitempty"`
	Green                 *ShardGreenEvidence          `json:"green,omitempty"`
	Verification          json.RawMessage              `json:"verification,omitempty"`
	ChangedFiles          []string                     `json:"changed_files,omitempty"`
	CommitSHA             string                       `json:"commit_sha,omitempty"`
	Status                ShardAttemptStatus           `json:"status"`
	Blockers              []string                     `json:"blockers,omitempty"`
	StartedAt             time.Time                    `json:"started_at"`
	WorkerStartedAt       time.Time                    `json:"worker_started_at"`
	FinishedAt            *time.Time                   `json:"finished_at,omitempty"`
	CreatedAt             time.Time                    `json:"created_at"`
	UpdatedAt             time.Time                    `json:"updated_at"`
}

type ShardBarrierState string

const (
	ShardBarrierPending        ShardBarrierState = "PENDING"
	ShardBarrierReady          ShardBarrierState = "BARRIER_READY"
	ShardBarrierBlocked        ShardBarrierState = "BARRIER_BLOCKED"
	ShardBarrierReplanRequired ShardBarrierState = "REPLAN_REQUIRED"
)

type ShardFanoutExecution struct {
	CompositionHistory      []CompositionAttempt      `json:"composition_history,omitempty"`
	CompositionAttempt      *CompositionAttempt       `json:"composition_attempt,omitempty"`
	BarrierReadyAt          *time.Time                `json:"barrier_ready_at,omitempty"`
	PlanID                  string                    `json:"plan_id"`
	ContractBaselineID      string                    `json:"contract_baseline_id"`
	BaselineCommit          string                    `json:"baseline_commit"`
	SourcePreflights        []SourcePreflightEvidence `json:"source_preflights,omitempty"`
	State                   string                    `json:"state"`
	Barrier                 ShardBarrierState         `json:"barrier"`
	BarrierReasons          []string                  `json:"barrier_reasons,omitempty"`
	Composition             string                    `json:"composition"`
	MaxSimultaneousWorkers  int                       `json:"max_simultaneous_workers"`
	AssemblyWorkspace       TaskWorkspace             `json:"assembly_workspace,omitempty"`
	AssemblyCommit          string                    `json:"assembly_commit,omitempty"`
	IntegrationVerification json.RawMessage           `json:"integration_verification,omitempty"`
	ReviewerThreadID        string                    `json:"reviewer_thread_id,omitempty"`
	ReviewerModel           string                    `json:"reviewer_model,omitempty"`
	ReviewerReasoningEffort string                    `json:"reviewer_reasoning_effort,omitempty"`
	ReviewerVerdict         string                    `json:"reviewer_verdict,omitempty"`
	ReviewerFindings        []string                  `json:"reviewer_findings,omitempty"`
	Attempts                []ShardAttempt            `json:"attempts"`
	CreatedAt               time.Time                 `json:"created_at"`
	UpdatedAt               time.Time                 `json:"updated_at"`
}

type SourcePreflightStatus string

const (
	SourcePreflightVerified         SourcePreflightStatus = "VERIFIED"
	SourcePreflightDrift            SourcePreflightStatus = "DRIFT"
	SourcePreflightInspectionFailed SourcePreflightStatus = "INSPECTION_FAILED"
)

// SourcePreflightEvidence records what the orchestrator could prove about the
// connected source checkout before permitting shard execution.
type SourcePreflightEvidence struct {
	RepositoryPath         string                `json:"repository_path"`
	ProjectID              string                `json:"project_id"`
	ExpectedRevision       string                `json:"expected_revision"`
	ActualRevision         string                `json:"actual_revision,omitempty"`
	ExpectedClean          bool                  `json:"expected_clean"`
	ActualStatus           string                `json:"actual_status"`
	ContractBaselineCommit string                `json:"contract_baseline_commit"`
	ExpectedIdentity       string                `json:"expected_identity,omitempty"`
	ActualIdentity         string                `json:"actual_identity,omitempty"`
	InspectionStatus       SourcePreflightStatus `json:"inspection_status"`
	ReasonCode             string                `json:"reason_code"`
	Error                  string                `json:"error,omitempty"`
	RecordedAt             time.Time             `json:"recorded_at"`
}

var ShardExecutionBlockers = []string{
	"CONTRACT_CHANGE_REQUIRED", "OUT_OF_SCOPE_CHANGE_REQUIRED", "DEPENDENCY_NOT_READY",
	"ARCHITECTURE_CONFLICT", "TEST_BOUNDARY_MISSING", "BASELINE_STALE",
}
