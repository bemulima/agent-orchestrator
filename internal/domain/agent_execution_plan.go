package domain

import "time"

type ShardStatus string

const (
	ShardStatusPlanned     ShardStatus = "PLANNED"
	ShardStatusComposition ShardStatus = "COMPOSITION_REQUIRED"
	ShardStatusOwnerReview ShardStatus = "OWNER_REVIEW_REQUIRED"
)

type ShardRiskAssessment struct {
	Level   RiskLevel `json:"level"`
	Reasons []string  `json:"reasons,omitempty"`
}

type ShardWriteScope struct {
	Allow             []string `json:"allow"`
	Deny              []string `json:"deny"`
	ReadOnlyContracts []string `json:"read_only_contracts,omitempty"`
	CompositionOnly   []string `json:"composition_only,omitempty"`
	TestPaths         []string `json:"test_paths,omitempty"`
	MaxFiles          int      `json:"max_files"`
}

type ContractReference struct {
	Kind                string `json:"kind"`
	RepositoryProjectID string `json:"repository_project_id"`
	Path                string `json:"path"`
	Symbol              string `json:"symbol,omitempty"`
	RouteID             string `json:"route_id"`
	Relation            string `json:"relation"`
}

type ShardVerification struct {
	Kind        string   `json:"kind"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Commands    []string `json:"commands,omitempty"`
}

type ShardExecutionBase struct {
	Kind               string `json:"kind"`
	Revision           string `json:"revision,omitempty"`
	ContractBaselineID string `json:"contract_baseline_id,omitempty"`
}

const (
	ShardBaseSourceRevision   = "SOURCE_REVISION"
	ShardBaseContractBaseline = "CONTRACT_BASELINE"
)

// ArchitecturalShard is an internal execution design unit. It never creates
// or replaces a project/repository-level Plan Task.
type ArchitecturalShard struct {
	ID                  string              `json:"id"`
	PlanID              string              `json:"plan_id"`
	TaskID              string              `json:"task_id"`
	RepositoryProjectID string              `json:"repository_project_id"`
	Repository          string              `json:"repository"`
	ProfileID           string              `json:"profile_id"`
	ProfileFingerprint  string              `json:"profile_fingerprint"`
	RouteID             string              `json:"route_id"`
	ExecutionBase       ShardExecutionBase  `json:"execution_base"`
	LocalIntent         string              `json:"local_intent"`
	Targets             RoutedTarget        `json:"targets"`
	Consumes            []ContractReference `json:"consumes,omitempty"`
	Implements          []ContractReference `json:"implements,omitempty"`
	WriteScope          ShardWriteScope     `json:"write_scope"`
	DependsOn           []string            `json:"depends_on,omitempty"`
	Acceptance          []string            `json:"acceptance"`
	Risk                ShardRiskAssessment `json:"risk"`
	Verification        []ShardVerification `json:"verification"`
	Confidence          float64             `json:"confidence"`
	Status              ShardStatus         `json:"status"`
	Parallel            bool                `json:"parallel"`
	Phase               string              `json:"phase"`
	CreatedAt           time.Time           `json:"created_at"`
}

type ContractBaselineState string

const (
	ContractBaselineNotRequired   ContractBaselineState = "NOT_REQUIRED"
	ContractBaselinePending       ContractBaselineState = "PENDING"
	ContractBaselineMaterializing ContractBaselineState = "MATERIALIZING"
	ContractBaselineFrozen        ContractBaselineState = "FROZEN"
	ContractBaselineInvalidated   ContractBaselineState = "INVALIDATED"
	ContractBaselineBlocked       ContractBaselineState = "BLOCKED"
)

type ContractBaselineFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Generated bool   `json:"generated"`
}

type ContractBaselineValidation struct {
	Passed           bool     `json:"passed"`
	InspectionFailed bool     `json:"inspection_failed,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
}

type ContractBaseline struct {
	PredecessorBaselineID      string                     `json:"predecessor_baseline_id,omitempty"`
	ID                         string                     `json:"id"`
	PlanID                     string                     `json:"plan_id"`
	TaskID                     string                     `json:"task_id"`
	RepositoryProjectID        string                     `json:"repository_project_id"`
	Repository                 string                     `json:"repository"`
	ProfileID                  string                     `json:"profile_id"`
	PlanningContractState      ContractPlanState          `json:"planning_contract_state"`
	ExecutionState             ContractBaselineState      `json:"execution_state"`
	ApprovedPlanFingerprint    string                     `json:"approved_plan_fingerprint"`
	PlannedContracts           []ContractReference        `json:"planned_contracts,omitempty"`
	MaterializedContracts      []ContractReference        `json:"materialized_contracts,omitempty"`
	Files                      []ContractBaselineFile     `json:"files,omitempty"`
	MaterializerChanges        []string                   `json:"materializer_changes,omitempty"`
	AgentChanges               []string                   `json:"agent_changes,omitempty"`
	BaselineChanges            []string                   `json:"baseline_changes,omitempty"`
	AggregateSHA256            string                     `json:"aggregate_sha256,omitempty"`
	ContractBaselineCommit     string                     `json:"contract_baseline_commit,omitempty"`
	ContractAgentPassed        bool                       `json:"contract_agent_passed"`
	ContractAgentThreadID      string                     `json:"contract_agent_thread_id,omitempty"`
	ContractAgentResultSHA256  string                     `json:"contract_agent_result_sha256,omitempty"`
	MechanicalReviewPassed     bool                       `json:"mechanical_review_passed"`
	MechanicalReviewSHA256     string                     `json:"mechanical_review_sha256,omitempty"`
	ContractVerificationPassed bool                       `json:"contract_verification_passed"`
	ContractVerification       string                     `json:"contract_verification,omitempty"`
	IndependentReviewPassed    bool                       `json:"independent_review_passed"`
	IndependentReviewerID      string                     `json:"independent_reviewer_id,omitempty"`
	IndependentReviewStatus    string                     `json:"independent_review_status,omitempty"`
	IndependentReviewSHA256    string                     `json:"independent_review_sha256,omitempty"`
	IndependentReviewFindings  []string                   `json:"independent_review_findings,omitempty"`
	ContractPlanFingerprint    string                     `json:"contract_plan_fingerprint"`
	ProfileFingerprint         string                     `json:"profile_fingerprint"`
	RepositoryRevision         string                     `json:"repository_revision"`
	Validation                 ContractBaselineValidation `json:"validation"`
	CreatedAt                  time.Time                  `json:"created_at"`
	UpdatedAt                  time.Time                  `json:"updated_at"`
}

type FanoutReadinessState string

const (
	FanoutReadinessReady       FanoutReadinessState = "READY_FOR_FANOUT"
	FanoutReadinessBlocked     FanoutReadinessState = "BLOCKED"
	FanoutReadinessOwnerReview FanoutReadinessState = "OWNER_REVIEW_REQUIRED"
)

const FanoutParallelismScopeSameRepository = "same_repository_only"

type FanoutReadinessEvidence struct {
	PlanID              string               `json:"plan_id"`
	State               FanoutReadinessState `json:"state"`
	ParallelismScope    string               `json:"parallelism_scope"`
	Reasons             []string             `json:"reasons,omitempty"`
	TaskIDs             []string             `json:"task_ids"`
	ShardIDs            []string             `json:"shard_ids"`
	BaselineIDs         []string             `json:"baseline_ids"`
	ProfileFingerprints map[string]string    `json:"profile_fingerprints"`
	RecordedAt          time.Time            `json:"recorded_at"`
}
