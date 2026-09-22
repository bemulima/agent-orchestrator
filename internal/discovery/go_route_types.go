package discovery

import (
	"go/ast"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Index only structural types and route-like configuration defaults. No
// environment files or secret-valued configuration fields are evaluated.
func (g *goRouteGraph) indexTypes(file *goRouteFile, parsed *ast.File) {
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typ, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typ.Type.(*ast.StructType)
			if !ok {
				continue
			}
			key := file.pkg + "/" + typ.Name.Name
			g.fields[key] = map[string]string{}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					g.fields[key][name.Name] = goQualifiedType(file, field.Type)
					if field.Tag != nil {
						tag, _ := strconv.Unquote(field.Tag.Value)
						value := reflect.StructTag(tag).Get("envDefault")
						if value == "" {
							value = reflect.StructTag(tag).Get("default")
						}
						if strings.HasPrefix(value, "/") {
							g.defaults[key+"/"+name.Name] = value
						}
					}
				}
			}
		}
	}
}

func goQualifiedType(file *goRouteFile, expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.StarExpr:
		return goQualifiedType(file, value.X)
	case *ast.Ident:
		return file.pkg + "/" + value.Name
	case *ast.SelectorExpr:
		if id, ok := value.X.(*ast.Ident); ok {
			if pkg, ok := file.imports[id.Name]; ok {
				return pkg + "/" + value.Sel.Name
			}
		}
	}
	return ""
}

func (g *goRouteGraph) expressionType(f *goRouteFunction, expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		for scope := f; scope != nil; scope = scope.parent {
			if typ := scope.variables[value.Name]; typ != "" {
				return typ
			}
		}
	case *ast.SelectorExpr:
		return g.fields[g.expressionType(f, value.X)][value.Sel.Name]
	case *ast.UnaryExpr:
		return g.expressionType(f, value.X)
	case *ast.CompositeLit:
		return goQualifiedType(f.file, value.Type)
	case *ast.CallExpr:
		// Resolve constructors without recursively resolving arbitrary method chains.
		if target := g.resolve(f, value.Fun); target != nil {
			return target.resultType
		}
	}
	return ""
}

func (g *goRouteGraph) routeString(f *goRouteFunction, expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			return text, err == nil
		}
	case *ast.SelectorExpr:
		text, ok := g.defaults[g.expressionType(f, value.X)+"/"+value.Sel.Name]
		return text, ok
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, ok := g.routeString(f, value.X)
			right, other := g.routeString(f, value.Y)
			return left + right, ok && other
		}
	}
	return "", false
}

func (g *goRouteGraph) handleHTTP(f *goRouteFunction, receiver string, call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	pattern, ok := g.routeString(f, call.Args[0])
	if !ok {
		return
	}
	if strip, ok := call.Args[1].(*ast.CallExpr); ok && len(strip.Args) == 2 {
		if selector, ok := strip.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "StripPrefix" {
			if prefix, ok := g.routeString(f, strip.Args[0]); ok {
				g.edge(receiver, g.node(f, strip.Args[1]), prefix)
			}
			return
		}
	}
	if fields := strings.Fields(pattern); len(fields) == 2 && strings.HasPrefix(fields[1], "/") {
		g.route(receiver, strings.ToUpper(fields[0]), fields[1], f.file.path)
		return
	}
	methods := g.handlerMethods(f, call.Args[1], map[string]bool{})
	if len(methods) == 0 {
		if isHealthRoute(pattern) {
			methods = []string{"GET"}
		} else {
			methods = []string{"ANY"}
		}
	}
	for _, method := range methods {
		g.route(receiver, method, pattern, f.file.path)
	}
}

func (g *goRouteGraph) handlerMethods(f *goRouteFunction, expr ast.Expr, visiting map[string]bool) []string {
	methods := map[string]bool{}
	var body *ast.BlockStmt
	if lit, ok := expr.(*ast.FuncLit); ok {
		body = lit.Body
	}
	if target := g.resolve(f, expr); target != nil && !visiting[target.scope] {
		visiting[target.scope] = true
		defer delete(visiting, target.scope)
		body = target.body
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		for _, arg := range call.Args {
			if literal, ok := arg.(*ast.CompositeLit); ok {
				if _, ok := literal.Type.(*ast.MapType); ok {
					for _, element := range literal.Elts {
						if pair, ok := element.(*ast.KeyValueExpr); ok {
							if method := goRouteMethod(pair.Key); method != "" {
								methods[method] = true
							}
						}
					}
				}
			}
			for _, method := range g.handlerMethods(f, arg, visiting) {
				methods[method] = true
			}
		}
	}
	if body != nil {
		ast.Inspect(body, func(node ast.Node) bool {
			if comparison, ok := node.(*ast.BinaryExpr); ok && (comparison.Op == token.NEQ || comparison.Op == token.EQL) {
				if selector, ok := comparison.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "Method" {
					if method := goRouteMethod(comparison.Y); method != "" {
						methods[method] = true
					}
				}
			}
			if choice, ok := node.(*ast.SwitchStmt); ok {
				if selector, ok := choice.Tag.(*ast.SelectorExpr); ok && selector.Sel.Name == "Method" {
					for _, statement := range choice.Body.List {
						if clause, ok := statement.(*ast.CaseClause); ok {
							for _, value := range clause.List {
								if method := goRouteMethod(value); method != "" {
									methods[method] = true
								}
							}
						}
					}
				}
			}
			return true
		})
	}
	result := make([]string, 0, len(methods))
	for method := range methods {
		result = append(result, method)
	}
	sort.Strings(result)
	return result
}
