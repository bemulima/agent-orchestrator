package contextretrieval

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

func projectExactCaller(a analysisDeclaration, selectors []core.Facet, declarations []analysisDeclaration) bool {
	if a.file == nil || a.node == nil || strings.HasSuffix(a.doc.RelativePath, "_test.go") || !analysisDeclarationUnique(a, declarations) {
		return false
	}
	for _, f := range selectors {
		if f.Symbol == "" || f.SourceIdentity != a.doc.Source.Identity || f.Path != a.doc.RelativePath || !core.EqualContentHash(f.ExpectedHash, a.doc.ContentHash) || !symbolMatches(a.symbol, f.Symbol) {
			continue
		}
		count := 0
		for _, d := range declarations {
			if d.doc.Source.Identity == f.SourceIdentity && d.doc.RelativePath == f.Path && symbolMatches(d.symbol, f.Symbol) {
				count++
			}
		}
		if count == 1 {
			return true
		}
	}
	return false
}

// This is a concrete type-reference witness. Adjacent declarations are labelled
// SAME_FILE_CONTEXT; neither a method call nor interface satisfaction is inferred.
func projectInfrastructureHTTPTypes(snapshot core.SnapshotResult, anchors []analysisDeclaration, selectors []core.Facet, areas []projectLayerDeclaration, syntaxContexts map[string]string) ([]AnalysisLayer, []core.RetrievalDiagnostic) {
	decls := projectAnalysisDeclarations(snapshot)
	out := []AnalysisLayer{}
	contexts, diagnostics := projectHTTPContextEligibility(snapshot, anchors, selectors, decls, syntaxContexts)
	for _, origin := range anchors {
		if !projectExactCaller(origin, selectors, decls) || !projectHTTPPackage(snapshot, origin) {
			continue
		}
		candidates := []AnalysisLayer{}
		add := func(context, terminal analysisDeclaration, module AnalysisAnchor) {
			for _, area := range areas {
				if area.name != "infrastructure" || area.doc.Source != terminal.doc.Source || !(terminal.doc.RelativePath == area.root || strings.HasPrefix(terminal.doc.RelativePath, area.root+"/")) {
					continue
				}
				chain := []AnalysisAnchor{{Source: area.doc.Source.Identity, Path: area.doc.RelativePath, Hash: area.doc.ContentHash}, analysisAnchor(origin)}
				relation := "authored infrastructure and at most one direct concrete current type reference; no method/interface satisfaction or runtime reachability"
				if origin.symbol != context.symbol {
					chain = append(chain, analysisAnchor(context))
					relation += "; SAME_FILE_CONTEXT is adjacent declaration context, not a call from the caller"
				}
				if module.Path != "" {
					chain = append(chain, module)
				}
				if analysisAnchor(origin) != analysisAnchor(terminal) {
					chain = append(chain, analysisAnchor(terminal))
				}
				evidence := projectEvidence(terminal)
				evidence.Limitations = append(evidence.Limitations, "Concrete imported net/http.Client field/type syntax only; SAME_FILE_CONTEXT and direct type references do not certify method satisfaction, provider execution or runtime wiring.")
				candidates = append(candidates, AnalysisLayer{Source: terminal.doc.Source.Identity, Layer: "backend.infrastructure.client", Witness: analysisAnchor(terminal), Evidence: evidence, Relation: relation, Chain: chain})
			}
		}
		// An explicit ordinary terminal is already unambiguous; unrelated adjacent
		// types cannot replace or broaden that exact caller selector.
		if projectNamedHTTPClient(origin, decls) {
			if _, ok := projectModuleProof(snapshot, origin, origin); ok {
				add(origin, origin, AnalysisAnchor{})
			}
			out = append(out, candidates...)
			continue
		}
		for _, context := range decls {
			if context.doc.Source.Identity != origin.doc.Source.Identity || context.doc.RelativePath != origin.doc.RelativePath || context.doc.ContentHash != origin.doc.ContentHash || !analysisDeclarationUnique(context, decls) {
				continue
			}
			if f, ok := context.node.(*ast.FuncDecl); ok && f.Recv != nil {
				if _, ordinary := projectLocalStruct(context, projectReceiverName(f), decls); !ordinary {
					continue
				}
			}
			if context.symbol != origin.symbol && !contexts[projectHTTPContextKey(context)] {
				continue
			}
			for _, expr := range projectDirectTypeReferences(context) {
				terminal, module, ok := projectConcreteHTTPType(snapshot, context, expr, decls)
				if !ok || terminal.doc.Source.Snapshot != origin.doc.Source.Snapshot || terminal.doc.Source.AdmissionDigest != origin.doc.Source.AdmissionDigest {
					continue
				}
				add(context, terminal, module)
			}
		}
		identities := map[AnalysisAnchor]bool{}
		for _, c := range candidates {
			identities[c.Witness] = true
		}
		if len(identities) > 1 {
			ids := []string{}
			for _, f := range selectors {
				if f.SourceIdentity == origin.doc.Source.Identity && f.Path == origin.doc.RelativePath && symbolMatches(origin.symbol, f.Symbol) && core.EqualContentHash(f.ExpectedHash, origin.doc.ContentHash) {
					ids = append(ids, f.ID)
				}
			}
			sort.Strings(ids)
			facetID := ""
			if len(ids) > 0 {
				facetID = ids[0]
			}
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_HTTP_CLIENT_TYPE_AMBIGUOUS", Status: core.Partial, SourceIdentity: origin.doc.Source.Identity, RelativePath: origin.doc.RelativePath, FacetID: facetID, Message: "More than one distinct current concrete client terminal in bounded caller/declaration context; no terminal selected and no runtime relationship inferred."})
			continue
		}
		out = append(out, candidates...)
	}
	return out, diagnostics
}

func projectHTTPContextKey(d analysisDeclaration) string {
	return d.doc.Source.Identity + "\x00" + d.doc.RelativePath + "\x00" + d.symbol
}

// Eligibility is a bounded adjacency relation from a retained caller, separate
// from why the core happened to acquire the declaration. Whole-file route seeds
// can acquire it first, but cannot originate a proof or bypass this global cap.
func projectHTTPContextEligibility(snapshot core.SnapshotResult, anchors []analysisDeclaration, selectors []core.Facet, declarations []analysisDeclaration, acquired map[string]string) (map[string]bool, []core.RetrievalDiagnostic) {
	fileKey := func(d analysisDeclaration) string {
		return d.doc.Source.Identity + "\x00" + d.doc.RelativePath + "\x00" + d.doc.ContentHash
	}
	origins := map[string][]analysisDeclaration{}
	for _, origin := range anchors {
		if projectExactCaller(origin, selectors, declarations) && projectHTTPPackage(snapshot, origin) {
			origins[fileKey(origin)] = append(origins[fileKey(origin)], origin)
		}
	}
	eligible := map[string]analysisDeclaration{}
	for _, d := range declarations {
		key := projectHTTPContextKey(d)
		if _, ok := acquired[key]; !ok {
			continue
		}
		for _, origin := range origins[fileKey(d)] {
			if d.symbol != origin.symbol {
				eligible[key] = d
				break
			}
		}
	}
	keys := make([]string, 0, len(eligible))
	for key := range eligible {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	allowed := map[string]bool{}
	diagnostics := []core.RetrievalDiagnostic{}
	limitedFiles := map[string]bool{}
	for i, key := range keys {
		if i < 1000 {
			allowed[key] = true
			continue
		}
		d := eligible[key]
		if !limitedFiles[fileKey(d)] {
			limitedFiles[fileKey(d)] = true
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_HTTP_CLIENT_CONTEXT_LIMIT", Status: core.Partial, SourceIdentity: d.doc.Source.Identity, RelativePath: d.doc.RelativePath, Message: "Adjacent concrete-client declaration context omitted at global limit1000, including declarations already acquired by whole-file seeds; no terminal or runtime relationship inferred from omitted context."})
		}
	}
	return allowed, diagnostics
}

func projectDirectTypeReferences(d analysisDeclaration) []ast.Expr {
	expressions := []ast.Expr{}
	add := func(fields *ast.FieldList) bool {
		if fields == nil {
			return true
		}
		names := map[string]bool{}
		for _, f := range fields.List {
			if len(f.Names) == 0 {
				return false
			}
			for _, n := range f.Names {
				if n.Name == "_" || names[n.Name] {
					return false
				}
				names[n.Name] = true
			}
			expressions = append(expressions, f.Type)
		}
		return true
	}
	switch n := d.node.(type) {
	case *ast.TypeSpec:
		s, ok := n.Type.(*ast.StructType)
		if !ok || n.Assign.IsValid() || n.TypeParams != nil || !add(s.Fields) {
			return nil
		}
	case *ast.FuncDecl:
		if n.Type.TypeParams != nil || n.Recv != nil && projectReceiverName(n) == "" || !add(n.Type.Params) {
			return nil
		}
	default:
		return nil
	}
	return expressions
}

func projectConcreteHTTPType(snapshot core.SnapshotResult, context analysisDeclaration, expr ast.Expr, decls []analysisDeclaration) (analysisDeclaration, AnalysisAnchor, bool) {
	if p, ok := expr.(*ast.StarExpr); ok {
		expr = p.X
	}
	name, directory := "", path.Dir(context.doc.RelativePath)
	var module AnalysisAnchor
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Obj != nil && e.Obj.Kind != ast.Typ || analysisImports(context)[e.Name] != "" {
			return analysisDeclaration{}, module, false
		}
		name = e.Name
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		if !ok || id.Obj != nil || projectNamespaceShadowed(context, id.Name, decls) {
			return analysisDeclaration{}, module, false
		}
		for _, doc := range snapshot.Documents {
			if doc.Source.Identity == context.doc.Source.Identity && doc.RelativePath == "go.mod" {
				if module.Path != "" {
					return analysisDeclaration{}, AnalysisAnchor{}, false
				}
				module = AnalysisAnchor{Source: doc.Source.Identity, Path: doc.RelativePath, Hash: doc.ContentHash, Symbol: projectStrictModule(doc.Content)}
			}
		}
		importPath := analysisImports(context)[id.Name]
		if module.Symbol == "" || !strings.HasPrefix(importPath, module.Symbol+"/") {
			return analysisDeclaration{}, module, false
		}
		directory = strings.TrimPrefix(importPath, module.Symbol+"/")
		if !core.SafeRelativePath(directory) {
			return analysisDeclaration{}, module, false
		}
		name = e.Sel.Name
	default:
		return analysisDeclaration{}, module, false
	}
	var terminal analysisDeclaration
	count := 0
	for _, d := range decls {
		if d.doc.Source.Identity != context.doc.Source.Identity || path.Dir(d.doc.RelativePath) != directory || strings.HasSuffix(d.doc.RelativePath, "_test.go") {
			continue
		}
		if n, ok := d.node.(*ast.TypeSpec); ok && n.Name.Name == name {
			count++
			terminal = d
		}
		if n, ok := d.node.(*ast.FuncDecl); ok && n.Recv == nil && n.Name.Name == name {
			return analysisDeclaration{}, module, false
		}
		if n, ok := d.node.(*ast.ValueSpec); ok {
			for _, id := range n.Names {
				if id.Name == name {
					return analysisDeclaration{}, module, false
				}
			}
		}
	}
	if count != 1 || terminal.file == nil || !projectHTTPPackage(snapshot, terminal) {
		return analysisDeclaration{}, module, false
	}
	if e, ok := expr.(*ast.SelectorExpr); ok {
		id := e.X.(*ast.Ident)
		explicit := false
		for _, imp := range context.file.Imports {
			if imp.Name != nil && imp.Name.Name == id.Name {
				explicit = true
			}
		}
		if !explicit && terminal.file.Name.Name != id.Name {
			return analysisDeclaration{}, module, false
		}
	}
	if directory == path.Dir(context.doc.RelativePath) && terminal.file.Name.Name != context.file.Name.Name {
		return analysisDeclaration{}, module, false
	}
	// The same verified root module must cover both packages; nested modules and
	// missing module evidence fail closed for imported and same-package references.
	rootModule, ok := projectModuleProof(snapshot, context, terminal)
	if !ok || module.Path != "" && rootModule != module {
		return analysisDeclaration{}, module, false
	}
	if module.Path != "" {
		module = rootModule
	}
	if terminal.doc.Source.Snapshot != context.doc.Source.Snapshot || terminal.doc.Source.AdmissionDigest != context.doc.Source.AdmissionDigest {
		return analysisDeclaration{}, module, false
	}
	if projectNamedHTTPClient(terminal, decls) {
		return terminal, module, true
	}
	return analysisDeclaration{}, module, false
}

func projectNamedHTTPClient(terminal analysisDeclaration, decls []analysisDeclaration) bool {
	typ, ok := terminal.node.(*ast.TypeSpec)
	if !ok || typ.Assign.IsValid() || typ.TypeParams != nil || !analysisDeclarationUnique(terminal, decls) {
		return false
	}
	if _, ok := typ.Type.(*ast.StructType); !ok {
		return false
	}
	fields := projectDirectTypeReferences(terminal)
	for _, field := range fields {
		if p, ok := field.(*ast.StarExpr); ok {
			field = p.X
		}
		s, ok := field.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != "Client" {
			continue
		}
		id, ok := s.X.(*ast.Ident)
		if ok && id.Obj == nil && !projectNamespaceShadowed(terminal, id.Name, decls) && analysisImports(terminal)[id.Name] == "net/http" {
			return true
		}
	}
	return false
}

func projectHTTPConstructor(d analysisDeclaration, declarations []analysisDeclaration) bool {
	f, ok := d.node.(*ast.FuncDecl)
	if !ok || f.Type.TypeParams != nil || f.Recv != nil && projectReceiverName(f) == "" {
		return false
	}
	if f.Recv != nil {
		if _, ordinary := projectLocalStruct(d, projectReceiverName(f), declarations); !ordinary {
			return false
		}
	}
	imports := analysisImports(d)
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Obj != nil || projectNamespaceShadowed(d, id.Name, declarations) || imports[id.Name] != "net/http" {
			return true
		}
		if sel.Sel.Name == "NewRequest" && len(call.Args) == 3 || sel.Sel.Name == "NewRequestWithContext" && len(call.Args) == 4 {
			found = true
		}
		return true
	})
	return found
}

func projectHTTPPackage(snapshot core.SnapshotResult, d analysisDeclaration) bool {
	if d.file == nil || !projectPackageVerified(snapshot, d.doc.Source.Identity, path.Dir(d.doc.RelativePath)) {
		return false
	}
	for _, doc := range snapshot.Documents {
		if doc.Source.Identity != d.doc.Source.Identity || path.Dir(doc.RelativePath) != path.Dir(d.doc.RelativePath) || !strings.HasSuffix(doc.RelativePath, ".go") || strings.HasSuffix(doc.RelativePath, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), doc.RelativePath, doc.Content, 0)
		if err != nil || f.Name.Name != d.file.Name.Name || doc.Source != d.doc.Source {
			return false
		}
	}
	return true
}
