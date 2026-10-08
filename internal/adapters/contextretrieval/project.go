package contextretrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

// AdaptRoute projects existing routing decisions into evidence requirements.
// The caller admits sources; neither contract ownership nor neighboring graph
// references admit another repository or authorize a write.
func AdaptRoute(route domain.RoutingResult, projectSources map[string]core.SourceAdmission, contract *domain.ContractPlan, coverage []core.CoverageResult) (core.RouteContext, error) {
	raw, err := json.Marshal(route)
	if err != nil {
		return core.RouteContext{}, fmt.Errorf("encode route: %w", err)
	}
	sum := sha256.Sum256(raw)
	result := core.RouteContext{Digest: "sha256:" + hex.EncodeToString(sum[:]), CatalogDigest: route.CatalogDigest,
		Status: core.Unknown, OwnerReviewRequired: route.OwnerReviewRequired, Avoid: append([]string{}, route.Avoid...),
		Coverage: append([]core.CoverageResult{}, coverage...)}
	switch route.Status {
	case domain.RoutingStatusResolved:
		result.Status = core.Complete
	case domain.RoutingStatusPartial:
		result.Status = core.Partial
	case domain.RoutingStatusUnresolved:
		result.Status = core.Unknown
	}
	identity := func(project string) (string, error) {
		source, ok := projectSources[project]
		if !ok || strings.TrimSpace(source.Identity) == "" {
			return "", fmt.Errorf("route project %q has no caller-admitted source", project)
		}
		return source.Identity, nil
	}
	selectedIDs := map[string]bool{}
	for _, id := range route.EvidenceIDs {
		selectedIDs[id] = true
	}
	for _, target := range route.Routes {
		source, err := identity(target.ProjectID)
		if err != nil {
			return core.RouteContext{}, err
		}
		result.Targets = append(result.Targets, core.RouteTarget{SourceIdentity: source, Layer: target.RouteID,
			Paths: append([]string{}, target.Paths...), Symbols: append([]string{}, target.Symbols...)})
		if !projectSources[target.ProjectID].Neighbor {
			result.Owners = append(result.Owners, source)
		}
		for _, id := range target.EvidenceIDs {
			selectedIDs[id] = true
		}
		for _, file := range target.Paths {
			result.SeedFacets = append(result.SeedFacets, core.Facet{ID: facetID("route", source, target.RouteID, file),
				Kind: "code", QueryKind: core.QueryExact, SourceIdentity: source, Path: file,
				Resolver: "exact", ClaimType: core.ImplementationBehavior, Required: true})
		}
	}
	for _, candidate := range route.RouteCandidates {
		source, err := identity(candidate.ProjectID)
		if err != nil {
			return core.RouteContext{}, err
		}
		result.Constraints = append(result.Constraints, core.RouteConstraint{SourceIdentity: source,
			Layer: candidate.RouteID, Polarity: string(candidate.Polarity), Decision: string(candidate.Decision)})
	}
	for _, inspection := range route.ContractsToInspect {
		for _, id := range inspection.EvidenceIDs {
			selectedIDs[id] = true
		}
	}
	for _, verification := range route.Verification {
		for _, id := range verification.EvidenceIDs {
			selectedIDs[id] = true
		}
		if verification.EvidenceState == domain.VerificationEvidenceToAdd {
			source, err := identity(verification.ProjectID)
			if err != nil {
				return core.RouteContext{}, err
			}
			result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "TEST_VERIFICATION_NOT_PROVEN", Status: core.Partial,
				SourceIdentity: source, Message: "Routing requires additional verification; finding a test does not certify execution."})
		}
	}
	for _, evidence := range route.EvidenceIndex {
		if !selectedIDs[evidence.ID] {
			continue
		}
		source, err := identity(evidence.ProjectID)
		if err != nil {
			return core.RouteContext{}, err
		}
		expectedHash := strings.TrimPrefix(evidence.Checksum, "sha256:")
		if !graphContentHash.MatchString(expectedHash) {
			return core.RouteContext{}, fmt.Errorf("routing evidence %q has no supported SHA-256 checksum", evidence.ID)
		}
		query, resolver, claim := core.QueryExact, "exact", core.ImplementationBehavior
		if strings.HasPrefix(evidence.Path, ".ai/contracts/") || len(evidence.BoundaryKinds) > 0 {
			query, resolver, claim = core.QueryContract, "contract", core.PublicAPI
		} else if strings.HasPrefix(evidence.Path, ".ai/") {
			query, resolver, claim = core.QueryMetadata, "metadata", core.ArchitectureRule
		} else if strings.HasSuffix(evidence.Path, "_test.go") {
			query, resolver, claim = core.QueryTests, "tests", core.TestingPolicy
		}
		result.SeedFacets = append(result.SeedFacets, core.Facet{ID: facetID("evidence", source, evidence.ID), Kind: evidence.Kind,
			QueryKind: query, SourceIdentity: source, Path: evidence.Path, Symbol: routingScalarSymbol(evidence.Symbol), Resolver: resolver,
			ClaimType: claim, ExpectedHash: expectedHash, Required: true})
	}
	if contract != nil {
		for _, boundary := range contract.Boundaries {
			source, err := identity(boundary.Owner.ProjectID)
			if err != nil {
				return core.RouteContext{}, err
			}
			id := facetID("contract", source, boundary.Kind, boundary.TargetPath)
			result.SeedFacets = append(result.SeedFacets, core.Facet{ID: id, Kind: "contract", QueryKind: core.QueryContract,
				SourceIdentity: source, Path: boundary.TargetPath, Resolver: "contract", ClaimType: core.PublicAPI,
				ClaimKey: boundary.Kind, Required: true})
			if !boundary.Existing {
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "CONTRACT_NOT_IMPLEMENTED", Status: core.Partial,
					SourceIdentity: source, RelativePath: boundary.TargetPath, FacetID: id,
					Message: "ContractPlan describes a proposed boundary; it is not a frozen or implemented contract."})
			}
		}
		if contract.Required && len(contract.Boundaries) == 0 {
			result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "CONTRACT_REQUIREMENTS_UNRESOLVED", Status: core.Partial,
				Message: "ContractPlan requires a contract but supplies no retrievable boundary."})
		}
	}
	if route.UnresolvedReason != "" || route.OwnerReviewRequired {
		result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "ROUTE_REQUIRES_OWNER_REVIEW", Status: core.Partial,
			Message: "Routing is unresolved or requires owner review; retrieval cannot resolve ownership or approve execution."})
	}
	result.Owners = uniqueStrings(result.Owners)
	if coverage == nil {
		result.Coverage = []core.CoverageResult{{Stage: "routing_historical", Status: core.Unknown, RequirementState: core.NotVerified,
			Complete: false, Reasons: []string{"historical_route_has_no_acquisition_coverage"}}}
		result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "ROUTING_COVERAGE_UNKNOWN", Status: core.Unknown,
			Message: "Serialized routing does not prove complete acquisition or projection; provide its bound R1 coverage companion."})
	}
	sort.Slice(result.SeedFacets, func(i, j int) bool { return result.SeedFacets[i].ID < result.SeedFacets[j].ID })
	return result, nil
}

// AdaptRoutingCoverage verifies the immutable route/contract/config binding
// before projecting R1 diagnostics. It certifies search coverage only, not Git
// provenance, source freshness, instruction authority or execution approval.
func AdaptRoutingCoverage(report planning.RoutingCoverageReport, route domain.RoutingResult, contract *domain.ContractPlan, admitted map[string]core.SourceAdmission) ([]core.CoverageResult, error) {
	plan := domain.ContractPlan{}
	if contract != nil {
		plan = *contract
	}
	if err := planning.ValidateRoutingCoverage(report, route, plan); err != nil {
		return nil, fmt.Errorf("routing coverage companion rejected: %w", err)
	}
	result := []core.CoverageResult{}
	for _, project := range report.Projects {
		source, found := admitted[project.ProjectID]
		if !found || source.Identity == "" {
			return nil, fmt.Errorf("coverage project %q has no admitted source", project.ProjectID)
		}
		if project.SourceIdentity != "" && project.SourceIdentity != source.Identity {
			return nil, fmt.Errorf("coverage source identity differs from caller admission")
		}
		if graphRevision.MatchString(source.Revision) && project.DeclaredHeadCommit != "" && source.Revision != project.DeclaredHeadCommit {
			return nil, fmt.Errorf("coverage declared source revision differs from caller pin")
		}
		status, complete := routingCoverageStatus(project.Status)
		inventory := core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_inventory", Status: status, Complete: complete,
			Visited: project.VisitedFiles, Indexed: project.IndexedFiles, SkippedByPolicy: project.ExcludedFiles + project.ExcludedDirectories + project.ExcludedSymlinks + project.ExcludedNonRegular,
			SkippedLarge: project.SkippedLargeFiles, Unreadable: project.ReadErrors, Errors: project.ReadErrors,
			BytesSelected: project.IndexedBytes, FileLimit: report.Config.MaxVisitedFiles, ByteLimit: report.Config.MaxInventoryBytes,
			TerminatedByLimit: project.UnscannedRemainder}
		if project.UnscannedRemainder || project.ReadErrors > 0 || project.SkippedLargeFiles > 0 || project.UnsupportedFiles > 0 {
			inventory.Complete = false
			if status != core.Unknown {
				inventory.Status = core.Partial
			}
		}
		if project.UnscannedRemainder {
			inventory.Reasons = append(inventory.Reasons, fmt.Sprintf("unscanned_remainder_count_known=%t", project.RemainderCountKnown))
		}
		if project.TerminationReason != "" {
			inventory.Reasons = append(inventory.Reasons, project.TerminationReason)
		}
		if project.SourceIdentity == "" {
			result = append(result, core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_source_identity", Status: core.Unknown,
				Complete: false, RequirementState: core.NotVerified, Reasons: []string{"historical_source_identity_unverified"}})
		}
		result = append(result, inventory)
		for _, extraction := range project.Extractions {
			omitted := extraction.OmittedMatches + extraction.OmittedImports
			stage := core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_symbols:" + extraction.Path,
				Status: core.Complete, Complete: omitted == 0, CandidateCount: extraction.SymbolMatches + extraction.ImportMatches,
				SelectedCount: extraction.SelectedSymbols + extraction.SelectedImports, Omitted: omitted}
			if omitted > 0 {
				stage.Status, stage.TerminatedByLimit = core.Partial, true
				stage.Reasons = []string{"symbol_or_import_match_limit"}
			}
			result = append(result, stage)
		}
		for _, target := range project.Targets {
			stage := core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_targets:" + target.RouteID,
				Status: core.Complete, Complete: len(target.OmittedPaths) == 0, CandidateCount: target.Candidates,
				SelectedCount: target.Selected, Omitted: len(target.OmittedPaths)}
			if len(target.OmittedPaths) > 0 {
				stage.Status, stage.TerminatedByLimit = core.Partial, true
				stage.Reasons = []string{"target_file_limit"}
			}
			result = append(result, stage)
		}
		factsStatus, factsComplete := routingCoverageStatus(project.Facts.Status)
		result = append(result, core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_repository_facts",
			Status: factsStatus, Complete: factsComplete && len(project.Facts.OmittedPaths) == 0,
			CandidateCount: project.Facts.CandidateFiles, SelectedCount: project.Facts.SelectedFiles,
			BytesConsidered: project.Facts.CandidateBytes, BytesSelected: project.Facts.SelectedBytes,
			ByteLimit: report.Config.MaxFactsBytes, Omitted: len(project.Facts.OmittedPaths), TerminatedByLimit: len(project.Facts.OmittedPaths) > 0})
		for _, omission := range project.Omissions {
			if strings.HasPrefix(omission.Reason, "excluded_") || omission.Stage == "task_span" {
				continue
			}
			result = append(result, core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_omission:" + omission.Stage,
				Status: core.Partial, Complete: false, Omitted: omission.Count, Reasons: []string{omission.Reason}})
		}
	}
	for _, requirement := range report.Requirements {
		source, found := admitted[requirement.ProjectID]
		if !found || source.Identity == "" {
			return nil, fmt.Errorf("coverage requirement has no admitted source")
		}
		stage := core.CoverageResult{SourceIdentity: source.Identity, Stage: "routing_required:" + requirement.Kind,
			FacetID: facetID("routing_requirement", source.Identity, requirement.RouteID, requirement.Kind, requirement.Path),
			Status:  core.Complete, Complete: true, RequirementState: core.Found}
		if requirement.Status != "FOUND" {
			stage.Status, stage.Complete, stage.RequirementState = core.Partial, false, core.NotVerified
			if requirement.Status == "UNKNOWN" {
				stage.Status = core.Unknown
			}
			stage.Reasons = []string{requirement.Reason}
			if strings.Contains(requirement.Reason, "omitted") || strings.Contains(requirement.Reason, "limit") {
				stage.RequirementState = core.OmittedByLimit
			}
			if strings.Contains(requirement.Reason, "read_error") || strings.Contains(requirement.Reason, "unreadable") {
				stage.RequirementState = core.Unreadable
			}
		}
		result = append(result, stage)
	}
	status, complete := routingCoverageStatus(report.EvidenceProjection.Status)
	result = append(result, core.CoverageResult{Stage: "routing_evidence_projection", Status: status,
		Complete:       complete && len(report.EvidenceProjection.OmittedEvidenceIDs) == 0,
		CandidateCount: report.EvidenceProjection.UniqueCandidates, SelectedCount: report.EvidenceProjection.Selected,
		Omitted: len(report.EvidenceProjection.OmittedEvidenceIDs), FileLimit: report.Config.MaxEvidence,
		TerminatedByLimit: len(report.EvidenceProjection.OmittedEvidenceIDs) > 0})
	status, complete = routingCoverageStatus(report.Status)
	result = append(result, core.CoverageResult{Stage: "routing_report", Status: status, Complete: complete,
		Reasons: []string{"R1 companion " + report.DiagnosticDigest}})
	return compactRoutingCoverage(result), nil
}

// The bound companion retains per-path acquisition detail. Its in-pack projection
// sums only additive counters for equal stages and safety states. Required
// outcomes remain individually addressable by FacetID.
func compactRoutingCoverage(rows []core.CoverageResult) []core.CoverageResult {
	result := make([]core.CoverageResult, 0, len(rows))
	indexes := map[string]int{}
	for _, row := range rows {
		aggregate := strings.HasPrefix(row.Stage, "routing_symbols:") || strings.HasPrefix(row.Stage, "routing_omission:")
		if !aggregate {
			result = append(result, row)
			continue
		}
		if strings.HasPrefix(row.Stage, "routing_symbols:") {
			row.Stage = "routing_symbols"
		}
		shape := row
		shape.CandidateCount, shape.SelectedCount, shape.Omitted = 0, 0, 0
		raw, _ := json.Marshal(shape)
		key := string(raw)
		if index, ok := indexes[key]; ok {
			result[index].CandidateCount += row.CandidateCount
			result[index].SelectedCount += row.SelectedCount
			result[index].Omitted += row.Omitted
		} else {
			indexes[key] = len(result)
			result = append(result, row)
		}
	}
	return result
}

// Routing Symbols describes a file inventory, not one Go scalar selector.
func routingScalarSymbol(symbol string) string {
	if strings.Contains(symbol, ",") {
		return ""
	}
	return symbol
}

func routingCoverageStatus(value string) (core.Status, bool) {
	switch value {
	case "COMPLETE":
		return core.Complete, true
	case "PARTIAL":
		return core.Partial, false
	default:
		return core.Unknown, false
	}
}

func facetID(values ...string) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return "facet-" + hex.EncodeToString(sum[:])
}

func uniqueStrings(values []string) []string {
	sort.Strings(values)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

// AdaptTaskRoute keeps routing as the search/ownership boundary while explicit
// caller facets define the evidence question. Generic layer anchors are defaults
// for unscoped requests, not additional mandatory task facts. ContractPlan
// obligations remain mandatory even when the caller did not repeat them.
func AdaptTaskRoute(route domain.RoutingResult, sources map[string]core.SourceAdmission, contract *domain.ContractPlan, coverage []core.CoverageResult, facets []core.Facet) (core.RouteContext, error) {
	adapted, err := AdaptRoute(route, sources, contract, coverage)
	if err != nil || len(facets) == 0 {
		return adapted, err
	}
	obligations := map[string]bool{}
	if contract != nil {
		for _, boundary := range contract.Boundaries {
			source := sources[boundary.Owner.ProjectID]
			obligations[facetID("contract", source.Identity, boundary.Kind, boundary.TargetPath)] = true
		}
	}
	seeds := make([]core.Facet, 0, len(adapted.SeedFacets))
	for _, seed := range adapted.SeedFacets {
		keep := obligations[seed.ID]
		for _, f := range facets {
			if f.SourceIdentity != seed.SourceIdentity || f.Path == "" {
				continue
			}
			if seed.Path == f.Path || strings.HasPrefix(seed.Path, f.Path+"/") {
				keep = true
				break
			}
		}
		if keep {
			seeds = append(seeds, seed)
		}
	}
	adapted.SeedFacets = seeds
	return adapted, nil
}
