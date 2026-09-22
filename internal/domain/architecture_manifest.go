package domain

// ArchitectureManifestSchemaV1 is the only accepted schema identifier for the
// first published architecture-manifest format. Mermaid is deliberately not
// part of this model: it is a deterministic presentation of these records.
const ArchitectureManifestSchemaV1 = "architecture/v1"

type ArchitectureOperationType string

const (
	ArchitectureOperationHTTP                ArchitectureOperationType = "http"
	ArchitectureOperationNATSRequestReply    ArchitectureOperationType = "nats_request_reply"
	ArchitectureOperationNATSEventSubscriber ArchitectureOperationType = "nats_event_subscriber"
	ArchitectureOperationWorker              ArchitectureOperationType = "worker"
	ArchitectureOperationScheduled           ArchitectureOperationType = "scheduled"
)

// ArchitectureEvidence identifies source material supporting a local
// manifest claim. A repository-relative path is required; a symbol and line
// span make the statement independently reviewable at the scanned commit.
type ArchitectureEvidence struct {
	SourcePath string `json:"source_path" yaml:"source_path"`
	Symbol     string `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	StartLine  int    `json:"start_line,omitempty" yaml:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	Checksum   string `json:"checksum,omitempty" yaml:"checksum,omitempty"`
}

// ArchitectureStatement is a human-readable assertion that remains tied to
// repository evidence. The literal value "unknown" is valid and required
// when a fact cannot be demonstrated from checked-in material.
type ArchitectureStatement struct {
	Value      string                 `json:"value" yaml:"value"`
	Confidence float64                `json:"confidence" yaml:"confidence"`
	Evidence   []ArchitectureEvidence `json:"evidence" yaml:"evidence"`
}

type ArchitectureServiceManifest struct {
	Schema               string                            `json:"schema" yaml:"schema"`
	Kind                 string                            `json:"kind" yaml:"kind"`
	ID                   string                            `json:"id" yaml:"id"`
	ManifestRevision     int                               `json:"manifest_revision" yaml:"manifest_revision"`
	Identity             ArchitectureServiceIdentity       `json:"identity" yaml:"identity"`
	Purpose              ArchitectureStatement             `json:"purpose" yaml:"purpose"`
	Responsibilities     []ArchitectureStatement           `json:"responsibilities" yaml:"responsibilities"`
	Capabilities         []ArchitectureStatement           `json:"capabilities" yaml:"capabilities"`
	OwnedResources       []ArchitectureOwnedResource       `json:"owned_resources" yaml:"owned_resources"`
	InboundInterfaces    []ArchitectureInterface           `json:"inbound_interfaces" yaml:"inbound_interfaces"`
	OutboundDependencies []ArchitectureExternalInteraction `json:"outbound_dependencies" yaml:"outbound_dependencies"`
	EndpointGroups       []ArchitectureEndpointGroup       `json:"endpoint_groups" yaml:"endpoint_groups"`
	ProducedContracts    []ArchitectureContractReference   `json:"produced_contracts" yaml:"produced_contracts"`
	ConsumedContracts    []ArchitectureContractReference   `json:"consumed_contracts" yaml:"consumed_contracts"`
	PublishedEvents      []ArchitectureContractReference   `json:"published_events" yaml:"published_events"`
	SubscribedEvents     []ArchitectureContractReference   `json:"subscribed_events" yaml:"subscribed_events"`
	BusinessRules        []ArchitectureStatement           `json:"business_rules" yaml:"business_rules"`
	OperationManifests   []string                          `json:"operation_manifests" yaml:"operation_manifests"`
	Evidence             []ArchitectureEvidence            `json:"evidence" yaml:"evidence"`
	Confidence           float64                           `json:"confidence" yaml:"confidence"`
}

type ArchitectureServiceIdentity struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"`
}

type ArchitectureOwnedResource struct {
	Type        string                `json:"type" yaml:"type"`
	Name        string                `json:"name" yaml:"name"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureInterface struct {
	Transport   string                `json:"transport" yaml:"transport"`
	Name        string                `json:"name" yaml:"name"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureEndpointGroup struct {
	ID          string                `json:"id" yaml:"id"`
	Name        string                `json:"name" yaml:"name"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
	Operations  []string              `json:"operations" yaml:"operations"`
}

type ArchitectureContractReference struct {
	Transport   string                `json:"transport" yaml:"transport"`
	Code        string                `json:"code" yaml:"code"`
	Direction   string                `json:"direction" yaml:"direction"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

// ArchitectureOperationManifest describes one executable or message-driven
// backend operation. HTTP is mandatory for platform coverage, but every
// operation type uses this same model and evidence discipline.
type ArchitectureOperationManifest struct {
	Schema               string                            `json:"schema" yaml:"schema"`
	Kind                 string                            `json:"kind" yaml:"kind"`
	ID                   string                            `json:"id" yaml:"id"`
	ManifestRevision     int                               `json:"manifest_revision" yaml:"manifest_revision"`
	ServiceID            string                            `json:"service_id" yaml:"service_id"`
	Type                 ArchitectureOperationType         `json:"type" yaml:"type"`
	Identity             ArchitectureOperationIdentity     `json:"identity" yaml:"identity"`
	Access               ArchitectureOperationAccess       `json:"access" yaml:"access"`
	Trigger              ArchitectureOperationTrigger      `json:"trigger" yaml:"trigger"`
	Input                ArchitectureOperationInput        `json:"input" yaml:"input"`
	BusinessTask         ArchitectureStatement             `json:"business_task" yaml:"business_task"`
	BusinessProcess      []ArchitectureOperationStep       `json:"business_process" yaml:"business_process"`
	BusinessRules        []ArchitectureStatement           `json:"business_rules" yaml:"business_rules"`
	Implementation       ArchitectureImplementationFlow    `json:"implementation" yaml:"implementation"`
	DataAccess           []ArchitectureDataAccess          `json:"data_access" yaml:"data_access"`
	ExternalInteractions []ArchitectureExternalInteraction `json:"external_interactions" yaml:"external_interactions"`
	SideEffects          []ArchitectureSideEffect          `json:"side_effects" yaml:"side_effects"`
	Output               ArchitectureOperationOutput       `json:"output" yaml:"output"`
	Errors               []ArchitectureOperationError      `json:"errors" yaml:"errors"`
	Evidence             []ArchitectureEvidence            `json:"evidence" yaml:"evidence"`
	Confidence           float64                           `json:"confidence" yaml:"confidence"`
}

type ArchitectureOperationIdentity struct {
	Transport string                         `json:"transport" yaml:"transport"`
	HTTP      *ArchitectureHTTPIdentity      `json:"http,omitempty" yaml:"http,omitempty"`
	NATS      *ArchitectureNATSIdentity      `json:"nats,omitempty" yaml:"nats,omitempty"`
	Worker    *ArchitectureWorkerIdentity    `json:"worker,omitempty" yaml:"worker,omitempty"`
	Scheduled *ArchitectureScheduledIdentity `json:"scheduled,omitempty" yaml:"scheduled,omitempty"`
}

type ArchitectureHTTPIdentity struct {
	Method string `json:"method" yaml:"method"`
	Path   string `json:"path" yaml:"path"`
}

type ArchitectureNATSIdentity struct {
	Subject string `json:"subject" yaml:"subject"`
	Role    string `json:"role" yaml:"role"`
	// Queue and Mode preserve optional, code-evidenced subscription metadata.
	// Role remains the canonical operation classification.
	Queue string `json:"queue,omitempty" yaml:"queue,omitempty"`
	Mode  string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type ArchitectureWorkerIdentity struct {
	Name string `json:"name" yaml:"name"`
}

type ArchitectureScheduledIdentity struct {
	Name     string `json:"name" yaml:"name"`
	Schedule string `json:"schedule" yaml:"schedule"`
}

type ArchitectureOperationTrigger struct {
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

// ArchitectureOperationAccess contains descriptions of access policy, never
// secret values or credentials. Each assertion is evidence-backed so an
// operation can distinguish an explicit policy from an unknown one.
type ArchitectureOperationAccess struct {
	Audience       ArchitectureStatement `json:"audience" yaml:"audience"`
	Authentication ArchitectureStatement `json:"authentication" yaml:"authentication"`
	Authorization  ArchitectureStatement `json:"authorization" yaml:"authorization"`
	Idempotency    ArchitectureStatement `json:"idempotency" yaml:"idempotency"`
}

type ArchitectureOperationInput struct {
	PathParams []ArchitectureInputField `json:"path_params" yaml:"path_params"`
	Query      []ArchitectureInputField `json:"query" yaml:"query"`
	Headers    []ArchitectureInputField `json:"headers" yaml:"headers"`
	Body       *ArchitectureSchemaRef   `json:"body,omitempty" yaml:"body,omitempty"`
}

type ArchitectureInputField struct {
	Name        string                `json:"name" yaml:"name"`
	Required    bool                  `json:"required" yaml:"required"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureSchemaRef struct {
	Name        string                `json:"name" yaml:"name"`
	Schema      map[string]any        `json:"schema,omitempty" yaml:"schema,omitempty"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureOperationStep struct {
	ID           string                `json:"id" yaml:"id"`
	Description  ArchitectureStatement `json:"description" yaml:"description"`
	Interactions []string              `json:"interactions" yaml:"interactions"`
}

type ArchitectureImplementationFlow struct {
	Router         []ArchitectureEvidence `json:"router" yaml:"router"`
	Handler        []ArchitectureEvidence `json:"handler" yaml:"handler"`
	UseCases       []ArchitectureEvidence `json:"use_cases" yaml:"use_cases"`
	DomainServices []ArchitectureEvidence `json:"domain_services" yaml:"domain_services"`
	Repositories   []ArchitectureEvidence `json:"repositories" yaml:"repositories"`
}

type ArchitectureDataAccess struct {
	Resource    string                `json:"resource" yaml:"resource"`
	Access      string                `json:"access" yaml:"access"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureExternalInteraction struct {
	ID          string                `json:"id" yaml:"id"`
	Transport   string                `json:"transport" yaml:"transport"`
	Target      string                `json:"target" yaml:"target"`
	Contract    string                `json:"contract,omitempty" yaml:"contract,omitempty"`
	Direction   string                `json:"direction" yaml:"direction"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureSideEffect struct {
	Type        string                `json:"type" yaml:"type"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}

type ArchitectureOperationOutput struct {
	Responses     []ArchitectureResponse          `json:"responses" yaml:"responses"`
	Result        *ArchitectureSchemaRef          `json:"result,omitempty" yaml:"result,omitempty"`
	EmittedEvents []ArchitectureContractReference `json:"emitted_events" yaml:"emitted_events"`
}

type ArchitectureResponse struct {
	StatusCode  int                    `json:"status_code" yaml:"status_code"`
	Description ArchitectureStatement  `json:"description" yaml:"description"`
	Body        *ArchitectureSchemaRef `json:"body,omitempty" yaml:"body,omitempty"`
}

type ArchitectureOperationError struct {
	Code        string                `json:"code" yaml:"code"`
	StatusCode  int                   `json:"status_code,omitempty" yaml:"status_code,omitempty"`
	Description ArchitectureStatement `json:"description" yaml:"description"`
}
