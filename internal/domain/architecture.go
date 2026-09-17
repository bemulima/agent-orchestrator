package domain

import "time"

// ArchitectureCurrent is a read-only CURRENT projection of TopologyCatalog.
// It deliberately does not own topology state or support architecture edits.
type ArchitectureCurrent struct {
	Mode                string                 `json:"mode"`
	TopologyRevisionID  string                 `json:"topology_revision_id"`
	TopologyFingerprint string                 `json:"topology_fingerprint"`
	GeneratedAt         time.Time              `json:"generated_at"`
	TopologyStale       bool                   `json:"topology_stale"`
	Services            []ArchitectureService  `json:"services"`
	Relations           []ArchitectureRelation `json:"relations"`
	Contracts           []ArchitectureContract `json:"contracts"`
	ContractDrift       []ContractDrift        `json:"contract_drift"`
}

const ArchitectureModeCurrent = "CURRENT"

type ArchitectureService struct {
	ProjectID       string              `json:"project_id"`
	Name            string              `json:"name"`
	RepositoryRole  RepositoryRole      `json:"repository_role"`
	ServiceKind     ServiceKind         `json:"service_kind"`
	Purpose         string              `json:"purpose"`
	Stack           []Evidence          `json:"stack"`
	Capabilities    []ServiceCapability `json:"capabilities"`
	Ownership       []ServiceOwnership  `json:"ownership"`
	ContractCount   int                 `json:"contract_count"`
	DependencyCount int                 `json:"dependency_count"`
	ConsumerCount   int                 `json:"consumer_count"`
	DriftStatus     string              `json:"drift_status"`
}

type ArchitectureRelation struct {
	ID              string       `json:"id"`
	SourceProjectID string       `json:"source_project_id"`
	TargetProjectID string       `json:"target_project_id"`
	RelationType    RelationType `json:"relation_type"`
	ContractCode    string       `json:"contract_code,omitempty"`
	Protocol        ContractType `json:"protocol,omitempty"`
	Direction       string       `json:"direction,omitempty"`
	Version         string       `json:"version,omitempty"`
	Evidence        Evidence     `json:"evidence"`
}

// ArchitectureContract exposes only contract facts already persisted in topology.
// SchemaDiscovered is false unless a future operation model adds payload evidence.
type ArchitectureContract struct {
	ID               string              `json:"id"`
	ProjectID        string              `json:"project_id"`
	Code             string              `json:"code"`
	Type             ContractType        `json:"type"`
	Direction        string              `json:"direction"`
	Version          string              `json:"version,omitempty"`
	Method           string              `json:"method,omitempty"`
	Path             string              `json:"path,omitempty"`
	Subject          string              `json:"subject,omitempty"`
	Resource         string              `json:"resource,omitempty"`
	SchemaDiscovered bool                `json:"schema_discovered"`
	Evidence         Evidence            `json:"evidence"`
	Providers        []ArchitectureParty `json:"providers"`
	Consumers        []ArchitectureParty `json:"consumers"`
}

type ArchitectureParty struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

type ArchitectureServiceDetail struct {
	ArchitectureCurrent
	Service  ArchitectureService    `json:"service"`
	Inbound  []ArchitectureRelation `json:"inbound"`
	Outbound []ArchitectureRelation `json:"outbound"`
}
