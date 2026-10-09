package contextretrieval

import (
	"context"
	"go/ast"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"gopkg.in/yaml.v3"
)

type projectLayerDeclaration struct {
	name, root string
	doc        core.Document
}

func projectReadAreaDeclarations(snapshot core.SnapshotResult) ([]projectLayerDeclaration, []core.RetrievalDiagnostic) {
	declarations := []projectLayerDeclaration{}
	diagnostics := []core.RetrievalDiagnostic{}
	for _, doc := range snapshot.Documents {

		if doc.RelativePath != ".ai/architecture.yaml" && doc.RelativePath != ".ai/service.yaml" {
			continue
		}
		var node yaml.Node
		decoder := yaml.NewDecoder(strings.NewReader(doc.Content))
		parseErr := decoder.Decode(&node)
		var trailing yaml.Node
		single := decoder.Decode(&trailing) == io.EOF
		if parseErr != nil || !single || !projectMetadataTree(&node, 0, new(int)) {
			diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Invalid/duplicate/aliased/unbounded authored read-layer structure; no new admission."))
			continue
		}
		if len(node.Content) != 1 {
			diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Exactly one authored metadata document required."))
			continue
		}
		root := node.Content[0]
		if root.Kind != yaml.MappingNode {
			diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Authored metadata requires a mapping."))
			continue
		}
		startDeclarations, startDiagnostics := len(declarations), len(diagnostics)
		add := func(name string, n *yaml.Node) {
			if n == nil {
				return
			}
			values := []*yaml.Node{n}
			if n.Kind == yaml.SequenceNode {
				values = n.Content
			}
			if len(values) > 64 {
				diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Read-layer path count exceeds64."))
				return
			}
			for _, v := range values {
				if v.Kind != yaml.ScalarNode {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Read-layer paths must be literal scalars."))
					continue
				}
				p := strings.TrimSuffix(strings.TrimSuffix(v.Value, "/**"), "/")
				if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`).MatchString(name) || !core.SafeRelativePath(p) || strings.ContainsAny(p, "*?[]{}$`\\") {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Unsafe literal read-layer name/path; metadata cannot admit paths."))
					continue
				}
				declarations = append(declarations, projectLayerDeclaration{name, p, doc})
			}
		}
		addComposition := func(n *yaml.Node) {
			if n != nil && n.Kind == yaml.ScalarNode && strings.ContainsAny(n.Value, "*?[]{}$`\\") {
				diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Composition path must be literal; no raw globs or shell forms."))
				return
			}
			add("composition", n)
		}
		layers := projectMapValue(root, "layers")
		if layers != nil && layers.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(layers.Content); i += 2 {
				add(layers.Content[i].Value, layers.Content[i+1])
			}
		}
		if layers != nil && layers.Kind != yaml.MappingNode && layers.Kind != yaml.SequenceNode {
			diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Authored layers require a mapping or named sequence."))
		}
		if layers != nil && layers.Kind == yaml.SequenceNode {
			for _, entry := range layers.Content {
				if entry.Kind != yaml.MappingNode {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Named layer requires a mapping."))
					continue
				}
				name := projectMapValue(entry, "name")
				if name == nil || name.Kind != yaml.ScalarNode {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Named layer requires a literal name."))
					continue
				}
				p := projectMapValue(entry, "source_path")
				if p == nil {
					p = projectMapValue(entry, "paths")
				}
				if p == nil {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Named layer requires supported paths."))
				}
				add(name.Value, p)
			}
		}
		for _, key := range []string{"entrypoint", "entrypoints", "composition_root", "composition_roots"} {
			entrypoints := projectMapValue(root, key)
			if entrypoints == nil {
				continue
			}
			identifier := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
			if entrypoints.Kind == yaml.MappingNode {
				if len(entrypoints.Content)/2 > 64 {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Composition entrypoint count exceeds64."))
					continue
				}
				for i := 0; i+1 < len(entrypoints.Content); i += 2 {
					if !identifier.MatchString(entrypoints.Content[i].Value) || entrypoints.Content[i+1].Kind != yaml.ScalarNode {
						diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Composition role requires a literal identifier and scalar path."))
						continue
					}
					addComposition(entrypoints.Content[i+1])
				}
				continue
			}
			if entrypoints.Kind != yaml.SequenceNode {
				addComposition(entrypoints)
				continue
			}
			if len(entrypoints.Content) > 64 {
				diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Composition entrypoint count exceeds64."))
				continue
			}
			for _, entry := range entrypoints.Content {
				if entry.Kind == yaml.ScalarNode {
					addComposition(entry)
					continue
				}
				name, p := projectMapValue(entry, "name"), projectMapValue(entry, "source_path")
				if entry.Kind != yaml.MappingNode || name == nil || name.Kind != yaml.ScalarNode || !identifier.MatchString(name.Value) || p == nil || p.Kind != yaml.ScalarNode {
					diagnostics = append(diagnostics, projectInvalidMetadata(doc, "Named composition entrypoint requires literal name and source_path."))
					continue
				}
				addComposition(p)
			}
		}
		if len(diagnostics) > startDiagnostics {
			declarations = declarations[:startDeclarations]
		}
	}
	return declarations, diagnostics
}

func projectReadLayers(snapshot core.SnapshotResult, anchors []analysisDeclaration, selectors []core.Facet, syntaxContexts map[string]string) ([]AnalysisLayer, []core.RetrievalDiagnostic) {
	declarations, diagnostics := projectReadAreaDeclarations(snapshot)
	sourceDeclarations := projectAnalysisDeclarations(snapshot)
	search := projectOperationAreas(snapshot, selectors)
	operations := search.areas
	diagnostics = append(diagnostics, search.diagnostics...)
	diagnostics = append(diagnostics, projectGeneralCitationDiagnostics(snapshot)...)
	witnesses := map[string]AnalysisLayer{}
	addWitness := func(a analysisDeclaration, layer, relation string, metadata core.Document) {
		if strings.HasSuffix(a.doc.RelativePath, "_test.go") {
			return
		}
		key := a.doc.Source.Identity + "\x00" + layer + "\x00" + a.doc.RelativePath
		meta := AnalysisAnchor{Source: metadata.Source.Identity, Path: metadata.RelativePath, Hash: metadata.ContentHash}
		w := AnalysisLayer{Source: a.doc.Source.Identity, Layer: layer, Witness: analysisAnchor(a), Evidence: projectEvidence(a), Relation: relation, Chain: []AnalysisAnchor{meta, analysisAnchor(a)}}
		old, exists := witnesses[key]
		if !exists || w.Witness.Symbol < old.Witness.Symbol {
			witnesses[key] = w
		}
	}
	for _, a := range anchors {
		if a.node == nil && !strings.HasSuffix(a.doc.RelativePath, ".sql") {
			continue
		}
		for _, decl := range declarations {
			if a.doc.Source.Identity != decl.doc.Source.Identity || !(a.doc.RelativePath == decl.root || strings.HasPrefix(a.doc.RelativePath, decl.root+"/")) {
				continue
			}
			layer := projectLayerID(decl.name, decl.root)
			if a.node == nil && layer != "backend.migration" {
				continue
			}
			addWitness(a, layer, "current authored read area and exact admitted source declaration; no business/write authority", decl.doc)
			if layer != "project."+decl.name {
				addWitness(a, "project."+decl.name, "literal authored read-area name; independent of semantic role refinement", decl.doc)
			}
			// Coarse transport/outbound is not a protocol claim. Direction refinement
			// needs actual HTTP AST, or an imported DTO/mapper reference from a current
			// incoming HTTP source declaration. These remain syntax relationships.
			if decl.name == "transport" || decl.name == "outbound" || decl.name == "infrastructure" && projectExactCaller(a, selectors, sourceDeclarations) && projectHTTPPackage(snapshot, a) {
				client, inbound := projectHTTPRole(a)
				if decl.name == "infrastructure" {
					client = projectHTTPConstructor(a, sourceDeclarations)
				}
				if client {
					addWitness(a, "backend.infrastructure.client", "authored area plus outgoing net/http syntax; no provider execution", decl.doc)
				}
				if decl.name != "infrastructure" && (inbound || projectHTTPDataReference(a, anchors, snapshot)) {
					addWitness(a, "backend.transport.http", "authored area plus incoming HTTP signature or imported DTO/mapper syntax", decl.doc)
				}
			}
		}
		// A verified strict reference attributes exactly its current file. The
		// declared use case remains the witness even when the caller asks about
		// an adjacent helper; a directory label never admits sibling files.
		for _, op := range operations {
			if op.declaration.doc.Source.Identity == a.doc.Source.Identity && op.declaration.doc.RelativePath == a.doc.RelativePath && op.declaration.doc.ContentHash == a.doc.ContentHash {
				addWitness(op.declaration, "backend.usecase", "strict current operation use_cases source reference; no runtime reachability", op.metadata)
				addWitness(op.declaration, "implementation.use_cases:"+path.Dir(a.doc.RelativePath), "strict current operation use_cases source reference", op.metadata)
				addWitness(op.declaration, "use_cases:"+path.Dir(a.doc.RelativePath), "strict current operation use_cases source reference; directory is provenance only", op.metadata)
				if op.unpinned {
					for _, layer := range []string{"backend.usecase", "implementation.use_cases:" + path.Dir(a.doc.RelativePath), "use_cases:" + path.Dir(a.doc.RelativePath)} {
						k := a.doc.Source.Identity + "\x00" + layer + "\x00" + a.doc.RelativePath
						w := witnesses[k]
						w.Relation += "; authored checksum absent; independently pinned current snapshot only"
						w.Evidence.Limitations = append(w.Evidence.Limitations, "Authored operation checksum absent; current caller/source/metadata snapshot only, no historical or Git-object certification.")
						witnesses[k] = w
					}
				}
			}
		}
	}
	typeWitnesses, typeDiagnostics := projectInfrastructureHTTPTypes(snapshot, anchors, selectors, declarations, syntaxContexts)
	diagnostics = append(diagnostics, typeDiagnostics...)
	for _, w := range typeWitnesses {
		key := w.Source + "\x00" + w.Layer + "\x00" + w.Witness.Path
		old, exists := witnesses[key]
		if !exists || w.Witness.Symbol < old.Witness.Symbol {
			witnesses[key] = w
		}
	}
	out := []AnalysisLayer{}
	for _, w := range witnesses {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Source+out[i].Layer+out[i].Witness.Path < out[j].Source+out[j].Layer+out[j].Witness.Path
	})
	sort.Slice(diagnostics, func(i, j int) bool {
		return diagnostics[i].SourceIdentity+diagnostics[i].RelativePath+diagnostics[i].Code < diagnostics[j].SourceIdentity+diagnostics[j].RelativePath+diagnostics[j].Code
	})
	return out, diagnostics
}

func projectLayerID(name, root string) string {
	aliases := map[string]string{"domain": "backend.domain", "client": "backend.infrastructure.client", "persistence": "backend.infrastructure.persistence", "migration": "backend.migration", "messaging": "backend.infrastructure.messaging", "usecase": "backend.usecase", "use_cases": "backend.usecase", "application": "backend.usecase", "composition": "backend.composition", "http_transport": "backend.transport.http", "transport-http": "backend.transport.http", "message_transport": "backend.transport.message", "postgres_infrastructure": "backend.infrastructure.persistence", "nats_infrastructure": "backend.infrastructure.messaging", "websocket_transport": "project.websocket_transport"}
	if v := aliases[name]; v != "" {
		return v
	}
	if name == "outbound" {
		return "outbound:" + root
	}
	return "project." + name
}
func projectMapValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func projectMetadataTree(n *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 16 || *count > 4096 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return false
	}
	if n.Kind == yaml.MappingNode {
		keys := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Value == "<<" || keys[k.Value] {
				return false
			}
			keys[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if !projectMetadataTree(c, depth+1, count) {
			return false
		}
	}
	return true
}
func projectHTTPRole(a analysisDeclaration) (client, inbound bool) {
	if a.file == nil {
		return false, false
	}
	aliases := map[string]bool{}
	for name, imported := range analysisImports(a) {
		if imported == "net/http" {
			aliases[name] = true
		}
	}
	ast.Inspect(a.node, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok && id.Obj == nil && aliases[id.Name] {
				switch s.Sel.Name {
				case "NewRequest", "NewRequestWithContext", "Client", "DefaultClient":
					client = true
				case "ResponseWriter", "HandlerFunc", "Handler":
					inbound = true
				}
			}
		}
		return true
	})
	return
}
func projectHTTPDataReference(a analysisDeclaration, anchors []analysisDeclaration, snapshot core.SnapshotResult) bool {
	module := analysisModule(snapshot, a.doc.Source.Identity)
	if module == "" {
		return false
	}
	for _, caller := range anchors {
		if caller.doc.Source.Identity != a.doc.Source.Identity || caller.file == nil || caller.node == nil {
			continue
		}
		_, inbound := projectHTTPRole(caller)
		if !inbound {
			continue
		}
		imports := analysisImports(caller)
		found := false
		ast.Inspect(caller.node, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && id.Obj == nil && imports[id.Name] == module+"/"+path.Dir(a.doc.RelativePath) && s.Sel.Name == shortAnalysisSymbol(a.symbol) {
					found = true
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func projectGeneralCitationDiagnostics(snapshot core.SnapshotResult) []core.RetrievalDiagnostic {
	diagnostics := []core.RetrievalDiagnostic{}
	for _, doc := range snapshot.Documents {
		if doc.RelativePath == ".ai/architecture/service.yaml" || doc.RelativePath == ".ai/architecture/service.yml" {
			result, _ := (MetadataResolver{}).Resolve(context.Background(), core.RetrievalPlan{Limits: core.Limits{MaxResults: 1000}}, core.Facet{ID: "project-metadata-citations", SourceIdentity: doc.Source.Identity, Path: doc.RelativePath, QueryKind: core.QueryMetadata, ClaimType: core.ArchitectureRule}, snapshot)
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}
	return diagnostics
}
