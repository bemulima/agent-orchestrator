package discovery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
)

// This index resolves only source-declared local constants and literal string
// collections. It never loads dependencies, evaluates code, or reads manifests.
type nonHTTPLiteralFile struct {
	pkg     string
	imports map[string]string
}

type nonHTTPLiteralBinding struct {
	file *nonHTTPLiteralFile
	expr ast.Expr
}

type nonHTTPLiteralIndex struct {
	files       map[string]*nonHTTPLiteralFile
	constants   map[string]nonHTTPLiteralBinding
	collections map[string]nonHTTPLiteralBinding
	functions   map[string]*ast.FuncDecl
}

func newNonHTTPLiteralIndex(state *detectorState, sources []string) *nonHTTPLiteralIndex {
	index := &nonHTTPLiteralIndex{files: map[string]*nonHTTPLiteralFile{}, constants: map[string]nonHTTPLiteralBinding{}, collections: map[string]nonHTTPLiteralBinding{}, functions: map[string]*ast.FuncDecl{}}
	module := ""
	for _, line := range strings.Split(string(state.filesByPath["go.mod"]), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			module = strings.Trim(fields[1], "\"")
		}
	}
	for _, source := range sources {
		parsed, err := parser.ParseFile(token.NewFileSet(), source, state.filesByPath[source], 0)
		if err != nil {
			continue
		}
		file := &nonHTTPLiteralFile{pkg: path.Dir(source), imports: map[string]string{}}
		index.files[source] = file
		for _, imp := range parsed.Imports {
			value, _ := strconv.Unquote(imp.Path.Value)
			if module == "" || !strings.HasPrefix(value, module+"/") {
				continue
			}
			name := path.Base(value)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			file.imports[name] = strings.TrimPrefix(value, module+"/")
		}
		for _, declaration := range parsed.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for n, name := range value.Names {
						if n >= len(value.Values) {
							continue
						}
						binding := nonHTTPLiteralBinding{file, value.Values[n]}
						key := file.pkg + "/" + name.Name
						if declaration.Tok == token.CONST {
							index.constants[key] = binding
						} else if declaration.Tok == token.VAR {
							index.collections[key] = binding
						}
					}
				}
			case *ast.FuncDecl:
				name := declaration.Name.Name
				if declaration.Recv != nil && len(declaration.Recv.List) == 1 {
					name = strings.TrimPrefix(nonHTTPExpr(declaration.Recv.List[0].Type), "*") + "." + name
				}
				index.functions[file.pkg+"/"+name] = declaration
			}
		}
	}
	return index
}

func (index *nonHTTPLiteralIndex) key(file *nonHTTPLiteralFile, expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return file.pkg + "/" + value.Name
	case *ast.SelectorExpr:
		if owner, ok := value.X.(*ast.Ident); ok {
			if pkg, ok := file.imports[owner.Name]; ok {
				return pkg + "/" + value.Sel.Name
			}
		}
	}
	return ""
}

func (index *nonHTTPLiteralIndex) scalar(file *nonHTTPLiteralFile, expr ast.Expr, depth int) (string, bool) {
	if file == nil || depth > 32 {
		return "", false
	}
	if id, ok := expr.(*ast.Ident); ok && id.Obj != nil {
		if id.Obj.Kind == ast.Var {
			return "", false
		}
		if id.Obj.Kind == ast.Con {
			if value := nonHTTPLocalBinding(id); value != nil {
				return index.scalar(file, value, depth+1)
			}
		}
	}
	if binding, ok := index.constants[index.key(file, expr)]; ok {
		return index.scalar(binding.file, binding.expr, depth+1)
	}
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			return text, err == nil
		}
	case *ast.ParenExpr:
		return index.scalar(file, value.X, depth+1)
	case *ast.CallExpr:
		if name, ok := value.Fun.(*ast.Ident); ok && name.Name == "string" && len(value.Args) == 1 {
			return index.scalar(file, value.Args[0], depth+1)
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := index.scalar(file, value.X, depth+1)
			right, rightOK := index.scalar(file, value.Y, depth+1)
			return left + right, leftOK && rightOK
		}
	}
	return "", false
}

func (index *nonHTTPLiteralIndex) collection(file *nonHTTPLiteralFile, expr ast.Expr, depth int) ([]string, bool) {
	if file == nil || depth > 32 {
		return nil, false
	}
	if id, ok := expr.(*ast.Ident); ok && id.Obj != nil {
		if value := nonHTTPLocalBinding(id); value != nil {
			return index.collection(file, value, depth+1)
		}
		return nil, false
	}
	if binding, ok := index.collections[index.key(file, expr)]; ok {
		return index.collection(binding.file, binding.expr, depth+1)
	}
	literal, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	if _, ok := literal.Type.(*ast.ArrayType); !ok {
		return nil, false
	}
	var values []string
	for _, element := range literal.Elts {
		value, ok := index.scalar(file, element, depth+1)
		if !ok || value == "" {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func nonHTTPLocalBinding(id *ast.Ident) ast.Expr {
	switch binding := id.Obj.Decl.(type) {
	case *ast.ValueSpec:
		for n, name := range binding.Names {
			if name.Name == id.Name && n < len(binding.Values) {
				return binding.Values[n]
			}
		}
	case *ast.AssignStmt:
		for n, lhs := range binding.Lhs {
			if name, ok := lhs.(*ast.Ident); ok && name.Name == id.Name && n < len(binding.Rhs) {
				return binding.Rhs[n]
			}
		}
	}
	return nil
}

func (index *nonHTTPLiteralIndex) subjects(source string, fn *ast.FuncDecl, call *ast.CallExpr) ([]string, bool) {
	file := index.files[source]
	if text, ok := index.scalar(file, call.Args[0], 0); ok {
		return []string{text}, true
	}
	id, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return nil, false
	}
	var enclosing *ast.RangeStmt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		loop, ok := node.(*ast.RangeStmt)
		if !ok || call.Pos() < loop.Body.Pos() || call.End() > loop.Body.End() {
			return true
		}
		if value, ok := loop.Value.(*ast.Ident); ok && value.Name == id.Name {
			enclosing = loop
		}
		return true
	})
	if enclosing == nil {
		return nil, false
	}
	return index.collection(file, enclosing.X, 0)
}

func (index *nonHTTPLiteralIndex) callback(source string, fn *ast.FuncDecl, callback ast.Expr) ast.Node {
	file := index.files[source]
	key := ""
	if id, ok := callback.(*ast.Ident); ok {
		key = file.pkg + "/" + id.Name
	}
	if selector, ok := callback.(*ast.SelectorExpr); ok && fn.Recv != nil && len(fn.Recv.List) == 1 {
		receiver := fn.Recv.List[0]
		if owner, ok := selector.X.(*ast.Ident); ok {
			for _, name := range receiver.Names {
				if owner.Name == name.Name {
					key = file.pkg + "/" + strings.TrimPrefix(nonHTTPExpr(receiver.Type), "*") + "." + selector.Sel.Name
				}
			}
		}
	}
	if target := index.functions[key]; target != nil {
		return target.Body
	}
	return callback
}
