package contextretrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

const projectLocalResultVariant = "application-local-result-concrete.v1"

// ProjectBoundaryProof describes read equivalence to current declarations. A
// local result is deliberately not labelled a domain owner or a frozen contract.
type ProjectBoundaryProof struct {
	Version            string         `json:"version"`
	Variant            string         `json:"variant"`
	FacetID            string         `json:"facet_id"`
	OriginalPath       string         `json:"original_path"`
	Kind               string         `json:"kind"`
	Owner              AnalysisAnchor `json:"owner"`
	Result             AnalysisAnchor `json:"result"`
	Consumer           AnalysisAnchor `json:"consumer"`
	Module             AnalysisAnchor `json:"module"`
	OwnerSelectorID    string         `json:"owner_selector_id"`
	ConsumerSelectorID string         `json:"consumer_selector_id"`
	Field              string         `json:"field"`
}

func projectProofJSON(p ProjectBoundaryProof) string { b, _ := json.Marshal(p); return string(b) }

// Reject duplicate keys as well as unknown fields: a permissive last-key-wins
// decoder is unsuitable for an independently replayed source proof.
func strictProjectProof(text string, out any) error {
	if len(text) == 0 || len(text) > 4096 {
		return fmt.Errorf("bounded proof required")
	}
	d := json.NewDecoder(strings.NewReader(text))
	nodes := 0
	var walk func(int) error
	walk = func(depth int) error {
		nodes++
		if depth > 8 || nodes > 256 {
			return fmt.Errorf("proof tree bound")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					if err != nil {
						return err
					}
					key, ok := k.(string)
					for _, r := range key {
						if r > 127 {
							return fmt.Errorf("non-ASCII proof key")
						}
					}
					key = strings.ToLower(key)
					if !ok || keys[key] {
						return fmt.Errorf("duplicate proof key")
					}
					keys[key] = true
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return fmt.Errorf("proof object")
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return fmt.Errorf("proof array")
				}
			default:
				return fmt.Errorf("proof delimiter")
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing proof input")
	}
	decode := json.NewDecoder(strings.NewReader(text))
	decode.DisallowUnknownFields()
	if err := decode.Decode(out); err != nil {
		return err
	}
	return nil
}

func projectOriginalSeed(plan core.RetrievalPlan, facet core.Facet) bool {
	for _, f := range plan.Route.SeedFacets {
		if hashJSON(f) == hashJSON(facet) {
			return true
		}
	}
	return false
}

func projectTargetAbsent(sources []core.SourceAdmission, snapshot core.SnapshotResult, f core.Facet) bool {
	admitted := false
	for _, s := range sources {
		if s.Identity == f.SourceIdentity && core.PathAdmitted(s, f.Path) {
			admitted = true
		}
	}
	if !admitted {
		return false
	}
	for _, d := range snapshot.Documents {
		if d.Source.Identity == f.SourceIdentity && d.RelativePath == f.Path {
			return false
		}
	}
	c := CoverageForFacet(snapshot, f, 0)
	return c.Complete && c.RequirementState == core.NotFound
}

func projectCallerSelector(d analysisDeclaration, facets []core.Facet) string {
	ids := []string{}
	for _, f := range facets {
		if f.ID != "" && f.Symbol != "" && f.SourceIdentity == d.doc.Source.Identity && f.Path == d.doc.RelativePath && core.EqualContentHash(f.ExpectedHash, d.doc.ContentHash) && symbolMatches(d.symbol, f.Symbol) {
			ids = append(ids, f.ID)
		}
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func projectReceiverName(f *ast.FuncDecl) string {
	if f.Recv == nil || len(f.Recv.List) != 1 || len(f.Recv.List[0].Names) > 1 || f.Type.TypeParams != nil {
		return ""
	}
	e := f.Recv.List[0].Type
	if p, ok := e.(*ast.StarExpr); ok {
		e = p.X
	}
	if n, ok := e.(*ast.Ident); ok {
		return n.Name
	}
	return ""
}

func projectLocalStruct(owner analysisDeclaration, name string, decls []analysisDeclaration) (analysisDeclaration, bool) {
	var result analysisDeclaration
	count := 0
	for _, d := range decls {
		if d.doc.Source.Identity != owner.doc.Source.Identity || path.Dir(d.doc.RelativePath) != path.Dir(owner.doc.RelativePath) || strings.HasSuffix(d.doc.RelativePath, "_test.go") || d.file.Name.Name != owner.file.Name.Name {
			continue
		}
		if s, ok := d.node.(*ast.TypeSpec); ok && s.Name.Name == name {
			count++
			result = d
			if s.Assign.IsValid() || s.TypeParams != nil {
				return analysisDeclaration{}, false
			}
			if _, ok := s.Type.(*ast.StructType); !ok {
				return analysisDeclaration{}, false
			}
		}
	}
	return result, count == 1
}

func projectNamespaceShadowed(owner analysisDeclaration, name string, decls []analysisDeclaration) bool {
	for _, d := range decls {
		if d.doc.Source.Identity != owner.doc.Source.Identity || path.Dir(d.doc.RelativePath) != path.Dir(owner.doc.RelativePath) || strings.HasSuffix(d.doc.RelativePath, "_test.go") {
			continue
		}
		switch n := d.node.(type) {
		case *ast.TypeSpec:
			if n.Name.Name == name {
				return true
			}
		case *ast.ValueSpec:
			for _, v := range n.Names {
				if v.Name == name {
					return true
				}
			}
		case *ast.FuncDecl:
			if n.Recv == nil && n.Name.Name == name {
				return true
			}
		}
	}
	return false
}

func projectModuleProof(snapshot core.SnapshotResult, owner, consumer analysisDeclaration) (AnalysisAnchor, bool) {
	if !projectPackageVerified(snapshot, owner.doc.Source.Identity, path.Dir(owner.doc.RelativePath)) || !projectPackageVerified(snapshot, consumer.doc.Source.Identity, path.Dir(consumer.doc.RelativePath)) {
		return AnalysisAnchor{}, false
	}
	for _, d := range snapshot.Diagnostics {
		if d.SourceIdentity != owner.doc.Source.Identity || !projectAcquisitionOmission(d) || !(d.RelativePath == "go.mod" || strings.HasSuffix(d.RelativePath, "/go.mod")) {
			continue
		}
		root := path.Dir(d.RelativePath)
		if root == "." || strings.HasPrefix(owner.doc.RelativePath, root+"/") || strings.HasPrefix(consumer.doc.RelativePath, root+"/") {
			return AnalysisAnchor{}, false
		}
	}
	var doc core.Document
	count := 0
	for _, d := range snapshot.Documents {
		if d.Source.Identity != owner.doc.Source.Identity {
			continue
		}
		if d.RelativePath == "go.mod" {
			count++
			doc = d
			continue
		}
		if strings.HasSuffix(d.RelativePath, "/go.mod") {
			root := path.Dir(d.RelativePath) + "/"
			if strings.HasPrefix(owner.doc.RelativePath, root) || strings.HasPrefix(consumer.doc.RelativePath, root) {
				return AnalysisAnchor{}, false
			}
		}
	}
	module := projectStrictModule(doc.Content)
	if count != 1 || module == "" {
		return AnalysisAnchor{}, false
	}
	return AnalysisAnchor{Source: doc.Source.Identity, Path: "go.mod", Hash: doc.ContentHash, Symbol: module}, true
}

func projectLocalResult(owner analysisDeclaration, decls []analysisDeclaration) (analysisDeclaration, bool) {
	f, ok := owner.node.(*ast.FuncDecl)
	if !ok || !analysisDeclarationUnique(owner, decls) || projectReceiverName(f) == "" || f.Type.Results == nil || len(f.Type.Results.List) != 2 || analysisSignature(f.Type, analysisImports(owner)) == "" {
		return analysisDeclaration{}, false
	}
	if _, ok := projectLocalStruct(owner, projectReceiverName(f), decls); !ok {
		return analysisDeclaration{}, false
	}
	for _, r := range f.Type.Results.List {
		if len(r.Names) > 1 {
			return analysisDeclaration{}, false
		}
	}
	result, ok := f.Type.Results.List[0].Type.(*ast.Ident)
	if !ok {
		return analysisDeclaration{}, false
	}
	errorType, ok := f.Type.Results.List[1].Type.(*ast.Ident)
	if !ok || errorType.Name != "error" || errorType.Obj != nil || projectNamespaceShadowed(owner, "error", decls) || analysisImports(owner)["error"] != "" {
		return analysisDeclaration{}, false
	}
	return projectLocalStruct(owner, result.Name, decls)
}

func projectCallArity(f *ast.FuncDecl, call *ast.CallExpr) bool {
	if call.Ellipsis.IsValid() {
		return false
	}
	count := 0
	if f.Type.Params != nil {
		for _, p := range f.Type.Params.List {
			if _, ok := p.Type.(*ast.Ellipsis); ok {
				return false
			}
			n := len(p.Names)
			if n == 0 {
				n = 1
			}
			count += n
		}
	}
	return count == len(call.Args)
}

func projectConcreteField(owner, consumer analysisDeclaration, decls []analysisDeclaration, module AnalysisAnchor) (string, bool) {
	f, ok := owner.node.(*ast.FuncDecl)
	if !ok {
		return "", false
	}
	c, ok := consumer.node.(*ast.FuncDecl)
	if !ok || !analysisDeclarationUnique(consumer, decls) || c.Body == nil || projectReceiverName(c) == "" || len(c.Recv.List[0].Names) != 1 {
		return "", false
	}
	receiver := c.Recv.List[0].Names[0]
	if receiver.Obj == nil {
		return "", false
	}
	decl, ok := projectLocalStruct(consumer, projectReceiverName(c), decls)
	if !ok {
		return "", false
	}
	structure := decl.node.(*ast.TypeSpec).Type.(*ast.StructType)
	names := map[string]bool{}
	matching := map[string]bool{}
	imports := analysisImports(decl)
	for _, field := range structure.Fields.List {
		if len(field.Names) == 0 {
			return "", false
		}
		for _, n := range field.Names {
			if names[n.Name] {
				return "", false
			}
			names[n.Name] = true
		}
		typ := field.Type
		if p, ok := typ.(*ast.StarExpr); ok {
			typ = p.X
		}
		qualified, ok := typ.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		alias, ok := qualified.X.(*ast.Ident)
		if !ok || alias.Obj != nil || projectNamespaceShadowed(decl, alias.Name, decls) || qualified.Sel.Name != projectReceiverName(f) || imports[alias.Name] != module.Symbol+"/"+path.Dir(owner.doc.RelativePath) {
			continue
		}
		for _, n := range field.Names {
			matching[n.Name] = true
		}
	}
	called := map[string]bool{}
	ast.Inspect(c.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !projectCallArity(f, call) {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != f.Name.Name {
			return true
		}
		field, ok := method.X.(*ast.SelectorExpr)
		if !ok || !matching[field.Sel.Name] {
			return true
		}
		object, ok := field.X.(*ast.Ident)
		if ok && object.Name == receiver.Name && object.Obj == receiver.Obj {
			called[field.Sel.Name] = true
		}
		return true
	})
	if len(called) != 1 {
		return "", false
	}
	for field := range called {
		return field, true
	}
	return "", false
}

func projectBoundaryProofs(seeds []core.Facet, anchors, decls []analysisDeclaration, snapshot core.SnapshotResult, sources []core.SourceAdmission, selectors []core.Facet) []ProjectBoundaryProof {
	result := []ProjectBoundaryProof{}
	for _, seed := range seeds {
		if seed.ClaimKey != "application-command-result" || !projectTargetAbsent(sources, snapshot, seed) {
			continue
		}
		unique := map[string]ProjectBoundaryProof{}
		for _, owner := range anchors {
			if owner.node == nil || owner.doc.Source.Identity != seed.SourceIdentity || strings.HasSuffix(owner.doc.RelativePath, "_test.go") || !analysisPathMatches(owner.doc.RelativePath, []string{"internal/usecase/**", "internal/application/**", "usecase/**", "application/**"}) {
				continue
			}
			ownerID := projectCallerSelector(owner, selectors)
			if ownerID == "" {
				continue
			}
			typ, ok := projectLocalResult(owner, decls)
			if !ok {
				continue
			}
			for _, consumer := range anchors {
				if consumer.node == nil || consumer.doc.Source.Identity != seed.SourceIdentity || strings.HasSuffix(consumer.doc.RelativePath, "_test.go") || !analysisPathMatches(consumer.doc.RelativePath, []string{"internal/transport/**", "internal/adapters/http/**", "internal/adapters/nats/**", "transport/**"}) {
					continue
				}
				consumerID := projectCallerSelector(consumer, selectors)
				if consumerID == "" {
					continue
				}
				module, ok := projectModuleProof(snapshot, owner, consumer)
				if !ok {
					continue
				}
				field, ok := projectConcreteField(owner, consumer, decls, module)
				if !ok {
					continue
				}
				proof := ProjectBoundaryProof{Version: ProjectAnalysisVersion, Variant: projectLocalResultVariant, FacetID: seed.ID, OriginalPath: seed.Path, Kind: seed.ClaimKey, Owner: analysisAnchor(owner), Result: analysisAnchor(typ), Consumer: analysisAnchor(consumer), Module: module, OwnerSelectorID: ownerID, ConsumerSelectorID: consumerID, Field: field}
				unique[projectProofJSON(proof)] = proof
			}
		}
		if len(unique) == 1 {
			for _, p := range unique {
				result = append(result, p)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return projectProofJSON(result[i]) < projectProofJSON(result[j]) })
	return result
}

type ProjectInterfaceBoundaryResolver struct{}

func (ProjectInterfaceBoundaryResolver) ID() string      { return "analysis-boundary" }
func (ProjectInterfaceBoundaryResolver) Version() string { return ProjectAnalysisVersion }
func (ProjectInterfaceBoundaryResolver) Resolve(ctx context.Context, p core.RetrievalPlan, f core.Facet, s core.SnapshotResult) (core.RetrievalResult, error) {
	return resolveAnalysisBoundary(ctx, p, f, s, true)
}

type ProjectBoundaryResolver struct{}

func (ProjectBoundaryResolver) ID() string      { return "analysis-project-boundary" }
func (ProjectBoundaryResolver) Version() string { return ProjectAnalysisVersion }
func (r ProjectBoundaryResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if !strings.HasPrefix(plan.Route.Digest, ProjectAnalysisVersion+":") || facet.QueryKind != core.QueryContract || facet.ClaimKey != "application-command-result" {
		return unsupportedResult(facet, r.ID()), nil
	}
	var b ProjectBoundaryProof
	if !projectOriginalSeed(plan, facet) || !projectTargetAbsent(plan.Sources, snapshot, facet) || strictProjectProof(facet.Text, &b) != nil || b.Version != ProjectAnalysisVersion || b.Variant != projectLocalResultVariant || b.FacetID != facet.ID || b.OriginalPath != facet.Path || b.Kind != facet.ClaimKey {
		return unavailableFacet(facet, core.NotVerified, "ANALYSIS_PROJECT_BOUNDARY_PROOF_INVALID", "Strict original seed and verified target absence required; exclusion and incomplete search cannot be substituted."), nil
	}
	decls := projectAnalysisDeclarations(snapshot)
	anchors := []analysisDeclaration{}
	selectors := append(append([]core.Facet{}, plan.RequiredFacets...), plan.OptionalFacets...)
	for _, a := range []AnalysisAnchor{b.Owner, b.Consumer} {
		matches := []analysisDeclaration{}
		for _, d := range decls {
			if analysisAnchor(d) == a && a.Source == facet.SourceIdentity {
				matches = append(matches, d)
			}
		}
		if len(matches) != 1 {
			return unavailableFacet(facet, core.NotVerified, "ANALYSIS_PROJECT_BOUNDARY_PROOF_STALE", "Current admitted original declarations required."), nil
		}
		anchors = append(anchors, matches[0])
	}
	proofs := projectBoundaryProofs([]core.Facet{facet}, anchors, decls, snapshot, plan.Sources, selectors)
	if len(proofs) != 1 || projectProofJSON(proofs[0]) != projectProofJSON(b) {
		return unavailableFacet(facet, core.NotVerified, "ANALYSIS_PROJECT_BOUNDARY_NOT_CORROBORATED", "Exact original selectors, local struct result and concrete receiver-field-call syntax not uniquely verified."), nil
	}
	result := core.RetrievalResult{}
	for _, actual := range []AnalysisAnchor{b.Owner, b.Result} {
		selected := facet
		selected.Path = actual.Path
		selected.Symbol = actual.Symbol
		selected.ExpectedHash = actual.Hash
		selected.Text = ""
		selected.Resolver = "contract"
		part, err := (ContractResolver{}).Resolve(ctx, plan, selected, snapshot)
		if err != nil {
			return result, err
		}
		for i := range part.Candidates {
			c := &part.Candidates[i]
			c.FacetIDs = []string{facet.ID}
			c.Authority.Role = "source_equivalent_application_result"
			c.Authority.Basis = "current named application result and concrete field-call syntax; original proposed file absent; no freeze or execution certification"
			for _, a := range []AnalysisAnchor{b.Result, b.Consumer, b.Module} {
				symbol := a.Symbol
				if a.Path != "go.mod" {
					symbol = shortAnalysisSymbol(symbol)
				}
				c.Links = append(c.Links, core.EvidenceLink{Kind: "declared_source", SourceIdentity: a.Source, RelativePath: a.Path, Symbol: symbol, ExpectedHash: a.Hash})
			}
			c.Limitations = append(c.Limitations, "Source-equivalent declaration syntax only; original contract owner/path/freeze retained. Compiler/type compatibility, runtime wiring and execution remain UNSUPPORTED.")
		}
		result.Candidates = append(result.Candidates, part.Candidates...)
		result.Diagnostics = append(result.Diagnostics, part.Diagnostics...)
	}
	result.Coverage = []core.CoverageResult{CoverageForFacet(snapshot, facet, len(result.Candidates))}
	return result, nil
}

// The opt-in concrete proof needs one unambiguous module directive. This
// bounded literal parser intentionally rejects quoted/compound forms instead
// of interpreting them or changing the legacy analysisModule contract.
func projectStrictModule(content string) string {
	if len(content) > 1<<20 {
		return ""
	}
	module := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if module != "" || len(fields) != 2 || strings.ContainsAny(fields[1], "\"'`(){}[]\\") {
			return ""
		}
		module = fields[1]
	}
	return module
}

// Concrete uniqueness is limited to the safely admitted package snapshot.
// Acquisition uncertainty and malformed current siblings cannot prove absence
// of conflicting declarations. This gate never reads excluded bytes.
func projectPackageVerified(snapshot core.SnapshotResult, source, directory string) bool {
	if !CoverageForFacet(snapshot, core.Facet{SourceIdentity: source}, 0).Complete {
		return false
	}
	for _, d := range snapshot.Documents {
		if d.Source.Identity == source && path.Dir(d.RelativePath) == directory && strings.HasSuffix(d.RelativePath, ".go") && !strings.HasSuffix(d.RelativePath, "_test.go") {
			if _, err := parser.ParseFile(token.NewFileSet(), d.RelativePath, d.Content, 0); err != nil {
				return false
			}
		}
	}
	for _, d := range snapshot.Diagnostics {
		if d.SourceIdentity != source || d.RelativePath == "" {
			continue
		}
		p := d.RelativePath
		if p == directory || strings.HasPrefix(directory, p+"/") || path.Dir(p) == directory && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			switch d.Code {
			case "EXCLUDED_BY_POLICY", "SECRET_CONTENT_EXCLUDED", "FILE_TYPE_EXCLUDED", "UNREADABLE_SOURCE", "SOURCE_TOO_LARGE", "BYTE_LIMIT", "FILE_LIMIT", "DEPTH_LIMIT", "GLOBAL_BYTE_LIMIT":
				return false
			}
		}
	}
	return true
}
