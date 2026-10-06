package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// RoutingCoverageReport is a companion to routing, never part of persisted
// PlannerOutput or an approval fingerprint. COMPLETE describes these bounded
// stages only; it does not certify architecture or authorize execution.
type RoutingCoverageReport struct {
	SchemaVersion       string                       `json:"schema_version"`
	Status              string                       `json:"status"`
	RoutingOutputDigest string                       `json:"routing_output_digest"`
	CatalogDigest       string                       `json:"catalog_digest"`
	Config              RoutingCoverageConfig        `json:"config"`
	Projects            []RoutingRepositoryCoverage  `json:"projects"`
	EvidenceProjection  RoutingEvidenceProjection    `json:"evidence_projection"`
	Requirements        []RoutingCoverageRequirement `json:"requirements"`
	DiagnosticDigest    string                       `json:"diagnostic_digest"`
}

type RoutingCoverageConfig struct {
	MaxVisitedFiles   int `json:"max_visited_files"`
	MaxFileBytes      int `json:"max_file_bytes"`
	MaxInventoryBytes int `json:"max_inventory_bytes"`
	MaxEvidence       int `json:"max_evidence"`
	MaxTargets        int `json:"max_targets"`
	MaxSymbolMatches  int `json:"max_symbol_matches"`
	MaxFactsBytes     int `json:"max_facts_bytes"`
	MaxClauseRunes    int `json:"max_clause_display_runes"`
	MaxTaskSpanBytes  int `json:"max_task_span_display_bytes"`
}

type RoutingRepositoryCoverage struct {
	requirements        []RoutingCoverageRequirement
	ProjectID           string                      `json:"project_id"`
	SourceIdentity      string                      `json:"source_identity"`
	DeclaredHeadCommit  string                      `json:"declared_head_commit,omitempty"`
	DeclaredDirty       bool                        `json:"declared_dirty"`
	IdentityState       string                      `json:"identity_state"`
	AcquiredDigest      string                      `json:"acquired_digest,omitempty"`
	Status              string                      `json:"status"`
	VisitedFiles        int                         `json:"visited_files"`
	IndexedFiles        int                         `json:"indexed_files"`
	IndexedBytes        int                         `json:"indexed_bytes"`
	ExcludedDirectories int                         `json:"excluded_directories"`
	ExcludedFiles       int                         `json:"excluded_files"`
	ExcludedSymlinks    int                         `json:"excluded_symlinks"`
	ExcludedNonRegular  int                         `json:"excluded_nonregular"`
	UnsupportedFiles    int                         `json:"unsupported_files"`
	SkippedLargeFiles   int                         `json:"skipped_large_files"`
	ReadErrors          int                         `json:"read_errors"`
	TerminationReason   string                      `json:"termination_reason,omitempty"`
	UnscannedRemainder  bool                        `json:"unscanned_remainder"`
	RemainderCountKnown bool                        `json:"remainder_count_known"`
	Extractions         []RoutingExtractionCoverage `json:"extractions"`
	Targets             []RoutingTargetCoverage     `json:"targets"`
	Facts               RoutingFactsCoverage        `json:"facts"`
	Omissions           []RoutingCoverageOmission   `json:"omissions"`
}

type RoutingCoverageOmission struct {
	Stage   string `json:"stage"`
	Path    string `json:"path,omitempty"`
	RouteID string `json:"route_id,omitempty"`
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
}

type RoutingExtractionCoverage struct {
	Path            string `json:"path"`
	SymbolMatches   int    `json:"symbol_matches"`
	SelectedSymbols int    `json:"selected_symbols"`
	OmittedMatches  int    `json:"omitted_symbol_matches"`
	ImportMatches   int    `json:"import_matches"`
	SelectedImports int    `json:"selected_imports"`
	OmittedImports  int    `json:"omitted_import_matches"`
}

type RoutingTargetCoverage struct {
	RouteID      string   `json:"route_id"`
	Candidates   int      `json:"candidates"`
	Selected     int      `json:"selected"`
	OmittedPaths []string `json:"omitted_paths"`
}

type RoutingFactsCoverage struct {
	Status         string   `json:"status"`
	CandidateFiles int      `json:"candidate_files"`
	CandidateBytes int      `json:"candidate_bytes"`
	SelectedFiles  int      `json:"selected_files"`
	SelectedBytes  int      `json:"selected_bytes"`
	OmittedPaths   []string `json:"omitted_paths"`
}

type RoutingEvidenceProjection struct {
	Status             string   `json:"status"`
	Acquired           int      `json:"acquired"`
	UniqueCandidates   int      `json:"unique_candidates"`
	Selected           int      `json:"selected"`
	OmittedEvidenceIDs []string `json:"omitted_evidence_ids"`
	RequiredOmittedIDs []string `json:"required_omitted_ids"`
}

type RoutingCoverageRequirement struct {
	ProjectID   string   `json:"project_id"`
	RouteID     string   `json:"route_id,omitempty"`
	Kind        string   `json:"kind"`
	Path        string   `json:"path,omitempty"`
	Status      string   `json:"status"`
	EvidenceIDs []string `json:"evidence_ids"`
	Reason      string   `json:"reason,omitempty"`
}

func routingCoverageConfig() RoutingCoverageConfig {
	return RoutingCoverageConfig{maxRoutingFilesVisited, maxRoutingFileBytes,
		maxRoutingInventoryBytes, maxRoutingEvidence, maxRouteTargets,
		maxRoutingSymbolMatches, maxRepositoryFactsBytes, 180, 360}
}

func newRoutingRepositoryCoverage(projectID string) *RoutingRepositoryCoverage {
	return &RoutingRepositoryCoverage{ProjectID: projectID, Status: "COMPLETE",
		IdentityState: "UNVERIFIED", RemainderCountKnown: true,
		Extractions: []RoutingExtractionCoverage{}, Targets: []RoutingTargetCoverage{},
		Omissions: []RoutingCoverageOmission{}}
}

func (coverage *RoutingRepositoryCoverage) omit(stage, path, reason string, count int) {
	coverage.Omissions = append(coverage.Omissions, RoutingCoverageOmission{Stage: stage, Path: path, Reason: reason, Count: count})
	if strings.HasPrefix(reason, "excluded_") || stage == "task_span" {
		return
	}
	coverage.Status = "PARTIAL"
}

func recordRoutingExtractionCoverage(coverage *RoutingRepositoryCoverage, relative string, content []byte) {
	if coverage == nil {
		return
	}
	pattern := tsSymbolPattern
	if strings.HasSuffix(relative, ".go") {
		pattern = goSymbolPattern
	}
	symbolCount := len(pattern.FindAllSubmatch(content, -1))
	importCount := 0
	if strings.HasSuffix(relative, ".go") {
		importCount = len(goImportPattern.FindAllSubmatch(content, -1))
	}
	observation := RoutingExtractionCoverage{Path: relative, SymbolMatches: symbolCount,
		SelectedSymbols: len(extractSymbols(relative, content)), OmittedMatches: max(0, symbolCount-maxRoutingSymbolMatches),
		ImportMatches: importCount, SelectedImports: len(extractImports(relative, content)), OmittedImports: max(0, importCount-maxRoutingSymbolMatches)}
	coverage.Extractions = append(coverage.Extractions, observation)
	if observation.OmittedMatches > 0 {
		coverage.omit("symbols", relative, "symbol_match_limit", observation.OmittedMatches)
	}
	if observation.OmittedImports > 0 {
		coverage.omit("imports", relative, "import_match_limit", observation.OmittedImports)
	}
}

func recordRoutingTargetCoverage(coverage *RoutingRepositoryCoverage, routeID string, files []indexedEvidence) {
	if coverage == nil {
		return
	}
	observation := RoutingTargetCoverage{RouteID: routeID, Candidates: len(files), Selected: min(len(files), maxRouteTargets), OmittedPaths: []string{}}
	for index := maxRouteTargets; index < len(files); index++ {
		observation.OmittedPaths = append(observation.OmittedPaths, files[index].value.Path)
		coverage.omit("targets", files[index].value.Path, "target_limit", 1)
		coverage.Omissions[len(coverage.Omissions)-1].RouteID = routeID
	}
	coverage.Targets = append(coverage.Targets, observation)
}

func factEvidenceKind(kind string) bool {
	switch kind {
	case "repository_instructions", "service_metadata", "architecture_metadata", "contract", "commands", "test_manifest", "stack_manifest":
		return true
	default:
		return false
	}
}

func recordRepositoryFactsCoverage(coverage *RoutingRepositoryCoverage, evidence []indexedEvidence, facts []plannerRepositoryFact, total int) {
	if coverage == nil {
		return
	}
	// This projection is idempotent even when the planner and report both ask for facts.
	filtered := coverage.Omissions[:0]
	for _, omission := range coverage.Omissions {
		if omission.Stage != "repository_facts" {
			filtered = append(filtered, omission)
		}
	}
	coverage.Omissions = filtered
	observation := RoutingFactsCoverage{Status: "COMPLETE", SelectedFiles: len(facts), SelectedBytes: total, OmittedPaths: []string{}}
	selected := make(map[string]bool, len(facts))
	for _, fact := range facts {
		selected[fact.Path] = true
	}
	for _, item := range evidence {
		if !factEvidenceKind(item.value.Kind) {
			continue
		}
		observation.CandidateFiles++
		observation.CandidateBytes += len(item.content)
		if !selected[item.value.Path] {
			observation.OmittedPaths = append(observation.OmittedPaths, item.value.Path)
			coverage.omit("repository_facts", item.value.Path, "facts_bytes_limit", 1)
			observation.Status = "PARTIAL"
		}
	}
	coverage.Facts = observation
}

// BuildRoutingMetadataWithCoverage reuses the exact existing router and emits
// additive diagnostics. No database, model, network, write or execution occurs.
func BuildRoutingMetadataWithCoverage(requestText string, baseline domain.PlannerOutput, projects []domain.Project, catalog agentcontrol.Catalog) (domain.RoutingResult, domain.ContractPlan, RoutingCoverageReport, error) {
	routing, plan, inventories, err := buildRoutingMetadata(requestText, baseline, projects, catalog)
	if err != nil {
		return routing, plan, RoutingCoverageReport{SchemaVersion: "routing-coverage.v1", Status: "UNKNOWN"}, err
	}
	return routing, plan, buildRoutingCoverageReport(routing, plan, projects, inventories), nil
}

// RoutingCoverageFromEvidence deliberately reports UNKNOWN for historical
// output: serialized evidence cannot establish scan, extraction or prompt caps.
func RoutingCoverageFromEvidence(routing domain.RoutingResult, plan domain.ContractPlan, projects []domain.Project) RoutingCoverageReport {
	return buildRoutingCoverageReport(routing, plan, projects, nil)
}

func buildRoutingCoverageReport(routing domain.RoutingResult, plan domain.ContractPlan, projects []domain.Project, inventories map[string]repositoryInventory) RoutingCoverageReport {
	report := RoutingCoverageReport{SchemaVersion: "routing-coverage.v1", Status: "COMPLETE",
		CatalogDigest: routing.CatalogDigest, Config: routingCoverageConfig(),
		Projects: []RoutingRepositoryCoverage{}, Requirements: []RoutingCoverageRequirement{}}
	raw, _ := json.Marshal(struct {
		Routing      domain.RoutingResult `json:"routing"`
		ContractPlan domain.ContractPlan  `json:"contract_plan"`
	}{routing, plan})
	report.RoutingOutputDigest = coverageDigest(raw)
	projectByID := make(map[string]domain.Project, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	ids := map[string]bool{}
	for _, profile := range routing.Profiles {
		ids[profile.ProjectID] = true
	}
	for id := range inventories {
		ids[id] = true
	}
	for _, route := range routing.Routes {
		ids[route.ProjectID] = true
	}
	for _, boundary := range plan.Boundaries {
		ids[boundary.Owner.ProjectID] = true
	}
	acquired := map[string]domain.RoutingEvidence{}
	for _, id := range sortedCoverageKeys(ids) {
		inventory, exists := inventories[id]
		coverage := newRoutingRepositoryCoverage(id)
		if !exists || inventory.coverage == nil {
			coverage.Status = "UNKNOWN"
			coverage.RemainderCountKnown = false
			coverage.Facts.Status = "UNKNOWN"
			coverage.UnscannedRemainder = true
		} else {
			_ = repositoryFacts(inventory)
			copyValue := *inventory.coverage
			coverage = &copyValue
			coverage.Omissions = append([]RoutingCoverageOmission{}, inventory.coverage.Omissions...)
			coverage.Extractions = append([]RoutingExtractionCoverage{}, inventory.coverage.Extractions...)
			coverage.Targets = append([]RoutingTargetCoverage{}, inventory.coverage.Targets...)
			coverage.RemainderCountKnown = !coverage.UnscannedRemainder
			values := make([]domain.RoutingEvidence, 0, len(inventory.evidence))
			for _, item := range inventory.evidence {
				acquired[item.value.ID] = item.value
				values = append(values, item.value)
			}
			bytes, _ := json.Marshal(values)
			coverage.AcquiredDigest = coverageDigest(bytes)
			sort.Slice(coverage.Extractions, func(i, j int) bool { return coverage.Extractions[i].Path < coverage.Extractions[j].Path })
			sort.Slice(coverage.Targets, func(i, j int) bool { return coverage.Targets[i].RouteID < coverage.Targets[j].RouteID })
			sort.Slice(coverage.Omissions, func(i, j int) bool {
				a, b := coverage.Omissions[i], coverage.Omissions[j]
				return a.Stage+"\x00"+a.RouteID+"\x00"+a.Path+"\x00"+a.Reason < b.Stage+"\x00"+b.RouteID+"\x00"+b.Path+"\x00"+b.Reason
			})
		}
		project := projectByID[id]
		coverage.SourceIdentity = project.SourceIdentity
		coverage.DeclaredHeadCommit, coverage.DeclaredDirty = project.HeadCommit, project.IsDirty
		// Catalog project identity is caller-provided; content acquisition alone does not verify Git pins.
		if coverage.Status != "COMPLETE" {
			report.Status = coverage.Status
		}
		report.Projects = append(report.Projects, *coverage)
	}
	projection := RoutingEvidenceProjection{Status: "COMPLETE", Acquired: 0, UniqueCandidates: len(acquired), Selected: len(routing.EvidenceIndex), OmittedEvidenceIDs: []string{}, RequiredOmittedIDs: []string{}}
	selected := map[string]bool{}
	for _, evidence := range routing.EvidenceIndex {
		selected[evidence.ID] = true
	}
	for _, inventory := range inventories {
		projection.Acquired += len(inventory.evidence)
	}
	for _, id := range sortedEvidenceKeys(acquired) {
		if !selected[id] {
			projection.OmittedEvidenceIDs = append(projection.OmittedEvidenceIDs, id)
		}
	}
	if len(projection.OmittedEvidenceIDs) > 0 {
		projection.Status = "PARTIAL"
		report.Status = "PARTIAL"
	}
	if inventories == nil {
		projection.Status = "UNKNOWN"
		report.Status = "UNKNOWN"
	}
	addRequirement := func(requirement RoutingCoverageRequirement) {
		requirement.Status = "FOUND"
		requirement.EvidenceIDs = uniqueSorted(requirement.EvidenceIDs)
		if requirement.EvidenceIDs == nil {
			requirement.EvidenceIDs = []string{}
		}
		if inventories == nil {
			requirement.Status, requirement.Reason = "UNKNOWN", "historical_output_has_no_scan_diagnostics"
		} else if len(requirement.EvidenceIDs) == 0 {
			requirement.Status = "REQUIRED_NOT_VERIFIED"
			if requirement.Reason == "" {
				requirement.Reason = "no_acquired_evidence; absence_is_not_proven"
			}
		} else {
			for _, id := range requirement.EvidenceIDs {
				if !selected[id] {
					requirement.Status, requirement.Reason = "REQUIRED_NOT_VERIFIED", "found_but_omitted_by_global_evidence_limit"
					projection.RequiredOmittedIDs = append(projection.RequiredOmittedIDs, id)
				}
			}
		}
		if requirement.Status != "FOUND" && report.Status == "COMPLETE" {
			report.Status = "PARTIAL"
		}
		report.Requirements = append(report.Requirements, requirement)
	}
	if inventories == nil {
		for _, requirement := range routingRequirements(routing, plan) {
			addRequirement(requirement)
		}
	} else {
		for _, project := range report.Projects {
			for _, omission := range project.Omissions {
				if omission.Stage != "inventory" || strings.HasPrefix(omission.Reason, "excluded_") {
					continue
				}
				kind := evidenceKind(omission.Path)
				if kind == "source" || kind == "test" || kind == "contract" {
					addRequirement(RoutingCoverageRequirement{ProjectID: project.ProjectID, Kind: kind, Path: omission.Path, Reason: omission.Reason + "; absence_is_not_proven"})
				}
			}
			for _, requirement := range project.requirements {
				addRequirement(requirement)
			}
		}
	}
	projection.RequiredOmittedIDs = uniqueSorted(projection.RequiredOmittedIDs)
	if projection.RequiredOmittedIDs == nil {
		projection.RequiredOmittedIDs = []string{}
	}
	report.EvidenceProjection = projection
	sort.Slice(report.Requirements, func(i, j int) bool {
		a, b := report.Requirements[i], report.Requirements[j]
		return a.ProjectID+"\x00"+a.RouteID+"\x00"+a.Kind+"\x00"+a.Path < b.ProjectID+"\x00"+b.RouteID+"\x00"+b.Kind+"\x00"+b.Path
	})
	if len(report.Projects) == 0 {
		report.Status = "UNKNOWN"
	}
	raw, _ = json.Marshal(report)
	report.DiagnosticDigest = coverageDigest(raw)
	return report
}

func coverageDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func sortedCoverageKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func sortedEvidenceKeys(values map[string]domain.RoutingEvidence) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// Keep pre-projection references so global clipping cannot erase a known requirement.
func recordRoutingRequirements(routing domain.RoutingResult, plan domain.ContractPlan, inventories map[string]repositoryInventory) {
	for _, requirement := range routingRequirements(routing, plan) {
		if inventory, ok := inventories[requirement.ProjectID]; ok && inventory.coverage != nil {
			inventory.coverage.requirements = append(inventory.coverage.requirements, requirement)
		}
	}
}
func routingRequirements(routing domain.RoutingResult, plan domain.ContractPlan) []RoutingCoverageRequirement {
	var requirements []RoutingCoverageRequirement
	for _, profile := range routing.Profiles {
		requirements = append(requirements, RoutingCoverageRequirement{ProjectID: profile.ProjectID, Kind: "profile", EvidenceIDs: profile.EvidenceIDs})
	}
	for _, candidate := range routing.RouteCandidates {
		if candidate.Polarity == domain.RoutePolarityPositive || candidate.Polarity == domain.RoutePolarityConditional || candidate.Polarity == domain.RoutePolarityConflicting {
			requirements = append(requirements, RoutingCoverageRequirement{ProjectID: candidate.ProjectID, RouteID: candidate.RouteID, Kind: "source", EvidenceIDs: candidate.SourceEvidence})
		}
	}
	for _, verification := range routing.Verification {
		requirements = append(requirements, RoutingCoverageRequirement{ProjectID: verification.ProjectID, RouteID: verification.RouteID, Kind: "test", EvidenceIDs: verification.EvidenceIDs})
	}
	for _, boundary := range plan.Boundaries {
		var ids []string
		if boundary.SourceEvidenceID != "" {
			ids = append(ids, boundary.SourceEvidenceID)
		}
		requirements = append(requirements, RoutingCoverageRequirement{ProjectID: boundary.Owner.ProjectID, RouteID: boundary.Owner.RouteID, Kind: "contract", Path: boundary.TargetPath, EvidenceIDs: ids})
	}
	return requirements
}

// ValidateRoutingCoverage verifies that a companion was produced under the
// current observation configuration and binds the exact routing/contract output.
// Digests are content-integrity bindings, not authorization or Git verification.
func ValidateRoutingCoverage(report RoutingCoverageReport, route domain.RoutingResult, plan domain.ContractPlan) error {
	invalid := func(reason string) error {
		return fmt.Errorf("invalid routing coverage: %s: %w", reason, domain.ErrValidation)
	}
	if report.SchemaVersion != "routing-coverage.v1" {
		return invalid("unsupported schema")
	}
	if report.Config != routingCoverageConfig() {
		return invalid("configuration does not match current bounded stages")
	}
	if report.CatalogDigest != route.CatalogDigest {
		return invalid("catalog digest does not match routing")
	}
	raw, err := json.Marshal(struct {
		Routing      domain.RoutingResult `json:"routing"`
		ContractPlan domain.ContractPlan  `json:"contract_plan"`
	}{route, plan})
	if err != nil {
		return invalid("routing output is not serializable")
	}
	if report.RoutingOutputDigest != coverageDigest(raw) {
		return invalid("routing output digest mismatch")
	}
	digest := report.DiagnosticDigest
	report.DiagnosticDigest = ""
	raw, err = json.Marshal(report)
	if err != nil {
		return invalid("diagnostics are not serializable")
	}
	if digest != coverageDigest(raw) {
		return invalid("diagnostic digest mismatch")
	}
	return nil
}
