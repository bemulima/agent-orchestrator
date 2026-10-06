// Package agentcontrol owns the canonical managed-agent asset catalog and its
// repository distribution proposal model. It deliberately does not own task
// planning, agent execution, or Temporal coordination.
package agentcontrol

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const ManifestSchemaVersion = 1

type Ownership string

const (
	OwnershipManaged   Ownership = "MANAGED"
	OwnershipLocal     Ownership = "LOCAL"
	OwnershipGenerated Ownership = "GENERATED"
)

type AssetKind string

const (
	AssetKindGlobalPolicy AssetKind = "global_policy"
	AssetKindSkill        AssetKind = "skill"
	AssetKindProfile      AssetKind = "architecture_profile"
)

type Manifest struct {
	SchemaVersion int     `yaml:"schema_version" json:"schema_version"`
	Assets        []Asset `yaml:"assets" json:"assets"`
}

// Asset describes one canonical source and its managed target. A checksum of
// "computed" asks the loader to derive the digest from source bytes; a pinned
// sha256 value can also be used by future manifest versions.
type Asset struct {
	ID         string    `yaml:"id" json:"id"`
	Kind       AssetKind `yaml:"kind" json:"kind"`
	SourcePath string    `yaml:"source_path" json:"source_path"`
	Ownership  Ownership `yaml:"ownership" json:"ownership"`
	Version    string    `yaml:"version" json:"version"`
	TargetPath string    `yaml:"target_path" json:"target_path"`
	Profiles   []string  `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Checksum   string    `yaml:"checksum" json:"checksum"`
}

type ResolvedAsset struct {
	Asset    Asset             `json:"asset"`
	Files    map[string][]byte `json:"-"`
	Checksum string            `json:"checksum"`
}

type Catalog struct {
	Manifest Manifest           `json:"manifest"`
	Assets   []ResolvedAsset    `json:"assets"`
	Profiles map[string]Profile `json:"profiles"`
	Digest   string             `json:"digest"`
}

// BoundaryOwnership explicitly permits contract-only owners independently of implementation routing.
type BoundaryOwnership struct {
	Kind              string   `yaml:"kind" json:"kind"`
	OwnerRoutes       []string `yaml:"owner_routes" json:"owner_routes"`
	ImplementerRoutes []string `yaml:"implementer_routes,omitempty" json:"implementer_routes,omitempty"`
}

type Profile struct {
	SchemaVersion       int                 `yaml:"schema_version" json:"schema_version"`
	ID                  string              `yaml:"id" json:"id"`
	Name                string              `yaml:"name" json:"name"`
	Stack               string              `yaml:"stack" json:"stack"`
	RepoShape           RepositoryShape     `yaml:"repo_shape" json:"repo_shape"`
	ContractLocation    ContractLocation    `yaml:"contract_location" json:"contract_location"`
	CompositionSurfaces []string            `yaml:"composition_surfaces" json:"composition_surfaces"`
	Variants            []ProfileVariant    `yaml:"variants,omitempty" json:"variants,omitempty"`
	BoundaryOwnership   []BoundaryOwnership `yaml:"boundary_ownership,omitempty" json:"boundary_ownership,omitempty"`
	Routes              []ProfileRoute      `yaml:"routes" json:"routes"`
}

// RepositoryShape contains the required structural evidence for resolving a
// profile. A language manifest alone is never enough to claim an architecture.
type RepositoryShape struct {
	RequiredPaths []string `yaml:"required" json:"required"`
}

// ContractLocation describes the profile-owned location and syntax for
// deterministic, behavior-free contract skeletons.
type ContractLocation struct {
	Directory              string   `yaml:"directory,omitempty" json:"directory,omitempty"`
	Language               string   `yaml:"language" json:"language"`
	PackageName            string   `yaml:"package_name,omitempty" json:"package_name,omitempty"`
	Extension              string   `yaml:"extension" json:"extension"`
	AllowImports           []string `yaml:"allow_imports,omitempty" json:"allow_imports,omitempty"`
	StandardLibraryImports string   `yaml:"standard_library_imports,omitempty" json:"standard_library_imports,omitempty"`
	ThirdPartyImportPolicy string   `yaml:"third_party_import_policy,omitempty" json:"third_party_import_policy,omitempty"`
	ExternalServiceImports []string `yaml:"external_service_imports,omitempty" json:"external_service_imports,omitempty"`
}

// ContractSurface binds a boundary to a package inside its owning route.
// Go profiles use this instead of inventing a repository-wide contracts layer.
type ContractSurface struct {
	Directory     string   `yaml:"directory" json:"directory"`
	PackageName   string   `yaml:"package_name" json:"package_name"`
	AllowedValues []string `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
}

type ProfileVariant struct {
	ID             string   `yaml:"id" json:"id"`
	RouteHints     []string `yaml:"route_hints,omitempty" json:"route_hints,omitempty"`
	CandidateRoots []string `yaml:"candidate_roots,omitempty" json:"candidate_roots,omitempty"`
}

type ProfileRoute struct {
	ID                  string           `yaml:"id" json:"id"`
	Purpose             string           `yaml:"purpose" json:"purpose"`
	Targets             []string         `yaml:"target_candidates" json:"target_candidates"`
	AllowedDependencies []string         `yaml:"allowed_dependencies,omitempty" json:"allowed_dependencies,omitempty"`
	ContractSurface     *ContractSurface `yaml:"contract_surface,omitempty" json:"contract_surface,omitempty"`
	Contracts           []string         `yaml:"contracts_to_inspect,omitempty" json:"contracts_to_inspect,omitempty"`
	SharedBoundaries    []string         `yaml:"shared_boundary_candidates,omitempty" json:"shared_boundary_candidates,omitempty"`
	InterfaceCandidates []string         `yaml:"interface_candidates,omitempty" json:"interface_candidates,omitempty"`
	RequireEvidence     bool             `yaml:"require_evidence" json:"require_evidence"`
	SharedHotspot       bool             `yaml:"shared_hotspot,omitempty" json:"shared_hotspot,omitempty"`
	VerificationKinds   []string         `yaml:"verification_kinds,omitempty" json:"verification_kinds,omitempty"`
}

type BlockerCode string

const (
	BlockerContractChangeRequired   BlockerCode = "CONTRACT_CHANGE_REQUIRED"
	BlockerOutOfScopeChangeRequired BlockerCode = "OUT_OF_SCOPE_CHANGE_REQUIRED"
	BlockerDependencyNotReady       BlockerCode = "DEPENDENCY_NOT_READY"
	BlockerArchitectureConflict     BlockerCode = "ARCHITECTURE_CONFLICT"
	BlockerTestBoundaryMissing      BlockerCode = "TEST_BOUNDARY_MISSING"
	BlockerBaselineStale            BlockerCode = "BASELINE_STALE"
)

type ContractBlocker struct {
	Code     BlockerCode `json:"code"`
	Boundary string      `json:"boundary,omitempty"`
	Evidence string      `json:"evidence"`
}

func ValidateContractBlocker(blocker ContractBlocker) error {
	switch blocker.Code {
	case BlockerContractChangeRequired, BlockerOutOfScopeChangeRequired,
		BlockerDependencyNotReady, BlockerArchitectureConflict,
		BlockerTestBoundaryMissing, BlockerBaselineStale:
	default:
		return fmt.Errorf("unknown contract blocker code %q", blocker.Code)
	}
	if strings.TrimSpace(blocker.Evidence) == "" {
		return errors.New("contract blocker requires evidence")
	}
	return nil
}

// ClassifyOwnership returns the ownership boundary for a repository path.
// Only paths listed by managed provenance are centrally managed; all unlisted
// repository skills remain local.
func ClassifyOwnership(repositoryPath string, provenance DistributionManifest) Ownership {
	clean := path.Clean(strings.TrimSpace(repositoryPath))
	if clean == ".agents/manifest.yaml" {
		return OwnershipGenerated
	}
	if strings.HasPrefix(clean, ".ai/architecture/") && strings.HasSuffix(clean, ".mmd") {
		return OwnershipGenerated
	}
	for _, asset := range provenance.Assets {
		if asset.Ownership != OwnershipManaged {
			continue
		}
		if clean == asset.TargetPath || strings.HasPrefix(clean, asset.TargetPath+"/") {
			return OwnershipManaged
		}
	}
	return OwnershipLocal
}

func validateRelativePath(value string) error {
	if value == "" || !fs.ValidPath(value) || path.Clean(value) != value || strings.HasPrefix(value, "/") {
		return fmt.Errorf("unsafe relative path %q", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("unsafe relative path %q", value)
		}
	}
	return nil
}

func ValidateRelativePath(value string) error {
	return validateRelativePath(value)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
