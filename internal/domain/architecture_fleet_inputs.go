package domain

const ArchitectureFleetInputsSchemaV1 = "architecture-fleet-inputs.v1"

type ArchitectureFleetInputs struct {
	SchemaVersion string                        `json:"schema_version"`
	Repositories  []ArchitectureFleetRepository `json:"repositories"`
}
type ArchitectureFleetRepository struct {
	RepositoryID   string                         `json:"repository_id"`
	SourceIdentity string                         `json:"source_identity"`
	RemoteURL      string                         `json:"remote_url"`
	CommitSHA      string                         `json:"commit_sha"`
	Profile        string                         `json:"profile"`
	ServiceID      string                         `json:"service_id"`
	RepositoryRole RepositoryRole                 `json:"repository_role"`
	Declarations   []ArchitectureFleetDeclaration `json:"declarations"`
}
type ArchitectureFleetDeclaration struct {
	Path          string `json:"path"`
	BlobOID       string `json:"blob_oid"`
	ContentSHA256 string `json:"content_sha256"`
}
type ArchitectureGraphFleetInputs struct {
	SchemaVersion    string   `json:"schema_version"`
	ContentSHA256    string   `json:"content_sha256"`
	SourceIdentities []string `json:"source_identities"`
}
