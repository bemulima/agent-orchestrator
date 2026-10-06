package contractbaseline

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
)

type GoImportClass string

const (
	GoImportStandardLibrary GoImportClass = "STANDARD_LIBRARY"
	GoImportSameModule      GoImportClass = "SAME_MODULE"
	GoImportThirdParty      GoImportClass = "THIRD_PARTY"
	GoImportCrossLayer      GoImportClass = "CROSS_LAYER"
	GoImportExternalService GoImportClass = "EXTERNAL_SERVICE"
)

// goImportsAllowed applies independent rules for the Go standard library,
// same-module architecture routes, third-party modules, and explicitly
// identified external-service clients.
func goImportsAllowed(ctx context.Context, root string, profile agentcontrol.Profile, ownerRouteID string, parsed *ast.File) error {
	modulePath, err := readGoModule(root)
	if err != nil {
		return err
	}
	stdPackages, err := goStandardPackages(ctx)
	if err != nil {
		return fmt.Errorf("classify Go standard-library imports: %w", err)
	}
	dependencies, err := goModuleDependencies(ctx, root)
	if err != nil {
		return err
	}
	for _, imported := range parsed.Imports {
		importPath := strings.Trim(imported.Path.Value, "\"")
		class, allowed := classifyGoImport(profile, ownerRouteID, modulePath, dependencies, stdPackages, importPath)
		if !allowed {
			return fmt.Errorf("%s import %q is not approved for contract owner route %q", class, importPath, ownerRouteID)
		}
	}
	return nil
}

func classifyGoImport(
	profile agentcontrol.Profile,
	ownerRouteID, modulePath string,
	dependencies, standardPackages map[string]struct{},
	importPath string,
) (GoImportClass, bool) {
	if _, ok := standardPackages[importPath]; ok {
		return GoImportStandardLibrary, profile.ContractLocation.StandardLibraryImports == "allow"
	}
	if importPath == modulePath || strings.HasPrefix(importPath, strings.TrimSuffix(modulePath, "/")+"/") {
		relative := strings.TrimPrefix(strings.TrimPrefix(importPath, modulePath), "/")
		targetRoute := routeForGoPackage(profile, relative)
		if targetRoute == "" {
			return GoImportCrossLayer, false
		}
		if targetRoute == ownerRouteID {
			return GoImportSameModule, true
		}
		for _, owner := range profile.Routes {
			if owner.ID != ownerRouteID {
				continue
			}
			for _, dependency := range owner.AllowedDependencies {
				if dependency == targetRoute {
					return GoImportSameModule, true
				}
			}
		}
		return GoImportCrossLayer, false
	}
	if matchesImportPrefix(importPath, profile.ContractLocation.ExternalServiceImports) {
		_, declared := moduleForImport(importPath, dependencies)
		return GoImportExternalService, declared
	}
	if matchesImportPrefix(importPath, profile.ContractLocation.AllowImports) {
		return GoImportThirdParty, true
	}
	_, declared := moduleForImport(importPath, dependencies)
	if profile.ContractLocation.ThirdPartyImportPolicy == "project_dependencies" && declared {
		return GoImportThirdParty, true
	}
	return GoImportThirdParty, false
}

func routeForGoPackage(profile agentcontrol.Profile, relative string) string {
	if relative == "" || relative == "." {
		return ""
	}
	bestLength := -1
	bestRoute := ""
	ambiguous := false
	for _, route := range profile.Routes {
		for _, target := range route.Targets {
			prefix := target
			if wildcard := strings.IndexAny(prefix, "*?["); wildcard >= 0 {
				prefix = strings.TrimSuffix(prefix[:wildcard], "/")
			}
			prefix = strings.TrimSuffix(prefix, "/")
			if prefix == "" || relative != prefix && !strings.HasPrefix(relative, prefix+"/") {
				continue
			}
			if len(prefix) > bestLength {
				bestLength, bestRoute, ambiguous = len(prefix), route.ID, false
			} else if len(prefix) == bestLength && bestRoute != route.ID {
				ambiguous = true
			}
		}
	}
	if ambiguous {
		return ""
	}
	return bestRoute
}

func matchesImportPrefix(importPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

func moduleForImport(importPath string, modules map[string]struct{}) (string, bool) {
	best := ""
	for module := range modules {
		if (importPath == module || strings.HasPrefix(importPath, module+"/")) && len(module) > len(best) {
			best = module
		}
	}
	return best, best != ""
}

func goStandardPackages(ctx context.Context) (map[string]struct{}, error) {
	command := exec.CommandContext(ctx, "go", "list", "std")
	command.Dir = "."
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go list std: %w", err)
	}
	packages := make(map[string]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			packages[line] = struct{}{}
		}
	}
	packages["unsafe"] = struct{}{}
	return packages, nil
}

func goModuleDependencies(ctx context.Context, root string) (map[string]struct{}, error) {
	command := exec.CommandContext(ctx, "go", "mod", "edit", "-json")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read project Go module dependencies: %w", err)
	}
	var module struct {
		Require []struct {
			Path string `json:"Path"`
		} `json:"Require"`
	}
	if err := json.Unmarshal(output, &module); err != nil {
		return nil, fmt.Errorf("decode project Go module dependencies: %w", err)
	}
	dependencies := make(map[string]struct{}, len(module.Require))
	for _, require := range module.Require {
		if require.Path != "" {
			dependencies[require.Path] = struct{}{}
		}
	}
	return dependencies, nil
}

func contractPackageName(profile agentcontrol.Profile, routeID, relative string, existed bool, base []byte) (string, error) {
	if existed {
		parsed, err := parser.ParseFile(token.NewFileSet(), relative, base, parser.AllErrors)
		if err != nil {
			return "", fmt.Errorf("source-base Go contract is invalid: %w", err)
		}
		return parsed.Name.Name, nil
	}
	surface, ok := agentcontrol.ContractSurfaceForRoute(profile, routeID)
	if !ok {
		return "", fmt.Errorf("Go contract owner route %q has no profile-defined contract surface", routeID)
	}
	if !agentcontrol.ContractPathUnderSurface(profile, routeID, relative) {
		return "", fmt.Errorf("new Go contract path %q is outside owner route %q surface", relative, routeID)
	}
	return surface.PackageName, nil
}

func modulePackageRelative(modulePath, importPath string) (string, bool) {
	if importPath == modulePath {
		return "", true
	}
	prefix := strings.TrimSuffix(modulePath, "/") + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	return path.Clean(strings.TrimPrefix(importPath, prefix)), true
}
