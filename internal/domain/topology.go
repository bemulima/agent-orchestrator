package domain

import (
	"encoding/json"
	"time"
)

const (
	ContractDirectionProvides   = "provides"
	ContractDirectionConsumes   = "consumes"
	ContractDirectionPublishes  = "publishes"
	ContractDirectionSubscribes = "subscribes"
	ContractDirectionOwns       = "owns"
)

type DriftSeverity string

const (
	DriftSeverityInfo     DriftSeverity = "info"
	DriftSeverityWarning  DriftSeverity = "warning"
	DriftSeverityError    DriftSeverity = "error"
	DriftSeverityCritical DriftSeverity = "critical"
)

type TopologySource struct {
	Project  Project
	Snapshot ServiceSnapshot
	Report   DiscoveryReport
}

type TopologyRevision struct {
	ID              string    `json:"id"`
	Fingerprint     string    `json:"fingerprint"`
	ProjectCount    int       `json:"project_count"`
	ServiceCount    int       `json:"service_count"`
	CapabilityCount int       `json:"capability_count"`
	OwnershipCount  int       `json:"ownership_count"`
	ContractCount   int       `json:"contract_count"`
	RelationCount   int       `json:"relation_count"`
	DriftCount      int       `json:"drift_count"`
	BuiltAt         time.Time `json:"built_at"`
}

type TopologyService struct {
	RevisionID     string         `json:"revision_id"`
	ProjectID      string         `json:"project_id"`
	SnapshotID     string         `json:"snapshot_id"`
	Name           string         `json:"name"`
	RepositoryRole RepositoryRole `json:"repository_role"`
	ServiceKind    ServiceKind    `json:"service_kind"`
	Purpose        string         `json:"purpose"`
	Stack          []Evidence     `json:"stack"`
}

type ContractDrift struct {
	ID                string          `json:"id"`
	RevisionID        string          `json:"revision_id"`
	ProducerProjectID *string         `json:"producer_project_id,omitempty"`
	ConsumerProjectID *string         `json:"consumer_project_id,omitempty"`
	ContractCode      string          `json:"contract_code"`
	ContractType      ContractType    `json:"contract_type"`
	ProducerVersion   string          `json:"producer_version"`
	ConsumerVersion   string          `json:"consumer_version"`
	Difference        json.RawMessage `json:"difference"`
	Severity          DriftSeverity   `json:"severity"`
	SuggestedAction   string          `json:"suggested_action"`
	CreatedAt         time.Time       `json:"created_at"`
}

type TopologyCatalog struct {
	Revision     TopologyRevision    `json:"revision"`
	Services     []TopologyService   `json:"services"`
	Capabilities []ServiceCapability `json:"capabilities"`
	Ownership    []ServiceOwnership  `json:"ownership"`
	Contracts    []Contract          `json:"contracts"`
	Relations    []ServiceRelation   `json:"relations"`
	Drifts       []ContractDrift     `json:"contract_drift"`
}

type ProjectTopology struct {
	Project      Project           `json:"project"`
	Dependencies []TopologyService `json:"dependencies"`
	Consumers    []TopologyService `json:"consumers"`
	Contracts    []Contract        `json:"contracts"`
	Impact       []TopologyService `json:"impact"`
}
