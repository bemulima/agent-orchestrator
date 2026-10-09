package contextretrieval

import (
	"path"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ProjectReadAreaGap certifies an attribution search in the admitted snapshot,
// never repository-wide absence, a business role, or execution authority.
type ProjectReadAreaGap struct {
	Source           string                `json:"source"`
	SourceSnapshot   string                `json:"source_snapshot"`
	AdmissionDigest  string                `json:"admission_digest"`
	Anchor           AnalysisAnchor        `json:"anchor"`
	FacetIDs         []string              `json:"facet_ids"`
	RequirementState core.RequirementState `json:"requirement_state"`
	Reason           string                `json:"reason"`
	Coverage         core.CoverageResult   `json:"coverage"`
	CatalogDigest    string                `json:"catalog_digest"`
	ProfileID        string                `json:"profile_id"`
	MetadataChain    []AnalysisAnchor      `json:"metadata_chain"`
	DiagnosticID     string                `json:"diagnostic_id"`
}

type projectOperationArea struct {
	declaration analysisDeclaration
	metadata    core.Document
	unpinned    bool
}
type projectOperationSearch struct {
	areas       []projectOperationArea
	diagnostics []core.RetrievalDiagnostic
	blockers    []core.RetrievalDiagnostic
}

func projectOperationAreas(snapshot core.SnapshotResult, selectors []core.Facet) projectOperationSearch {
	declarations := projectAnalysisDeclarations(snapshot)
	out := projectOperationSearch{areas: []projectOperationArea{}, diagnostics: []core.RetrievalDiagnostic{}, blockers: []core.RetrievalDiagnostic{}}
	block := func(d core.RetrievalDiagnostic) {
		out.diagnostics = append(out.diagnostics, d)
		out.blockers = append(out.blockers, d)
	}
	for _, doc := range snapshot.Documents {
		if !strings.HasPrefix(doc.RelativePath, ".ai/architecture/endpoints/") || !strings.HasSuffix(doc.RelativePath, ".yaml") {
			continue
		}
		operation, err := architecturemanifest.ParseOperation([]byte(doc.Content))
		if err != nil {
			block(projectInvalidMetadata(doc, "Strict operation metadata invalid; authored-area search is not certified complete."))
			continue
		}
		for _, ref := range operation.Implementation.UseCases {
			matches := []analysisDeclaration{}
			if ref.Symbol != "" && core.SafeRelativePath(ref.SourcePath) && !strings.HasSuffix(ref.SourcePath, "_test.go") {
				for _, d := range declarations {
					if d.doc.Source.Identity == doc.Source.Identity && d.doc.RelativePath == ref.SourcePath && (ref.Checksum == "" || core.EqualContentHash(ref.Checksum, d.doc.ContentHash)) && symbolMatches(d.symbol, ref.Symbol) {
						matches = append(matches, d)
					}
				}
			}
			ids := []string{}
			if len(matches) == 1 {
				for _, f := range selectors {
					if f.ID == "" || f.Symbol == "" || f.SourceIdentity != doc.Source.Identity || f.Path != ref.SourcePath || !core.EqualContentHash(f.ExpectedHash, matches[0].doc.ContentHash) {
						continue
					}
					count := 0
					for _, d := range declarations {
						if d.doc.Source.Identity == f.SourceIdentity && d.doc.RelativePath == f.Path && symbolMatches(d.symbol, f.Symbol) && analysisDeclarationUnique(d, declarations) {
							count++
						}
					}
					if count == 1 {
						ids = append(ids, f.ID)
					}
				}
			}
			if len(matches) == 1 && len(ids) > 0 && projectOperationSymbolUnique(matches[0], ref.Symbol, declarations) && projectPackageVerified(snapshot, doc.Source.Identity, path.Dir(ref.SourcePath)) && matches[0].doc.Source.Snapshot == doc.Source.Snapshot && matches[0].doc.Source.AdmissionDigest == doc.Source.AdmissionDigest {
				out.areas = append(out.areas, projectOperationArea{matches[0], doc, ref.Checksum == ""})
				if ref.Checksum == "" {
					sort.Strings(ids)
					ids = projectUniqueStrings(ids)
					id := "analysis-current-operation:" + hashJSON(struct {
						Metadata, Source AnalysisAnchor
						Selectors        []string
					}{AnalysisAnchor{Source: doc.Source.Identity, Path: doc.RelativePath, Hash: doc.ContentHash}, analysisAnchor(matches[0]), ids})
					for _, facetID := range ids {
						out.diagnostics = append(out.diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_OPERATION_REFERENCE_UNPINNED", Status: core.Partial, SourceIdentity: doc.Source.Identity, RelativePath: doc.RelativePath, FacetID: facetID, EvidenceID: id, Message: "Authored operation checksum absent; literal current source reference independently pinned to admitted caller/source/metadata snapshot only. No historical or Git-object certification."})
					}
				}
			} else {
				block(core.RetrievalDiagnostic{Code: "ANALYSIS_OPERATION_AREA_NOT_VERIFIED", Status: core.Partial, SourceIdentity: doc.Source.Identity, RelativePath: doc.RelativePath, Message: "Strict operation current source/hash/unique symbol and explicit caller pin not verified.", EvidenceID: ref.SourcePath})
			}
		}
	}
	return out
}

func projectReadAreaGaps(snapshot core.SnapshotResult, request core.RetrievalRequest, route domain.RoutingResult, catalog agentcontrol.Catalog) ([]ProjectReadAreaGap, []core.RetrievalDiagnostic) {
	declarations := projectAnalysisDeclarations(snapshot)
	areas, areaDiagnostics := projectReadAreaDeclarations(snapshot)
	search := projectOperationAreas(snapshot, request.RequiredFacets)
	operations := search.areas
	invalid := append(areaDiagnostics, search.blockers...)
	out := []ProjectReadAreaGap{}
	diagnostics := []core.RetrievalDiagnostic{}
	index := map[string]int{}
	for _, f := range request.RequiredFacets {
		if f.ID == "" || f.Symbol == "" || f.ExpectedHash == "" || !core.SafeRelativePath(f.Path) || !strings.HasSuffix(f.Path, ".go") || strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		matches := []analysisDeclaration{}
		for _, d := range declarations {
			if d.doc.Source.Identity == f.SourceIdentity && d.doc.RelativePath == f.Path && core.EqualContentHash(f.ExpectedHash, d.doc.ContentHash) && symbolMatches(d.symbol, f.Symbol) {
				matches = append(matches, d)
			}
		}
		if len(matches) != 1 || !analysisDeclarationUnique(matches[0], declarations) {
			continue
		}
		a := matches[0]
		profileID := ""
		var profile agentcontrol.Profile
		for _, source := range request.Sources {
			if source.Identity == f.SourceIdentity {
				for _, p := range route.Profiles {
					if p.ProjectID == source.RouteIdentity && projectVerifiedProfile(catalog, p) {
						if resolved, ok := catalog.Profiles[p.ProfileID]; ok {
							profileID = p.ProfileID
							profile = resolved
						}
					}
				}
			}
		}
		member := false
		for _, r := range profile.Routes {
			member = member || analysisPathMatches(f.Path, r.Targets)
		}
		for _, d := range areas {
			if d.doc.Source.Identity == f.SourceIdentity && (f.Path == d.root || strings.HasPrefix(f.Path, d.root+"/")) {
				member = true
			}
		}
		for _, op := range operations {
			if op.declaration.doc.Source.Identity == f.SourceIdentity && op.declaration.doc.RelativePath == f.Path && op.declaration.doc.ContentHash == a.doc.ContentHash {
				member = true
			}
		}
		if member {
			continue
		}
		coverage := CoverageForFacet(snapshot, core.Facet{SourceIdentity: f.SourceIdentity}, 0)
		coverage.Stage = "authored_read_area_attribution"
		if !projectPackageVerified(snapshot, f.SourceIdentity, path.Dir(f.Path)) {
			coverage.Complete = false
			coverage.Reasons = append(coverage.Reasons, "anchor_package_not_verified")
		}
		coverage.RequirementState = core.NotVerified
		coverage.Status = core.Partial
		if profileID == "" {
			coverage.Complete = false
			coverage.Reasons = append(coverage.Reasons, "profile_unresolved")
		}
		chain := []AnalysisAnchor{}
		for _, doc := range snapshot.Documents {
			if doc.Source.Identity != f.SourceIdentity {
				continue
			}
			if doc.RelativePath == ".ai/architecture.yaml" || doc.RelativePath == ".ai/service.yaml" || strings.HasPrefix(doc.RelativePath, ".ai/architecture/endpoints/") && strings.HasSuffix(doc.RelativePath, ".yaml") {
				role := "authored_area_manifest"
				if strings.HasPrefix(doc.RelativePath, ".ai/architecture/endpoints/") {
					role = "strict_operation"
				}
				chain = append(chain, AnalysisAnchor{Source: doc.Source.Identity, Path: doc.RelativePath, Hash: doc.ContentHash, Symbol: role})
			}
		}
		for _, d := range invalid {
			if d.SourceIdentity == f.SourceIdentity && (d.Code != "ANALYSIS_OPERATION_AREA_NOT_VERIFIED" || d.EvidenceID == f.Path || d.EvidenceID == "") {
				coverage.Complete = false
				coverage.Reasons = append(coverage.Reasons, d.Code+":"+d.RelativePath)
			}
		}
		// Policy omissions outside supported metadata do not imply an incomplete
		// metadata search. Missing admitted metadata, unreadability and limits do.
		for _, d := range snapshot.Diagnostics {
			if d.SourceIdentity == f.SourceIdentity && projectAcquisitionOmission(d) && projectMetadataPathRelevant(d.RelativePath) {
				coverage.Complete = false
				coverage.Reasons = append(coverage.Reasons, d.Code+":"+d.RelativePath)
			}
		}
		sort.Strings(coverage.Reasons)
		sort.Slice(chain, func(i, j int) bool { return hashJSON(chain[i]) < hashJSON(chain[j]) })
		k := hashJSON(analysisAnchor(a))
		if i, ok := index[k]; ok {
			out[i].FacetIDs = append(out[i].FacetIDs, f.ID)
			continue
		}
		index[k] = len(out)
		out = append(out, ProjectReadAreaGap{Source: f.SourceIdentity, SourceSnapshot: a.doc.Source.Snapshot, AdmissionDigest: a.doc.Source.AdmissionDigest, Anchor: analysisAnchor(a), FacetIDs: []string{f.ID}, RequirementState: core.NotVerified, Reason: "No supported authored read-area binding for this current anchor in admitted search scope.", Coverage: coverage, CatalogDigest: catalog.Digest, ProfileID: profileID, MetadataChain: chain})
	}
	for i := range out {
		sort.Strings(out[i].FacetIDs)
		out[i].FacetIDs = projectUniqueStrings(out[i].FacetIDs)
		out[i].DiagnosticID = "analysis-read-area-gap:" + hashJSON(out[i])
		for _, id := range out[i].FacetIDs {
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_AUTHORED_READ_AREA_MISSING", Status: core.Partial, SourceIdentity: out[i].Source, RelativePath: out[i].Anchor.Path, FacetID: id, EvidenceID: out[i].DiagnosticID, Message: "Current anchor hash " + out[i].Anchor.Hash + " has NOT_VERIFIED authored attribution in admitted metadata search scope; no runtime/write authority."})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Source+out[i].Anchor.Path+out[i].Anchor.Hash+out[i].Anchor.Symbol < out[j].Source+out[j].Anchor.Path+out[j].Anchor.Hash+out[j].Anchor.Symbol
	})
	return out, diagnostics
}

func projectUniqueStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func projectMetadataPathRelevant(p string) bool {
	if p == "" {
		return true
	}
	for _, target := range []string{".ai/architecture.yaml", ".ai/service.yaml", ".ai/architecture/endpoints"} {
		if p == target || strings.HasPrefix(target, p+"/") {
			return true
		}
	}
	return strings.HasPrefix(p, ".ai/architecture/endpoints/") && strings.HasSuffix(p, ".yaml")
}

func projectVerifiedProfile(catalog agentcontrol.Catalog, profile domain.ArchitectureProfileResolution) bool {
	fingerprint, err := agentcontrol.ProfileFingerprint(catalog, profile.ProfileID)
	return err == nil && profile.Status == domain.ProfileResolutionResolved && profile.ProfileFingerprint == fingerprint
}

func projectOperationSymbolUnique(selected analysisDeclaration, symbol string, declarations []analysisDeclaration) bool {
	count := 0
	for _, d := range declarations {
		if d.doc.Source.Identity == selected.doc.Source.Identity && path.Dir(d.doc.RelativePath) == path.Dir(selected.doc.RelativePath) && !strings.HasSuffix(d.doc.RelativePath, "_test.go") && symbolMatches(d.symbol, symbol) {
			count++
		}
	}
	return count == 1
}

func projectAcquisitionOmission(d core.RetrievalDiagnostic) bool {
	switch d.Code {
	case "EXCLUDED_BY_POLICY", "SECRET_CONTENT_EXCLUDED", "FILE_TYPE_EXCLUDED", "UNREADABLE_SOURCE", "SOURCE_TOO_LARGE", "BYTE_LIMIT", "FILE_LIMIT", "DEPTH_LIMIT", "GLOBAL_BYTE_LIMIT":
		return true
	}
	return false
}
