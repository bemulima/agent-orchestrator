package discovery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// This is a bounded syntax analysis, not Go execution. Conditional registrations
// are included; external constructors and dynamic route strings are unresolved.
type goRouteFunction struct {
	file       *goRouteFile
	body       *ast.BlockStmt
	params     []string
	scope      string
	parent     *goRouteFunction
	closures   map[string]*goRouteFunction
	returns    []ast.Expr
	variables  map[string]string
	resultType string
}

type goRouteFile struct {
	path    string
	pkg     string
	imports map[string]string
}

type goRouteEdge struct{ child, prefix string }
type goRouteRegistration struct{ method, path, source string }
type goRouteGraph struct {
	functions  map[string]*goRouteFunction
	all        []*goRouteFunction
	edges      map[string][]goRouteEdge
	routes     map[string][]goRouteRegistration
	fields     map[string]map[string]string
	defaults   map[string]string
	unresolved map[string]string
}

func (s Scanner) extractMountedGoHTTPRoutes(state *detectorState) {
	g := goRouteGraph{functions: map[string]*goRouteFunction{}, edges: map[string][]goRouteEdge{}, routes: map[string][]goRouteRegistration{}}
	g.fields, g.defaults = map[string]map[string]string{}, map[string]string{}
	g.unresolved = map[string]string{}
	module := ""
	for _, line := range strings.Split(string(state.filesByPath["go.mod"]), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			module = strings.Trim(fields[1], "\"")
		}
	}
	var sources []string
	for source := range state.filesByPath {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		content := state.filesByPath[source]
		if !strings.HasSuffix(source, ".go") || isNonProductionEvidencePath(source) {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), source, content, 0)
		if err != nil {
			continue
		}
		file := &goRouteFile{path: source, pkg: path.Dir(source), imports: map[string]string{}}
		for _, imp := range parsed.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			name := path.Base(value)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			file.imports[name] = value
			if module != "" && strings.HasPrefix(value, module+"/") {
				file.imports[name] = strings.TrimPrefix(value, module+"/")
			}
		}
		g.indexTypes(file, parsed)
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := file.pkg + "/" + fn.Name.Name
			if fn.Recv != nil {
				key = goQualifiedType(file, fn.Recv.List[0].Type) + "#" + fn.Name.Name
			}
			f := g.function(file, fn.Type, fn.Body, key, nil)
			if fn.Recv != nil {
				for _, name := range fn.Recv.List[0].Names {
					f.variables[name.Name] = goQualifiedType(file, fn.Recv.List[0].Type)
				}
			}
			g.functions[key] = f
		}
	}
	for index := 0; index < len(g.all); index++ {
		g.inspect(g.all[index])
	}
	incoming := map[string]bool{}
	for _, edges := range g.edges {
		for _, edge := range edges {
			incoming[edge.child] = true
		}
	}
	visited := make(map[string]bool)
	var walk func(string, string, map[string]bool)
	walk = func(node, prefix string, visiting map[string]bool) {
		if _, unresolved := g.unresolved[node]; unresolved {
			return
		}
		key := node + "\x00" + prefix
		if visiting[node] || visited[key] || len(visited) >= 100000 {
			return
		}
		visited[key] = true
		visiting[node] = true
		defer delete(visiting, node)
		for _, route := range g.routes[node] {
			collectHTTPRoute(state, route.method, joinGoRoute(prefix, route.path), .9, route.source,
				"Go route registration resolved through local router mounts and registration callbacks; conditional registrations and source-declared configuration defaults are included.")
		}
		for _, edge := range g.edges[node] {
			walk(edge.child, joinGoRoute(prefix, edge.prefix), visiting)
		}
	}
	var roots []string
	for node := range g.routes {
		if !incoming[node] {
			roots = append(roots, node)
		}
	}
	for node := range g.edges {
		if !incoming[node] {
			roots = append(roots, node)
		}
	}
	sort.Strings(roots)
	for _, node := range roots {
		walk(node, "", map[string]bool{})
	}
	for node, source := range g.unresolved {
		state.collector.conflict("unresolved_go_router_prefix", node, .9, source, "A router prefix is computed dynamically without a source-declared default; its relative routes are not reported as root HTTP endpoints.")
	}
	if len(visited) >= 100000 {
		state.collector.conflict("go_route_resolution_limit", "http_routes", .9, "", "The bounded Go router graph reached its resolution limit; HTTP inventory may be incomplete.")
	}
}

func (g *goRouteGraph) function(file *goRouteFile, typ *ast.FuncType, body *ast.BlockStmt, scope string, parent *goRouteFunction) *goRouteFunction {
	f := &goRouteFunction{file: file, body: body, scope: scope, parent: parent, closures: map[string]*goRouteFunction{}, variables: map[string]string{}}
	if typ.Params != nil {
		for _, field := range typ.Params.List {
			for _, name := range field.Names {
				f.params = append(f.params, name.Name)
				f.variables[name.Name] = goQualifiedType(file, field.Type)
			}
		}
	}
	if typ.Results != nil && len(typ.Results.List) > 0 {
		f.resultType = goQualifiedType(file, typ.Results.List[0].Type)
	}
	g.all = append(g.all, f)
	ast.Inspect(body, func(n ast.Node) bool {
		if assignment, ok := n.(*ast.AssignStmt); ok {
			for i, expr := range assignment.Rhs {
				if lit, ok := expr.(*ast.FuncLit); ok && i < len(assignment.Lhs) {
					if id, ok := assignment.Lhs[i].(*ast.Ident); ok {
						f.closures[id.Name] = g.function(file, lit.Type, lit.Body, scope+"/"+id.Name, f)
					}
				}
			}
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if ret, ok := n.(*ast.ReturnStmt); ok {
			f.returns = append(f.returns, ret.Results...)
		}
		return true
	})
	return f
}

func (g *goRouteGraph) inspect(f *goRouteFunction) {
	ast.Inspect(f.body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if assign, ok := n.(*ast.AssignStmt); ok {
			for i, rhs := range assign.Rhs {
				if i < len(assign.Lhs) {
					if id, ok := assign.Lhs[i].(*ast.Ident); ok {
						if typ := g.expressionType(f, rhs); typ != "" {
							f.variables[id.Name] = typ
						}
					}
					lhs := g.node(f, assign.Lhs[i])
					child := g.node(f, rhs)
					if lhs != "" && child != "" {
						// With and plain aliases share an existing router's mount;
						// constructor results instead become children of the holder.
						_, alias := rhs.(*ast.Ident)
						if call, ok := rhs.(*ast.CallExpr); ok {
							if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "With" || sel.Sel.Name == "Group") {
								alias = true
							}
						}
						if alias {
							g.edge(child, lhs, "")
						} else {
							g.edge(lhs, child, "")
						}
					}
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			receiver := g.node(f, sel.X)
			method := strings.ToUpper(sel.Sel.Name)
			switch method {
			case "HANDLE", "HANDLEFUNC":
				g.handleHTTP(f, receiver, call)
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
				if len(call.Args) >= 2 {
					if route, ok := g.routeString(f, call.Args[0]); ok {
						g.route(receiver, method, route, f.file.path)
					}
				}
			case "METHOD", "METHODFUNC":
				if len(call.Args) >= 3 {
					g.route(receiver, goRouteMethod(call.Args[0]), goRouteString(call.Args[1]), f.file.path)
				}
			case "MOUNT":
				if len(call.Args) >= 2 {
					if prefix, ok := g.routeString(f, call.Args[0]); ok && prefix != "" {
						g.edge(receiver, g.node(f, call.Args[1]), prefix)
					} else {
						g.edge(g.unresolvedRouter(f, call), g.node(f, call.Args[1]), "")
					}
				}
			case "ROUTE", "GROUP":
				arg, prefix := 0, ""
				if method == "ROUTE" {
					arg = 1
					if len(call.Args) > 0 {
						prefix = goRouteString(call.Args[0])
					}
					if prefix == "" {
						return true
					}
				}
				if len(call.Args) > arg {
					if callback := g.callback(f, call.Args[arg]); callback != nil && len(callback.params) > 0 {
						g.edge(receiver, callback.scope+":"+callback.params[0], prefix)
					}
				}
			}
		}
		if target := g.resolve(f, call.Fun); target != nil {
			for i, arg := range call.Args {
				if i < len(target.params) {
					g.edge(g.node(f, arg), target.scope+":"+target.params[i], "")
				}
			}
		}
		return true
	})
}

func (g *goRouteGraph) resolve(f *goRouteFunction, expr ast.Expr) *goRouteFunction {
	if id, ok := expr.(*ast.Ident); ok {
		for scope := f; scope != nil; scope = scope.parent {
			if fn := scope.closures[id.Name]; fn != nil {
				return fn
			}
		}
		return g.functions[f.file.pkg+"/"+id.Name]
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if target := g.functions[g.expressionType(f, sel.X)+"#"+sel.Sel.Name]; target != nil {
			return target
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if pkg, ok := f.file.imports[id.Name]; ok {
				return g.functions[pkg+"/"+sel.Sel.Name]
			}
		}
	}
	return nil
}

func (g *goRouteGraph) callback(f *goRouteFunction, expr ast.Expr) *goRouteFunction {
	if lit, ok := expr.(*ast.FuncLit); ok {
		callback := g.function(f.file, lit.Type, lit.Body, f.scope+"/@"+strconv.Itoa(int(lit.Pos())), f)
		return callback
	}
	return g.resolve(f, expr)
}

func (g *goRouteGraph) node(f *goRouteFunction, expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "nil" || value.Name == "_" {
			return ""
		}
		// Callback parameters are local; references to enclosing routers retain
		// the enclosing identity (e.g. a closure using root.With(...)).
		if f.parent != nil && value.Obj != nil && value.Obj.Pos() < f.body.Pos() {
			local := false
			for _, param := range f.params {
				if param == value.Name {
					local = true
				}
			}
			if !local {
				return g.node(f.parent, expr)
			}
		}
		return f.scope + ":" + value.Name
	case *ast.CallExpr:
		if sel, ok := value.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "With" {
			return g.node(f, sel.X)
		}
		if sel, ok := value.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Group" && len(value.Args) > 0 {
			if prefix, ok := g.routeString(f, value.Args[0]); ok {
				node := f.scope + ":group@" + strconv.Itoa(int(value.Pos()))
				g.edge(g.node(f, sel.X), node, prefix)
				return node
			}
			if _, callback := value.Args[0].(*ast.FuncLit); !callback {
				return g.unresolvedRouter(f, value)
			}
		}
		if target := g.resolve(f, value.Fun); target != nil {
			result := target.scope + ":return"
			for _, ret := range target.returns {
				if id, ok := ret.(*ast.Ident); ok {
					g.edge(result, target.scope+":"+id.Name, "")
				}
			}
			return result
		}
	}
	return ""
}

func (g *goRouteGraph) unresolvedRouter(f *goRouteFunction, call *ast.CallExpr) string {
	node := f.scope + ":unresolved@" + strconv.Itoa(int(call.Pos()))
	g.unresolved[node] = f.file.path
	return node
}

func (g *goRouteGraph) edge(parent, child, prefix string) {
	if parent == "" || child == "" || parent == child {
		return
	}
	edge := goRouteEdge{child, prefix}
	for _, existing := range g.edges[parent] {
		if existing == edge {
			return
		}
	}
	g.edges[parent] = append(g.edges[parent], edge)
}

func (g *goRouteGraph) route(node, method, route, source string) {
	if node == "" || method == "" || (route != "" && !looksLikeRoute(route)) {
		return
	}
	g.routes[node] = append(g.routes[node], goRouteRegistration{method, route, source})
}

func goRouteString(expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, _ := strconv.Unquote(lit.Value)
		return value
	}
	return ""
}

func goRouteMethod(expr ast.Expr) string {
	if value := goRouteString(expr); value != "" {
		return strings.ToUpper(value)
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "Method") {
		return strings.ToUpper(strings.TrimPrefix(sel.Sel.Name, "Method"))
	}
	return ""
}

func joinGoRoute(prefix, route string) string {
	if prefix == "" || prefix == "/" {
		return route
	}
	if route == "" {
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(route, "/")
}
