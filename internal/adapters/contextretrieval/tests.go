package contextretrieval

import (
	"context"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

type TestResolver struct{}

func (TestResolver) ID() string      { return "tests" }
func (TestResolver) Version() string { return "project.v1" }

func (r TestResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if facet.QueryKind != core.QueryTests {
		return unsupportedResult(facet, r.ID()), nil
	}
	if err := ctx.Err(); err != nil {
		return core.RetrievalResult{}, err
	}
	if facet.Path == ".ai/testing/test-manifest.yaml" || facet.Path == ".ai/testing/test-manifest.yml" {
		doc, found := LookupDocument(snapshot, facet.SourceIdentity, facet.Path)
		result := core.RetrievalResult{Coverage: []core.CoverageResult{CoverageForFacet(snapshot, facet, boolCount(found))}}
		if found {
			candidate := CandidateFromDocument(doc, facet, r.ID(), r.Version(), core.EvidenceSpan{}, "test_manifest", core.TrustedProjectMetadata)
			candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "testing_declaration", Basis: "existing test-manifest; reviewed policy runner remains the certification authority"}
			candidate.Limitations = []string{"Suite PRESENT and command AVAILABLE are declarations. Test certification is NOT_VERIFIED; no command was executed."}
			result.Candidates = []core.EvidenceCandidate{candidate}
		}
		return result, nil
	}
	goFacet := facet
	goFacet.QueryKind = core.QueryTests
	result, err := (GoResolver{}).Resolve(ctx, plan, goFacet, snapshot)
	if err != nil {
		return result, err
	}
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		candidate.Resolver, candidate.ResolverVersion = r.ID(), r.Version()
		candidate.EvidenceKind, candidate.Provenance = "test", core.ProjectTest
		candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "executable_assertion", Basis: "actual test source; syntax association only, execution not certified"}
		candidate.Limitations = append(candidate.Limitations, "Test certification is NOT_VERIFIED; locating assertions does not prove a successful run.")
	}
	if len(result.Candidates) == 0 && facet.Path != "" && !strings.HasSuffix(facet.Path, ".go") {
		return unavailableFacet(facet, core.RequirementUnsupported, "TEST_LANGUAGE_UNSUPPORTED", "Only Go syntax test associations are supported; the existing manifest can be retrieved as data."), nil
	}
	return result, nil
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
