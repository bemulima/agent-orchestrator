package contextretrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

type memoryLoader struct {
	content  string
	calls    int
	mutateOn int
}

func (l *memoryLoader) Load(ctx context.Context, sources []SourceAdmission, limits Limits) (SnapshotResult, error) {
	l.calls++
	if err := ctx.Err(); err != nil {
		return SnapshotResult{}, err
	}
	if l.mutateOn == l.calls {
		l.content += "\nchanged"
	}
	hash := sha256.Sum256([]byte(l.content))
	digest := hex.EncodeToString(hash[:])
	source := EvidenceSource{Identity: sources[0].Identity, Revision: "snapshot:" + digest, Snapshot: digest, AdmissionDigest: hashJSON(sources)}
	return SnapshotResult{Sources: []EvidenceSource{source}, Documents: []Document{{Source: source, RelativePath: "main.go", Content: l.content, ContentHash: digest}}, Coverage: []CoverageResult{{SourceIdentity: source.Identity, Stage: "inventory", Status: Complete, Complete: true, Indexed: 1}}}, nil
}

type memoryResolver struct{ forge bool }

func (memoryResolver) ID() string      { return "exact" }
func (memoryResolver) Version() string { return "test.v1" }
func (r memoryResolver) Resolve(ctx context.Context, plan RetrievalPlan, facet Facet, snapshot SnapshotResult) (RetrievalResult, error) {
	doc := snapshot.Documents[0]
	c := EvidenceCandidate{EvidenceID: "raw", SourceIdentity: doc.Source.Identity, SourceRevision: doc.Source.Revision, SourceSnapshot: doc.Source.Snapshot, RelativePath: doc.RelativePath, ContentHash: doc.ContentHash, Span: EvidenceSpan{StartLine: 1, EndLine: 1 + strings.Count(doc.Content, "\n"), EndByte: len(doc.Content)}, EvidenceKind: "source", Provenance: ProjectSource, Freshness: Current, ClaimType: facet.ClaimType, Content: doc.Content, FacetIDs: []string{facet.ID}}
	if r.forge {
		c.Content = "forged"
	}
	return RetrievalResult{Candidates: []EvidenceCandidate{c}, Coverage: []CoverageResult{facetCoverage(facet, Complete, Found, true, "exact span")}}, nil
}
func memoryRequest() RetrievalRequest {
	return RetrievalRequest{SchemaVersion: SchemaVersion, Task: "inspect function", Purpose: "review", Route: RouteContext{Digest: "route1", Status: Complete, Owners: []string{"repo"}}, Sources: []SourceAdmission{{Identity: "repo", Root: "/explicit", ReadPaths: []string{"."}}}, RequiredFacets: []Facet{{ID: "main", Kind: "source", QueryKind: QueryExact, SourceIdentity: "repo", Path: "main.go", ClaimType: ImplementationBehavior, Required: true}}, Budget: Budget{MaxContextTokens: 32768, MaxSourceBytes: 10000}, Limits: Limits{MaxExpandDepth: 2}}
}
func memoryEngine(l *memoryLoader) *Engine {
	return &Engine{Loader: l, Resolvers: map[string]Resolver{"exact": memoryResolver{}}}
}

func TestPlanDoesNotGrantWrites(t *testing.T) {
	request := memoryRequest()
	request.Sources = append(request.Sources, SourceAdmission{Identity: "neighbor", Root: "/other", ReadPaths: []string{"src"}, Neighbor: true})
	request.Route.Constraints = []RouteConstraint{{SourceIdentity: "neighbor", Polarity: "negative", Decision: "not_allowed"}}
	plan, err := BuildPlan(request)
	if err != nil {
		t.Fatal(err)
	}
	if hashJSON(plan.Route) != hashJSON(request.Route) {
		t.Fatal("route changed")
	}
	if strings.Join(plan.ForbiddenScope.Write, ",") != "neighbor,repo" || strings.Join(plan.ForbiddenScope.ReadOnlyEvidence, ",") != "neighbor" {
		t.Fatalf("read-only boundary lost: %+v", plan.ForbiddenScope)
	}
	if PathAdmitted(request.Sources[1], "src2/file.go") {
		t.Fatal("prefix admitted sibling")
	}
	for _, p := range []string{"../outside.go", ".env", "keys/private.pem", "src/../main.go"} {
		request.RequiredFacets[0].Path = p
		if _, err := BuildPlan(request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unsafe selector %q accepted", p)
		}
	}
}
func TestPrepareDeterminismAndSemanticChanges(t *testing.T) {
	ctx := context.Background()
	loader := &memoryLoader{content: "package p\nfunc Run() {}\n"}
	engine := memoryEngine(loader)
	request := memoryRequest()
	a, trace, err := engine.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := engine.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != Complete || a.ContentDigest != b.ContentDigest || len(a.Evidence) != 1 {
		t.Fatalf("bad deterministic pack: %s %+v", a.Status, a.UnresolvedQuestions)
	}
	if err := VerifyDigest(a); err != nil {
		t.Fatal(err)
	}
	if trace.PackDigest != a.ContentDigest || trace.SelectedCount != 1 {
		t.Fatal("trace mismatch")
	}
	request.RequestID = "a much longer operational request identifier"
	differentID, _, err := engine.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if differentID.ContentDigest != a.ContentDigest {
		t.Fatal("request ID entered semantic digest")
	}
	request.Route.Digest = "route2"
	changedRoute, _, _ := engine.Prepare(ctx, request)
	if changedRoute.ContentDigest == a.ContentDigest {
		t.Fatal("route ignored")
	}
	loader.content += "func Other() {}\n"
	changedSource, _, _ := engine.Prepare(ctx, memoryRequest())
	if changedSource.ContentDigest == a.ContentDigest {
		t.Fatal("source ignored")
	}
}
func TestPrepareRejectsForgedSpanAndConcurrentMutation(t *testing.T) {
	engine := memoryEngine(&memoryLoader{content: "package p"})
	engine.Resolvers["exact"] = memoryResolver{forge: true}
	pack, _, err := engine.Prepare(context.Background(), memoryRequest())
	if err != nil {
		t.Fatal(err)
	}
	if pack.Status == Complete || len(pack.Evidence) != 0 {
		t.Fatal("forged resolver bytes accepted")
	}
	engine = memoryEngine(&memoryLoader{content: "package p", mutateOn: 2})
	if _, _, err = engine.Prepare(context.Background(), memoryRequest()); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("mutation not rejected: %v", err)
	}
}
func TestExpandScopeSnapshotDepthAndBudget(t *testing.T) {
	ctx := context.Background()
	request := memoryRequest()
	loader := &memoryLoader{content: "package p\nfunc Run() {}\n"}
	engine := memoryEngine(loader)
	base, _, err := engine.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	expansion := ExpandRequest{Facet: Facet{ID: "extra", Kind: "source", QueryKind: QueryExact, SourceIdentity: "repo", Path: "main.go", ClaimType: ImplementationBehavior}, Reason: "inspect another facet"}
	delta, _, err := engine.Expand(ctx, base, request, expansion)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Pack.BudgetUsed.ExpandCount != 1 || delta.Pack.BudgetUsed.CumulativeSourceBytes < base.BudgetUsed.CumulativeSourceBytes {
		t.Fatal("cumulative accounting refunded")
	}
	expansion.Facet.ID = "extra2"
	delta2, _, err := engine.Expand(ctx, delta.Pack, request, expansion)
	if err != nil {
		t.Fatal(err)
	}
	if delta2.Pack.BudgetUsed.ExpandCount != 2 {
		t.Fatal("expand count")
	}
	expansion.Facet.ID = "extra3"
	stopped, _, err := engine.Expand(ctx, delta2.Pack, request, expansion)
	if err != nil || stopped.Status != Blocked {
		t.Fatalf("depth not bounded: %v %+v", err, stopped)
	}
	request.Sources[0].ReadPaths = []string{"main.go"}
	if _, _, err = engine.Expand(ctx, base, request, expansion); !errors.Is(err, ErrScopeChange) {
		t.Fatalf("scope not bound: %v", err)
	}
	request = memoryRequest()
	expansion.Facet.SourceIdentity = "outside"
	if _, _, err = engine.Expand(ctx, base, request, expansion); !errors.Is(err, ErrScopeChange) {
		t.Fatalf("neighbor escape: %v", err)
	}
	expansion.Facet.SourceIdentity = "repo"
	expansion.RemainingBudget.MaxContextTokens = 1
	limited, _, err := engine.Expand(ctx, base, request, expansion)
	if err != nil || limited.Status != Blocked {
		t.Fatalf("remaining budget ignored: %v %s", err, limited.Status)
	}
	expansion.RemainingBudget = Budget{}
	expansion.RemainingBudget.PerFacetTokens = 1
	perFacet, _, err := engine.Expand(ctx, base, request, expansion)
	if err != nil || perFacet.Status != Blocked {
		t.Fatalf("remaining per-facet budget ignored: %v %s", err, perFacet.Status)
	}
	expansion.RemainingBudget = Budget{}
	loader.content += "// changed"
	if _, _, err = engine.Expand(ctx, base, request, expansion); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("stale accepted: %v", err)
	}
}
func TestUnsupportedAndCancelledRetrieval(t *testing.T) {
	request := memoryRequest()
	request.RequiredFacets[0].QueryKind = QueryCallers
	pack, _, err := memoryEngine(&memoryLoader{content: "package p"}).Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Status == Complete {
		t.Fatal("unavailable capability marked complete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = memoryEngine(&memoryLoader{content: "package p"}).Prepare(ctx, memoryRequest()); err == nil {
		t.Fatal("cancelled request ran")
	}
}

func TestExpandRejectsRecomputedUntrustedCache(t *testing.T) {
	ctx := context.Background()
	request := memoryRequest()
	engine := memoryEngine(&memoryLoader{content: "package p\nfunc Run() {}\n"})
	base, _, err := engine.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	expansion := ExpandRequest{Facet: Facet{ID: "extra", Kind: "source", QueryKind: QueryExact, SourceIdentity: "repo", Path: "main.go", ClaimType: ImplementationBehavior}, Reason: "additional context"}
	forged := base
	forged.Evidence = append([]EvidenceCandidate{}, base.Evidence...)
	forged.Evidence[0].Provenance = TrustedProjectMetadata
	measurePack(&forged, BudgetUsed{})
	forged.ContentDigest, _ = SemanticDigest(forged)
	if err := VerifyDigest(forged); err != nil {
		t.Fatal("fixture accounting must be self-consistent", err)
	}
	if _, _, err = engine.Expand(ctx, forged, request, expansion); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cache could promote provenance: %v", err)
	}
	for _, mode := range []string{"depth", "budget"} {
		t.Run("early_"+mode, func(t *testing.T) {
			bad := base
			bad.Evidence = append([]EvidenceCandidate{}, base.Evidence...)
			bad.Evidence[0].Provenance = TrustedProjectMetadata
			step := expansion
			if mode == "depth" {
				bad.BudgetUsed.ExpandCount = request.Limits.MaxExpandDepth
			} else {
				step.RemainingBudget.MaxSourceBytes = request.Budget.MaxSourceBytes
			}
			measurePack(&bad, BudgetUsed{})
			bad.ContentDigest, _ = SemanticDigest(bad)
			if err := VerifyDigest(bad); err != nil {
				t.Fatal(err)
			}
			if delta, _, err := engine.Expand(ctx, bad, request, step); !errors.Is(err, ErrInvalidRequest) || len(delta.Pack.Evidence) > 0 {
				t.Fatalf("early guard returned untrusted base: %v %+v", err, delta)
			}
		})
	}
	valid, _, err := engine.Expand(ctx, base, request, expansion)
	if err != nil {
		t.Fatal(err)
	}
	forged = valid.Pack
	forged.BudgetUsed.ExpandCount = 0
	measurePack(&forged, BudgetUsed{})
	forged.ContentDigest, _ = SemanticDigest(forged)
	if _, _, err = engine.Expand(ctx, forged, request, expansion); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cache could reset cumulative depth: %v", err)
	}
}
