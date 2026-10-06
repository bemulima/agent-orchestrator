// Package contextretrieval provides deterministic, read-only context preparation.
package contextretrieval

import "context"

const SchemaVersion = "context-pack.v1"
const EngineVersion = "1.0.0"

type Status string

const (
	Complete    Status = "COMPLETE"
	Partial     Status = "PARTIAL"
	Blocked     Status = "BLOCKED"
	Unsupported Status = "UNSUPPORTED"
	Stale       Status = "STALE"
	Invalid     Status = "INVALID"
	Unknown     Status = "UNKNOWN"
)

type RequirementState string

const (
	Found                  RequirementState = "FOUND"
	NotFound               RequirementState = "NOT_FOUND_AFTER_COMPLETE_SEARCH"
	NotVerified            RequirementState = "NOT_VERIFIED"
	OmittedByLimit         RequirementState = "OMITTED_BY_LIMIT"
	Unreadable             RequirementState = "UNREADABLE"
	RequirementUnsupported RequirementState = "UNSUPPORTED"
	ExcludedByPolicy       RequirementState = "EXCLUDED_BY_POLICY"
)

type Provenance string

const (
	TrustedPolicy          Provenance = "TRUSTED_POLICY"
	TrustedProjectMetadata Provenance = "TRUSTED_PROJECT_METADATA"
	ProjectSource          Provenance = "PROJECT_SOURCE"
	ProjectTest            Provenance = "PROJECT_TEST"
	ProjectDoc             Provenance = "PROJECT_DOC"
	ExternalContent        Provenance = "EXTERNAL_CONTENT"
	GeneratedContent       Provenance = "GENERATED_CONTENT"
)

type Freshness string

const (
	Current       Freshness = "CURRENT"
	StaleEvidence Freshness = "STALE"
	DirtySnapshot Freshness = "DIRTY_SNAPSHOT"
	Unverified    Freshness = "UNVERIFIED"
	Missing       Freshness = "MISSING"
	Invalidated   Freshness = "INVALIDATED"
)

type ClaimType string

const (
	BusinessOwnership      ClaimType = "business_ownership"
	PublicAPI              ClaimType = "public_api_contract"
	ImplementationBehavior ClaimType = "implementation_behavior"
	DatabaseSchema         ClaimType = "database_schema_query"
	DataSemantics          ClaimType = "metric_data_semantics"
	TestingPolicy          ClaimType = "testing_policy"
	ArchitectureRule       ClaimType = "architecture_rule"
	HistoricalDecision     ClaimType = "historical_decision"
)

type QueryKind string

const (
	QueryExact          QueryKind = "exact"
	QueryMetadata       QueryKind = "metadata"
	QueryArchitecture   QueryKind = "architecture"
	QueryDefinition     QueryKind = "definition"
	QueryDeclarations   QueryKind = "declarations"
	QueryImports        QueryKind = "imports"
	QueryReferences     QueryKind = "references"
	QueryImplementation QueryKind = "implementation"
	QueryCallers        QueryKind = "callers"
	QueryContract       QueryKind = "contract"
	QueryTests          QueryKind = "tests"
	QueryErrorMapping   QueryKind = "error_mapping"
	QuerySchema         QueryKind = "schema"
	QueryHistory        QueryKind = "history"
	QueryDocs           QueryKind = "docs"
)

type SourceAdmission struct {
	Identity         string   `json:"identity"`
	RouteIdentity    string   `json:"route_identity,omitempty"`
	Root             string   `json:"-"`
	ReadPaths        []string `json:"read_paths"`
	ExcludePaths     []string `json:"exclude_paths,omitempty"`
	Neighbor         bool     `json:"read_only_neighbor,omitempty"`
	External         bool     `json:"external,omitempty"`
	Dirty            bool     `json:"dirty,omitempty"`
	Revision         string   `json:"revision,omitempty"`
	ExpectedSnapshot string   `json:"expected_snapshot,omitempty"`
}
type EvidenceSource struct {
	Identity        string `json:"identity"`
	Revision        string `json:"revision"`
	Snapshot        string `json:"snapshot"`
	Dirty           bool   `json:"dirty"`
	AdmissionDigest string `json:"admission_digest"`
}
type EvidenceSpan struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
}
type EvidenceLink struct {
	Kind           string `json:"kind"`
	SourceIdentity string `json:"source_identity,omitempty"`
	RelativePath   string `json:"relative_path,omitempty"`
	Symbol         string `json:"symbol,omitempty"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
	EvidenceID     string `json:"evidence_id,omitempty"`
}
type Authority struct {
	ClaimType ClaimType `json:"claim_type"`
	Role      string    `json:"role"`
	Basis     string    `json:"basis"`
}
type EvidenceCandidate struct {
	EvidenceID            string         `json:"evidence_id"`
	SourceIdentity        string         `json:"source_identity"`
	SourceRevision        string         `json:"source_revision"`
	SourceSnapshot        string         `json:"source_snapshot"`
	RelativePath          string         `json:"relative_path"`
	ContentHash           string         `json:"content_hash"`
	ExpectedHash          string         `json:"expected_hash,omitempty"`
	Symbol                string         `json:"symbol,omitempty"`
	Span                  EvidenceSpan   `json:"span"`
	EvidenceKind          string         `json:"evidence_kind"`
	Resolver              string         `json:"resolver"`
	ResolverVersion       string         `json:"resolver_version"`
	Query                 QueryKind      `json:"query"`
	Provenance            Provenance     `json:"provenance"`
	ClaimType             ClaimType      `json:"claim_type"`
	ClaimKey              string         `json:"claim_key,omitempty"`
	ClaimValue            string         `json:"claim_value,omitempty"`
	Freshness             Freshness      `json:"freshness"`
	Authority             Authority      `json:"authority"`
	Content               string         `json:"content"`
	Size                  int            `json:"size"`
	TokenEstimate         int            `json:"token_estimate"`
	FacetIDs              []string       `json:"facet_ids"`
	Required              bool           `json:"required"`
	ExactMatch            bool           `json:"exact_match"`
	ArchitecturalDistance int            `json:"architectural_distance"`
	Limitations           []string       `json:"limitations"`
	Links                 []EvidenceLink `json:"links"`
}
type Document struct {
	Source       EvidenceSource
	RelativePath string
	Content      string
	ContentHash  string
	External     bool
}
type CoverageResult struct {
	SourceIdentity    string           `json:"source_identity"`
	Stage             string           `json:"stage"`
	FacetID           string           `json:"facet_id,omitempty"`
	Status            Status           `json:"status"`
	RequirementState  RequirementState `json:"requirement_state,omitempty"`
	Complete          bool             `json:"complete"`
	Visited           int              `json:"visited"`
	Indexed           int              `json:"indexed"`
	CandidateCount    int              `json:"candidate_count"`
	SelectedCount     int              `json:"selected_count"`
	SkippedByPolicy   int              `json:"skipped_by_policy"`
	SkippedLarge      int              `json:"skipped_large"`
	Unreadable        int              `json:"unreadable"`
	Errors            int              `json:"errors"`
	BytesConsidered   int              `json:"bytes_considered"`
	BytesSelected     int              `json:"bytes_selected"`
	DepthLimit        int              `json:"depth_limit"`
	FileLimit         int              `json:"file_limit"`
	ByteLimit         int              `json:"byte_limit"`
	Omitted           int              `json:"omitted"`
	TerminatedByLimit bool             `json:"terminated_by_limit"`
	Reasons           []string         `json:"reasons"`
}
type RetrievalDiagnostic struct {
	Code           string `json:"code"`
	Status         Status `json:"status"`
	SourceIdentity string `json:"source_identity,omitempty"`
	RelativePath   string `json:"relative_path,omitempty"`
	FacetID        string `json:"facet_id,omitempty"`
	EvidenceID     string `json:"evidence_id,omitempty"`
	Message        string `json:"message"`
}
type Facet struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	QueryKind      QueryKind `json:"query_kind"`
	SourceIdentity string    `json:"source_identity"`
	Path           string    `json:"path,omitempty"`
	Symbol         string    `json:"symbol,omitempty"`
	Text           string    `json:"text,omitempty"`
	Resolver       string    `json:"resolver,omitempty"`
	ClaimType      ClaimType `json:"claim_type"`
	ClaimKey       string    `json:"claim_key,omitempty"`
	ClaimValue     string    `json:"claim_value,omitempty"`
	ExpectedHash   string    `json:"expected_hash,omitempty"`
	Required       bool      `json:"required"`
}
type RouteTarget struct {
	SourceIdentity string   `json:"source_identity"`
	Layer          string   `json:"layer"`
	Paths          []string `json:"paths"`
	Symbols        []string `json:"symbols"`
}
type RouteConstraint struct {
	SourceIdentity string `json:"source_identity"`
	Layer          string `json:"layer"`
	Polarity       string `json:"polarity"`
	Decision       string `json:"decision"`
}
type RouteContext struct {
	Digest              string                `json:"digest"`
	CatalogDigest       string                `json:"catalog_digest"`
	Status              Status                `json:"status"`
	OwnerReviewRequired bool                  `json:"owner_review_required"`
	Owners              []string              `json:"owners"`
	Targets             []RouteTarget         `json:"targets"`
	Constraints         []RouteConstraint     `json:"constraints"`
	Avoid               []string              `json:"avoid"`
	SeedFacets          []Facet               `json:"seed_facets"`
	Diagnostics         []RetrievalDiagnostic `json:"diagnostics"`
	Coverage            []CoverageResult      `json:"coverage"`
}
type PolicyRegistration struct {
	SourceIdentity string `json:"source_identity"`
	RelativePath   string `json:"relative_path"`
	ContentHash    string `json:"content_hash"`
	Scope          string `json:"scope"`
}
type Budget struct {
	MaxSourceBytes       int `json:"max_source_bytes"`
	MaxContextTokens     int `json:"max_context_tokens"`
	ReservedPromptTokens int `json:"reserved_prompt_tokens"`
	PerFacetTokens       int `json:"per_facet_tokens"`
	PerSourceBytes       int `json:"per_source_bytes"`
}
type Limits struct {
	MaxFiles          int `json:"max_files"`
	MaxFileBytes      int `json:"max_file_bytes"`
	MaxTotalBytes     int `json:"max_total_bytes"`
	MaxDepth          int `json:"max_depth"`
	MaxResults        int `json:"max_results"`
	MaxSymbolMatches  int `json:"max_symbol_matches"`
	MaxExpandDepth    int `json:"max_expand_depth"`
	MaxDurationMillis int `json:"max_duration_millis"`
}
type RetrievalRequest struct {
	SchemaVersion   string               `json:"schema_version"`
	RequestID       string               `json:"request_id"`
	Task            string               `json:"task"`
	Purpose         string               `json:"purpose"`
	Route           RouteContext         `json:"route"`
	Sources         []SourceAdmission    `json:"sources"`
	RequiredFacets  []Facet              `json:"required_facets"`
	OptionalFacets  []Facet              `json:"optional_facets"`
	Budget          Budget               `json:"budget"`
	Limits          Limits               `json:"limits"`
	TrustedPolicies []PolicyRegistration `json:"trusted_policies"`
}
type ForbiddenScope struct {
	Read             []string `json:"read"`
	Write            []string `json:"write"`
	ReadOnlyEvidence []string `json:"read_only_evidence"`
}
type RetrievalPlan struct {
	ExpansionHistory []ExpandRequest       `json:"expansion_history,omitempty"`
	AdapterVersions  map[string]string     `json:"adapter_versions"`
	SchemaVersion    string                `json:"schema_version"`
	RequestID        string                `json:"request_id"`
	Task             string                `json:"task"`
	Purpose          string                `json:"purpose"`
	Route            RouteContext          `json:"route"`
	Sources          []SourceAdmission     `json:"sources"`
	RequiredFacets   []Facet               `json:"required_facets"`
	OptionalFacets   []Facet               `json:"optional_facets"`
	ForbiddenScope   ForbiddenScope        `json:"forbidden_scope"`
	Budget           Budget                `json:"budget"`
	Limits           Limits                `json:"limits"`
	TrustedPolicies  []PolicyRegistration  `json:"trusted_policies"`
	Unresolved       []RetrievalDiagnostic `json:"unresolved"`
}
type SnapshotResult struct {
	Sources     []EvidenceSource
	Documents   []Document
	Coverage    []CoverageResult
	Diagnostics []RetrievalDiagnostic
}
type RetrievalResult struct {
	Candidates  []EvidenceCandidate
	Coverage    []CoverageResult
	Diagnostics []RetrievalDiagnostic
}
type SourceLoader interface {
	Load(context.Context, []SourceAdmission, Limits) (SnapshotResult, error)
}
type Resolver interface {
	ID() string
	Version() string
	Resolve(context.Context, RetrievalPlan, Facet, SnapshotResult) (RetrievalResult, error)
}
type AuthorityConflict struct {
	ID          string    `json:"id"`
	ClaimType   ClaimType `json:"claim_type"`
	ClaimKey    string    `json:"claim_key"`
	EvidenceIDs []string  `json:"evidence_ids"`
	Values      []string  `json:"values"`
	Resolution  string    `json:"resolution"`
}
type Omission struct {
	EvidenceID     string `json:"evidence_id,omitempty"`
	FacetID        string `json:"facet_id,omitempty"`
	SourceIdentity string `json:"source_identity,omitempty"`
	RelativePath   string `json:"relative_path,omitempty"`
	Reason         string `json:"reason"`
	Required       bool   `json:"required"`
}
type BudgetUsed struct {
	SourceBytes             int    `json:"source_bytes"`
	ContextTokens           int    `json:"context_tokens"`
	ReservedPromptTokens    int    `json:"reserved_prompt_tokens"`
	CumulativeSourceBytes   int    `json:"cumulative_source_bytes"`
	CumulativeContextTokens int    `json:"cumulative_context_tokens"`
	ExpandCount             int    `json:"expand_count"`
	Estimator               string `json:"estimator"`
}
type EngineInfo struct {
	Version         string            `json:"version"`
	PolicyDigest    string            `json:"policy_digest"`
	AdapterVersions map[string]string `json:"adapter_versions"`
}
type ContextPack struct {
	SchemaVersion        string                `json:"schema_version"`
	Status               Status                `json:"status"`
	RequestID            string                `json:"request_id"`
	Purpose              string                `json:"purpose"`
	RouteRef             RouteContext          `json:"route_ref"`
	Engine               EngineInfo            `json:"engine"`
	Sources              []EvidenceSource      `json:"sources"`
	OwnerRepositories    []string              `json:"owner_repositories"`
	AffectedLayers       []string              `json:"affected_layers"`
	RetrievalPlan        RetrievalPlan         `json:"retrieval_plan"`
	ApplicableRules      []string              `json:"applicable_rules"`
	RelevantSymbols      []string              `json:"relevant_symbols"`
	RelevantCode         []string              `json:"relevant_code"`
	Contracts            []string              `json:"contracts"`
	Tests                []string              `json:"tests"`
	ArchitectureEvidence []string              `json:"architecture_evidence"`
	Dependencies         []EvidenceLink        `json:"dependencies"`
	ForbiddenScope       ForbiddenScope        `json:"forbidden_scope"`
	Evidence             []EvidenceCandidate   `json:"evidence"`
	Coverage             []CoverageResult      `json:"coverage"`
	UnresolvedQuestions  []RetrievalDiagnostic `json:"unresolved_questions"`
	AuthorityConflicts   []AuthorityConflict   `json:"authority_conflicts"`
	Omissions            []Omission            `json:"omissions"`
	BudgetUsed           BudgetUsed            `json:"budget_used"`
	ContentDigest        string                `json:"content_digest"`
}
type RetrievalTrace struct {
	RequestID        string            `json:"request_id"`
	PackDigest       string            `json:"pack_digest"`
	SnapshotIDs      []string          `json:"snapshot_ids"`
	ResolverVersions map[string]string `json:"resolver_versions"`
	QueryKinds       []QueryKind       `json:"query_kinds"`
	CandidateCount   int               `json:"candidate_count"`
	SelectedCount    int               `json:"selected_count"`
	OmissionReasons  []string          `json:"omission_reasons"`
	CoverageStatus   Status            `json:"coverage_status"`
	ConflictCount    int               `json:"conflict_count"`
	Bytes            int               `json:"bytes"`
	TokenEstimate    int               `json:"token_estimate"`
	LatencyMillis    int64             `json:"latency_millis"`
	CacheHit         bool              `json:"cache_hit"`
	ExpandCount      int               `json:"expand_count"`
}
type ExpandRequest struct {
	Facet           Facet  `json:"facet"`
	Reason          string `json:"reason"`
	RemainingBudget Budget `json:"remaining_budget"`
}
type ContextDelta struct {
	SchemaVersion    string                `json:"schema_version"`
	Status           Status                `json:"status"`
	BaseDigest       string                `json:"base_digest"`
	ContentDigest    string                `json:"content_digest"`
	AddedEvidenceIDs []string              `json:"added_evidence_ids"`
	Pack             ContextPack           `json:"pack"`
	Diagnostics      []RetrievalDiagnostic `json:"diagnostics"`
}
