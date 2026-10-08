package domain

import (
	"encoding/json"
	"time"
)

// CompositionWorkPackage authorizes only serialized assembly code after the
// verified layer barrier. It is deliberately distinct from a layer WorkPackage.
type CompositionWorkPackage struct {
	SchemaVersion    int                      `json:"schema_version"`
	ID               string                   `json:"id"`
	ShardID          string                   `json:"shard_id"`
	PlanID           string                   `json:"plan_id"`
	PlanFingerprint  string                   `json:"plan_fingerprint"`
	TaskID           string                   `json:"task_id"`
	ProjectID        string                   `json:"project_id"`
	Repository       string                   `json:"repository"`
	Route            string                   `json:"route"`
	ExecutionBase    ShardExecutionBase       `json:"execution_base"`
	AssemblyCommit   string                   `json:"assembly_commit"`
	EffectiveCommits []CompositionShardCommit `json:"effective_commits"`
	WriteScope       ShardWriteScope          `json:"write_scope"`
	ReadOnlyPaths    []string                 `json:"readonly_paths"`
	APIs             []CompositionAPI         `json:"apis"`
	Verification     WorkPackageVerification  `json:"verification"`
	LocalIntent      string                   `json:"local_intent"`
}

type CompositionShardCommit struct {
	ShardID        string `json:"shard_id"`
	Route          string `json:"route"`
	CommitSHA      string `json:"commit_sha"`
	BaselineCommit string `json:"baseline_commit"`
}

// Signature is parsed from actual assembled source, never inferred by a worker.
type CompositionAPI struct {
	Path      string `json:"path"`
	Package   string `json:"package"`
	Kind      string `json:"kind"`
	Symbol    string `json:"symbol"`
	Signature string `json:"signature"`
}

type CompositionRecoveryEvidence struct {
	RecordedAt          time.Time          `json:"recorded_at"`
	PriorState          string             `json:"prior_state"`
	PriorStatus         ShardAttemptStatus `json:"prior_status"`
	PriorBlockers       []string           `json:"prior_blockers"`
	PriorBarrierReasons []string           `json:"prior_barrier_reasons"`
	PriorFinishedAt     *time.Time         `json:"prior_finished_at"`
	Reason              string             `json:"reason"`
}

type CompositionFixtureReplay struct {
	PriorAttemptID     string            `json:"prior_attempt_id"`
	PriorCommit        string            `json:"prior_commit"`
	PriorWorkPackageID string            `json:"prior_work_package_id"`
	NewWorkPackageID   string            `json:"new_work_package_id"`
	RecordedAt         time.Time         `json:"recorded_at"`
	SourceHashes       map[string]string `json:"source_hashes"`
}

type CompositionBudgetBlocker struct {
	RecordedAt                     time.Time `json:"recorded_at"`
	Phase                          string    `json:"phase"`
	Reason                         string    `json:"reason"`
	RemainingCompositionAgentCalls int       `json:"remaining_composition_agent_calls"`
	AdditionalRunsRequired         int       `json:"additional_runs_required"`
	Mode                           string    `json:"mode,omitempty"`
	Limit                          int64     `json:"limit,omitempty"`
	FiveHourRuns                   int64     `json:"five_hour_runs,omitempty"`
	CountAvailable                 bool      `json:"count_available"`
}

type CompositionAttempt struct {
	FixtureReplay             *CompositionFixtureReplay     `json:"fixture_replay,omitempty"`
	BudgetBlocker             *CompositionBudgetBlocker     `json:"budget_blocker,omitempty"`
	PriorCompositionAttemptID string                        `json:"prior_composition_attempt_id,omitempty"`
	ReuseDecision             string                        `json:"reuse_decision,omitempty"`
	RecoveryHistory           []CompositionRecoveryEvidence `json:"recovery_history,omitempty"`
	ID                        string                        `json:"id"`
	WorkPackage               CompositionWorkPackage        `json:"work_package"`
	BaselineCommit            string                        `json:"baseline_commit"`
	REDSetup                  *ShardREDSetupEvidence        `json:"red_setup,omitempty"`
	Workspace                 TaskWorkspace                 `json:"workspace"`
	Phase                     ShardExecutionPhase           `json:"phase"`
	PhaseHistory              []ShardPhaseEvent             `json:"phase_history"`
	WorkerThreadID            string                        `json:"worker_thread_id,omitempty"`
	Model                     string                        `json:"model,omitempty"`
	ReasoningEffort           string                        `json:"reasoning_effort,omitempty"`
	Red                       *ShardREDEvidence             `json:"red,omitempty"`
	Green                     *ShardGreenEvidence           `json:"green,omitempty"`
	Implementation            *WorkerResult                 `json:"implementation_result,omitempty"`
	Verification              json.RawMessage               `json:"verification,omitempty"`
	ChangedFiles              []string                      `json:"changed_files,omitempty"`
	CommitSHA                 string                        `json:"commit_sha,omitempty"`
	Status                    ShardAttemptStatus            `json:"status"`
	Blockers                  []string                      `json:"blockers,omitempty"`
	StartedAt                 time.Time                     `json:"started_at"`
	FinishedAt                *time.Time                    `json:"finished_at,omitempty"`
	UpdatedAt                 time.Time                     `json:"updated_at"`
}
