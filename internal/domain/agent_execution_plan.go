package domain

import "time"

type ContractReference struct {
	Kind                string `json:"kind"`
	RepositoryProjectID string `json:"repository_project_id"`
	Path                string `json:"path"`
	Symbol              string `json:"symbol,omitempty"`
	RouteID             string `json:"route_id"`
	Relation            string `json:"relation"`
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
