package agentcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

func ProfileFingerprint(catalog Catalog, profileID string) (string, error) {
	profile, ok := catalog.Profiles[profileID]
	if !ok {
		return "", fmt.Errorf("unknown architecture profile %q", profileID)
	}
	for _, asset := range catalog.Assets {
		if asset.Asset.ID == "profile/"+profileID && asset.Checksum != "" {
			return asset.Checksum, nil
		}
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("encode architecture profile %q: %w", profileID, err)
	}
	hash := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

var requiredProfileRoutes = map[string][]string{
	"go.canonical": {
		"backend.domain",
		"backend.usecase",
		"backend.transport.http",
		"backend.transport.message",
		"backend.infrastructure.persistence",
		"backend.infrastructure.client",
		"backend.infrastructure.messaging",
		"backend.migration",
		"backend.composition",
	},
	"nextjs.common": {
		"frontend.route",
		"frontend.module.ui",
		"frontend.module.usecase",
		"frontend.module.api",
		"frontend.module.model",
		"frontend.shared.bff",
		"frontend.shared.ui",
		"frontend.app-runtime",
		"frontend.i18n",
	},
}

func ValidateProfile(profile Profile) error {
	if profile.SchemaVersion != 1 {
		return fmt.Errorf("profile %q has unsupported schema version %d", profile.ID, profile.SchemaVersion)
	}
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Stack) == "" {
		return fmt.Errorf("profile requires id, name, and stack")
	}
	if len(profile.RepoShape.RequiredPaths) == 0 {
		return fmt.Errorf("profile %q has no required repository shape evidence", profile.ID)
	}
	for _, requiredPath := range profile.RepoShape.RequiredPaths {
		requiredPath = strings.TrimSuffix(requiredPath, "/")
		if err := validateTargetPattern(requiredPath); err != nil {
			return fmt.Errorf("profile %q required repository shape: %w", profile.ID, err)
		}
	}
	if profile.ContractLocation.Extension == "" || !strings.HasPrefix(profile.ContractLocation.Extension, ".") || strings.Contains(profile.ContractLocation.Extension, "/") {
		return fmt.Errorf("profile %q has invalid contract extension", profile.ID)
	}
	switch profile.ContractLocation.Language {
	case "go":
		if profile.Stack != "go" || profile.ContractLocation.Directory != "" || profile.ContractLocation.PackageName != "" {
			return fmt.Errorf("profile %q Go contracts must use route-owned surfaces, not a global directory or package", profile.ID)
		}
		if profile.ContractLocation.StandardLibraryImports != "allow" && profile.ContractLocation.StandardLibraryImports != "deny" {
			return fmt.Errorf("profile %q must explicitly allow or deny standard-library contract imports", profile.ID)
		}
		if profile.ContractLocation.ThirdPartyImportPolicy != "project_dependencies" && profile.ContractLocation.ThirdPartyImportPolicy != "deny" {
			return fmt.Errorf("profile %q has unsupported third-party import policy %q", profile.ID, profile.ContractLocation.ThirdPartyImportPolicy)
		}
		for _, prefix := range append(append([]string(nil), profile.ContractLocation.AllowImports...), profile.ContractLocation.ExternalServiceImports...) {
			if err := validateImportPrefix(prefix); err != nil {
				return fmt.Errorf("profile %q has invalid approved import prefix: %w", profile.ID, err)
			}
		}
	case "typescript":
		if profile.Stack != "nextjs" || profile.ContractLocation.PackageName != "" || profile.ContractLocation.Directory == "" {
			return fmt.Errorf("profile %q has invalid TypeScript contract location", profile.ID)
		}
		if err := validateTargetPattern(profile.ContractLocation.Directory); err != nil {
			return fmt.Errorf("profile %q contract location: %w", profile.ID, err)
		}
	default:
		return fmt.Errorf("profile %q has unsupported contract language %q", profile.ID, profile.ContractLocation.Language)
	}
	for _, surface := range profile.CompositionSurfaces {
		if err := validateTargetPattern(strings.TrimSuffix(surface, "/")); err != nil {
			return fmt.Errorf("profile %q composition surface: %w", profile.ID, err)
		}
	}
	required, known := requiredProfileRoutes[profile.ID]
	if !known {
		return fmt.Errorf("profile %q is not a supported canonical profile", profile.ID)
	}
	if len(profile.Routes) == 0 {
		return fmt.Errorf("profile %q has no routes", profile.ID)
	}
	routes := make(map[string]struct{}, len(profile.Routes))
	for _, route := range profile.Routes {
		if strings.TrimSpace(route.ID) == "" || strings.TrimSpace(route.Purpose) == "" {
			return fmt.Errorf("profile %q route requires id and purpose", profile.ID)
		}
		if _, duplicate := routes[route.ID]; duplicate {
			return fmt.Errorf("profile %q contains duplicate route %q", profile.ID, route.ID)
		}
		routes[route.ID] = struct{}{}
		if len(route.Targets) == 0 {
			return fmt.Errorf("profile %q route %q has no target candidates", profile.ID, route.ID)
		}
		if !route.RequireEvidence {
			return fmt.Errorf("profile %q route %q must require repository evidence", profile.ID, route.ID)
		}
		if len(route.VerificationKinds) == 0 {
			return fmt.Errorf("profile %q route %q has no verification strategy", profile.ID, route.ID)
		}
		for _, kind := range route.VerificationKinds {
			switch kind {
			case "domain-unit", "usecase-unit", "handler-test", "integration-test", "component-test", "typecheck", "build", "migration-check", "owner-review":
			default:
				return fmt.Errorf("profile %q route %q has unknown verification strategy %q", profile.ID, route.ID, kind)
			}
		}
		for _, target := range route.Targets {
			if err := validateTargetPattern(target); err != nil {
				return fmt.Errorf("profile %q route %q: %w", profile.ID, route.ID, err)
			}
		}
		if route.ContractSurface != nil {
			if profile.ContractLocation.Language != "go" {
				return fmt.Errorf("profile %q route %q declares a Go contract surface for a non-Go profile", profile.ID, route.ID)
			}
			if err := validateTargetPattern(route.ContractSurface.Directory); err != nil {
				return fmt.Errorf("profile %q route %q contract surface: %w", profile.ID, route.ID, err)
			}
			if !validProfileIdentifier(route.ContractSurface.PackageName) {
				return fmt.Errorf("profile %q route %q has an invalid contract package name", profile.ID, route.ID)
			}
			allowedValues := map[string]struct{}{}
			for _, name := range route.ContractSurface.AllowedValues {
				if !validProfileIdentifier(name) {
					return fmt.Errorf("profile %q route %q has an invalid contract value name %q", profile.ID, route.ID, name)
				}
				if _, duplicate := allowedValues[name]; duplicate {
					return fmt.Errorf("profile %q route %q repeats allowed contract value %q", profile.ID, route.ID, name)
				}
				allowedValues[name] = struct{}{}
			}
			if !surfaceMatchesRoute(route.ContractSurface.Directory, route.Targets) {
				return fmt.Errorf("profile %q route %q contract surface is outside its owned target candidates", profile.ID, route.ID)
			}
		}
		if len(route.AllowedDependencies) > 0 && profile.ContractLocation.Language != "go" {
			return fmt.Errorf("profile %q route %q declares Go dependencies for a non-Go profile", profile.ID, route.ID)
		}
		for _, candidate := range route.InterfaceCandidates {
			if err := validateTargetPattern(candidate); err != nil {
				return fmt.Errorf("profile %q route %q interface candidate: %w", profile.ID, route.ID, err)
			}
		}
		for _, boundary := range route.SharedBoundaries {
			if strings.TrimSpace(boundary) == "" {
				return fmt.Errorf("profile %q route %q has an empty shared-boundary candidate", profile.ID, route.ID)
			}
		}
	}
	for _, route := range profile.Routes {
		dependencies := map[string]struct{}{}
		for _, dependency := range route.AllowedDependencies {
			if dependency == route.ID {
				return fmt.Errorf("profile %q route %q cannot declare itself as an imported dependency", profile.ID, route.ID)
			}
			if _, ok := routes[dependency]; !ok {
				return fmt.Errorf("profile %q route %q allows unknown dependency route %q", profile.ID, route.ID, dependency)
			}
			if _, duplicate := dependencies[dependency]; duplicate {
				return fmt.Errorf("profile %q route %q repeats dependency route %q", profile.ID, route.ID, dependency)
			}
			dependencies[dependency] = struct{}{}
		}
	}

	ownershipKinds := map[string]bool{}
	for _, rule := range profile.BoundaryOwnership {
		if strings.TrimSpace(rule.Kind) == "" || ownershipKinds[rule.Kind] || len(rule.OwnerRoutes) == 0 {
			return fmt.Errorf("invalid or duplicate boundary ownership rule %q", rule.Kind)
		}
		ownershipKinds[rule.Kind] = true
		seen := map[string]bool{}
		for _, id := range rule.OwnerRoutes {
			if _, ok := routes[id]; !ok || seen[id] {
				return fmt.Errorf("invalid boundary owner route %q", id)
			}
			seen[id] = true
			if profile.ContractLocation.Language == "go" {
				if _, ok := ContractSurfaceForRoute(profile, id); !ok {
					return fmt.Errorf("boundary owner %q has no contract surface", id)
				}
			}
		}
		seen = map[string]bool{}
		for _, id := range rule.ImplementerRoutes {
			if _, ok := routes[id]; !ok || seen[id] {
				return fmt.Errorf("invalid boundary implementer %q", id)
			}
			seen[id] = true
		}
	}
	for _, routeID := range required {
		if _, ok := routes[routeID]; !ok {
			return fmt.Errorf("profile %q is missing required route %q", profile.ID, routeID)
		}
	}
	variants := make(map[string]struct{}, len(profile.Variants))
	for _, variant := range profile.Variants {
		if strings.TrimSpace(variant.ID) == "" {
			return fmt.Errorf("profile %q contains an empty variant id", profile.ID)
		}
		if _, duplicate := variants[variant.ID]; duplicate {
			return fmt.Errorf("profile %q contains duplicate variant %q", profile.ID, variant.ID)
		}
		variants[variant.ID] = struct{}{}
		for _, hint := range variant.RouteHints {
			if _, ok := routes[hint]; !ok {
				return fmt.Errorf("profile %q variant %q references unknown route %q", profile.ID, variant.ID, hint)
			}
		}
		for _, candidate := range variant.CandidateRoots {
			if err := validateTargetPattern(candidate); err != nil {
				return fmt.Errorf("profile %q variant %q: %w", profile.ID, variant.ID, err)
			}
		}
	}
	if profile.ID == "nextjs.common" {
		for _, requiredVariant := range []string{"student", "admin"} {
			if _, ok := variants[requiredVariant]; !ok {
				return fmt.Errorf("profile %q is missing required variant %q", profile.ID, requiredVariant)
			}
		}
	}
	return nil
}

func validProfileIdentifier(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for _, char := range value[1:] {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func validateTargetPattern(value string) error {
	if value == "" || path.IsAbs(value) || strings.Contains(value, "\\") {
		return fmt.Errorf("unsafe target candidate %q", value)
	}
	clean := path.Clean(value)
	if clean != value || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("unsafe target candidate %q", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("unsafe target candidate %q", value)
		}
	}
	return nil
}

func validateImportPrefix(value string) error {
	if strings.TrimSpace(value) != value || value == "" || strings.ContainsAny(value, "\\*\t\r\n ") ||
		strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "..") {
		return fmt.Errorf("unsafe import prefix %q", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("unsafe import prefix %q", value)
		}
	}
	return nil
}

func surfaceMatchesRoute(directory string, targets []string) bool {
	for _, target := range targets {
		fixed := target
		if wildcard := strings.IndexAny(fixed, "*?["); wildcard >= 0 {
			fixed = strings.TrimSuffix(fixed[:wildcard], "/")
		}
		if fixed == directory || fixed != "" && strings.HasPrefix(directory, fixed+"/") {
			return true
		}
	}
	return false
}

// ContractSurfaceForRoute resolves only explicitly declared owner surfaces.
// An absent surface is a planning/materialization error, never a cue to use a
// universal fallback directory.
func ContractSurfaceForRoute(profile Profile, routeID string) (ContractSurface, bool) {
	for _, route := range profile.Routes {
		if route.ID == routeID && route.ContractSurface != nil {
			return *route.ContractSurface, true
		}
	}
	return ContractSurface{}, false
}

// ContractPathOwnedByRoute verifies that a referenced source contract is in an
// explicitly declared owner surface or owner interface candidate. The global
// TypeScript contract directory remains supported by its profile.
func ContractPathOwnedByRoute(profile Profile, routeID, relative string) bool {
	if err := validateRelativePath(relative); err != nil {
		return false
	}
	if profile.ContractLocation.Language == "typescript" {
		return pathUnder(relative, profile.ContractLocation.Directory)
	}
	for _, route := range profile.Routes {
		if route.ID != routeID {
			continue
		}
		if route.ContractSurface != nil && pathUnder(relative, route.ContractSurface.Directory) {
			return true
		}
		for _, candidate := range route.InterfaceCandidates {
			if patternContainsPath(candidate, relative) {
				return true
			}
		}
		return false
	}
	return false
}

func ContractPathUnderSurface(profile Profile, routeID, relative string) bool {
	surface, ok := ContractSurfaceForRoute(profile, routeID)
	return ok && pathUnder(relative, surface.Directory)
}

func pathUnder(relative, directory string) bool {
	return directory != "" && (relative == directory || strings.HasPrefix(relative, strings.TrimSuffix(directory, "/")+"/"))
}

func patternContainsPath(pattern, relative string) bool {
	if pattern == relative {
		return true
	}
	wildcard := strings.IndexAny(pattern, "*?[")
	if wildcard < 0 {
		return false
	}
	prefix := strings.TrimSuffix(pattern[:wildcard], "/")
	return prefix != "" && (relative == prefix || strings.HasPrefix(relative, prefix+"/"))
}

func BoundaryOwnershipFor(profile Profile, kind string) (BoundaryOwnership, bool) {
	for _, rule := range profile.BoundaryOwnership {
		if rule.Kind == kind {
			return rule, true
		}
	}
	return BoundaryOwnership{}, false
}

// ContractDependencyAllowed preserves each profile's inward dependency direction.
func ContractDependencyAllowed(profile Profile, consumer, owner string) bool {
	if consumer == owner || profile.ContractLocation.Language != "go" {
		return true
	}
	for _, r := range profile.Routes {
		if r.ID == consumer {
			for _, id := range r.AllowedDependencies {
				if id == owner {
					return true
				}
			}
		}
	}
	return false
}
