package execution

import "github.com/bemulima/agent-orchestrator/internal/domain"

// agentLandscape is the bounded, evidence-backed cross-project context sent to
// Codex. Database IDs, timestamps, checksums, and raw contract definitions are
// deliberately omitted: agents need the catalog shape and evidence paths, not
// a second copy of potentially large schemas.
type agentLandscape struct {
	Revision     agentLandscapeRevision `json:"revision"`
	Services     []agentService         `json:"services"`
	Capabilities []agentCapability      `json:"capabilities"`
	Ownership    []agentOwnership       `json:"ownership"`
	Contracts    []agentContract        `json:"contracts"`
	Relations    []agentRelation        `json:"relations"`
	Drifts       []agentDrift           `json:"contract_drift"`
}

type agentLandscapeRevision struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

type agentService struct {
	ProjectID      string                `json:"project_id"`
	Name           string                `json:"name"`
	RepositoryRole domain.RepositoryRole `json:"repository_role"`
	ServiceKind    domain.ServiceKind    `json:"service_kind"`
	Purpose        string                `json:"purpose"`
	Stack          []agentEvidence       `json:"stack"`
}

type agentEvidence struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	SourcePath string `json:"source_path"`
}

type agentCapability struct {
	ProjectID   string  `json:"project_id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Source      string  `json:"source"`
}

type agentOwnership struct {
	ProjectID    string  `json:"project_id"`
	ResourceType string  `json:"resource_type"`
	ResourceName string  `json:"resource_name"`
	Confidence   float64 `json:"confidence"`
	Source       string  `json:"source"`
}

type agentContract struct {
	ProjectID  string              `json:"project_id"`
	Code       string              `json:"code"`
	Type       domain.ContractType `json:"type"`
	Version    string              `json:"version"`
	Direction  string              `json:"direction"`
	SourcePath string              `json:"source_path"`
}

type agentRelation struct {
	SourceProjectID string              `json:"source_project_id"`
	TargetProjectID string              `json:"target_project_id"`
	RelationType    domain.RelationType `json:"relation_type"`
	ContractCode    *string             `json:"contract_code,omitempty"`
	Confidence      float64             `json:"confidence"`
	Source          string              `json:"source"`
}

type agentDrift struct {
	ProducerProjectID *string              `json:"producer_project_id,omitempty"`
	ConsumerProjectID *string              `json:"consumer_project_id,omitempty"`
	ContractCode      string               `json:"contract_code"`
	ContractType      domain.ContractType  `json:"contract_type"`
	ProducerVersion   string               `json:"producer_version"`
	ConsumerVersion   string               `json:"consumer_version"`
	Severity          domain.DriftSeverity `json:"severity"`
	SuggestedAction   string               `json:"suggested_action"`
}

func landscapeForAgent(catalog domain.TopologyCatalog) agentLandscape {
	result := agentLandscape{
		Revision:     agentLandscapeRevision{ID: catalog.Revision.ID, Fingerprint: catalog.Revision.Fingerprint},
		Services:     make([]agentService, 0, len(catalog.Services)),
		Capabilities: make([]agentCapability, 0, len(catalog.Capabilities)),
		Ownership:    make([]agentOwnership, 0, len(catalog.Ownership)),
		Contracts:    make([]agentContract, 0, len(catalog.Contracts)),
		Relations:    make([]agentRelation, 0, len(catalog.Relations)),
		Drifts:       make([]agentDrift, 0, len(catalog.Drifts)),
	}
	for _, service := range catalog.Services {
		value := agentService{
			ProjectID: service.ProjectID, Name: service.Name, RepositoryRole: service.RepositoryRole,
			ServiceKind: service.ServiceKind, Purpose: service.Purpose,
			Stack: make([]agentEvidence, 0, len(service.Stack)),
		}
		for _, fact := range service.Stack {
			value.Stack = append(value.Stack, agentEvidence{Name: fact.Name, Value: fact.Value, SourcePath: fact.SourcePath})
		}
		result.Services = append(result.Services, value)
	}
	for _, value := range catalog.Capabilities {
		result.Capabilities = append(result.Capabilities, agentCapability{
			ProjectID: value.ProjectID, Code: value.Code, Name: value.Name, Description: value.Description,
			Confidence: value.Confidence, Source: value.Source,
		})
	}
	for _, value := range catalog.Ownership {
		result.Ownership = append(result.Ownership, agentOwnership{
			ProjectID: value.ProjectID, ResourceType: value.ResourceType, ResourceName: value.ResourceName,
			Confidence: value.Confidence, Source: value.Source,
		})
	}
	for _, value := range catalog.Contracts {
		result.Contracts = append(result.Contracts, agentContract{
			ProjectID: value.ProjectID, Code: value.Code, Type: value.Type, Version: value.Version,
			Direction: value.Direction, SourcePath: value.SourcePath,
		})
	}
	for _, value := range catalog.Relations {
		result.Relations = append(result.Relations, agentRelation{
			SourceProjectID: value.SourceProjectID, TargetProjectID: value.TargetProjectID,
			RelationType: value.RelationType, ContractCode: value.ContractCode,
			Confidence: value.Confidence, Source: value.Source,
		})
	}
	for _, value := range catalog.Drifts {
		result.Drifts = append(result.Drifts, agentDrift{
			ProducerProjectID: value.ProducerProjectID, ConsumerProjectID: value.ConsumerProjectID,
			ContractCode: value.ContractCode, ContractType: value.ContractType,
			ProducerVersion: value.ProducerVersion, ConsumerVersion: value.ConsumerVersion,
			Severity: value.Severity, SuggestedAction: value.SuggestedAction,
		})
	}
	return result
}
