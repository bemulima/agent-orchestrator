package contextretrieval

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const ProjectAnalysisVersion = "source-bound-analysis.v1.2"

var projectReadStart = regexp.MustCompile(`(?i)^\s*(?:in\s+[^,]+,\s*)?(inspect|trace|investigate|audit|review|locate|verify|explain|analyze|analyse|select|distinguish|identify|check|determine)\b`)
var projectSourceToken = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+\b|\b[A-Za-z_][A-Za-z0-9_]*[a-z][A-Z][A-Za-z0-9_]*\b`)
var projectLiteralPath = regexp.MustCompile(`(?:/[A-Za-z0-9_.:{}-]+)+`)
var projectAction = regexp.MustCompile(`(?i)\b(add|change|modify|implement|delete|remove|rewrite|update|fix|create|replace|refactor|apply|patch|write|extend|deploy|execute|publish|run|launch|start|stop|merge|push|send|install|append)\b`)

// ProjectAnalysisIntent recognizes source selectors as data. Its finite read
// and nominal vocabulary never grants execution or accepts an unknown action.
func ProjectAnalysisIntent(task string) string {
	if !projectReadStart.MatchString(task) {
		return "AMBIGUOUS_INTENT"
	}
	text := projectLiteralPath.ReplaceAllString(task, "source")
	text = regexp.MustCompile(`(?i)\bversion-append\s+(?:conditions|rules|policy)\b`).ReplaceAllString(text, "version conditions")
	text = projectSourceToken.ReplaceAllString(text, "source")
	text = regexp.MustCompile(`(?i)\bappend idempotency\b`).ReplaceAllString(text, "idempotency")
	sentences := regexp.MustCompile(`(?i)[;.!?]\s+|[\n&]+|\s+(?:then|and then|but)\s+`).Split(text, -1)
	nominal := map[string]bool{}
	for _, w := range strings.Fields("a an the and or with without including source input output request response schema domain application composition http api contracts contract tests test provider consumer repository persistence transport parameter version versioning error errors boundary boundaries current existing exact bounded strict safe private public trusted active related immutable semantic fenced typed opaque required optional associated advisory localized token-protected body-free per-member disabled-space ready-before-buffered fail-closed mode dependency failure observation gateway origin single-use resource compose docker portable result validation workspace lifecycle reuse broker storage idle cold legacy reread attestation configured checks retained rejected terminalizing persisting difference full complete durable canonical learner-state completion for its internal backend normalization sanitized default-off empty-array source-message membership outbox realtime read-only execution ttl runtime prompt permission runner language idempotent migration member inline delegation selection outbound registry ownership shapes stage stages evidence context replay token ordered failed ticket digest technical receipt pedagogy candidate timed-out archive validator regression idempotency executable teacher student") {
		nominal[w] = true
	}
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		negativeSentence := regexp.MustCompile(`(?i)^(?:no|do not|must not|never|cannot|without)\b`).MatchString(sentence)
		if !projectReadStart.MatchString(sentence) && !negativeSentence {
			return "AMBIGUOUS_INTENT"
		}
		clauses := regexp.MustCompile(`(?i),\s*|\s+(?:and|or)\s+`).Split(sentence, -1)
		for i, clause := range clauses {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}
			lc := strings.ToLower(clause)
			negative := regexp.MustCompile(`(?i)^(?:no|do not|must not|never|cannot|without)\b`).MatchString(clause)
			for _, action := range projectAction.FindAllStringIndex(lc, -1) {
				prefix := lc[:action[0]]
				describedNegative := projectReadStart.MatchString(clause) && regexp.MustCompile(`(?i)\b(?:cannot|must not|does not|never)\s*$`).MatchString(prefix)
				if negative || describedNegative {
					continue
				}
				if analysisCondition.MatchString(task) {
					return "CONDITIONAL_MUTATION"
				}
				return "EXPLICIT_MUTATION"
			}
			if i > 0 && !negative && !projectReadStart.MatchString(clause) {
				words := strings.Fields(lc)
				if len(words) == 0 {
					continue
				}
				first := strings.Trim(words[0], ".:()")
				if !nominal[first] {
					return "AMBIGUOUS_INTENT"
				}
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(task)), "explain ") {
		return "EXPLANATION"
	}
	return "READ_ONLY_ANALYSIS"
}

func NewProjectAnalysisEngine() *core.Engine {
	e := NewAnalysisEngine()
	e.Resolvers["analysis-boundary"] = ProjectInterfaceBoundaryResolver{}
	e.Resolvers["analysis-project-boundary"] = ProjectBoundaryResolver{}
	return e
}

func PrepareProjectAnalysisRequest(ctx context.Context, request core.RetrievalRequest, route domain.RoutingResult, contract *domain.ContractPlan, coverage []core.CoverageResult, catalog agentcontrol.Catalog) (core.RetrievalRequest, AnalysisScope, error) {
	return prepareAnalysisRequest(ctx, request, route, contract, coverage, catalog, true, nil)
}

// ProjectAnalysisScope binds a separate cumulative companion to the exact pack.
// Scope.Digest remains the intrinsic read-scope digest; BindingDigest avoids a
// circular pack→route→scope→pack dependency and binds the added fields.
type ProjectAnalysisScope struct {
	ReadAreaGaps      []ProjectReadAreaGap `json:"read_area_gaps"`
	AttributionStatus core.Status          `json:"attribution_status"`
	AnalysisScope
	ContextPackDigest         string                     `json:"context_pack_digest"`
	InitialSelectorsDigest    string                     `json:"initial_selectors_digest"`
	CumulativeSelectorsDigest string                     `json:"cumulative_selectors_digest"`
	ExpansionHistoryDigest    string                     `json:"expansion_history_digest"`
	MetadataDiagnostics       []core.RetrievalDiagnostic `json:"metadata_diagnostics"`
	BindingDigest             string                     `json:"binding_digest"`
	JointBudgetStatus         core.Status                `json:"joint_budget_status"`
	JointBudgetUsed           ProjectJointBudget         `json:"joint_budget_used"`
	BudgetDiagnostics         []core.RetrievalDiagnostic `json:"budget_diagnostics"`
	BoundaryProofs            []ProjectBoundaryProof     `json:"boundary_proofs"`
}

func PrepareProjectContext(ctx context.Context, request core.RetrievalRequest, route domain.RoutingResult, contract *domain.ContractPlan, coverage []core.CoverageResult, catalog agentcontrol.Catalog) (core.ContextPack, core.RetrievalTrace, ProjectAnalysisScope, error) {
	prepared, scope, err := PrepareProjectAnalysisRequest(ctx, request, route, contract, coverage, catalog)
	if err != nil {
		return core.ContextPack{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, err
	}
	pack, trace, err := NewProjectAnalysisEngine().Prepare(ctx, prepared)
	if err != nil {
		return pack, trace, ProjectAnalysisScope{}, err
	}
	companion, err := bindProjectCompanion(ctx, prepared, scope, pack, route, contract, coverage, catalog)
	return pack, trace, companion, err
}

func ExpandProjectContext(ctx context.Context, base core.ContextPack, request core.RetrievalRequest, route domain.RoutingResult, contract *domain.ContractPlan, coverage []core.CoverageResult, catalog agentcontrol.Catalog, expand core.ExpandRequest) (core.ContextDelta, core.RetrievalTrace, ProjectAnalysisScope, error) {
	if err := core.VerifyDigest(base); err != nil {
		return core.ContextDelta{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, fmt.Errorf("%w: base digest", core.ErrInvalidRequest)
	}
	if base.SchemaVersion != core.SchemaVersion || base.Engine.Version != core.EngineVersion {
		return core.ContextDelta{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, fmt.Errorf("%w: base ABI", core.ErrInvalidRequest)
	}
	prepared, scope, err := PrepareProjectAnalysisRequest(ctx, request, route, contract, coverage, catalog)
	if err != nil {
		return core.ContextDelta{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, err
	}
	currentPlan, err := core.BuildPlan(prepared)
	if err != nil {
		return core.ContextDelta{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, err
	}
	if projectStaticAdmission(currentPlan, true) == projectStaticAdmission(base.RetrievalPlan, false) && projectSourceSetDigest(scope.Sources) != projectSourceSetDigest(base.Sources) {
		return core.ContextDelta{}, core.RetrievalTrace{}, ProjectAnalysisScope{}, core.ErrStaleBase
	}
	// The core replays the base with current trusted roots before any companion
	// uses serialized selectors/history. Failed attempts never enter the plan.
	delta, trace, err := NewProjectAnalysisEngine().Expand(ctx, base, prepared, expand)
	if err != nil {
		return delta, trace, ProjectAnalysisScope{}, err
	}
	companion, err := bindProjectCompanion(ctx, prepared, scope, delta.Pack, route, contract, coverage, catalog)
	return delta, trace, companion, err
}

func bindProjectCompanion(ctx context.Context, initial core.RetrievalRequest, initialScope AnalysisScope, pack core.ContextPack, route domain.RoutingResult, contract *domain.ContractPlan, coverage []core.CoverageResult, catalog agentcontrol.Catalog) (ProjectAnalysisScope, error) {
	current := initial
	current.RequiredFacets = nil
	current.OptionalFacets = nil
	// Source-equivalence materialization prerequisites are not source anchors.
	// Every retained exact selector, including diagnosed misses, stays present.
	for _, f := range append(append([]core.Facet{}, pack.RetrievalPlan.RequiredFacets...), pack.RetrievalPlan.OptionalFacets...) {
		if f.Path != "" && f.ExpectedHash != "" {
			current.RequiredFacets = append(current.RequiredFacets, f)
		}
	}
	callerSelectors := projectRetainedCallerSelectors(initial, pack)
	prepared, scope, err := prepareAnalysisRequest(ctx, current, route, contract, coverage, catalog, true, &callerSelectors)
	if err != nil {
		return ProjectAnalysisScope{}, err
	}
	plan, err := core.BuildPlan(current)
	if err != nil {
		return ProjectAnalysisScope{}, err
	}
	snap, err := NewEngine().Loader.Load(ctx, plan.Sources, plan.Limits)
	if err != nil {
		return ProjectAnalysisScope{}, err
	}
	for _, d := range snap.Diagnostics {
		if d.Status == core.Stale {
			return ProjectAnalysisScope{}, core.ErrStaleBase
		}
	}
	_, diagnostics := projectReadLayers(snap, nil, callerSelectors, nil)
	for _, d := range prepared.Route.Diagnostics {
		if d.Code == "ANALYSIS_HTTP_CLIENT_TYPE_AMBIGUOUS" || d.Code == "ANALYSIS_HTTP_CLIENT_CONTEXT_LIMIT" {
			diagnostics = append(diagnostics, d)
		}
	}
	result := ProjectAnalysisScope{AnalysisScope: scope, ContextPackDigest: pack.ContentDigest, InitialSelectorsDigest: initialScope.SelectorsDigest, CumulativeSelectorsDigest: hashJSON(current.RequiredFacets), ExpansionHistoryDigest: hashJSON(pack.RetrievalPlan.ExpansionHistory), MetadataDiagnostics: diagnostics}
	gapRequest := current
	gapRequest.RequiredFacets = callerSelectors
	result.ReadAreaGaps, diagnostics = projectReadAreaGaps(snap, gapRequest, route, catalog)
	result.MetadataDiagnostics = append(result.MetadataDiagnostics, diagnostics...)
	result.AttributionStatus = core.Complete
	if len(result.ReadAreaGaps) > 0 {
		result.AttributionStatus = core.Partial
	}
	for _, f := range pack.RouteRef.SeedFacets {
		if f.Resolver == "analysis-project-boundary" {
			var p ProjectBoundaryProof
			if strictProjectProof(f.Text, &p) == nil {
				result.BoundaryProofs = append(result.BoundaryProofs, p)
			}
		}
	}
	return boundProjectBudget(pack, result)
}

func projectAnalysisDeclarations(snapshot core.SnapshotResult) []analysisDeclaration {
	out := analysisDeclarations(snapshot)
	for i := range out {
		if !strings.Contains(out[i].symbol, ".") {
			out[i].symbol = out[i].file.Name.Name + "." + out[i].symbol
		}
	}
	for _, d := range snapshot.Documents {
		if !strings.HasSuffix(d.RelativePath, ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), d.RelativePath, d.Content, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, n := range value.Names {
						out = append(out, analysisDeclaration{d, value, file, file.Name.Name + "." + n.Name})
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].doc.Source.Identity+out[i].doc.RelativePath+out[i].symbol < out[j].doc.Source.Identity+out[j].doc.RelativePath+out[j].symbol
	})
	return out
}

func projectDocumentAnchor(snapshot core.SnapshotResult, f core.Facet) (analysisDeclaration, bool) {
	if strings.HasSuffix(f.Path, ".go") || f.Symbol != "" || f.ExpectedHash == "" {
		return analysisDeclaration{}, false
	}
	switch f.QueryKind {
	case core.QueryExact, core.QueryContract, core.QueryMetadata, core.QueryDocs, core.QuerySchema:
	default:
		return analysisDeclaration{}, false
	}
	for _, d := range snapshot.Documents {
		if d.Source.Identity == f.SourceIdentity && d.RelativePath == f.Path && core.EqualContentHash(f.ExpectedHash, d.ContentHash) {
			return analysisDeclaration{doc: d}, true
		}
	}
	return analysisDeclaration{}, false
}

func projectEvidence(d analysisDeclaration) core.EvidenceCandidate {
	e := analysisWitnessEvidence(d)
	e.ResolverVersion = ProjectAnalysisVersion
	if d.node == nil {
		e.Authority.Role = "document_witness"
		e.Authority.Basis = "exact admitted hash-bound document bytes; no AST or instruction authority"
		e.Limitations = []string{"Document bytes are evidence only; no AST relationship or execution certification."}
	}
	return e
}

func projectInvalidMetadata(d core.Document, message string) core.RetrievalDiagnostic {
	return core.RetrievalDiagnostic{Code: "ANALYSIS_METADATA_NOT_VERIFIED", Status: core.Partial, SourceIdentity: d.Source.Identity, RelativePath: d.RelativePath, Message: message}
}

// Origin is proven only after trusted core replay. Route-generated selectors
// remain in the cumulative companion, but cannot author an explicit caller pin.
func projectRetainedCallerSelectors(initial core.RetrievalRequest, pack core.ContextPack) []core.Facet {
	origins := append(append([]core.Facet{}, initial.RequiredFacets...), initial.OptionalFacets...)
	for _, step := range pack.RetrievalPlan.ExpansionHistory {
		origins = append(origins, step.Facet)
	}
	out := []core.Facet{}
	seen := map[string]bool{}
	for _, f := range append(append([]core.Facet{}, pack.RetrievalPlan.RequiredFacets...), pack.RetrievalPlan.OptionalFacets...) {
		for _, origin := range origins {
			if f.ID != "" && f.ID == origin.ID && f.SourceIdentity == origin.SourceIdentity && f.Path == origin.Path && f.Symbol == origin.Symbol && core.EqualContentHash(f.ExpectedHash, origin.ExpectedHash) && f.QueryKind == origin.QueryKind {
				if !seen[hashJSON(f)] {
					seen[hashJSON(f)] = true
					out = append(out, f)
				}
				break
			}
		}
	}
	return out
}
