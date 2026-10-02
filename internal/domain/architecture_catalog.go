package domain

// ArchitectureCatalogModeCurrent identifies a catalog projected only from the
// latest immutable discovery and topology sources. It does not express a
// proposed or target architecture.
const ArchitectureCatalogModeCurrent = "CURRENT"

// ArchitectureCatalogSource joins immutable topology evidence with the
// validated architecture manifests discovered for that exact source. A zero
// ServiceManifest is intentional: the catalog must retain projects whose
// architecture manifests have not yet been supplied.
type ArchitectureCatalogSource struct {
	Topology             TopologySource                  `json:"-"`
	ServiceManifest      ArchitectureServiceManifest     `json:"service_manifest"`
	Operations           []ArchitectureOperationManifest `json:"operations"`
	DiscoveredOperations []DiscoveredOperation           `json:"discovered_operations"`
}

// ArchitectureCatalog is a read-only CURRENT projection. It is separate from
// TopologyCatalog so manifest coverage can evolve without changing topology
// semantics or persisted topology relations.
type ArchitectureCatalog struct {
	Mode        string                      `json:"mode"`
	Fingerprint string                      `json:"fingerprint"`
	Platform    ArchitectureCatalogPlatform `json:"platform"`
}

// ArchitectureCatalogPlatform is the root of the platform/service/operation
// hierarchy. Services includes every supplied topology source, including
// uncovered projects.
type ArchitectureCatalogPlatform struct {
	Services []ArchitectureCatalogService `json:"services"`
	// Relations is the deterministic, manifest-backed interaction graph for
	// CURRENT. It deliberately retains unresolved counterparts as explicit
	// external targets instead of guessing a platform service from a similar
	// name.
	Relations    []ArchitectureCatalogRelation   `json:"relations"`
	Completeness ArchitectureCatalogCompleteness `json:"completeness"`
}

// ArchitectureCatalogRelationType identifies the manifest assertion from
// which a cross-service CURRENT relation was derived. These are not inferred
// from naming conventions or runtime traffic.
type ArchitectureCatalogRelationType string

const (
	ArchitectureCatalogRelationOutboundDependency   ArchitectureCatalogRelationType = "outbound_dependency"
	ArchitectureCatalogRelationOperationInteraction ArchitectureCatalogRelationType = "operation_external_interaction"
	ArchitectureCatalogRelationContract             ArchitectureCatalogRelationType = "contract"
	ArchitectureCatalogRelationEvent                ArchitectureCatalogRelationType = "event"
)

// ArchitectureCatalogRelation is one evidence-backed edge in the CURRENT
// platform graph. SourceProjectID is always the project whose manifest made
// the claim. TargetProjectID is supplied only when a counterpart matches an
// exact catalog service identity or an exact manifest contract/event key.
// Otherwise ExternalTarget records the literal unresolved target, including
// an explicit "unknown …" counterpart for an unpaired contract or event.
// Direction describes the actual edge direction relative to SourceProjectID:
// "outbound" means source -> target and "inbound" means target -> source.
type ArchitectureCatalogRelation struct {
	ID string `json:"id"`
	// EdgeID is independent of transient topology/project database IDs. It is
	// derived from the authoritative source and target reference identities and
	// the owner-declared relationship tuple.
	EdgeID            string                          `json:"edge_id"`
	SourceReferenceID string                          `json:"source_reference_id"`
	TargetReferenceID string                          `json:"target_reference_id,omitempty"`
	Type              ArchitectureCatalogRelationType `json:"type"`
	SourceProjectID   string                          `json:"source_project_id"`
	TargetProjectID   string                          `json:"target_project_id,omitempty"`
	ExternalTarget    string                          `json:"external_target,omitempty"`
	OperationID       string                          `json:"operation_id,omitempty"`
	Transport         string                          `json:"transport"`
	Contract          string                          `json:"contract,omitempty"`
	Direction         string                          `json:"direction"`
	Description       ArchitectureStatement           `json:"description"`
	Evidence          []ArchitectureEvidence          `json:"evidence"`
	Confidence        float64                         `json:"confidence"`
}

// ArchitectureCatalogService preserves a source's CURRENT status and, where
// available, its service manifest. Covered is false only when the source has
// no service manifest; it is never inferred from Mermaid or topology facts.
type ArchitectureCatalogService struct {
	Source       ArchitectureCatalogSourceStatus    `json:"source"`
	Covered      bool                               `json:"covered"`
	Manifest     *ArchitectureServiceManifest       `json:"manifest,omitempty"`
	Groups       []ArchitectureCatalogEndpointGroup `json:"groups"`
	Ungrouped    []ArchitectureCatalogOperation     `json:"ungrouped_operations"`
	Completeness ArchitectureCatalogCompleteness    `json:"completeness"`
}

// ArchitectureCatalogSourceStatus records the immutable discovery/topology
// identity needed to assess whether a catalog entry describes CURRENT source
// material. SourceCurrent only evaluates source agreement and cleanliness; a
// caller can use ProjectStatus to apply lifecycle policy independently.
type ArchitectureCatalogSourceStatus struct {
	ProjectID string `json:"project_id"`
	// ReferenceID is the stable graph identity derived from the persisted
	// canonical source identity and owner-authored service manifest identity.
	ReferenceID            string         `json:"reference_id"`
	ProjectName            string         `json:"project_name"`
	ProjectStatus          ProjectStatus  `json:"project_status"`
	RepositoryRole         RepositoryRole `json:"repository_role"`
	SnapshotID             string         `json:"snapshot_id"`
	SnapshotStatus         string         `json:"snapshot_status"`
	CommitSHA              string         `json:"commit_sha"`
	Branch                 string         `json:"branch"`
	ContentChecksum        string         `json:"content_checksum"`
	DiscoverySchemaVersion int            `json:"discovery_schema_version"`
	SourceCurrent          bool           `json:"source_current"`
	IsDirty                bool           `json:"is_dirty"`
}

// ArchitectureCatalogEndpointGroup keeps manifest-declared group membership.
// MissingOperationIDs exposes declarations for which no operation manifest was
// supplied instead of silently dropping the evidence.
type ArchitectureCatalogEndpointGroup struct {
	ID                  string                         `json:"id"`
	Name                string                         `json:"name"`
	Description         ArchitectureStatement          `json:"description"`
	Operations          []ArchitectureCatalogOperation `json:"operations"`
	MissingOperationIDs []string                       `json:"missing_operation_ids"`
}

// ArchitectureCatalogOperation is deliberately a manifest projection, not a
// transport-specific rendering. It therefore covers HTTP, NATS, worker, and
// scheduled operation kinds uniformly.
type ArchitectureCatalogOperation struct {
	Manifest ArchitectureOperationManifest `json:"manifest"`
}

// ArchitectureCatalogCompleteness compares code-discovered operations with
// manifest declarations without guessing facts that are absent from an
// immutable source. OperationKinds always contains every supported
// ArchitectureOperationType, including zero counts, and counts discoveries
// rather than parsed manifest documents.
type ArchitectureCatalogCompleteness struct {
	SourceCount                            int                                 `json:"source_count"`
	CoveredServiceCount                    int                                 `json:"covered_service_count"`
	UncoveredServiceCount                  int                                 `json:"uncovered_service_count"`
	TotalDiscoveredOperations              int                                 `json:"total_discovered_operations"`
	HTTPOperations                         int                                 `json:"http_operations"`
	NATSRequestReplyOperations             int                                 `json:"nats_request_reply_operations"`
	EventOperations                        int                                 `json:"event_operations"`
	WorkerOperations                       int                                 `json:"worker_operations"`
	ScheduledOperations                    int                                 `json:"scheduled_operations"`
	OperationsWithManifests                int                                 `json:"operations_with_manifests"`
	OperationsMissingManifests             int                                 `json:"operations_missing_manifests"`
	OperationsBlocked                      int                                 `json:"operations_blocked"`
	DeclaredOperationManifestCount         int                                 `json:"declared_operation_manifest_count"`
	ParsedOperationManifestCount           int                                 `json:"parsed_operation_manifest_count"`
	MissingDeclaredOperationManifestCount  int                                 `json:"missing_declared_operation_manifest_count"`
	UnexpectedParsedOperationManifestCount int                                 `json:"unexpected_parsed_operation_manifest_count"`
	ManifestOperationsWithoutDiscovery     int                                 `json:"manifest_operations_without_discovery"`
	GroupOperationReferenceCount           int                                 `json:"group_operation_reference_count"`
	MissingGroupOperationCount             int                                 `json:"missing_group_operation_count"`
	OperationKinds                         []ArchitectureCatalogOperationCount `json:"operation_kinds"`
}

// ArchitectureCatalogOperationCount is one deterministic operation-kind
// counter in an ArchitectureCatalogCompleteness projection.
type ArchitectureCatalogOperationCount struct {
	Type  ArchitectureOperationType `json:"type"`
	Count int                       `json:"count"`
}
