// Package architectureprocess loads version-controlled CURRENT business
// process manifests. It deliberately does not persist or infer a process from
// the service dependency graph: confirmed CURRENT processes remain reviewable
// repository files.
package architectureprocess

import "github.com/bemulima/agent-orchestrator/internal/domain"

const (
	// SchemaV1 identifies the first stable business-process manifest schema.
	SchemaV1 = "architecture/business-process/v1"
	KindV1   = "business_process"

	// ManifestDirectory is relative to the owning repository root.
	ManifestDirectory = ".ai/architecture/processes"
)

// Provenance tells a reader how a process became visible in CURRENT. A
// candidate is intentionally not a confirmed CURRENT process and is rejected
// by LoadCurrent; candidates belong in a separate proposal/snapshot store.
type Provenance string

const (
	ProvenanceOwnerAuthored       Provenance = "owner-authored"
	ProvenanceEvidenceBacked      Provenance = "evidence-backed"
	ProvenanceCandidateUnverified Provenance = "candidate/unverified"
)

// Manifest is a version-controlled, evidence-backed business-process
// definition. Ordered Steps are the source for the process visualisation;
// participating collections are explicit cross-links for navigation and are
// never inferred from graph reachability.
type Manifest struct {
	Schema           string                        `json:"schema" yaml:"schema"`
	Kind             string                        `json:"kind" yaml:"kind"`
	ID               string                        `json:"id" yaml:"id"`
	ManifestRevision int                           `json:"manifest_revision" yaml:"manifest_revision"`
	Identity         Identity                      `json:"identity" yaml:"identity"`
	Goal             domain.ArchitectureStatement  `json:"goal" yaml:"goal"`
	Trigger          domain.ArchitectureStatement  `json:"trigger" yaml:"trigger"`
	Outcome          domain.ArchitectureStatement  `json:"outcome" yaml:"outcome"`
	Steps            []Step                        `json:"steps" yaml:"steps"`
	Services         []ServiceParticipant          `json:"participating_services" yaml:"participating_services"`
	Operations       []OperationParticipant        `json:"participating_operations" yaml:"participating_operations"`
	Contracts        []ContractReference           `json:"contracts" yaml:"contracts"`
	Events           []EventReference              `json:"events" yaml:"events"`
	Provenance       Provenance                    `json:"provenance" yaml:"provenance"`
	Evidence         []domain.ArchitectureEvidence `json:"evidence" yaml:"evidence"`
	Confidence       float64                       `json:"confidence" yaml:"confidence"`
}

type Identity struct {
	Name string `json:"name" yaml:"name"`
}

// Step order is represented by the array position. ID remains stable so the
// process, service and operation views can link to an exact step.
type Step struct {
	ID          string                        `json:"id" yaml:"id"`
	Name        string                        `json:"name" yaml:"name"`
	Description domain.ArchitectureStatement  `json:"description" yaml:"description"`
	ServiceID   string                        `json:"service_id" yaml:"service_id"`
	OperationID string                        `json:"operation_id" yaml:"operation_id"`
	Contracts   []string                      `json:"contracts" yaml:"contracts"`
	Events      []string                      `json:"events" yaml:"events"`
	Evidence    []domain.ArchitectureEvidence `json:"evidence" yaml:"evidence"`
	Confidence  float64                       `json:"confidence" yaml:"confidence"`
}

type ServiceParticipant struct {
	ServiceID string                       `json:"service_id" yaml:"service_id"`
	Role      domain.ArchitectureStatement `json:"role" yaml:"role"`
}

type OperationParticipant struct {
	ServiceID   string                       `json:"service_id" yaml:"service_id"`
	OperationID string                       `json:"operation_id" yaml:"operation_id"`
	Role        domain.ArchitectureStatement `json:"role" yaml:"role"`
}

type ContractReference struct {
	ID          string                       `json:"id" yaml:"id"`
	Transport   string                       `json:"transport" yaml:"transport"`
	Direction   string                       `json:"direction" yaml:"direction"`
	Description domain.ArchitectureStatement `json:"description" yaml:"description"`
}

type EventReference struct {
	ID          string                       `json:"id" yaml:"id"`
	Transport   string                       `json:"transport" yaml:"transport"`
	Direction   string                       `json:"direction" yaml:"direction"`
	Description domain.ArchitectureStatement `json:"description" yaml:"description"`
}

// IsConfirmed reports whether this provenance is allowed in CURRENT
// exploration. It intentionally returns false for candidate/unverified.
func (value Provenance) IsConfirmed() bool {
	return value == ProvenanceOwnerAuthored || value == ProvenanceEvidenceBacked
}
