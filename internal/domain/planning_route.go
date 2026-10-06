package domain

// PlannerMetadataVersionV1 marks plans enriched with evidence-backed
// architecture routing and a contract-plan decision. Version zero is reserved
// for persisted plans created before routing metadata existed.
const PlannerMetadataVersionV1 = 1

type RoutingStatus string

const (
	RoutingStatusResolved   RoutingStatus = "resolved"
	RoutingStatusPartial    RoutingStatus = "partial"
	RoutingStatusUnresolved RoutingStatus = "unresolved"
)

type ProfileResolutionStatus string

const (
	ProfileResolutionResolved   ProfileResolutionStatus = "resolved"
	ProfileResolutionUnresolved ProfileResolutionStatus = "unresolved"
)

type ContractPlanState string

const (
	ContractPlanNotRequired  ContractPlanState = "NOT_REQUIRED"
	ContractPlanPlanned      ContractPlanState = "PLANNED"
	ContractPlanFreezeNeeded ContractPlanState = "FREEZE_REQUIRED"
)

type VerificationEvidenceState string

const (
	VerificationEvidenceExisting VerificationEvidenceState = "existing"
	VerificationEvidenceToAdd    VerificationEvidenceState = "to_add"
)

type RouteReference struct {
	ProjectID string `json:"project_id"`
	RouteID   string `json:"route_id"`
}

type RoutePolarity string

const (
	RoutePolarityPositive    RoutePolarity = "positive"
	RoutePolarityNegative    RoutePolarity = "negative"
	RoutePolarityConditional RoutePolarity = "conditional"
	RoutePolarityConflicting RoutePolarity = "conflicting"
	RoutePolarityNeutral     RoutePolarity = "neutral"
)

type RouteDecision string

const (
	RouteDecisionSelected    RouteDecision = "selected"
	RouteDecisionExcluded    RouteDecision = "excluded"
	RouteDecisionOwnerReview RouteDecision = "owner_review"
	RouteDecisionNotSelected RouteDecision = "not_selected"
)

// RoutingCandidateEvidence records deterministic task-language and repository
// evidence for a profile route, including routes that were explicitly excluded.
type RoutingCandidateEvidence struct {
	RouteReference
	CandidateSignal      string        `json:"candidate_signal,omitempty"`
	Polarity             RoutePolarity `json:"polarity"`
	MatchedPhrase        string        `json:"matched_phrase,omitempty"`
	TaskSpan             string        `json:"task_span,omitempty"`
	ArchitectureEvidence []string      `json:"architecture_evidence,omitempty"`
	SourceEvidence       []string      `json:"source_evidence,omitempty"`
	Decision             RouteDecision `json:"decision"`
}

type ArchitectureProfileResolution struct {
	ProjectID          string                  `json:"project_id"`
	Status             ProfileResolutionStatus `json:"status"`
	ProfileID          string                  `json:"profile_id,omitempty"`
	ProfileFingerprint string                  `json:"profile_fingerprint,omitempty"`
	Variant            string                  `json:"variant,omitempty"`
	Reason             string                  `json:"reason,omitempty"`
	EvidenceIDs        []string                `json:"evidence_ids,omitempty"`
}

// RoutingEvidence is a planner-time, bounded repository evidence index.
// Content itself is not retained in the plan; checksums and source locations
// make selected claims reviewable and bind them into plan approval.
type RoutingEvidence struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Kind          string   `json:"kind"`
	Path          string   `json:"path"`
	Symbol        string   `json:"symbol,omitempty"`
	Summary       string   `json:"summary"`
	Checksum      string   `json:"checksum"`
	BoundaryKinds []string `json:"boundary_kinds,omitempty"`
}

type RoutedTarget struct {
	RouteReference
	Paths       []string `json:"paths"`
	Symbols     []string `json:"symbols,omitempty"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type ContractInspection struct {
	Kind        string           `json:"kind"`
	ProjectID   string           `json:"project_id"`
	EvidenceIDs []string         `json:"evidence_ids"`
	Routes      []RouteReference `json:"routes"`
}

type RouteVerification struct {
	RouteReference
	Boundary      string                    `json:"boundary"`
	EvidenceState VerificationEvidenceState `json:"evidence_state"`
	EvidenceIDs   []string                  `json:"evidence_ids,omitempty"`
	Rationale     string                    `json:"rationale"`
}

// SharedBoundaryCandidate is derived from canonical profile metadata after
// route selection. It is an analysis signal only, never a frozen contract.
type SharedBoundaryCandidate struct {
	Kind   string           `json:"kind"`
	Routes []RouteReference `json:"routes"`
}

type RoutingResult struct {
	Status                   RoutingStatus                   `json:"status"`
	Classification           string                          `json:"classification"`
	Profiles                 []ArchitectureProfileResolution `json:"profiles"`
	RouteCandidates          []RoutingCandidateEvidence      `json:"route_candidates,omitempty"`
	Routes                   []RoutedTarget                  `json:"routes"`
	PrimaryRoute             *RouteReference                 `json:"primary_route,omitempty"`
	ContractsToInspect       []ContractInspection            `json:"contracts_to_inspect,omitempty"`
	Verification             []RouteVerification             `json:"verification"`
	Avoid                    []string                        `json:"avoid,omitempty"`
	EvidenceIDs              []string                        `json:"evidence_ids"`
	EvidenceIndex            []RoutingEvidence               `json:"evidence_index"`
	SharedBoundaryCandidates []SharedBoundaryCandidate       `json:"shared_boundary_candidates,omitempty"`
	CatalogDigest            string                          `json:"catalog_digest"`
	Confidence               string                          `json:"confidence"`
	UnresolvedReason         string                          `json:"unresolved_reason,omitempty"`
	ScopeMode                string                          `json:"scope_mode"`
	OwnerReviewRequired      bool                            `json:"owner_review_required"`
}

type PlannedContractBoundary struct {
	Kind             string           `json:"kind"`
	Owner            RouteReference   `json:"owner"`
	TargetPath       string           `json:"target_path"`
	Consumers        []RouteReference `json:"consumers"`
	Implementers     []RouteReference `json:"implementers,omitempty"`
	Existing         bool             `json:"existing"`
	SourceEvidenceID string           `json:"source_evidence_id,omitempty"`
	Rationale        string           `json:"rationale"`
}

type PlannedBoundaryChange struct {
	Kind     string         `json:"kind"`
	Owner    RouteReference `json:"owner"`
	Consumer RouteReference `json:"consumer"`
	Reason   string         `json:"reason"`
}

type ContractPlan struct {
	ContractOwnerRoutes    []RouteReference          `json:"contract_owner_routes,omitempty"`
	State                  ContractPlanState         `json:"state"`
	Required               bool                      `json:"required"`
	Reason                 string                    `json:"reason"`
	AffectedRoutes         []RouteReference          `json:"affected_routes,omitempty"`
	Boundaries             []PlannedContractBoundary `json:"boundaries,omitempty"`
	ChangesRequired        []PlannedBoundaryChange   `json:"changes_required,omitempty"`
	FreezeRequired         bool                      `json:"freeze_required"`
	IndependentAfterFreeze [][]RouteReference        `json:"independent_after_freeze,omitempty"`
}
