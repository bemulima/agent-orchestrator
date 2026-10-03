package domain

const ArchitectureGraphSchemaV1 = "architecture-graph.v1"

// ArchitectureGraph is a portable projection of the supplied CURRENT sources.
// Its scope is the captured topology, never an inferred complete fleet.
type ArchitectureGraph struct {
	ExcludedInterfaceMetadata int                             `json:"excluded_interface_metadata,omitempty"`
	OperationSemanticDebt     int                             `json:"operation_semantic_debt,omitempty"`
	SchemaVersion             string                          `json:"schema_version"`
	Mode                      string                          `json:"mode"`
	Producer                  ArchitectureGraphProducer       `json:"producer"`
	References                []ArchitectureGraphReference    `json:"references"`
	Edges                     []ArchitectureGraphEdge         `json:"edges"`
	Completeness              ArchitectureCatalogCompleteness `json:"completeness"`
	Diagnostics               []ArchitectureGraphDiagnostic   `json:"diagnostics"`
	FleetInputs               *ArchitectureGraphFleetInputs   `json:"fleet_inputs,omitempty"`
	Inventory                 *ArchitectureGraphInventory     `json:"inventory,omitempty"`
	ContentSHA256             string                          `json:"content_sha256,omitempty"`
}
type ArchitectureGraphProducer struct {
	RepositoryID string `json:"repository_id"`
	CommitSHA    string `json:"commit_sha"`
}
type ArchitectureGraphPin struct {
	SourceIdentity string `json:"source_identity"`
	CommitSHA      string `json:"commit_sha"`
	Path           string `json:"path"`
	BlobOID        string `json:"blob_oid"`
	ContentSHA256  string `json:"content_sha256"`
}
type ArchitectureGraphReference struct {
	Classification  string                 `json:"classification,omitempty"`
	ReferenceKind   string                 `json:"reference_kind,omitempty"`
	ReferenceID     string                 `json:"reference_id"`
	SourceIdentity  string                 `json:"source_identity"`
	ManifestID      string                 `json:"manifest_id"`
	RepositoryRole  RepositoryRole         `json:"repository_role"`
	CommitSHA       string                 `json:"commit_sha"`
	Covered         bool                   `json:"covered"`
	SourceCurrent   bool                   `json:"source_current"`
	IsDirty         bool                   `json:"is_dirty"`
	DeclarationPins []ArchitectureGraphPin `json:"declaration_pins"`
}
type ArchitectureGraphEdge struct {
	EdgeID            string                          `json:"edge_id"`
	Relation          ArchitectureCatalogRelationType `json:"relation"`
	SourceReferenceID string                          `json:"source_reference_id"`
	TargetReferenceID string                          `json:"target_reference_id,omitempty"`
	ExternalTarget    string                          `json:"external_target,omitempty"`
	OperationID       string                          `json:"operation_id"`
	Transport         string                          `json:"transport"`
	Contract          string                          `json:"contract"`
	Direction         string                          `json:"direction"`
	DeclarationPins   []ArchitectureGraphPin          `json:"declaration_pins"`
}
type ArchitectureGraphDiagnostic struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	ReferenceID string `json:"reference_id,omitempty"`
	EdgeID      string `json:"edge_id,omitempty"`
	Path        string `json:"path,omitempty"`
	Message     string `json:"message"`
}

// ArchitectureGraphInventory is the verified CDO-owned scope declaration.
type ArchitectureGraphInventory struct {
	Pin              ArchitectureGraphPin `json:"pin"`
	SourceIdentities []string             `json:"source_identities"`
}
