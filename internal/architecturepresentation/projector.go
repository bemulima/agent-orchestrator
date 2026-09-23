package architecturepresentation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// Projector is a pure CURRENT-to-V2 presentation transformer. It has no
// storage dependency and can therefore be composed behind a future use case
// without changing catalog or target behavior.
type Projector struct{ classifier Classifier }

func NewProjector(rules ClassificationRules) Projector {
	return Projector{classifier: NewClassifier(rules)}
}

// Platform builds a domain-clustered service graph. It never emits operation
// nodes: platform exploration remains at service/relation level by contract.
func (p Projector) Platform(ctx context.Context, catalog domain.ArchitectureCatalog, view PlatformViewMode) (PlatformPresentation, error) {
	if err := validateCatalog(catalog); err != nil {
		return PlatformPresentation{}, err
	}
	if view != PlatformViewBusiness && view != PlatformViewTechnical {
		return PlatformPresentation{}, fmt.Errorf("unsupported platform view %q: %w", view, domain.ErrValidation)
	}

	visible := make(map[string]bool, len(catalog.Platform.Services))
	groups := make(map[string]*PlatformDomainGroup)
	for _, service := range catalog.Platform.Services {
		if err := ctx.Err(); err != nil {
			return PlatformPresentation{}, err
		}
		classification := p.classifier.Classify(service)
		if !visibleIn(view, classification.Kind) {
			continue
		}
		visible[service.Source.ProjectID] = true
		key := nonEmpty(classification.DomainID, "unclassified")
		group := groups[key]
		if group == nil {
			group = &PlatformDomainGroup{ID: key, Name: nonEmpty(classification.DomainName, "Unclassified"), Kind: classification.Kind, Services: []ServiceSummary{}, Evidence: cloneEvidence(classification.Reason.Evidence)}
			groups[key] = group
		}
		group.Services = append(group.Services, summary(service, classification))
	}

	result := PlatformPresentation{Mode: domain.ArchitectureCatalogModeCurrent, CatalogFingerprint: catalog.Fingerprint, View: view, Domains: []PlatformDomainGroup{}, Graph: emptyDiagram()}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group.Services, func(i, j int) bool {
			return serviceSummaryKey(group.Services[i]) < serviceSummaryKey(group.Services[j])
		})
		result.Domains = append(result.Domains, *group)
		clusterID := "domain:" + group.ID
		result.Graph.Nodes = append(result.Graph.Nodes, DiagramNode{ID: clusterID, Type: NodeTypeDomainCluster, Label: group.Name, Description: groupDescription(group), Evidence: cloneEvidence(group.Evidence)})
		for _, item := range group.Services {
			classification := item.Classification
			result.Graph.Nodes = append(result.Graph.Nodes, DiagramNode{ID: serviceNodeID(item.ProjectID), Type: NodeTypeService, Label: item.ServiceName, Description: classification.Reason, ServiceID: item.ProjectID, Classification: &classification, Evidence: cloneEvidence(classification.Reason.Evidence)})
			result.Graph.Edges = append(result.Graph.Edges, DiagramEdge{ID: "contains:" + clusterID + ":" + item.ProjectID, Type: EdgeTypeContains, Source: clusterID, Target: serviceNodeID(item.ProjectID), Description: statement("Contains service", 1, nil), Evidence: []domain.ArchitectureEvidence{}})
		}
	}

	external := map[string]bool{}
	for _, relation := range sortedRelations(catalog.Platform.Relations) {
		if err := ctx.Err(); err != nil {
			return PlatformPresentation{}, err
		}
		if !visible[relation.SourceProjectID] {
			continue
		}
		source := serviceNodeID(relation.SourceProjectID)
		target := serviceNodeID(relation.TargetProjectID)
		if relation.TargetProjectID != "" && !visible[relation.TargetProjectID] {
			// Do not create a hidden business/technical endpoint. The visible
			// source retains the relation as an explicitly external technical or
			// business counterpart, never as a guessed visible service.
			target = externalNodeID("hidden:" + relation.TargetProjectID)
			if !external[target] {
				external[target] = true
				result.Graph.Nodes = append(result.Graph.Nodes, DiagramNode{ID: target, Type: NodeTypeExternalService, Label: "filtered service " + relation.TargetProjectID, Description: statement("Counterpart is outside the selected platform mode", relation.Confidence, relation.Evidence), Evidence: cloneEvidence(relation.Evidence)})
			}
		}
		if relation.TargetProjectID == "" {
			target = externalNodeID(nonEmpty(relation.ExternalTarget, "unknown"))
			if !external[target] {
				external[target] = true
				result.Graph.Nodes = append(result.Graph.Nodes, DiagramNode{ID: target, Type: NodeTypeExternalService, Label: nonEmpty(relation.ExternalTarget, "unknown"), Description: relation.Description, Evidence: cloneEvidence(relation.Evidence)})
			}
		}
		if target == "" {
			continue
		}
		if relationInbound(relation.Direction) {
			source, target = target, source
		}
		result.Graph.Edges = append(result.Graph.Edges, DiagramEdge{ID: "relation:" + relation.ID, Type: platformEdgeType(relation), Source: source, Target: target, Label: relationLabel(relation), Description: relation.Description, Evidence: cloneEvidence(relation.Evidence)})
	}
	sortDiagram(&result.Graph)
	return result, nil
}

// Service builds a service-focused presentation with both deterministic
// operation groupings. The caller selects which grouping to display; exposing
// both lets the UI switch without another backend round trip.
func (p Projector) Service(ctx context.Context, catalog domain.ArchitectureCatalog, projectID string) (ServicePresentation, error) {
	service, err := findService(ctx, catalog, projectID)
	if err != nil {
		return ServicePresentation{}, err
	}
	classification := p.classifier.Classify(service)
	result := ServicePresentation{Mode: domain.ArchitectureCatalogModeCurrent, CatalogFingerprint: catalog.Fingerprint, Service: summary(service, classification), CapabilityGroups: capabilityGroups(service), TransportGroups: transportGroups(service), Graph: serviceGraph(service, classification)}
	if service.Manifest == nil {
		result.Purpose = unknownStatement("Service architecture manifest is unavailable")
		return result, nil
	}
	manifest := service.Manifest
	result.Purpose = manifest.Purpose
	result.Responsibilities = cloneStatements(manifest.Responsibilities)
	result.Capabilities = cloneStatements(manifest.Capabilities)
	result.OwnedResources = append([]domain.ArchitectureOwnedResource{}, manifest.OwnedResources...)
	result.InboundInterfaces = append([]domain.ArchitectureInterface{}, manifest.InboundInterfaces...)
	result.OutboundDependencies = append([]domain.ArchitectureExternalInteraction{}, manifest.OutboundDependencies...)
	result.ProducedContracts = append([]domain.ArchitectureContractReference{}, manifest.ProducedContracts...)
	result.ConsumedContracts = append([]domain.ArchitectureContractReference{}, manifest.ConsumedContracts...)
	result.PublishedEvents = append([]domain.ArchitectureContractReference{}, manifest.PublishedEvents...)
	result.SubscribedEvents = append([]domain.ArchitectureContractReference{}, manifest.SubscribedEvents...)
	return result, nil
}

// Operation builds all operation-level views from a single manifest. It never
// claims a business decision that the v1 manifest cannot prove.
func (p Projector) Operation(ctx context.Context, catalog domain.ArchitectureCatalog, projectID, operationID string) (OperationPresentation, error) {
	service, err := findService(ctx, catalog, projectID)
	if err != nil {
		return OperationPresentation{}, err
	}
	operation, err := findOperation(service, operationID)
	if err != nil {
		return OperationPresentation{}, err
	}
	if err := ctx.Err(); err != nil {
		return OperationPresentation{}, err
	}
	classification := p.classifier.Classify(service)
	manifest := cloneOperation(operation.Manifest)
	return OperationPresentation{
		Mode:                domain.ArchitectureCatalogModeCurrent,
		CatalogFingerprint:  catalog.Fingerprint,
		Service:             summary(service, classification),
		Operation:           manifest,
		BusinessExplanation: businessExplanation(manifest),
		Flow:                operationFlow(manifest),
		Sequence:            operationSequence(manifest),
		Contracts:           operationContracts(manifest),
		Evidence:            cloneEvidence(manifest.Evidence),
	}, nil
}

func validateCatalog(catalog domain.ArchitectureCatalog) error {
	if catalog.Mode != domain.ArchitectureCatalogModeCurrent {
		return fmt.Errorf("architecture presentation requires CURRENT catalog: %w", domain.ErrValidation)
	}
	return nil
}

func findService(ctx context.Context, catalog domain.ArchitectureCatalog, projectID string) (domain.ArchitectureCatalogService, error) {
	if err := validateCatalog(catalog); err != nil {
		return domain.ArchitectureCatalogService{}, err
	}
	for _, service := range catalog.Platform.Services {
		if err := ctx.Err(); err != nil {
			return domain.ArchitectureCatalogService{}, err
		}
		if service.Source.ProjectID == projectID {
			return service, nil
		}
	}
	return domain.ArchitectureCatalogService{}, domain.ErrNotFound
}

func findOperation(service domain.ArchitectureCatalogService, operationID string) (domain.ArchitectureCatalogOperation, error) {
	for _, group := range service.Groups {
		for _, operation := range group.Operations {
			if operation.Manifest.ID == operationID {
				return operation, nil
			}
		}
	}
	for _, operation := range service.Ungrouped {
		if operation.Manifest.ID == operationID {
			return operation, nil
		}
	}
	return domain.ArchitectureCatalogOperation{}, domain.ErrNotFound
}

func visibleIn(view PlatformViewMode, kind ServiceClassificationKind) bool {
	if kind == ServiceClassificationUnclassified {
		return true
	}
	if view == PlatformViewBusiness {
		return kind == ServiceClassificationBusiness
	}
	return kind == ServiceClassificationTechnical
}

func summary(service domain.ArchitectureCatalogService, classification ServiceClassification) ServiceSummary {
	value := ServiceSummary{ProjectID: service.Source.ProjectID, ProjectName: service.Source.ProjectName, ServiceName: serviceName(service), Covered: service.Covered, Classification: classification}
	if service.Manifest != nil {
		value.ServiceID = service.Manifest.ID
	}
	return value
}

func groupDescription(group *PlatformDomainGroup) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: "Services grouped by explicit classification metadata or deterministic rule", Confidence: 1, Evidence: cloneEvidence(group.Evidence)}
}

func serviceSummaryKey(value ServiceSummary) string {
	return value.ServiceName + "\x00" + value.ProjectID
}

func statement(value string, confidence float64, evidence []domain.ArchitectureEvidence) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: value, Confidence: confidence, Evidence: cloneEvidence(evidence)}
}

func cloneStatements(value []domain.ArchitectureStatement) []domain.ArchitectureStatement {
	result := make([]domain.ArchitectureStatement, len(value))
	for index, item := range value {
		result[index] = item
		result[index].Evidence = cloneEvidence(item.Evidence)
	}
	return result
}

func cloneOperation(value domain.ArchitectureOperationManifest) domain.ArchitectureOperationManifest {
	// The catalog has already cloned its immutable source. Copy the fields that
	// presentation callers commonly mutate (slices and evidence) before
	// returning an operation DTO.
	value.BusinessRules = cloneStatements(value.BusinessRules)
	value.Evidence = cloneEvidence(value.Evidence)
	value.BusinessProcess = append([]domain.ArchitectureOperationStep{}, value.BusinessProcess...)
	for index := range value.BusinessProcess {
		value.BusinessProcess[index].Description.Evidence = cloneEvidence(value.BusinessProcess[index].Description.Evidence)
		value.BusinessProcess[index].Interactions = append([]string{}, value.BusinessProcess[index].Interactions...)
	}
	value.DataAccess = append([]domain.ArchitectureDataAccess{}, value.DataAccess...)
	value.ExternalInteractions = append([]domain.ArchitectureExternalInteraction{}, value.ExternalInteractions...)
	value.SideEffects = append([]domain.ArchitectureSideEffect{}, value.SideEffects...)
	value.Errors = append([]domain.ArchitectureOperationError{}, value.Errors...)
	value.Output.Responses = append([]domain.ArchitectureResponse{}, value.Output.Responses...)
	value.Output.EmittedEvents = append([]domain.ArchitectureContractReference{}, value.Output.EmittedEvents...)
	return value
}

func emptyDiagram() Diagram { return Diagram{Nodes: []DiagramNode{}, Edges: []DiagramEdge{}} }

func sortDiagram(value *Diagram) {
	value.Nodes = uniqueNodes(value.Nodes)
	value.Edges = uniqueEdges(value.Edges)
	sort.Slice(value.Nodes, func(i, j int) bool { return value.Nodes[i].ID < value.Nodes[j].ID })
	sort.Slice(value.Edges, func(i, j int) bool { return value.Edges[i].ID < value.Edges[j].ID })
}

func uniqueNodes(values []DiagramNode) []DiagramNode {
	seen := make(map[string]struct{}, len(values))
	result := make([]DiagramNode, 0, len(values))
	for _, value := range values {
		if value.ID == "" {
			continue
		}
		if _, exists := seen[value.ID]; exists {
			continue
		}
		seen[value.ID] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueEdges(values []DiagramEdge) []DiagramEdge {
	seen := make(map[string]struct{}, len(values))
	result := make([]DiagramEdge, 0, len(values))
	for _, value := range values {
		if value.ID == "" {
			continue
		}
		if _, exists := seen[value.ID]; exists {
			continue
		}
		seen[value.ID] = struct{}{}
		result = append(result, value)
	}
	return result
}

func serviceNodeID(projectID string) string { return "service:" + projectID }
func externalNodeID(value string) string {
	return "external:" + strings.ReplaceAll(strings.TrimSpace(value), " ", "_")
}

func sortedRelations(value []domain.ArchitectureCatalogRelation) []domain.ArchitectureCatalogRelation {
	result := append([]domain.ArchitectureCatalogRelation{}, value...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func relationInbound(direction string) bool {
	switch normalized(direction) {
	case "inbound", "consumed", "subscribed", "receive", "received":
		return true
	default:
		return false
	}
}

func relationLabel(relation domain.ArchitectureCatalogRelation) string {
	parts := []string{}
	if relation.Transport != "" {
		parts = append(parts, relation.Transport)
	}
	if relation.Contract != "" {
		parts = append(parts, relation.Contract)
	}
	if relation.OperationID != "" {
		parts = append(parts, relation.OperationID)
	}
	if len(parts) == 0 {
		return string(relation.Type)
	}
	return strings.Join(parts, " · ")
}

func platformEdgeType(relation domain.ArchitectureCatalogRelation) EdgeType {
	transport := normalized(relation.Transport)
	if relation.Type == domain.ArchitectureCatalogRelationEvent {
		if relationInbound(relation.Direction) {
			return EdgeTypeEventConsume
		}
		return EdgeTypeEventPublish
	}
	if transport == "nats" {
		return EdgeTypeNATSRequestReply
	}
	if transport == "http" {
		return EdgeTypeHTTPRequest
	}
	return EdgeTypeTechnical
}
