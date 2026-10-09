package contextretrieval

import (
	"context"
	"encoding/json"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"go/ast"
	"path"
	"strconv"
	"strings"
)

// AnalysisBinding preserves the original proposed requirement while explicitly
// citing source-equivalent declarations. It approves no shared contract freeze.
type AnalysisBinding struct {
	FacetID      string         `json:"facet_id"`
	OriginalPath string         `json:"original_path"`
	Kind         string         `json:"kind"`
	Owner        AnalysisAnchor `json:"owner"`
	Domain       AnalysisAnchor `json:"domain"`
	Consumer     AnalysisAnchor `json:"consumer"`
}

func analysisBindingJSON(b AnalysisBinding) string { v, _ := json.Marshal(b); return string(v) }
func analysisBindings(seeds []core.Facet, anchors, decls []analysisDeclaration, snapshot core.SnapshotResult) []AnalysisBinding {
	result := []AnalysisBinding{}
	for _, seed := range seeds {
		if seed.ClaimKey != "domain-model" && seed.ClaimKey != "application-command-result" {
			continue
		}
		existing := false
		for _, d := range snapshot.Documents {
			existing = existing || d.Source.Identity == seed.SourceIdentity && d.RelativePath == seed.Path
		}
		if existing {
			continue
		}
		candidates := []AnalysisBinding{}
		for _, owner := range anchors {
			method, ok := owner.node.(*ast.FuncDecl)
			if !ok || method.Recv == nil || strings.HasSuffix(owner.doc.RelativePath, "_test.go") || !analysisPathMatches(owner.doc.RelativePath, []string{"internal/usecase/**", "internal/application/**", "usecase/**", "application/**"}) || owner.doc.Source.Identity != seed.SourceIdentity {
				continue
			}
			for _, typ := range anchors {
				_, ok := typ.node.(*ast.TypeSpec)
				if !ok || strings.HasSuffix(typ.doc.RelativePath, "_test.go") || !strings.HasPrefix(typ.doc.RelativePath, "internal/domain/") || typ.doc.Source.Identity != seed.SourceIdentity {
					continue
				}
				if !analysisDeclarationUnique(owner, decls) || !analysisDeclarationUnique(typ, decls) {
					continue
				}
				if !analysisResultType(owner, typ, snapshot) {
					continue
				}
				for _, consumer := range anchors {
					if consumer.doc.Source.Identity != seed.SourceIdentity || strings.HasSuffix(consumer.doc.RelativePath, "_test.go") || !analysisDeclarationUnique(consumer, decls) || !analysisPathMatches(consumer.doc.RelativePath, []string{"internal/transport/**", "internal/adapters/http/**", "internal/adapters/nats/**", "transport/**"}) {
						continue
					}
					if !analysisInterfaceCall(owner, consumer, decls, snapshot) {
						continue
					}
					candidates = append(candidates, AnalysisBinding{seed.ID, seed.Path, seed.ClaimKey, analysisAnchor(owner), analysisAnchor(typ), analysisAnchor(consumer)})
				}
			}
		}
		// Ambiguity is a real diagnostic, never arbitrary first-file selection.
		unique := map[string]AnalysisBinding{}
		for _, b := range candidates {
			unique[analysisBindingJSON(b)] = b
		}
		if len(unique) == 1 {
			for _, b := range unique {
				result = append(result, b)
			}
		}
	}
	return result
}
func analysisImports(d analysisDeclaration) map[string]string {
	out := map[string]string{}
	for _, i := range d.file.Imports {
		p, _ := strconv.Unquote(i.Path.Value)
		a := path.Base(p)
		if i.Name != nil {
			a = i.Name.Name
		}
		if a != "." && a != "_" {
			if _, exists := out[a]; exists {
				return map[string]string{}
			}
			out[a] = p
		}
	}
	return out
}
func analysisModule(snapshot core.SnapshotResult, source string) string {
	for _, d := range snapshot.Documents {
		if d.Source.Identity == source && d.RelativePath == "go.mod" {
			for _, line := range strings.Split(d.Content, "\n") {
				f := strings.Fields(line)
				if len(f) == 2 && f[0] == "module" {
					return strings.Trim(f[1], "\"")
				}
			}
		}
	}
	return ""
}
func analysisResultType(owner, typ analysisDeclaration, snapshot core.SnapshotResult) bool {
	fn, ok := owner.node.(*ast.FuncDecl)
	if !ok || fn.Type.Results == nil {
		return false
	}
	spec, ok := typ.node.(*ast.TypeSpec)
	if !ok {
		return false
	}
	module := analysisModule(snapshot, owner.doc.Source.Identity)
	if module == "" {
		return false
	}
	imports := analysisImports(owner)
	found := false
	for _, r := range fn.Type.Results.List {
		ast.Inspect(r.Type, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && s.Sel.Name == spec.Name.Name && imports[id.Name] == module+"/"+path.Dir(typ.doc.RelativePath) {
					found = true
				}
			}
			return true
		})
	}
	return found
}
func analysisType(e ast.Expr, imports map[string]string) string {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		if a, ok := n.X.(*ast.Ident); ok && imports[a.Name] != "" {
			return imports[a.Name] + "." + n.Sel.Name
		}
	case *ast.StarExpr:
		if s := analysisType(n.X, imports); s != "" {
			return "*" + s
		}
	case *ast.ArrayType:
		if n.Len == nil {
			if s := analysisType(n.Elt, imports); s != "" {
				return "[]" + s
			}
		}
	case *ast.Ellipsis:
		if s := analysisType(n.Elt, imports); s != "" {
			return "..." + s
		}
	}
	return ""
}
func analysisSignature(f *ast.FuncType, imports map[string]string) string {
	out := []string{}
	for _, list := range []*ast.FieldList{f.Params, f.Results} {
		out = append(out, "|")
		if list != nil {
			for _, field := range list.List {
				s := analysisType(field.Type, imports)
				if s == "" {
					return ""
				}
				count := len(field.Names)
				if count == 0 {
					count = 1
				}
				for i := 0; i < count; i++ {
					out = append(out, s)
				}
			}
		}
	}
	return strings.Join(out, ",")
}
func analysisInterfaceCall(owner, consumer analysisDeclaration, decls []analysisDeclaration, snapshot core.SnapshotResult) bool {
	fn, ok := owner.node.(*ast.FuncDecl)
	if !ok {
		return false
	}
	caller, ok := consumer.node.(*ast.FuncDecl)
	if !ok || caller.Recv == nil || len(caller.Recv.List) != 1 || len(caller.Recv.List[0].Names) != 1 {
		return false
	}
	recv := caller.Recv.List[0]
	receiver := recv.Names[0].Name
	receiverObject := recv.Names[0].Obj
	if receiverObject == nil {
		return false
	}
	typ := recv.Type
	if p, ok := typ.(*ast.StarExpr); ok {
		typ = p.X
	}
	id, ok := typ.(*ast.Ident)
	if !ok {
		return false
	}
	signature := analysisSignature(fn.Type, analysisImports(owner))
	if signature == "" {
		return false
	}
	receiverCount := 0
	for _, d := range decls {
		if d.doc.Source.Identity == consumer.doc.Source.Identity && path.Dir(d.doc.RelativePath) == path.Dir(consumer.doc.RelativePath) && !strings.HasSuffix(d.doc.RelativePath, "_test.go") {
			if spec, ok := d.node.(*ast.TypeSpec); ok && spec.Name.Name == id.Name {
				receiverCount++
			}
		}
	}
	if receiverCount != 1 {
		return false
	}
	for _, d := range decls {
		if d.doc.Source.Identity != consumer.doc.Source.Identity || path.Dir(d.doc.RelativePath) != path.Dir(consumer.doc.RelativePath) || strings.HasSuffix(d.doc.RelativePath, "_test.go") {
			continue
		}
		s, ok := d.node.(*ast.TypeSpec)
		if !ok || s.Name.Name != id.Name {
			continue
		}
		structure, ok := s.Type.(*ast.StructType)
		if !ok {
			continue
		}
		fieldNames := map[string]bool{}
		uniqueFields := true
		for _, field := range structure.Fields.List {
			for _, name := range field.Names {
				if fieldNames[name.Name] {
					uniqueFields = false
				}
				fieldNames[name.Name] = true
			}
		}
		if !uniqueFields {
			return false
		}
		for _, field := range structure.Fields.List {
			it, ok := field.Type.(*ast.Ident)
			if !ok {
				continue
			}
			interfaceCount := 0
			for _, definition := range decls {
				if definition.doc.Source.Identity == d.doc.Source.Identity && path.Dir(definition.doc.RelativePath) == path.Dir(d.doc.RelativePath) && !strings.HasSuffix(definition.doc.RelativePath, "_test.go") {
					if spec, ok := definition.node.(*ast.TypeSpec); ok && spec.Name.Name == it.Name {
						interfaceCount++
					}
				}
			}
			if interfaceCount != 1 {
				continue
			}
			for _, iface := range decls {
				if iface.doc.Source.Identity != d.doc.Source.Identity || path.Dir(iface.doc.RelativePath) != path.Dir(d.doc.RelativePath) || strings.HasSuffix(iface.doc.RelativePath, "_test.go") {
					continue
				}
				spec, ok := iface.node.(*ast.TypeSpec)
				if !ok || spec.Name.Name != it.Name {
					continue
				}
				inter, ok := spec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				declared := false
				methodCount := 0
				for _, m := range inter.Methods.List {
					for _, name := range m.Names {
						if name.Name == fn.Name.Name {
							methodCount++
						}
					}
				}
				if methodCount != 1 {
					continue
				}
				for _, m := range inter.Methods.List {
					mt, ok := m.Type.(*ast.FuncType)
					if !ok || len(m.Names) != 1 || m.Names[0].Name != fn.Name.Name {
						continue
					}
					declared = declared || analysisSignature(mt, analysisImports(iface)) == signature
				}
				if !declared {
					continue
				}
				called := false
				ast.Inspect(caller.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					method, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || method.Sel.Name != fn.Name.Name {
						return true
					}
					f, ok := method.X.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					r, ok := f.X.(*ast.Ident)
					if !ok || r.Name != receiver || r.Obj != receiverObject {
						return true
					}
					for _, name := range field.Names {
						called = called || name.Name == f.Sel.Name
					}
					return true
				})
				if called {
					return true
				}
			}
		}
	}
	return false
}

type AnalysisBoundaryResolver struct{}

func (AnalysisBoundaryResolver) ID() string      { return "analysis-boundary" }
func (AnalysisBoundaryResolver) Version() string { return AnalysisVersion }
func (r AnalysisBoundaryResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if !strings.HasPrefix(plan.Route.Digest, AnalysisVersion+":") || facet.QueryKind != core.QueryContract {
		return unsupportedResult(facet, r.ID()), nil
	}
	var b AnalysisBinding
	if json.Unmarshal([]byte(facet.Text), &b) != nil || b.FacetID != facet.ID || b.OriginalPath != facet.Path || b.Kind != facet.ClaimKey {
		return unavailableFacet(facet, core.NotVerified, "ANALYSIS_BOUNDARY_PROOF_INVALID", "Exact versioned source-equivalence proof required."), nil
	}
	anchors := []analysisDeclaration{}
	decls := analysisDeclarations(snapshot)
	for _, a := range []AnalysisAnchor{b.Owner, b.Domain, b.Consumer} {
		admitted := false
		for _, s := range plan.Sources {
			admitted = admitted || s.Identity == a.Source && core.PathAdmitted(s, a.Path)
		}
		matches := []analysisDeclaration{}
		if admitted && a.Source == facet.SourceIdentity {
			for _, d := range decls {
				if analysisAnchor(d) == a {
					matches = append(matches, d)
				}
			}
		}
		if len(matches) != 1 {
			return unavailableFacet(facet, core.NotVerified, "ANALYSIS_BOUNDARY_PROOF_STALE", "Source-equivalence declaration/hash/admission is missing or ambiguous."), nil
		}
		anchors = append(anchors, matches[0])
	}
	bindings := analysisBindings([]core.Facet{facet}, anchors, decls, snapshot)
	if len(bindings) != 1 || analysisBindingJSON(bindings[0]) != analysisBindingJSON(b) {
		return unavailableFacet(facet, core.NotVerified, "ANALYSIS_BOUNDARY_NOT_CORROBORATED", "Local domain result and interface field-call signature syntax not verified."), nil
	}
	actual := b.Owner
	if b.Kind == "domain-model" {
		actual = b.Domain
	}
	selected := facet
	selected.Path = actual.Path
	selected.Symbol = actual.Symbol
	selected.ExpectedHash = actual.Hash
	selected.Text = ""
	selected.Resolver = "contract"
	result, err := (ContractResolver{}).Resolve(ctx, plan, selected, snapshot)
	for i := range result.Candidates {
		c := &result.Candidates[i]
		c.FacetIDs = []string{facet.ID}
		c.Links = append(c.Links, core.EvidenceLink{Kind: "declared_source", SourceIdentity: b.Domain.Source, RelativePath: b.Domain.Path, Symbol: shortAnalysisSymbol(b.Domain.Symbol), ExpectedHash: b.Domain.Hash}, core.EvidenceLink{Kind: "declared_source", SourceIdentity: b.Consumer.Source, RelativePath: b.Consumer.Path, Symbol: shortAnalysisSymbol(b.Consumer.Symbol), ExpectedHash: b.Consumer.Hash})
		c.Limitations = append(c.Limitations, "Source-equivalent conceptual boundary; original proposed path is not an existing file. Interface satisfaction and type-aware semantics UNSUPPORTED.")
	}
	return result, err
}

func analysisDeclarationUnique(selected analysisDeclaration, decls []analysisDeclaration) bool {
	count := 0
	for _, d := range decls {
		if d.doc.Source.Identity == selected.doc.Source.Identity && path.Dir(d.doc.RelativePath) == path.Dir(selected.doc.RelativePath) && d.symbol == selected.symbol && !strings.HasSuffix(d.doc.RelativePath, "_test.go") {
			count++
		}
	}
	return count == 1
}
