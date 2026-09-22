package discovery

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// Non-HTTP discovery is bounded syntax analysis. It never executes repository
// code or imports a declaration from an architecture manifest. Dynamic subjects
// remain unresolved evidence rather than invented operation identities.
func (s Scanner) extractNonHTTPOperations(state *detectorState) {
	var sources []string
	for source := range state.filesByPath {
		if strings.HasSuffix(source, ".go") && !isNonProductionEvidencePath(source) {
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)
	literals := newNonHTTPLiteralIndex(state, sources)
	for _, source := range sources {
		discoverGoNonHTTP(state, source, literals)
	}
}

func discoverGoNonHTTP(state *detectorState, source string, literals *nonHTTPLiteralIndex) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, source, state.filesByPath[source], 0)
	if err != nil {
		return
	}
	hasNATS := false
	timeAlias := ""
	for _, imp := range file.Imports {
		name, _ := strconv.Unquote(imp.Path.Value)
		if name == "github.com/nats-io/nats.go" {
			hasNATS = true
		}
		if name == "time" {
			timeAlias = "time"
			if imp.Name != nil {
				timeAlias = imp.Name.Name
			}
		}
	}
	functions := map[string]*ast.FuncDecl{}
	background := nonHTTPBackgroundFunctions(file, source)
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil {
				functions[node.Name.Name] = node
			}
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			name = strings.TrimPrefix(nonHTTPExpr(fn.Recv.List[0].Type), "*") + "." + name
		}
		backgroundCalls := map[*ast.CallExpr]bool{}
		if background[fn] {
			nonHTTPInspectBackground(fn.Body, func(call *ast.CallExpr) { backgroundCalls[call] = true })
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if hasNATS && (selector.Sel.Name == "Subscribe" || selector.Sel.Name == "QueueSubscribe") && len(call.Args) >= 2 {
				subjects, resolved := literals.subjects(source, fn, call)
				if !resolved {
					state.collector.conflict("unresolved_operation", "nats subscription", .9, source,
						"NATS subscription at line "+strconv.Itoa(fset.Position(call.Pos()).Line)+" has a dynamic subject; no canonical identity was inferred.")
					return true
				}
				callbackIndex := 1
				if selector.Sel.Name == "QueueSubscribe" {
					callbackIndex = 2
				}
				if len(call.Args) <= callbackIndex {
					return true
				}
				body := literals.callback(source, fn, call.Args[callbackIndex])
				operationType := domain.ArchitectureOperationNATSEventSubscriber
				role := "event_subscriber"
				if nonHTTPResponds(body) {
					operationType = domain.ArchitectureOperationNATSRequestReply
					role = "request_handler"
				}
				for _, subject := range subjects {
					if strings.TrimSpace(subject) != "" {
						state.operation(domain.DiscoveredOperation{Type: operationType, Protocol: "nats", Subject: subject,
							Role: role, SourcePath: source, SourceLine: fset.Position(call.Pos()).Line, Confidence: .8})
					}
				}
			}
			// A ticker is an operation only when a loop consumes its ticks. An
			// unrelated loop or an unused ticker must not inflate completeness.
			if owner, ok := selector.X.(*ast.Ident); ok && backgroundCalls[call] && owner.Name == timeAlias && (selector.Sel.Name == "NewTicker" || selector.Sel.Name == "Tick") && len(call.Args) == 1 && nonHTTPConsumesTicks(fn.Body, call, selector.Sel.Name == "NewTicker") {
				state.operation(domain.DiscoveredOperation{Type: domain.ArchitectureOperationScheduled, Protocol: "scheduled",
					Name: path.Dir(source) + "/" + name, Schedule: nonHTTPExpr(call.Args[0]), SourcePath: source,
					SourceLine: fset.Position(call.Pos()).Line, Confidence: .8})
			}
			return true
		})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			launch, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			id, ok := launch.Call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			target := functions[id.Name]
			if target == nil || !nonHTTPLoops(target.Body) || !strings.Contains(strings.ToLower(id.Name), "consumer") && !strings.Contains(strings.ToLower(id.Name), "worker") {
				return true
			}
			state.operation(domain.DiscoveredOperation{Type: domain.ArchitectureOperationWorker, Protocol: "worker",
				Name: path.Dir(source) + "/" + id.Name, SourcePath: source, SourceLine: fset.Position(launch.Pos()).Line, Confidence: .8})
			return true
		})
	}
}

// A consumed ticker alone is not a platform schedule: HTTP streams, websocket
// sessions and task activities all have request-scoped timers. Follow local
// startup calls and goroutine launches instead. Worker.Run is an explicit
// lifecycle interface whose composition root commonly lives in another package.
func nonHTTPBackgroundFunctions(file *ast.File, source string) map[*ast.FuncDecl]bool {
	eligible := map[*ast.FuncDecl]bool{}
	functions := map[string][]*ast.FuncDecl{}
	var queue []*ast.FuncDecl
	requestScoped := func(fn *ast.FuncDecl) bool {
		if strings.Contains("/"+source, "/activities/") {
			return true
		}
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				typ := nonHTTPExpr(field.Type)
				if strings.HasSuffix(typ, ".ResponseWriter") || strings.HasSuffix(typ, ".Request") || strings.HasSuffix(typ, ".Context") && (strings.HasPrefix(typ, "*gin.") || strings.HasPrefix(typ, "*fiber.")) {
					return true
				}
			}
		}
		return false
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || requestScoped(fn) {
			continue
		}
		functions[fn.Name.Name] = append(functions[fn.Name.Name], fn)
		receiver := ""
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			receiver = strings.TrimPrefix(nonHTTPExpr(fn.Recv.List[0].Type), "*")
		}
		entrypoint := fn.Recv == nil && (fn.Name.Name == "main" || fn.Name.Name == "init")
		constructor := fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "New")
		lifecycle := (fn.Name.Name == "Run" || fn.Name.Name == "Start") && (strings.HasSuffix(receiver, "Worker") || strings.HasPrefix(source, "cmd/"))
		if entrypoint || constructor || lifecycle {
			eligible[fn] = true
			queue = append(queue, fn)
		}
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		nonHTTPInspectBackground(fn.Body, func(call *ast.CallExpr) {
			name := ""
			switch target := call.Fun.(type) {
			case *ast.Ident:
				name = target.Name
			case *ast.SelectorExpr:
				name = target.Sel.Name
			}
			// Ambiguous method names need type resolution; do not guess.
			if targets := functions[name]; len(targets) == 1 && !eligible[targets[0]] {
				eligible[targets[0]] = true
				queue = append(queue, targets[0])
			}
		})
	}
	return eligible
}

func nonHTTPInspectBackground(body ast.Node, visit func(*ast.CallExpr)) {
	ast.Inspect(body, func(node ast.Node) bool {
		// Registered callback bodies execute later in another lifecycle. Only
		// goroutine literals are explicit background launches at this call site.
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if launch, ok := node.(*ast.GoStmt); ok {
			if literal, ok := launch.Call.Fun.(*ast.FuncLit); ok {
				nonHTTPInspectBackground(literal.Body, visit)
				return false
			}
		}
		if call, ok := node.(*ast.CallExpr); ok {
			visit(call)
		}
		return true
	})
}

func nonHTTPConsumesTicks(body ast.Node, ticker *ast.CallExpr, hasChannelField bool) bool {
	channel := ""
	ast.Inspect(body, func(node ast.Node) bool {
		switch binding := node.(type) {
		case *ast.AssignStmt:
			for i, value := range binding.Rhs {
				if value == ticker && i < len(binding.Lhs) {
					channel = nonHTTPExpr(binding.Lhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, value := range binding.Values {
				if value == ticker && i < len(binding.Names) {
					channel = binding.Names[i].Name
				}
			}
		}
		return true
	})
	if hasChannelField && channel != "" {
		channel += ".C"
	}
	matches := func(expr ast.Expr) bool {
		return !hasChannelField && expr == ticker || channel != "" && nonHTTPExpr(expr) == channel
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if loop, ok := node.(*ast.RangeStmt); ok && matches(loop.X) {
			found = true
		}
		if loop, ok := node.(*ast.ForStmt); ok {
			ast.Inspect(loop, func(inner ast.Node) bool {
				if receive, ok := inner.(*ast.UnaryExpr); ok && receive.Op == token.ARROW && matches(receive.X) {
					found = true
				}
				return true
			})
		}
		return !found
	})
	return found
}

func nonHTTPResponds(body ast.Node) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Respond" || selector.Sel.Name == "RespondMsg" || selector.Sel.Name == "Reply") {
			found = true
		}
		return true
	})
	return found
}

func nonHTTPLoops(body ast.Node) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			found = true
		}
		return true
	})
	return found
}

func nonHTTPExpr(expr ast.Expr) string {
	var out bytes.Buffer
	_ = format.Node(&out, token.NewFileSet(), expr)
	return out.String()
}
