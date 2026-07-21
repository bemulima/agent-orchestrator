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

// DiscoveryReport is the immutable, read-only output of one repository scan.
type DiscoveryReport struct {
	SchemaVersion   int              `json:"schema_version"`
	ProjectID       string           `json:"project_id"`
	ProjectName     string           `json:"project_name"`
	RepositoryRole  RepositoryRole   `json:"repository_role"`
	RepositoryPath  string           `json:"repository_path"`
	CommitSHA       string           `json:"commit_sha"`
	Branch          string           `json:"branch"`
	IsDirty         bool             `json:"is_dirty"`
	ContentChecksum string           `json:"content_checksum"`
	StartedAt       time.Time        `json:"started_at"`
	CompletedAt     time.Time        `json:"completed_at"`
	Inventory       InventorySummary `json:"inventory"`
	Facts           []Evidence       `json:"facts"`
	Conflicts       []Evidence       `json:"conflicts,omitempty"`
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
