package contextretrieval

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// GoResolver is bounded Go syntax analysis. It never implies type-aware callers,
// references, build selection or runtime execution.
type GoResolver struct{}

func (GoResolver) ID() string      { return "go" }
func (GoResolver) Version() string { return "go-ast.v1" }
func (resolver GoResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	switch facet.QueryKind {
	case core.QueryDefinition, core.QueryDeclarations, core.QueryImports, core.QueryTests:
	default:
		return unsupportedResult(facet, resolver.ID()), nil
	}
	result := core.RetrievalResult{}
	for _, doc := range snapshot.Documents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !strings.HasSuffix(doc.RelativePath, ".go") || (facet.SourceIdentity != "" && doc.Source.Identity != facet.SourceIdentity) {
			continue
		}
		test := strings.HasSuffix(doc.RelativePath, "_test.go")
		if facet.QueryKind == core.QueryTests && !test {
			continue
		}
		if facet.Path != "" && doc.RelativePath != facet.Path && !strings.HasPrefix(doc.RelativePath, facet.Path+"/") {
			if facet.QueryKind != core.QueryTests || path.Dir(doc.RelativePath) != path.Dir(facet.Path) {
				continue
			}
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, doc.RelativePath, doc.Content, parser.ParseComments)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "GO_PARSE_FAILED", Status: core.Partial, SourceIdentity: doc.Source.Identity, RelativePath: doc.RelativePath, FacetID: facet.ID, Message: "Go source could not be parsed"})
			continue
		}
		emit := func(node ast.Node, symbol, kind string) {
			if facet.QueryKind != core.QueryTests && facet.Symbol != "" && !symbolMatches(symbol, facet.Symbol) {
				return
			}
			if facet.Text != "" && !strings.Contains(doc.Content[int(node.Pos())-1:int(node.End())-1], facet.Text) {
				return
			}
			start, end := fset.Position(node.Pos()), fset.Position(node.End())
			span := core.EvidenceSpan{StartLine: start.Line, EndLine: end.Line, StartByte: start.Offset, EndByte: end.Offset}
			provenance := documentProvenance(doc.RelativePath)
			candidate := CandidateFromDocument(doc, facet, resolver.ID(), resolver.Version(), span, kind, provenance)
			candidate.Symbol = symbol
			candidate.EvidenceID = "evidence:" + hashJSON(struct{ Base, Symbol string }{candidate.EvidenceID, symbol})
			candidate.Limitations = append(candidate.Limitations, "Go AST syntax only; build tags and type-aware relationships are unverified")
			if test {
				candidate.Limitations = append(candidate.Limitations, "test certification NOT_VERIFIED; assertions were not executed")
			}
			result.Candidates = append(result.Candidates, candidate)
		}
		if facet.QueryKind == core.QueryImports {
			for _, imp := range file.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				emit(imp, value, "import")
			}
			continue
		}
		for _, decl := range file.Decls {
			switch value := decl.(type) {
			case *ast.FuncDecl:
				symbol := file.Name.Name + "." + value.Name.Name
				if value.Recv != nil && len(value.Recv.List) > 0 {
					receiver := value.Recv.List[0].Type
					if star, ok := receiver.(*ast.StarExpr); ok {
						receiver = star.X
					}
					if ident, ok := receiver.(*ast.Ident); ok {
						symbol = ident.Name + "." + value.Name.Name
					}
				}
				if facet.QueryKind == core.QueryTests {
					if !strings.HasPrefix(value.Name.Name, "Test") && !strings.HasPrefix(value.Name.Name, "Benchmark") && !strings.HasPrefix(value.Name.Name, "Fuzz") {
						continue
					}
					if facet.Symbol != "" && !symbolMatches(symbol, facet.Symbol) && !identPresent(value.Body, facet.Symbol) {
						continue
					}
					emit(value, symbol, "test")
				} else {
					emit(value, symbol, "definition")
				}
			case *ast.GenDecl:
				if facet.QueryKind == core.QueryTests {
					continue
				}
				for _, spec := range value.Specs {
					switch declared := spec.(type) {
					case *ast.TypeSpec:
						emit(value, file.Name.Name+"."+declared.Name.Name, "definition")
					case *ast.ValueSpec:
						for _, name := range declared.Names {
							emit(value, file.Name.Name+"."+name.Name, "definition")
						}
					}
				}
			}
		}
	}
	result.Coverage = []core.CoverageResult{CoverageForFacet(snapshot, facet, len(result.Candidates))}
	if len(result.Diagnostics) > 0 {
		result.Coverage[0].Complete = false
		result.Coverage[0].Status = core.Partial
		if len(result.Candidates) == 0 {
			result.Coverage[0].RequirementState = core.NotVerified
		}
	}
	symbolLimit := plan.Limits.MaxSymbolMatches
	if symbolLimit <= 0 {
		symbolLimit = 100
	}
	capResult(&result, symbolLimit)
	capResult(&result, plan.Limits.MaxResults)
	return result, nil
}
func symbolMatches(actual, wanted string) bool {
	return actual == wanted || strings.TrimPrefix(actual, strings.Split(actual, ".")[0]+".") == wanted || strings.HasSuffix(actual, "."+wanted)
}
func identPresent(node ast.Node, wanted string) bool {
	if node == nil {
		return false
	}
	parts := strings.Split(wanted, ".")
	name := parts[len(parts)-1]
	found := false
	ast.Inspect(node, func(value ast.Node) bool {
		if ident, ok := value.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// OperationsFromSnapshot feeds the existing route/NATS detector primitives from
// the already acquired snapshot, so operation queries do not run a second scan.
func OperationsFromSnapshot(snapshot core.SnapshotResult, sourceIdentity string) ([]domain.DiscoveredOperation, []domain.Evidence) {
	files := []discovery.InventoryFile{}
	for _, doc := range snapshot.Documents {
		if doc.Source.Identity == sourceIdentity {
			files = append(files, discovery.InventoryFile{Path: doc.RelativePath, Content: []byte(doc.Content)})
		}
	}
	return discovery.OperationsFromInventory(files)
}

var _ core.Resolver = GoResolver{}
