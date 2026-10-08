package shardexecution

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// checkIntegrationSourceBoundaries checks owned canonical layer sources, including
// their tests, without executing application code or changing the assembled tree.
func checkIntegrationSourceBoundaries(root string, workers []PreparedShard) domain.WorkspaceCheckResult {
	result := domain.WorkspaceCheckResult{Command: "canonical architecture dependencies and no reflect/unsafe construction"}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		result.ExitCode = 1
		result.Output = err.Error()
		return result
	}
	module := ""
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			module = fields[1]
			break
		}
	}
	if module == "" {
		result.ExitCode = 1
		result.Output = "module declaration unavailable"
		return result
	}
	var failures []string
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root && !strings.HasPrefix(path, filepath.Join(root, "internal")) && !strings.HasPrefix(path, filepath.Join(root, "cmd")) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic source path: %s", path)
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		route := ""
		for _, worker := range workers {
			for _, pattern := range worker.WorkPackage.WriteScope.Allow {
				if pathMatches(pattern, rel) {
					route = worker.WorkPackage.Route
				}
			}
		}
		if route == "" {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		scanned++
		for _, spec := range source.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if imp == "unsafe" || imp == "reflect" && (!strings.HasSuffix(rel, "_test.go") || !testReflectDeepEqualOnly(source, spec)) {
				failures = append(failures, rel+": prohibited construction import "+imp)
			}
			if !strings.HasPrefix(imp, module+"/internal/") {
				continue
			}
			local := strings.TrimPrefix(imp, module+"/internal/")
			// Shared fixture dependencies are approved test boundaries, never production dependencies.
			if strings.HasSuffix(rel, "_test.go") && strings.HasPrefix(local, "testsupport/") {
				continue
			}
			permitted := false
			switch route {
			case "backend.transport.http":
				permitted = strings.HasPrefix(local, "usecase/") || local == "usecase" || strings.HasPrefix(local, "transport/http/") || local == "transport/http"
			case "backend.usecase":
				permitted = local == "domain" || strings.HasPrefix(local, "domain/") || local == "usecase" || strings.HasPrefix(local, "usecase/")
			case "backend.infrastructure.persistence":
				permitted = local == "domain" || strings.HasPrefix(local, "domain/") || local == "infrastructure/persistence" || strings.HasPrefix(local, "infrastructure/persistence/")
			case "backend.composition":
				permitted = local == "domain" || strings.HasPrefix(local, "domain/") || local == "usecase" || strings.HasPrefix(local, "usecase/") || strings.HasPrefix(local, "transport/http/") || local == "transport/http" || strings.HasPrefix(local, "infrastructure/persistence/") || local == "infrastructure/persistence"
			default:
				permitted = false
			}
			if !permitted {
				failures = append(failures, rel+": forbidden layer dependency "+imp)
			}
		}
		return nil
	})
	if err != nil {
		failures = append(failures, err.Error())
	}
	if scanned == 0 {
		failures = append(failures, "no owned Go sources checked")
	}
	if len(failures) != 0 {
		result.ExitCode = 1
		result.Output = strings.Join(failures, "\n")
	} else {
		result.Output = fmt.Sprintf("PASS: %d owned Go files; HTTP -> usecase, usecase -> domain, persistence -> domain; no reflect/unsafe construction (test-only direct reflect.DeepEqual assertions allowed)", scanned)
	}
	return result
}

// A mechanical failure may be retried on the same assembled tree before a
// reviewer has started. A frozen-hash failure is never recoverable here.
func integrationVerificationCanResume(execution domain.ShardFanoutExecution) bool {
	if execution.AssemblyCommit == "" {
		return false
	}
	if execution.State == "ASSEMBLED" {
		return true
	}
	if execution.State != "INTEGRATION_VERIFICATION_FAILED" || execution.ReviewerThreadID != "" {
		return false
	}
	var previous IntegrationCheckReport
	return json.Unmarshal(execution.IntegrationVerification, &previous) == nil && !previous.Passed && previous.FrozenHashesPassed
}

// testReflectDeepEqualOnly permits assertion comparisons while preventing any
// reflection API that can manufacture values or bypass private dependencies.
func testReflectDeepEqualOnly(source *ast.File, spec *ast.ImportSpec) bool {
	alias := "reflect"
	if spec.Name != nil {
		alias = spec.Name.Name
	}
	if alias == "." || alias == "_" {
		return false
	}
	parents := map[ast.Node]ast.Node{}
	stack := []ast.Node{}
	ast.Inspect(source, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	valid := true
	calls := 0
	ast.Inspect(source, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || identifier.Name != alias || identifier == spec.Name {
			return true
		}
		parent := parents[identifier]
		// A selector's field name is not a package reference.
		if selector, ok := parent.(*ast.SelectorExpr); ok && selector.Sel == identifier {
			return true
		}
		selector, ok := parent.(*ast.SelectorExpr)
		if !ok || selector.X != identifier || selector.Sel.Name != "DeepEqual" || identifier.Obj != nil {
			valid = false
			return false
		}
		call, ok := parents[selector].(*ast.CallExpr)
		if !ok || call.Fun != selector || len(call.Args) != 2 || call.Ellipsis.IsValid() {
			valid = false
			return false
		}
		calls++
		return true
	})
	return valid && calls > 0
}
