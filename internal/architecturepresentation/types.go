// Package architecturepresentation derives read-only V2 exploration models
// from the manifest-backed CURRENT catalog. It is deliberately a presentation
// boundary: it does not persist classifications, infer business facts, or
// alter the Architecture Manifest v1 contract.
package architecturepresentation

import "github.com/bemulima/agent-orchestrator/internal/domain"

// PlatformViewMode selects the explicitly labelled platform projection.
// Unclassified services remain visible in both modes instead of being silently
// assigned a business or technical meaning.
type PlatformViewMode string

const (
	PlatformViewBusiness  PlatformViewMode = "business"
	PlatformViewTechnical PlatformViewMode = "technical"
)

// ServiceClassificationKind is intentionally conservative. A value is never
// classified as business merely because its name, capability, or dependency
// sounds business-oriented.
type ServiceClassificationKind string

const (
	ServiceClassificationBusiness     ServiceClassificationKind = "business"
	ServiceClassificationTechnical    ServiceClassificationKind = "technical"
	ServiceClassificationUnclassified ServiceClassificationKind = "unclassified"
)

// ClassificationSource says why a classification can be rendered. It makes
// deterministic rules and owner-authored metadata inspectable in the UI.
type ClassificationSource string

const (
	ClassificationSourceOwnerMetadata ClassificationSource = "owner_authored_metadata"
	ClassificationSourceManifestKind  ClassificationSource = "explicit_manifest_kind"
	ClassificationSourceNamingRule    ClassificationSource = "deterministic_naming_rule"
	ClassificationSourceUnknown       ClassificationSource = "unknown"
)

// ServiceClassification is evidence-aware presentation metadata. Evidence is
// copied from the architecture manifest when it is the available repository
// proof; deterministic rule names are also retained rather than hidden.
type ServiceClassification struct {
	Kind       ServiceClassificationKind    `json:"kind"`
	DomainID   string                       `json:"domain_id"`
	DomainName string                       `json:"domain_name"`
	Source     ClassificationSource         `json:"source"`
	Rule       string                       `json:"rule,omitempty"`
	Reason     domain.ArchitectureStatement `json:"reason"`
}

// ExplicitClassification is a versioned-owner-metadata-ready input. The
// caller supplies it from an explicit source; this package never invents it.
// ProjectID is the canonical catalog service identity.
type ExplicitClassification struct {
	Kind       ServiceClassificationKind
	DomainID   string
	DomainName string
	Reason     domain.ArchitectureStatement
}

// ClassificationRules holds only deterministic, reviewable classification
// inputs. Explicit takes precedence over any manifest or naming rule.
type ClassificationRules struct {
	Explicit map[string]ExplicitClassification
	// TechnicalManifestKinds contains exact normalized manifest identity kinds
	// that an owner has approved as infrastructure/technical classifications.
	// The default includes only names that are role declarations, not product
	// concepts or capability wording.
	TechnicalManifestKinds map[string]struct{}
	// TechnicalNameSuffixes contains exact suffixes with deterministic technical
	// meaning in this installation. It is intentionally small and only applied
	// after normalization.
	TechnicalNameSuffixes []string
}

// DefaultClassificationRules returns the intentionally conservative built-in
// deterministic rules. Consumers may narrow or extend them with reviewed
// owner-authored metadata. There are no implicit business rules.
func DefaultClassificationRules() ClassificationRules {
	return ClassificationRules{
		Explicit: map[string]ExplicitClassification{},
		TechnicalManifestKinds: map[string]struct{}{
			"gateway":        {},
			"infrastructure": {},
			"runtime":        {},
			"storage":        {},
			"validator":      {},
		},
		TechnicalNameSuffixes: []string{"-validator", "-runtime-validator"},
	}
}

// NodeType is a semantic XYFlow node type, not a styling instruction. The
// client maps it to an accessible shape and label.
type NodeType string

const (
	NodeTypeDomainCluster   NodeType = "domain_cluster"
	NodeTypeService         NodeType = "service"
	NodeTypeExternalService NodeType = "external_service"
	NodeTypeServiceHub      NodeType = "service_hub"
	NodeTypeOwnedResource   NodeType = "owned_resource"
	NodeTypeContract        NodeType = "contract"
	NodeTypeCapabilityGroup NodeType = "capability_group"
	NodeTypeTransportGroup  NodeType = "transport_group"
	NodeTypeStartEnd        NodeType = "start_end"
	NodeTypeInputOutput     NodeType = "input_output"
	NodeTypeProcess         NodeType = "process"
	NodeTypeDecision        NodeType = "decision"
	NodeTypeDatastore       NodeType = "datastore"
	NodeTypeEventQueue      NodeType = "event_queue"
	NodeTypeEvidence        NodeType = "evidence_annotation"
)

// EdgeType carries the relation semantics needed by XYFlow and Mermaid
// exports. Styling must not be the only expression of the edge's meaning.
type EdgeType string

const (
	EdgeTypeHTTPRequest      EdgeType = "http_request"
	EdgeTypeNATSRequestReply EdgeType = "nats_request_reply"
	EdgeTypeEventPublish     EdgeType = "event_publish"
	EdgeTypeEventConsume     EdgeType = "event_consume"
	EdgeTypeTechnical        EdgeType = "technical_dependency"
	EdgeTypeDataRead         EdgeType = "data_read"
	EdgeTypeDataWrite        EdgeType = "data_write"
	EdgeTypeSuccess          EdgeType = "success"
	EdgeTypeFailure          EdgeType = "failure"
	EdgeTypeContains         EdgeType = "contains"
	EdgeTypeUses             EdgeType = "uses"
)

// DiagramNode and DiagramEdge form deterministic, layout-free semantic IR.
// Coordinates, animation, and colours belong solely to the web client.
type DiagramNode struct {
	ID             string                        `json:"id"`
	Type           NodeType                      `json:"type"`
	Label          string                        `json:"label"`
	Description    domain.ArchitectureStatement  `json:"description"`
	ServiceID      string                        `json:"service_id,omitempty"`
	OperationID    string                        `json:"operation_id,omitempty"`
	Classification *ServiceClassification        `json:"classification,omitempty"`
	Evidence       []domain.ArchitectureEvidence `json:"evidence"`
}

type DiagramEdge struct {
	ID          string                        `json:"id"`
	Type        EdgeType                      `json:"type"`
	Source      string                        `json:"source"`
	Target      string                        `json:"target"`
	Label       string                        `json:"label,omitempty"`
	Description domain.ArchitectureStatement  `json:"description"`
	Evidence    []domain.ArchitectureEvidence `json:"evidence"`
}

type Diagram struct {
	Nodes []DiagramNode `json:"nodes"`
	Edges []DiagramEdge `json:"edges"`
}

type ServiceSummary struct {
	ProjectID      string                `json:"project_id"`
	ProjectName    string                `json:"project_name"`
	ServiceID      string                `json:"service_id,omitempty"`
	ServiceName    string                `json:"service_name"`
	Covered        bool                  `json:"covered"`
	Classification ServiceClassification `json:"classification"`
}

type PlatformDomainGroup struct {
	ID       string                        `json:"id"`
	Name     string                        `json:"name"`
	Kind     ServiceClassificationKind     `json:"kind"`
	Services []ServiceSummary              `json:"services"`
	Evidence []domain.ArchitectureEvidence `json:"evidence"`
}

// PlatformPresentation is the V2, no-operation-node platform projection.
type PlatformPresentation struct {
	Mode               string                `json:"mode"`
	CatalogFingerprint string                `json:"catalog_fingerprint"`
	View               PlatformViewMode      `json:"view"`
	Domains            []PlatformDomainGroup `json:"domains"`
	Graph              Diagram               `json:"graph"`
}

type OperationGrouping string

const (
	OperationGroupingCapability OperationGrouping = "capability"
	OperationGroupingTransport  OperationGrouping = "transport"
)

type OperationSummary struct {
	ID           string                           `json:"id"`
	Type         domain.ArchitectureOperationType `json:"type"`
	Identity     string                           `json:"identity"`
	BusinessTask domain.ArchitectureStatement     `json:"business_task"`
	Confidence   float64                          `json:"confidence"`
}

type OperationGroup struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Grouping    OperationGrouping            `json:"grouping"`
	Transport   string                       `json:"transport,omitempty"`
	Description domain.ArchitectureStatement `json:"description"`
	Operations  []OperationSummary           `json:"operations"`
}

type ServicePresentation struct {
	Mode                 string                                   `json:"mode"`
	CatalogFingerprint   string                                   `json:"catalog_fingerprint"`
	Service              ServiceSummary                           `json:"service"`
	Purpose              domain.ArchitectureStatement             `json:"purpose"`
	Responsibilities     []domain.ArchitectureStatement           `json:"responsibilities"`
	Capabilities         []domain.ArchitectureStatement           `json:"capabilities"`
	OwnedResources       []domain.ArchitectureOwnedResource       `json:"owned_resources"`
	InboundInterfaces    []domain.ArchitectureInterface           `json:"inbound_interfaces"`
	OutboundDependencies []domain.ArchitectureExternalInteraction `json:"outbound_dependencies"`
	ProducedContracts    []domain.ArchitectureContractReference   `json:"produced_contracts"`
	ConsumedContracts    []domain.ArchitectureContractReference   `json:"consumed_contracts"`
	PublishedEvents      []domain.ArchitectureContractReference   `json:"published_events"`
	SubscribedEvents     []domain.ArchitectureContractReference   `json:"subscribed_events"`
	CapabilityGroups     []OperationGroup                         `json:"capability_groups"`
	TransportGroups      []OperationGroup                         `json:"transport_groups"`
	Graph                Diagram                                  `json:"graph"`
}

// BusinessExplanation contains only direct manifest claims. In particular,
// important decisions are unknown until an Architecture Manifest adds a
// decision-specific, evidence-backed declaration.
type BusinessExplanation struct {
	BusinessTask   domain.ArchitectureStatement `json:"business_task"`
	WhenWhyInvoked domain.ArchitectureStatement `json:"when_why_invoked"`
	// Validation is explicit even when the v1 manifest cannot prove validation
	// behavior. Consumers must render its unknown value rather than implying a
	// validation step from input schema shape alone.
	Validation         domain.ArchitectureStatement   `json:"validation"`
	MainRules          []domain.ArchitectureStatement `json:"main_rules"`
	ImportantDecisions []domain.ArchitectureStatement `json:"important_decisions"`
	SideEffects        []domain.ArchitectureStatement `json:"side_effects"`
	Result             domain.ArchitectureStatement   `json:"result"`
	FailureMeaning     []domain.ArchitectureStatement `json:"failure_meaning"`
}

type OperationPresentation struct {
	Mode                string                               `json:"mode"`
	CatalogFingerprint  string                               `json:"catalog_fingerprint"`
	Service             ServiceSummary                       `json:"service"`
	Operation           domain.ArchitectureOperationManifest `json:"operation"`
	BusinessExplanation BusinessExplanation                  `json:"business_explanation"`
	Flow                Diagram                              `json:"flow"`
	Sequence            Diagram                              `json:"sequence"`
	Contracts           Diagram                              `json:"contracts"`
	Evidence            []domain.ArchitectureEvidence        `json:"evidence"`
}
