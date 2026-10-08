package capability

import (
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"testing"
)

func TestCapabilityAcceptanceKeepsSemanticAndLexicalSeparate(t *testing.T) {
	pack := core.ContextPack{Status: core.Partial, Evidence: []core.EvidenceCandidate{{SourceIdentity: "local:x", RelativePath: "migration.sql", FacetIDs: []string{"lexical"}}}, Coverage: []core.CoverageResult{{FacetID: "semantic", RequirementState: core.RequirementUnsupported}}, UnresolvedQuestions: []core.RetrievalDiagnostic{{FacetID: "semantic", Code: "UNSUPPORTED_QUERY"}}}
	req := []CapabilityRequirement{
		{Selector: Selector{Source: "local:x", Path: "migration.sql", Facet: "lexical"}, Class: SupportedRequired, Basis: "exact literal retrieval"},
		{Selector: Selector{Source: "local:x", Path: "migration.sql", Facet: "semantic"}, Class: UnsupportedSemantic, Diagnostic: "UNSUPPORTED_QUERY", Basis: "schema semantic capability unavailable"},
	}
	r := EvaluateCapabilities(pack, req)
	if !r.Passed || r.Total != 2 || r.Categories[SupportedRequired].Expected != 1 || r.Categories[UnsupportedSemantic].Expected != 1 {
		t.Fatalf("lost denominator/distinction: %+v", r)
	}
	pack.Evidence[0].FacetIDs = append(pack.Evidence[0].FacetIDs, "semantic")
	if EvaluateCapabilities(pack, req).Passed {
		t.Fatal("lexical evidence impersonated semantic support")
	}
}
func TestCapabilityAcceptanceRejectsSilentAndUnprovenMissing(t *testing.T) {
	pack := core.ContextPack{Status: core.Partial, Coverage: []core.CoverageResult{{FacetID: "metric", RequirementState: core.NotVerified}}, UnresolvedQuestions: []core.RetrievalDiagnostic{{FacetID: "metric", Code: "REQUIRED_EVIDENCE_NOT_VERIFIED"}}}
	req := []CapabilityRequirement{{Selector: Selector{Facet: "metric"}, Class: MissingBusinessContract, Diagnostic: "REQUIRED_EVIDENCE_NOT_VERIFIED", Basis: "owner contract absent"}}
	if EvaluateCapabilities(pack, req).Passed {
		t.Fatal("incomplete search certified absence")
	}
	req[0].Class = SecurityExcluded
	req[0].Diagnostic = "REQUIRED_EVIDENCE_EXCLUDED_BY_SECURITY"
	if EvaluateCapabilities(pack, req).Passed {
		t.Fatal("silent security exclusion waived")
	}
}
