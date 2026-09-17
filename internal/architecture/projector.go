package architecture

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type Projector struct{ Now func() time.Time }

func (p Projector) Current(catalog domain.TopologyCatalog, stale bool) domain.ArchitectureCurrent {
	now := p.Now
	if now == nil {
		now = time.Now
	}
	result := domain.ArchitectureCurrent{Mode: domain.ArchitectureModeCurrent, TopologyRevisionID: catalog.Revision.ID, TopologyFingerprint: catalog.Revision.Fingerprint, GeneratedAt: now().UTC(), TopologyStale: stale, Services: []domain.ArchitectureService{}, Relations: []domain.ArchitectureRelation{}, Contracts: []domain.ArchitectureContract{}, ContractDrift: append([]domain.ContractDrift(nil), catalog.Drifts...)}
	services := map[string]domain.TopologyService{}
	for _, service := range catalog.Services {
		services[service.ProjectID] = service
	}
	for _, service := range catalog.Services {
		result.Services = append(result.Services, domain.ArchitectureService{ProjectID: service.ProjectID, Name: service.Name, RepositoryRole: service.RepositoryRole, ServiceKind: service.ServiceKind, Purpose: service.Purpose, Stack: append([]domain.Evidence(nil), service.Stack...), Capabilities: capabilitiesFor(catalog.Capabilities, service.ProjectID), Ownership: ownershipFor(catalog.Ownership, service.ProjectID), ContractCount: contractsFor(catalog.Contracts, service.ProjectID), DriftStatus: driftStatus(catalog.Drifts, service.ProjectID)})
	}
	for i := range result.Services {
		deps, consumers := map[string]struct{}{}, map[string]struct{}{}
		for _, relation := range catalog.Relations {
			if relation.SourceProjectID == result.Services[i].ProjectID {
				deps[relation.TargetProjectID] = struct{}{}
			}
			if relation.TargetProjectID == result.Services[i].ProjectID {
				consumers[relation.SourceProjectID] = struct{}{}
			}
		}
		result.Services[i].DependencyCount, result.Services[i].ConsumerCount = len(deps), len(consumers)
	}
	seen := map[string]struct{}{}
	for _, relation := range catalog.Relations {
		if _, ok := services[relation.SourceProjectID]; !ok {
			continue
		}
		if _, ok := services[relation.TargetProjectID]; !ok {
			continue
		}
		code := ""
		if relation.ContractCode != nil {
			code = *relation.ContractCode
		}
		key := strings.Join([]string{relation.SourceProjectID, relation.TargetProjectID, string(relation.RelationType), code, relation.Source}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		protocol, direction, version := contractMeta(catalog.Contracts, relation, code)
		result.Relations = append(result.Relations, domain.ArchitectureRelation{ID: relation.ID, SourceProjectID: relation.SourceProjectID, TargetProjectID: relation.TargetProjectID, RelationType: relation.RelationType, ContractCode: code, Protocol: protocol, Direction: direction, Version: version, Evidence: domain.Evidence{Category: "relation", Name: string(relation.RelationType), Value: code, Confidence: relation.Confidence, SourcePath: relation.Source}})
	}
	for _, contract := range catalog.Contracts {
		result.Contracts = append(result.Contracts, projectContract(contract, catalog.Contracts, services))
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Name < result.Services[j].Name })
	sort.Slice(result.Relations, func(i, j int) bool { return relationKey(result.Relations[i]) < relationKey(result.Relations[j]) })
	sort.Slice(result.Contracts, func(i, j int) bool { return result.Contracts[i].ID < result.Contracts[j].ID })
	return result
}

func (p Projector) Service(current domain.ArchitectureCurrent, projectID string) (domain.ArchitectureServiceDetail, error) {
	for _, service := range current.Services {
		if service.ProjectID == projectID {
			detail := domain.ArchitectureServiceDetail{ArchitectureCurrent: current, Service: service, Inbound: []domain.ArchitectureRelation{}, Outbound: []domain.ArchitectureRelation{}}
			for _, relation := range current.Relations {
				if relation.TargetProjectID == projectID {
					detail.Inbound = append(detail.Inbound, relation)
				}
				if relation.SourceProjectID == projectID {
					detail.Outbound = append(detail.Outbound, relation)
				}
			}
			return detail, nil
		}
	}
	return domain.ArchitectureServiceDetail{}, domain.ErrNotFound
}

func capabilitiesFor(values []domain.ServiceCapability, projectID string) []domain.ServiceCapability {
	result := []domain.ServiceCapability{}
	for _, value := range values {
		if value.ProjectID == projectID {
			result = append(result, value)
		}
	}
	return result
}
func ownershipFor(values []domain.ServiceOwnership, projectID string) []domain.ServiceOwnership {
	result := []domain.ServiceOwnership{}
	for _, value := range values {
		if value.ProjectID == projectID {
			result = append(result, value)
		}
	}
	return result
}
func contractsFor(values []domain.Contract, projectID string) int {
	n := 0
	for _, value := range values {
		if value.ProjectID == projectID {
			n++
		}
	}
	return n
}
func driftStatus(values []domain.ContractDrift, projectID string) string {
	status := "none"
	rank := map[domain.DriftSeverity]int{domain.DriftSeverityInfo: 1, domain.DriftSeverityWarning: 2, domain.DriftSeverityError: 3, domain.DriftSeverityCritical: 4}
	max := 0
	for _, value := range values {
		if pointer(value.ProducerProjectID) == projectID || pointer(value.ConsumerProjectID) == projectID {
			if rank[value.Severity] > max {
				max = rank[value.Severity]
				status = string(value.Severity)
			}
		}
	}
	return status
}
func pointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func relationKey(value domain.ArchitectureRelation) string {
	return value.SourceProjectID + "\x00" + value.TargetProjectID + "\x00" + string(value.RelationType) + "\x00" + value.ContractCode
}
func contractMeta(contracts []domain.Contract, relation domain.ServiceRelation, code string) (domain.ContractType, string, string) {
	for _, contract := range contracts {
		if contract.Code == code && (contract.ProjectID == relation.SourceProjectID || contract.ProjectID == relation.TargetProjectID) {
			return contract.Type, contract.Direction, contract.Version
		}
	}
	return "", "", ""
}

func projectContract(contract domain.Contract, all []domain.Contract, services map[string]domain.TopologyService) domain.ArchitectureContract {
	result := domain.ArchitectureContract{ID: contract.ID, ProjectID: contract.ProjectID, Code: contract.Code, Type: contract.Type, Direction: contract.Direction, Version: contract.Version, SchemaDiscovered: false, Evidence: domain.Evidence{Category: "contract", Name: string(contract.Type), Value: contract.Code, SourcePath: contract.SourcePath}, Providers: []domain.ArchitectureParty{}, Consumers: []domain.ArchitectureParty{}}
	var definition map[string]any
	_ = json.Unmarshal(contract.Definition, &definition)
	result.Method = stringValue(definition, "method")
	result.Path = stringValue(definition, "path")
	result.Subject = stringValue(definition, "subject")
	result.Resource = stringValue(definition, "resource")
	for _, other := range all {
		if other.Code != contract.Code {
			continue
		}
		service, ok := services[other.ProjectID]
		if !ok {
			continue
		}
		party := domain.ArchitectureParty{ProjectID: other.ProjectID, Name: service.Name}
		if other.Direction == domain.ContractDirectionProvides || other.Direction == domain.ContractDirectionPublishes || other.Direction == domain.ContractDirectionOwns {
			result.Providers = appendUnique(result.Providers, party)
		}
		if other.Direction == domain.ContractDirectionConsumes || other.Direction == domain.ContractDirectionSubscribes {
			result.Consumers = appendUnique(result.Consumers, party)
		}
	}
	return result
}
func stringValue(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}
func appendUnique(values []domain.ArchitectureParty, value domain.ArchitectureParty) []domain.ArchitectureParty {
	for _, existing := range values {
		if existing.ProjectID == value.ProjectID {
			return values
		}
	}
	return append(values, value)
}
