package domain

import "time"

// Evidence is one discovery fact together with the exact repository evidence
// that supports it. Discovery consumers must not treat a value without this
// provenance as authoritative.
type Evidence struct {
	Category    string  `json:"category"`
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Confidence  float64 `json:"confidence"`
	SourcePath  string  `json:"source_path"`
	Explanation string  `json:"explanation"`
}

type InventorySummary struct {
	FilesVisited    int      `json:"files_visited"`
	FilesAnalyzed   int      `json:"files_analyzed"`
	BytesAnalyzed   int64    `json:"bytes_analyzed"`
	ContentChecksum string   `json:"content_checksum"`
	Truncated       bool     `json:"truncated"`
	ExcludedPaths   int      `json:"excluded_paths"`
	SkippedLarge    int      `json:"skipped_large_files"`
	Warnings        []string `json:"warnings,omitempty"`
}

// DiscoveredOperation is a bounded, code-observed operation inventory. It is
// deliberately distinct from service-owned architecture manifests: consumers
// may use it as discovery evidence, while a manifest remains a declaration
// that can disagree with the implementation.
//
// The five operation types match Architecture Manifest v1. SourceLine is a
// one-based implementation location, not a line in the architecture manifest.
type DiscoveredOperation struct {
	Type       ArchitectureOperationType `json:"type"`
	Protocol   string                    `json:"protocol"`
	Method     string                    `json:"method,omitempty"`
	Path       string                    `json:"path,omitempty"`
	Subject    string                    `json:"subject,omitempty"`
	Role       string                    `json:"role,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Schedule   string                    `json:"schedule,omitempty"`
	SourcePath string                    `json:"source_path"`
	SourceLine int                       `json:"source_line,omitempty"`
	Confidence float64                   `json:"confidence"`
}

// ArchitectureManifestMetadata is the safe, snapshot-persisted projection of
// an architecture YAML document. The scanner never persists YAML source or
// free-form manifest statements, which prevents a manifest from becoming a
// secret-bearing or topology-authoritative discovery fact.
type ArchitectureManifestMetadata struct {
	Path             string                    `json:"path"`
	Checksum         string                    `json:"checksum"`
	Schema           string                    `json:"schema"`
	Kind             string                    `json:"kind"`
	ID               string                    `json:"id"`
	ManifestRevision int                       `json:"manifest_revision"`
	ServiceID        string                    `json:"service_id,omitempty"`
	OperationType    ArchitectureOperationType `json:"operation_type,omitempty"`
	HTTPMethod       string                    `json:"http_method,omitempty"`
	HTTPPath         string                    `json:"http_path,omitempty"`
}

// DiscoveryReport is the immutable, read-only output of one repository scan.
type DiscoveryReport struct {
	SchemaVersion         int                            `json:"schema_version"`
	ProjectID             string                         `json:"project_id"`
	ProjectName           string                         `json:"project_name"`
	RepositoryRole        RepositoryRole                 `json:"repository_role"`
	RepositoryPath        string                         `json:"repository_path"`
	CommitSHA             string                         `json:"commit_sha"`
	Branch                string                         `json:"branch"`
	IsDirty               bool                           `json:"is_dirty"`
	ContentChecksum       string                         `json:"content_checksum"`
	StartedAt             time.Time                      `json:"started_at"`
	CompletedAt           time.Time                      `json:"completed_at"`
	Inventory             InventorySummary               `json:"inventory"`
	Facts                 []Evidence                     `json:"facts"`
	Operations            []DiscoveredOperation          `json:"operations,omitempty"`
	ArchitectureManifests []ArchitectureManifestMetadata `json:"architecture_manifests,omitempty"`
	// ArchitectureService and ArchitectureOperations retain only the validated,
	// typed manifest projection needed by the immutable architecture catalog.
	// They never contain raw YAML documents or parser diagnostics.
	ArchitectureService    *ArchitectureServiceManifest    `json:"architecture_service,omitempty"`
	ArchitectureOperations []ArchitectureOperationManifest `json:"architecture_operations,omitempty"`
	Conflicts              []Evidence                      `json:"conflicts,omitempty"`
}

// RepositorySource is a validated Git checkout ready for read-only discovery.
type RepositorySource struct {
	Name          string
	Identity      string
	LocalPath     string
	GitURL        string
	DefaultBranch string
	CurrentBranch string
	HeadCommit    string
	IsDirty       bool
}
