package contextretrieval

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"gopkg.in/yaml.v3"
)

const AnalysisVersion = "source-bound-analysis.v1.1"

// AnalysisScope is a read acquisition companion, never an implementation plan.
// Method-name relationships are disclosed syntax candidates, not resolved calls.
type AnalysisScope struct {
	Version           string                     `json:"version"`
	Intent            string                     `json:"intent"`
	LegacyRouteDigest string                     `json:"legacy_route_digest"`
	ContractDigest    string                     `json:"contract_digest"`
	CatalogDigest     string                     `json:"catalog_digest"`
	SelectorsDigest   string                     `json:"selectors_digest"`
	Sources           []core.EvidenceSource      `json:"sources"`
	Layers            []AnalysisLayer            `json:"layers"`
	Bindings          []AnalysisBinding          `json:"bindings"`
	WritableOwners    []string                   `json:"writable_owners"`
	LegacyCoverage    []core.CoverageResult      `json:"legacy_coverage"`
	Limitations       []string                   `json:"limitations"`
	Digest            string                     `json:"digest"`
	LegacyDiagnostics []core.RetrievalDiagnostic `json:"legacy_diagnostics"`
	TaskDigest        string                     `json:"task_digest"`
}
type AnalysisAnchor struct{ Source, Path, Symbol, Hash string }
type AnalysisLayer struct {
	Source   string                 `json:"source"`
	Layer    string                 `json:"layer"`
	Witness  AnalysisAnchor         `json:"witness"`
	Relation string                 `json:"relation"`
	Evidence core.EvidenceCandidate `json:"evidence"`
	Chain    []AnalysisAnchor       `json:"chain"`
}
type analysisDeclaration struct {
	doc    core.Document
	node   ast.Node
	file   *ast.File
	symbol string
}

var analysisStart = regexp.MustCompile(`(?i)^\s*(?:(?:in\s+[^,]+,\s*)?)(inspect|trace|investigate|audit|review|locate|verify|explain|select\s+[^.;]*\btests?|select\s+(?:focused\s+|regression\s+|durable\s+)?[^.;]*regressions)\b`)
var analysisMutation = regexp.MustCompile(`(?i)\b(add|change|modify|implement|delete|remove|rewrite|update|fix|create|replace|refactor|apply|patch|write|extend|deploy|execute|publish)\s+(?:the\s+|a\s+|an\s+)?[a-z][a-z0-9_-]*(?:\s+[a-z][a-z0-9_-]*)?`)
var analysisCondition = regexp.MustCompile(`(?i)\b(if|unless|when necessary|only if)\b`)

// AnalysisIntent fails closed. Descriptions, quoted source and subordinate
// clauses never authorize mutation. This API accepts only unambiguous reads.
func AnalysisIntent(task string) string {
	read := analysisStart.MatchString(task)
	// Unfamiliar independent instructions are ambiguous. A finite vocabulary
	// cannot safely infer that every other imperative is a read request.
	independent := regexp.MustCompile(`(?i)[;.!?]\s+|[\n&]+|\s+(?:and then|then|but)\s+`).Split(task, -1)
	unknown := false
	for _, clause := range independent {
		lower := strings.ToLower(strings.TrimSpace(clause))
		if lower == "" {
			continue
		}
		safe := analysisStart.MatchString(clause)
		for _, prefix := range []string{"preserve ", "find ", "check ", "identify ", "distinguish ", "determine ", "make no ", "do not ", "no ", "without ", "including "} {
			safe = safe || strings.HasPrefix(lower, prefix)
		}
		// Third-person descriptions are source facts, not imperative authorization.
		safe = safe || regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_/-]*(?:\s+[A-Za-z0-9_/-]+){0,5}\s+(?:must|cannot|never|owns|retains|supplies|remains|does|is|are|keeps|requires)\b`).MatchString(strings.TrimSpace(clause))
		safe = safe || regexp.MustCompile(`(?i)^(?:current|existing|resolved|active|exact|stable|bounded|strict|original)\s+`).MatchString(lower)
		safe = safe || regexp.MustCompile(`^[A-Z][A-Za-z]+\s+[A-Z][A-Z0-9]+\s+[A-Za-z]+ing\b`).MatchString(strings.TrimSpace(clause))
		unknown = unknown || !safe
	}
	for _, statement := range independent {
		negativePosition := regexp.MustCompile(`(?i)\b(?:must not|do not|cannot|never)\b`).FindStringIndex(statement)
		cursor := 0
		for _, clause := range regexp.MustCompile(`(?i),\s*|\s+(?:and|or)\s+`).Split(statement, -1) {
			offset := strings.Index(statement[cursor:], clause) + cursor
			cursor = offset + len(clause)
			lower := strings.ToLower(strings.TrimSpace(clause))
			// A read verb governs source descriptions, not arbitrary later
			// instructions. Unknown affirmative conjunctions fail closed.
			if offset > 0 && !analysisNominalContinuation(strings.TrimSpace(clause)) && !analysisReadPrefix(lower) && !regexp.MustCompile(`(?i)^(?:do not|must not|never|cannot|no|without|make no)\b`).MatchString(lower) {
				describedProcedure := analysisStart.MatchString(statement) && strings.Contains(statement[:offset], ":") && regexp.MustCompile(`(?i)^(?:bind|ensure|maps|emits|applies)\b`).MatchString(lower)
				comparative := strings.Contains(strings.ToLower(statement[:offset]), "rather than")
				governingNegative := regexp.MustCompile(`(?i)\b(?:must not|do not|cannot|never)\b`).MatchString(statement[:offset]) && !analysisReadPrefix(statement)
				if !describedProcedure && !comparative && !governingNegative {
					unknown = true
				}
			}
			matches := analysisMutation.FindAllStringIndex(lower, -1)
			for _, match := range matches {
				prefix := strings.TrimSpace(lower[:match[0]])
				// Negative/current-source explanations do not ask for a mutation. Anything
				// with an affirmative action that is not confidently explanatory fails
				// closed, including polite prefixes and unfamiliar instruction phrasing.
				goalMutation := strings.Contains(prefix, " to") && !strings.HasPrefix(lower, "explain ")
				negative := negativePosition != nil && negativePosition[0] < offset+match[0] && !analysisReadPrefix(statement)
				// A comma beginning another affirmative action is a new instruction;
				// descriptive negation in the preceding clause cannot govern it.
				if strings.HasSuffix(strings.TrimSpace(statement[:offset]), ",") && negativePosition != nil && negativePosition[0] < offset {
					negative = false
				}
				if regexp.MustCompile(`(?i)^(?:please|now|also|we|you)\b`).MatchString(lower) {
					negative = false
				}
				if negative || !goalMutation && (strings.HasPrefix(prefix, "do not") || strings.HasSuffix(prefix, "must not") || strings.HasSuffix(prefix, "cannot") || strings.HasSuffix(prefix, "never") || analysisStart.MatchString(lower) || strings.HasPrefix(prefix, "find tests") || strings.HasPrefix(prefix, "check ") || strings.HasPrefix(prefix, "preserve ") || strings.HasPrefix(prefix, "identify ") || strings.HasPrefix(prefix, "distinguish ")) {
					continue
				}
				if analysisCondition.MatchString(task) {
					return "CONDITIONAL_MUTATION"
				}
				return "EXPLICIT_MUTATION"
			}
		}
	}
	if !read || unknown {
		return "AMBIGUOUS_INTENT"
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(task)), "explain ") {
		return "EXPLANATION"
	}
	return "READ_ONLY_ANALYSIS"
}

// NewAnalysisEngine opts into source-equivalence syntax evidence. Default
// NewEngine's resolver set and all historical digests remain unchanged.
func NewAnalysisEngine() *core.Engine {
	e := NewEngine()
	e.Resolvers["analysis-boundary"] = AnalysisBoundaryResolver{}
	return e
}

// PrepareAnalysisRequest acquires bounded read scope through the same production
// loader used by Prepare/Expand. Expected labels and case IDs are not inputs.
func PrepareAnalysisRequest(ctx context.Context, request core.RetrievalRequest, route domain.RoutingResult, contract *domain.ContractPlan, legacyCoverage []core.CoverageResult, catalog agentcontrol.Catalog) (core.RetrievalRequest, AnalysisScope, error) {
	scope := AnalysisScope{Version: AnalysisVersion, Intent: AnalysisIntent(request.Task), LegacyRouteDigest: hashJSON(route), ContractDigest: hashJSON(contract), CatalogDigest: catalog.Digest, WritableOwners: []string{}, LegacyCoverage: legacyCoverage, Limitations: []string{"read evidence only; every source forbidden for writes", "AST syntax candidates; cross-package type-aware semantic resolution UNSUPPORTED", "finding tests is not execution certification", "ContractPlan ownership/freeze/approval are unchanged"}}
	if scope.Intent != "READ_ONLY_ANALYSIS" && scope.Intent != "EXPLANATION" {
		return request, scope, fmt.Errorf("analysis requires unambiguous read intent: %s", scope.Intent)
	}
	if err := agentcontrol.ValidateCatalog(catalog); err != nil || catalog.Digest != route.CatalogDigest {
		return request, scope, fmt.Errorf("analysis catalog binding invalid")
	}
	admitted := map[string]core.SourceAdmission{}
	for _, s := range request.Sources {
		id := s.RouteIdentity
		if id == "" {
			id = s.Identity
		}
		admitted[id] = s
	}
	facets := append(append([]core.Facet{}, request.RequiredFacets...), request.OptionalFacets...)
	adapted, err := AdaptTaskRoute(route, admitted, contract, legacyCoverage, facets)
	if err != nil {
		return request, scope, err
	}
	scope.LegacyDiagnostics = adapted.Diagnostics
	scope.TaskDigest = hashJSON(request.Task)
	request.Route = adapted
	plan, err := core.BuildPlan(request)
	if err != nil {
		return request, scope, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(plan.Limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	snapshot, err := NewEngine().Loader.Load(ctx, plan.Sources, plan.Limits)
	if err != nil {
		return request, scope, err
	}
	for _, d := range snapshot.Diagnostics {
		if d.Status == core.Stale {
			return request, scope, core.ErrStaleBase
		}
	}
	scope.Sources = snapshot.Sources
	scope.SelectorsDigest = hashJSON(facets)
	request.Sources = append([]core.SourceAdmission(nil), request.Sources...)
	for i := range request.Sources {
		for _, s := range snapshot.Sources {
			if request.Sources[i].Identity == s.Identity {
				request.Sources[i].ExpectedSnapshot = s.Snapshot
			}
		}
	}
	declarations := analysisDeclarations(snapshot)
	namesByNode := map[ast.Node]map[string]bool{}
	for _, d := range declarations {
		namesByNode[d.node] = analysisNames(d.node)
	}
	anchors := []analysisDeclaration{}
	diagnostics := append([]core.RetrievalDiagnostic{}, snapshot.Diagnostics...)
	for _, f := range facets {
		if f.Path == "" || f.ExpectedHash == "" {
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_ANCHOR_REQUIRED", Status: core.Partial, FacetID: f.ID, Message: "Analysis requires an exact path and hash; business nouns cannot establish source ownership."})
			continue
		}
		matches := []analysisDeclaration{}
		for _, d := range declarations {
			if d.doc.Source.Identity == f.SourceIdentity && d.doc.RelativePath == f.Path && core.EqualContentHash(f.ExpectedHash, d.doc.ContentHash) && (f.Symbol == "" || symbolMatches(d.symbol, f.Symbol)) {
				matches = append(matches, d)
			}
		}
		if len(matches) == 0 {
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_ANCHOR_NOT_VERIFIED", Status: core.Partial, FacetID: f.ID, RelativePath: f.Path, Message: "No exact admitted AST declaration matches this analysis anchor."})
		}
		anchors = append(anchors, matches...)
	}
	profileBySource := map[string]agentcontrol.Profile{}
	owners := []string{}
	for _, p := range route.Profiles {
		s, ok := admitted[p.ProjectID]
		if !ok {
			return request, scope, fmt.Errorf("analysis profile source is not admitted")
		}
		if p.Status == domain.ProfileResolutionResolved {
			profileBySource[s.Identity] = catalog.Profiles[p.ProfileID]
			if !s.Neighbor && !s.External {
				owners = append(owners, s.Identity)
			}
		} else {
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_PROFILE_UNRESOLVED", Status: core.Partial, SourceIdentity: s.Identity, Message: "Canonical source-layer attribution requires verified profile metadata."})
		}
	}
	// Two local syntax hops acquire dependencies and reverse callers. A name
	// match remains a candidate; it proves neither interface satisfaction nor
	// runtime reachability. No external module is traversed.
	acquired := map[string]string{}
	chains := map[string][]AnalysisAnchor{}
	queue := append([]analysisDeclaration{}, anchors...)
	key := func(d analysisDeclaration) string {
		return d.doc.Source.Identity + "\x00" + d.doc.RelativePath + "\x00" + d.symbol
	}
	for _, d := range queue {
		acquired[key(d)] = "exact caller anchor"
		chains[key(d)] = []AnalysisAnchor{analysisAnchor(d)}
	}
	// SAME_FILE_CONTEXT is a single bounded read of declaration context in
	// original caller-admitted files. It never replaces exact typed anchors.
	contexts := 0
	for _, a := range anchors {
		for _, d := range declarations {
			if d.doc.Source.Identity != a.doc.Source.Identity || d.doc.RelativePath != a.doc.RelativePath || strings.HasSuffix(d.doc.RelativePath, "_test.go") {
				continue
			}
			if _, exists := acquired[key(d)]; exists {
				continue
			}
			if contexts >= 1000 {
				diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_CONTEXT_LIMIT", Status: core.Partial, SourceIdentity: d.doc.Source.Identity, Message: "SAME_FILE_CONTEXT declarations omitted at limit1000."})
				break
			}
			acquired[key(d)] = "SAME_FILE_CONTEXT; adjacent source declaration, not a call from the original anchor"
			chains[key(d)] = []AnalysisAnchor{analysisAnchor(a), analysisAnchor(d)}
			queue = append(queue, d)
			contexts++
		}
	}
	for depth := 0; depth < 2; depth++ {
		next := []analysisDeclaration{}
		for _, a := range queue {
			if err := ctx.Err(); err != nil {
				return request, scope, err
			}
			names := namesByNode[a.node]
			for _, d := range declarations {
				if d.doc.Source.Identity != a.doc.Source.Identity || strings.HasSuffix(d.doc.RelativePath, "_test.go") {
					continue
				}
				if _, ok := acquired[key(d)]; ok {
					continue
				}
				name := d.symbol
				if i := strings.LastIndex(name, "."); i >= 0 {
					name = name[i+1:]
				}
				relation := ""
				if names[name] {
					relation = "local declaration name referenced by anchored AST; syntax candidate"
				} else if namesByNode[d.node][shortAnalysisSymbol(a.symbol)] {
					relation = "reverse local AST reference; syntax candidate"
				}
				if relation != "" {
					acquired[key(d)] = relation
					chains[key(d)] = append(append([]AnalysisAnchor{}, chains[key(a)]...), analysisAnchor(d))
					next = append(next, d)
				}
			}
		}
		queue = next
	}
	// Exact string source references and explicit layer inspection can acquire
	// non-Go schema evidence. SQL is not treated as a Go declaration.
	for _, d := range snapshot.Documents {
		if !strings.HasSuffix(d.RelativePath, ".sql") {
			continue
		}
		for _, a := range anchors {
			if a.doc.Source.Identity != d.Source.Identity {
				continue
			}
			referenced := false
			ast.Inspect(a.node, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					referenced = referenced || path.Clean(path.Join(path.Dir(a.doc.RelativePath), v)) == d.RelativePath || strings.HasSuffix(v, "/"+path.Base(d.RelativePath))
				}
				return true
			})
			if referenced || regexp.MustCompile(`(?i)\bmigration\b`).MatchString(request.Task) {
				declarations = append(declarations, analysisDeclaration{doc: d, symbol: "SQL"})
				acquired[d.Source.Identity+"\x00"+d.RelativePath+"\x00SQL"] = "exact schema reference or explicit migration inspection"
				break
			}
		}
	}
	witnesses := map[string]AnalysisLayer{}
	for _, d := range declarations {
		relation, ok := acquired[key(d)]
		if !ok {
			continue
		}
		p, ok := profileBySource[d.doc.Source.Identity]
		if !ok {
			continue
		}
		for _, r := range p.Routes {
			if !analysisPathMatches(d.doc.RelativePath, r.Targets) || !analysisOutbound(r.ID, d.doc, snapshot) {
				continue
			}
			k := d.doc.Source.Identity + "\x00" + r.ID
			w := AnalysisLayer{Evidence: analysisWitnessEvidence(d), Chain: chains[key(d)], Source: d.doc.Source.Identity, Layer: r.ID, Witness: analysisAnchor(d), Relation: relation}
			old, exists := witnesses[k]
			if !exists || w.Witness.Path+w.Witness.Symbol < old.Witness.Path+old.Witness.Symbol {
				witnesses[k] = w
			}
		}
	}
	for _, w := range witnesses {
		scope.Layers = append(scope.Layers, w)
	}
	sort.Slice(scope.Layers, func(i, j int) bool {
		return scope.Layers[i].Source+scope.Layers[i].Layer < scope.Layers[j].Source+scope.Layers[j].Layer
	})
	scope.Bindings = analysisBindings(adapted.SeedFacets, anchors, declarations, snapshot)
	seeds := append([]core.Facet(nil), adapted.SeedFacets...)
	for i := range seeds {
		for _, b := range scope.Bindings {
			if seeds[i].ID == b.FacetID {
				seeds[i].Resolver = "analysis-boundary"
				seeds[i].Text = analysisBindingJSON(b)
			}
		}
	}
	for _, d := range adapted.Diagnostics {
		if d.Code == "CONTRACT_REQUIREMENTS_UNRESOLVED" {
			// An unresolved writer route has not yet decided whether a future
			// change needs a contract. Preserve that prerequisite in the legacy
			// companion; it declares no read facet. All actual boundary seeds
			// and other unresolved contract states still fail normally.
			writerPrerequisite := contract != nil && contract.Required && contract.State == domain.ContractPlanPlanned && !contract.FreezeRequired && len(contract.Boundaries) == 0 && len(contract.ChangesRequired) == 0 && len(contract.ContractOwnerRoutes) == 0 && route.Status == domain.RoutingStatusUnresolved && route.OwnerReviewRequired
			if !writerPrerequisite {
				diagnostics = append(diagnostics, d)
			}
		}
		if d.Code == "CONTRACT_NOT_IMPLEMENTED" {
			bound := false
			for _, b := range scope.Bindings {
				bound = bound || b.FacetID == d.FacetID
			}
			if !bound {
				diagnostics = append(diagnostics, d)
			}
		}
	}
	scope.Digest = hashJSON(scope)
	read := core.RouteContext{Digest: AnalysisVersion + ":" + scope.Digest, CatalogDigest: catalog.Digest, Status: core.Complete, Owners: uniqueStrings(owners), SeedFacets: seeds, Diagnostics: diagnostics, Coverage: append([]core.CoverageResult{}, snapshot.Coverage...)}
	if len(anchors) == 0 || len(owners) == 0 {
		read.Status = core.Partial
		read.Diagnostics = append(read.Diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_SOURCE_SCOPE_UNRESOLVED", Status: core.Partial, Message: "No verified non-neighbor source custodian and anchored analysis scope."})
	}
	for _, l := range scope.Layers {
		read.Targets = append(read.Targets, core.RouteTarget{SourceIdentity: l.Source, Layer: l.Layer, Paths: []string{l.Witness.Path}, Symbols: []string{l.Witness.Symbol}})
	}
	request.Route = read
	return request, scope, nil
}

func analysisAnchor(d analysisDeclaration) AnalysisAnchor {
	return AnalysisAnchor{d.doc.Source.Identity, d.doc.RelativePath, d.symbol, d.doc.ContentHash}
}
func shortAnalysisSymbol(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
func analysisDeclarations(snapshot core.SnapshotResult) []analysisDeclaration {
	out := []analysisDeclaration{}
	for _, d := range snapshot.Documents {
		if !strings.HasSuffix(d.RelativePath, ".go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), d.RelativePath, d.Content, 0)
		if e != nil {
			continue
		}
		for _, decl := range f.Decls {
			switch n := decl.(type) {
			case *ast.FuncDecl:
				name := n.Name.Name
				if n.Recv != nil && len(n.Recv.List) == 1 {
					typ := n.Recv.List[0].Type
					if p, ok := typ.(*ast.StarExpr); ok {
						typ = p.X
					}
					if v, ok := typ.(*ast.Ident); ok {
						name = v.Name + "." + name
					}
				}
				out = append(out, analysisDeclaration{d, n, f, name})
			case *ast.GenDecl:
				for _, spec := range n.Specs {
					if s, ok := spec.(*ast.TypeSpec); ok {
						out = append(out, analysisDeclaration{d, s, f, s.Name.Name})
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
func analysisNames(node ast.Node) map[string]bool {
	names := map[string]bool{}
	if node == nil {
		return names
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			names[x.Sel.Name] = true
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok {
				names[id.Name] = true
			}
		case *ast.Ident:
			if x.Obj == nil {
				names[x.Name] = true
			}
		}
		return true
	})
	return names
}
func analysisPathMatches(p string, patterns []string) bool {
	for _, v := range patterns {
		if strings.HasSuffix(v, "/**") {
			root := strings.TrimSuffix(v, "/**")
			if strings.HasPrefix(p, root+"/") {
				return true
			}
		} else if ok, _ := path.Match(v, p); ok {
			return true
		}
	}
	return false
}
func analysisOutbound(route string, doc core.Document, snapshot core.SnapshotResult) bool {
	if route != "backend.infrastructure.client" || (!strings.HasPrefix(doc.RelativePath, "internal/infrastructure/http/") && !strings.HasPrefix(doc.RelativePath, "internal/infrastructure/adapters/")) {
		return true
	}
	directory := path.Dir(doc.RelativePath)
	declared, http := false, false
	for _, d := range snapshot.Documents {
		if d.Source.Identity != doc.Source.Identity {
			continue
		}
		if d.RelativePath == ".ai/service.yaml" || d.RelativePath == ".ai/architecture.yaml" {
			var n yaml.Node
			if yaml.Unmarshal([]byte(d.Content), &n) == nil {
				declared = declared || analysisOutboundDirectory(&n, directory, false)
			}
		}
		if path.Dir(d.RelativePath) == directory && strings.HasSuffix(d.RelativePath, ".go") && !strings.HasSuffix(d.RelativePath, "_test.go") {
			f, e := parser.ParseFile(token.NewFileSet(), d.RelativePath, d.Content, 0)
			if e != nil {
				continue
			}
			aliases := map[string]bool{}
			for _, imp := range f.Imports {
				v, _ := strconv.Unquote(imp.Path.Value)
				if v == "net/http" {
					a := "http"
					if imp.Name != nil {
						a = imp.Name.Name
					}
					aliases[a] = true
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := s.X.(*ast.Ident); ok && aliases[id.Name] && (s.Sel.Name == "NewRequest" || s.Sel.Name == "NewRequestWithContext" || s.Sel.Name == "Client" || s.Sel.Name == "DefaultClient") {
						http = true
					}
				}
				return true
			})
		}
	}
	return declared && http
}
func analysisOutboundDirectory(n *yaml.Node, dir string, outbound bool) bool {
	if n.Kind == yaml.AliasNode {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return outbound && (n.Value == dir || strings.HasPrefix(dir, strings.TrimSuffix(n.Value, "/")+"/"))
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := strings.ToLower(n.Content[i].Value)
			direction := outbound || strings.Contains(k, "outbound") || strings.Contains(k, "outgoing") || strings.Contains(k, "client") || k == "adapters"
			if strings.Contains(k, "inbound") {
				direction = false
			}
			if analysisOutboundDirectory(n.Content[i+1], dir, direction) {
				return true
			}
		}
		return false
	}
	for _, c := range n.Content {
		if analysisOutboundDirectory(c, dir, outbound) {
			return true
		}
	}
	return false
}

func analysisWitnessEvidence(d analysisDeclaration) core.EvidenceCandidate {
	span := core.EvidenceSpan{}
	if d.node != nil {
		start, end := int(d.node.Pos())-1, int(d.node.End())-1
		span.StartByte = start
		span.EndByte = end
		span.StartLine = strings.Count(d.doc.Content[:start], "\n") + 1
		span.EndLine = span.StartLine + strings.Count(d.doc.Content[start:end], "\n")
	}
	facet := core.Facet{SourceIdentity: d.doc.Source.Identity, Path: d.doc.RelativePath, Symbol: d.symbol, ExpectedHash: d.doc.ContentHash, ClaimType: core.ImplementationBehavior}
	c := CandidateFromDocument(d.doc, facet, "analysis-scope", AnalysisVersion, span, "read_layer_witness", documentProvenance(d.doc.RelativePath))
	c.Symbol = d.symbol
	c.Authority = core.Authority{ClaimType: core.ImplementationBehavior, Role: "syntax_candidate", Basis: "admitted source declaration and profile path; no business/write ownership"}
	c.Limitations = append(c.Limitations, "AST syntax candidate only; no resolved caller, interface satisfaction, type-aware semantics or test execution certificate.")
	return c
}

func analysisReadPrefix(statement string) bool {
	lower := strings.ToLower(strings.TrimSpace(statement))
	if analysisStart.MatchString(statement) {
		return true
	}
	for _, prefix := range []string{"preserve ", "find ", "check ", "identify ", "distinguish ", "determine ", "current ", "existing ", "resolved ", "active ", "exact ", "stable ", "bounded ", "strict ", "original "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// This small controlled-English grammar recognizes nominal engineering lists.
// It never assigns a repository, architecture route or writable owner. Other
// conjunct instructions remain ambiguous, even when preceded by a read verb.
func analysisNominalContinuation(clause string) bool {
	clause = strings.TrimSpace(clause)
	for _, conjunction := range []string{"and ", "or "} {
		clause = strings.TrimPrefix(clause, conjunction)
	}
	words := strings.Fields(clause)
	if len(words) == 0 {
		return true
	}
	rawFirst := strings.Trim(words[0], ".,:()")
	first := strings.ToLower(rawFirst)
	if len(words) > 1 {
		switch words[1] {
		case "the", "a", "an", "this", "that", "our", "your":
			if !strings.HasSuffix(first, "ing") {
				return false
			}
		}
	}

	switch first {
	case "saving", "reaching", "trailing", "milestone/final", "free-text", "ack-after-placement", "waiting/intent", "conversion", "generation-policy", "verification", "ready/staged", "server-owned", "validation", "digest-bound", "rejection", "ready-only", "utc", "github", "new-fact", "timestamps", "maps", "emits", "applies", "full", "removal", "replay", "cleanup", "path", "retry", "a", "an", "the", "one", "exactly", "who", "scope", "unknown", "issue", "worker", "receipt", "source", "input", "output", "snapshot", "student", "lease", "claim",
		"strict", "idempotent", "exact", "atomic", "aborted", "safe", "fixed", "latest", "stale", "foreign", "passed", "human", "permanent", "mismatched", "bounded", "returned", "scoped", "monotonic", "stable", "transactional", "generated", "resolved", "insignificant", "actual", "including", "transient", "coherent", "objective", "typed", "active", "trusted", "deterministic", "executable", "expected", "independent", "prerequisite", "private", "dedicated", "authoring", "legacy", "short":
		return true
	}
	// A paired Go-style identifier followed by an explicit relation noun is
	// a technical noun phrase, not a generic command or suffix inference.
	if len(words) > 1 && regexp.MustCompile(`^[A-Z][a-z]+[A-Z][A-Za-z0-9]+/[A-Z][a-z]+[A-Z][A-Za-z0-9]+$`).MatchString(rawFirst) && words[1] == "fencing" {
		return true
	}
	return false
}
