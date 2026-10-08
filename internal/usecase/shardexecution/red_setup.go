package shardexecution

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ValidateREDSetup permits a callable unimplemented persistence shell or trivial
// constructor allocation for an HTTP/usecase implementation object.
// The caller must independently verify the frozen baseline and complete changed-file list.
func ValidateREDSetup(root string, wp domain.WorkPackage, changedFiles []string, priorSources ...map[string]string) error {
	fail := func() error {
		return fmt.Errorf("RED_SETUP contains behavior, unsupported scaffolding, or a changed frozen boundary")
	}
	if wp.Route == "backend.transport.http" || wp.Route == "backend.usecase" {
		if len(priorSources) > 0 && len(priorSources[0]) > 0 {
			return validateConstructorSetupDelta(root, wp, changedFiles, priorSources[0])
		}
		return validateConstructorREDSetup(root, wp, changedFiles)
	}
	if wp.Route != "backend.infrastructure.persistence" || !scopeAllowsFiles(wp, changedFiles) {
		return fail()
	}
	frozen := "internal/domain/repository_port.go"
	port, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, frozen), nil, 0)
	if err != nil {
		return fmt.Errorf("RED_SETUP frozen port: %w", err)
	}
	methods := map[string]string{}
	for _, decl := range port.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok && typ.Name.Name == "RepositoryPort" {
					iface, ok := typ.Type.(*ast.InterfaceType)
					if !ok {
						return fail()
					}
					for _, field := range iface.Methods.List {
						fn, ok := field.Type.(*ast.FuncType)
						if !ok || len(field.Names) != 1 {
							return fail()
						}
						methods[field.Names[0].Name] = setupSignature(fn, true)
					}
				}
			}
		}
	}
	if len(methods) == 0 {
		return fail()
	}
	structs := map[string]string{}
	constructors := map[string]bool{}
	implemented := map[string]map[string]bool{}
	sentinel := false
	for _, name := range changedFiles {
		if name != filepath.ToSlash(filepath.Clean(name)) || filepath.IsAbs(name) || strings.HasPrefix(name, "../") || name == frozen || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return fail()
		}
		full := filepath.Join(root, name)
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fail()
		}
		rel, err := filepath.Rel(canonical, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fail()
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			value, _ := strconv.Unquote(imp.Path.Value)
			if imp.Name != nil || !(value == "context" || value == "errors" || value == "github.com/jackc/pgx/v5/pgxpool" || strings.HasSuffix(value, "/internal/domain")) {
				return fail()
			}
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				if len(d.Specs) != 1 {
					return fail()
				}
				switch s := d.Specs[0].(type) {
				case *ast.TypeSpec:
					st, ok := s.Type.(*ast.StructType)
					if !ok || s.Assign.IsValid() || s.TypeParams != nil || len(st.Fields.List) != 1 {
						return fail()
					}
					field := st.Fields.List[0]
					if len(field.Names) != 1 || field.Tag != nil || setupText(field.Type) != "*pgxpool.Pool" {
						return fail()
					}
					if _, exists := structs[s.Name.Name]; exists {
						return fail()
					}
					structs[s.Name.Name] = field.Names[0].Name
				case *ast.ValueSpec:
					if d.Tok != token.VAR || sentinel || s.Type != nil || len(s.Names) != 1 || s.Names[0].Name != "ErrNotImplemented" || len(s.Values) != 1 || setupText(s.Values[0]) != `errors.New("not implemented")` {
						return fail()
					}
					sentinel = true
				default:
					return fail()
				}
			case *ast.FuncDecl:
				if d.Body == nil || len(d.Body.List) != 1 || d.Type.TypeParams != nil {
					return fail()
				}
				ret, ok := d.Body.List[0].(*ast.ReturnStmt)
				if !ok {
					return fail()
				}
				if d.Recv != nil {
					if len(d.Recv.List) != 1 {
						return fail()
					}
					receiver := strings.TrimPrefix(setupText(d.Recv.List[0].Type), "*")
					if receiver == setupText(d.Recv.List[0].Type) {
						return fail()
					}
					sig, exists := methods[d.Name.Name]
					if !exists || sig != setupSignature(d.Type, false) || len(ret.Results) != 2 || setupText(ret.Results[0]) != "nil" || setupText(ret.Results[1]) != "ErrNotImplemented" {
						return fail()
					}
					if implemented[receiver] == nil {
						implemented[receiver] = map[string]bool{}
					}
					if implemented[receiver][d.Name.Name] {
						return fail()
					}
					implemented[receiver][d.Name.Name] = true
				} else {
					if len(d.Type.Params.List) != 1 || len(d.Type.Params.List[0].Names) != 1 || setupText(d.Type.Params.List[0].Type) != "*pgxpool.Pool" || d.Type.Results == nil || len(d.Type.Results.List) != 1 || len(d.Type.Results.List[0].Names) != 0 || len(ret.Results) != 1 {
						return fail()
					}
					target := strings.TrimPrefix(setupText(d.Type.Results.List[0].Type), "*")
					if setupText(d.Type.Results.List[0].Type) != "*"+target || d.Name.Name != "New"+target || constructors[target] {
						return fail()
					}
					expected := "&" + target + "{"
					expr := setupText(ret.Results[0])
					if !strings.HasPrefix(expr, expected) {
						return fail()
					}
					unary, ok := ret.Results[0].(*ast.UnaryExpr)
					if !ok || unary.Op != token.AND {
						return fail()
					}
					literal, ok := unary.X.(*ast.CompositeLit)
					if !ok || setupText(literal.Type) != target || len(literal.Elts) != 1 {
						return fail()
					}
					kv, ok := literal.Elts[0].(*ast.KeyValueExpr)
					if !ok || setupText(kv.Value) != d.Type.Params.List[0].Names[0].Name {
						return fail()
					}
					constructors[target] = true
					constructors[target+":"+setupText(kv.Key)] = true
				}
			default:
				return fail()
			}
		}
	}
	if !sentinel || len(structs) != 1 || len(constructors) != 2 {
		return fail()
	}
	for name, field := range structs {
		if !constructors[name] || !constructors[name+":"+field] || len(implemented[name]) != len(methods) {
			return fail()
		}
	}
	if len(implemented) != 1 {
		return fail()
	}
	return nil
}

func setupText(node ast.Node) string {
	var out bytes.Buffer
	_ = printer.Fprint(&out, token.NewFileSet(), node)
	return out.String()
}

// Parameter names are immaterial; unqualified frozen domain types are qualified
// while standard/predeclared and already-qualified types retain their identity.
func setupSignature(fn *ast.FuncType, frozen bool) string {
	list := func(fields *ast.FieldList) string {
		if fields == nil {
			return ""
		}
		var result []string
		for _, field := range fields.List {
			value := setupText(field.Type)
			if frozen {
				value = setupFrozenType(field.Type)
			}
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			for i := 0; i < count; i++ {
				result = append(result, value)
			}
		}
		return strings.Join(result, ",")
	}
	return list(fn.Params) + "->" + list(fn.Results)
}

func setupFrozenType(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		switch e.Name {
		case "error", "string", "bool", "byte", "rune", "any", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "complex64", "complex128":
			return e.Name
		}
		return "domain." + e.Name
	case *ast.SelectorExpr:
		return setupText(e)
	case *ast.StarExpr:
		return "*" + setupFrozenType(e.X)
	case *ast.ArrayType:
		length := ""
		if e.Len != nil {
			length = setupText(e.Len)
		}
		return "[" + length + "]" + setupFrozenType(e.Elt)
	case *ast.MapType:
		return "map[" + setupFrozenType(e.Key) + "]" + setupFrozenType(e.Value)
	default:
		return "unsupported frozen type"
	}
}

// Constructor setup never supplies business methods. The ensuing test must still
// exercise the absent production behavior through the newly constructible object.
func validateConstructorREDSetup(root string, wp domain.WorkPackage, changedFiles []string) error {
	fail := func() error {
		return fmt.Errorf("constructor RED_SETUP permits dependency storage and trivial allocation only")
	}
	if len(changedFiles) != 1 || !scopeAllowsFiles(wp, changedFiles) {
		return fail()
	}
	dependency := "domain.RepositoryPort"
	importSuffix := "/internal/domain"
	target := "AvailabilityUsecase"
	if wp.Route == "backend.transport.http" {
		dependency = "usecase.ApplicationCommandResult"
		importSuffix = "/internal/usecase"
		target = "AvailabilityHandler"
	}
	name := changedFiles[0]
	if name != filepath.ToSlash(filepath.Clean(name)) || filepath.IsAbs(name) || strings.HasPrefix(name, "../") || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return fail()
	}
	for _, frozen := range wp.Contracts.ReadOnlyPaths {
		if name == frozen {
			return fail()
		}
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fail()
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return fail()
	}
	rel, err := filepath.Rel(canonical, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fail()
	}
	file, err := parser.ParseFile(token.NewFileSet(), full, nil, 0)
	if err != nil {
		return fail()
	}
	if len(file.Imports) != 1 {
		return fail()
	}
	imp := file.Imports[0]
	value, err := strconv.Unquote(imp.Path.Value)
	if err != nil || imp.Name != nil || !strings.HasSuffix(value, importSuffix) {
		return fail()
	}
	fieldName := ""
	var constructor *ast.FuncDecl
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			if d.Tok != token.TYPE || len(d.Specs) != 1 || fieldName != "" {
				return fail()
			}
			typ, ok := d.Specs[0].(*ast.TypeSpec)
			if !ok || typ.Name.Name != target || typ.Assign.IsValid() || typ.TypeParams != nil {
				return fail()
			}
			st, ok := typ.Type.(*ast.StructType)
			if !ok || len(st.Fields.List) != 1 {
				return fail()
			}
			field := st.Fields.List[0]
			if len(field.Names) != 1 || field.Tag != nil || setupText(field.Type) != dependency {
				return fail()
			}
			fieldName = field.Names[0].Name
		case *ast.FuncDecl:
			if constructor != nil || d.Recv != nil || d.Name.Name != "New"+target || d.Type.TypeParams != nil {
				return fail()
			}
			constructor = d
		default:
			return fail()
		}
	}
	if fieldName == "" || constructor == nil {
		return fail()
	}
	fn := constructor
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 || setupText(fn.Type.Params.List[0].Type) != dependency || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 || len(fn.Type.Results.List[0].Names) != 0 || setupText(fn.Type.Results.List[0].Type) != "*"+target || fn.Body == nil || len(fn.Body.List) != 1 {
		return fail()
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return fail()
	}
	unary, ok := ret.Results[0].(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return fail()
	}
	literal, ok := unary.X.(*ast.CompositeLit)
	if !ok || setupText(literal.Type) != target || len(literal.Elts) != 1 {
		return fail()
	}
	kv, ok := literal.Elts[0].(*ast.KeyValueExpr)
	if !ok || setupText(kv.Key) != fieldName || setupText(kv.Value) != fn.Type.Params.List[0].Names[0].Name {
		return fail()
	}
	return nil
}

// A previously VERIFIED production preimage may be reproduced by the orchestrator
// while HEAD stays frozen. Constructor setup must preserve every original AST node.
func validateConstructorSetupDelta(root string, wp domain.WorkPackage, paths []string, prior map[string]string) error {
	fail := func() error { return fmt.Errorf("constructor RED_SETUP altered the verified production preimage") }
	if wp.Route != "backend.transport.http" || len(paths) != 1 || len(prior) != 1 || !scopeAllowsFiles(wp, paths) {
		return fail()
	}
	path := paths[0]
	before, ok := prior[path]
	if !ok {
		return fail()
	}
	old, err := parser.ParseFile(token.NewFileSet(), path, before, 0)
	if err != nil {
		return fail()
	}
	current, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, path), nil, 0)
	if err != nil {
		return fail()
	}
	var constructor *ast.FuncDecl
	var typ *ast.GenDecl
	var dependencyImport *ast.ImportSpec
	preserved := []ast.Decl{}
	for _, decl := range current.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "NewAvailabilityHandler" && fn.Recv == nil {
			if constructor != nil {
				return fail()
			}
			constructor = fn
			continue
		}
		preserved = append(preserved, decl)
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "AvailabilityHandler" {
					typ = gen
				}
				if imp, ok := spec.(*ast.ImportSpec); ok {
					value, _ := strconv.Unquote(imp.Path.Value)
					if strings.HasSuffix(value, "/internal/usecase") {
						dependencyImport = imp
					}
				}
			}
		}
	}
	if constructor == nil || typ == nil || dependencyImport == nil {
		return fail()
	}
	current.Decls = preserved
	if setupText(current) != setupText(old) {
		return fail()
	}
	// Reuse the same allocation-only validator on a synthetic dependency/type/ctor
	// projection; this projection is disposable verification input, never workspace code.
	synthetic := &ast.File{Name: current.Name, Decls: []ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{dependencyImport}}, typ, constructor}}
	temp, err := os.MkdirTemp("", "constructor-red-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	filename := filepath.Join(temp, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filename, []byte(setupText(synthetic)), 0600); err != nil {
		return err
	}
	return validateConstructorREDSetup(temp, wp, paths)
}
