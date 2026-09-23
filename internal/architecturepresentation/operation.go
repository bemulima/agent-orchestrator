package architecturepresentation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func capabilityGroups(service domain.ArchitectureCatalogService) []OperationGroup {
	result := make([]OperationGroup, 0, len(service.Groups)+1)
	grouped := make(map[string]struct{})
	for _, group := range service.Groups {
		operations := summaries(group.Operations)
		for _, operation := range group.Operations {
			grouped[operation.Manifest.ID] = struct{}{}
		}
		result = append(result, OperationGroup{ID: nonEmpty(group.ID, "capability:"+group.Name), Name: nonEmpty(group.Name, "Unnamed manifest group"), Grouping: OperationGroupingCapability, Description: group.Description, Operations: operations})
	}
	ungrouped := make([]domain.ArchitectureCatalogOperation, 0, len(service.Ungrouped))
	for _, operation := range service.Ungrouped {
		if _, exists := grouped[operation.Manifest.ID]; !exists {
			ungrouped = append(ungrouped, operation)
		}
	}
	if len(ungrouped) > 0 {
		result = append(result, OperationGroup{ID: "capability:ungrouped", Name: "Ungrouped", Grouping: OperationGroupingCapability, Description: unknownStatement("No explicit manifest endpoint group declares this operation"), Operations: summaries(ungrouped)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func transportGroups(service domain.ArchitectureCatalogService) []OperationGroup {
	byTransport := map[string][]domain.ArchitectureCatalogOperation{}
	for _, operation := range allOperations(service) {
		key := transportGroupName(operation.Manifest)
		byTransport[key] = append(byTransport[key], operation)
	}
	keys := make([]string, 0, len(byTransport))
	for key := range byTransport {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]OperationGroup, 0, len(keys))
	for _, key := range keys {
		result = append(result, OperationGroup{ID: "transport:" + normalized(key), Name: key, Grouping: OperationGroupingTransport, Transport: key, Description: statement("Operations grouped by explicit Architecture Manifest operation type", 1, nil), Operations: summaries(byTransport[key])})
	}
	return result
}

func allOperations(service domain.ArchitectureCatalogService) []domain.ArchitectureCatalogOperation {
	byID := make(map[string]domain.ArchitectureCatalogOperation)
	for _, group := range service.Groups {
		for _, operation := range group.Operations {
			byID[operation.Manifest.ID] = operation
		}
	}
	for _, operation := range service.Ungrouped {
		byID[operation.Manifest.ID] = operation
	}
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]domain.ArchitectureCatalogOperation, 0, len(keys))
	for _, key := range keys {
		result = append(result, byID[key])
	}
	return result
}

func summaries(operations []domain.ArchitectureCatalogOperation) []OperationSummary {
	result := make([]OperationSummary, 0, len(operations))
	for _, operation := range operations {
		result = append(result, OperationSummary{ID: operation.Manifest.ID, Type: operation.Manifest.Type, Identity: operationIdentity(operation.Manifest), BusinessTask: operation.Manifest.BusinessTask, Confidence: operation.Manifest.Confidence})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func transportGroupName(operation domain.ArchitectureOperationManifest) string {
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		return "HTTP"
	case domain.ArchitectureOperationNATSRequestReply:
		return "NATS request/reply"
	case domain.ArchitectureOperationNATSEventSubscriber:
		if normalized(natsRole(operation.Identity)) == "publisher" {
			return "Event publisher"
		}
		return "Event subscriber"
	case domain.ArchitectureOperationWorker:
		return "Worker"
	case domain.ArchitectureOperationScheduled:
		return "Scheduled"
	default:
		return "Unknown transport"
	}
}

// natsRole exists to keep all nil-safe NATS access in one place.
func natsRole(identity domain.ArchitectureOperationIdentity) string {
	if identity.NATS == nil {
		return ""
	}
	return identity.NATS.Role
}

func operationIdentity(operation domain.ArchitectureOperationManifest) string {
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		if operation.Identity.HTTP != nil {
			return strings.TrimSpace(operation.Identity.HTTP.Method + " " + operation.Identity.HTTP.Path)
		}
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		if operation.Identity.NATS != nil {
			return strings.TrimSpace(operation.Identity.NATS.Subject + " " + operation.Identity.NATS.Role)
		}
	case domain.ArchitectureOperationWorker:
		if operation.Identity.Worker != nil {
			return operation.Identity.Worker.Name
		}
	case domain.ArchitectureOperationScheduled:
		if operation.Identity.Scheduled != nil {
			return strings.TrimSpace(operation.Identity.Scheduled.Name + " " + operation.Identity.Scheduled.Schedule)
		}
	}
	return "unknown"
}

func serviceGraph(service domain.ArchitectureCatalogService, classification ServiceClassification) Diagram {
	graph := emptyDiagram()
	hub := serviceNodeID(service.Source.ProjectID)
	graph.Nodes = append(graph.Nodes, DiagramNode{ID: hub, Type: NodeTypeServiceHub, Label: serviceName(service), Description: classification.Reason, ServiceID: service.Source.ProjectID, Classification: &classification, Evidence: cloneEvidence(classification.Reason.Evidence)})
	if service.Manifest == nil {
		return graph
	}
	for _, resource := range service.Manifest.OwnedResources {
		id := "resource:" + service.Source.ProjectID + ":" + resource.Name
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeOwnedResource, Label: resource.Name, Description: resource.Description, ServiceID: service.Source.ProjectID, Evidence: cloneEvidence(resource.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: "owns:" + hub + ":" + resource.Name, Type: EdgeTypeContains, Source: hub, Target: id, Label: resource.Type, Description: resource.Description, Evidence: cloneEvidence(resource.Description.Evidence)})
	}
	for _, dependency := range service.Manifest.OutboundDependencies {
		id := externalNodeID(dependency.Target)
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeExternalService, Label: nonEmpty(dependency.Target, "unknown"), Description: dependency.Description, Evidence: cloneEvidence(dependency.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: "outbound:" + hub + ":" + dependency.ID, Type: interactionEdgeType(dependency), Source: hub, Target: id, Label: interactionLabel(dependency), Description: dependency.Description, Evidence: cloneEvidence(dependency.Description.Evidence)})
	}
	for _, group := range capabilityGroups(service) {
		id := "capability:" + service.Source.ProjectID + ":" + group.ID
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeCapabilityGroup, Label: group.Name, Description: group.Description, ServiceID: service.Source.ProjectID, Evidence: cloneEvidence(group.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: "capability:" + hub + ":" + group.ID, Type: EdgeTypeContains, Source: hub, Target: id, Description: group.Description, Evidence: cloneEvidence(group.Description.Evidence)})
	}
	sortDiagram(&graph)
	return graph
}

func businessExplanation(operation domain.ArchitectureOperationManifest) BusinessExplanation {
	sideEffects := make([]domain.ArchitectureStatement, 0, len(operation.SideEffects)+len(operation.Output.EmittedEvents))
	for _, value := range operation.SideEffects {
		sideEffects = append(sideEffects, value.Description)
	}
	for _, event := range operation.Output.EmittedEvents {
		sideEffects = append(sideEffects, event.Description)
	}
	failures := make([]domain.ArchitectureStatement, 0, len(operation.Errors))
	for _, value := range operation.Errors {
		failures = append(failures, value.Description)
	}
	result := unknownStatement("Operation result is not explicitly described by a result schema")
	if operation.Output.Result != nil {
		result = operation.Output.Result.Description
	}
	if len(sideEffects) == 0 {
		sideEffects = []domain.ArchitectureStatement{unknownStatement("No side effect is explicitly declared")}
	}
	if len(failures) == 0 {
		failures = []domain.ArchitectureStatement{unknownStatement("Failure meaning is not explicitly declared")}
	}
	return BusinessExplanation{
		BusinessTask:       operation.BusinessTask,
		WhenWhyInvoked:     nonUnknownStatement(operation.Trigger.Description, "When/why this operation is invoked is not explicitly described"),
		Validation:         unknownStatement("Validation behavior is not a typed Architecture Manifest v1 claim"),
		MainRules:          nonEmptyStatements(operation.BusinessRules, "No business rules are explicitly declared"),
		ImportantDecisions: []domain.ArchitectureStatement{unknownStatement("Important decisions are not a typed Architecture Manifest v1 claim")},
		SideEffects:        cloneStatements(sideEffects),
		Result:             nonUnknownStatement(result, "Operation result is not explicitly described"),
		FailureMeaning:     cloneStatements(failures),
	}
}

func nonEmptyStatements(values []domain.ArchitectureStatement, unknown string) []domain.ArchitectureStatement {
	if len(values) == 0 {
		return []domain.ArchitectureStatement{unknownStatement(unknown)}
	}
	return cloneStatements(values)
}

func operationFlow(operation domain.ArchitectureOperationManifest) Diagram {
	graph := emptyDiagram()
	start := operationNodeID(operation, "start")
	input := operationNodeID(operation, "input")
	graph.Nodes = append(graph.Nodes,
		DiagramNode{ID: start, Type: NodeTypeStartEnd, Label: "Start", Description: nonUnknownStatement(operation.Trigger.Description, "Operation trigger is unknown"), OperationID: operation.ID, Evidence: cloneEvidence(operation.Trigger.Description.Evidence)},
		DiagramNode{ID: input, Type: NodeTypeInputOutput, Label: "Input", Description: inputDescription(operation), OperationID: operation.ID, Evidence: cloneEvidence(operation.Evidence)},
	)
	graph.Edges = append(graph.Edges, DiagramEdge{ID: start + ":" + input, Type: EdgeTypeSuccess, Source: start, Target: input, Description: statement("Operation begins with received input", 1, operation.Evidence), Evidence: cloneEvidence(operation.Evidence)})
	validation := operationNodeID(operation, "validation")
	validationDescription := businessExplanation(operation).Validation
	graph.Nodes = append(graph.Nodes, DiagramNode{ID: validation, Type: NodeTypeDecision, Label: "Validation (unknown)", Description: validationDescription, OperationID: operation.ID, Evidence: cloneEvidence(validationDescription.Evidence)})
	graph.Edges = append(graph.Edges, DiagramEdge{ID: input + ":" + validation, Type: EdgeTypeSuccess, Source: input, Target: validation, Description: validationDescription, Evidence: cloneEvidence(validationDescription.Evidence)})
	previous := validation
	for index, step := range operation.BusinessProcess {
		id := operationNodeID(operation, fmt.Sprintf("business-process:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeProcess, Label: nonEmpty(step.ID, fmt.Sprintf("Business step %d", index+1)), Description: step.Description, OperationID: operation.ID, Evidence: cloneEvidence(step.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: EdgeTypeSuccess, Source: previous, Target: id, Description: step.Description, Evidence: cloneEvidence(step.Description.Evidence)})
		previous = id
	}
	for _, phase := range implementationPhases(operation.Implementation) {
		id := operationNodeID(operation, "implementation:"+phase.name)
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeProcess, Label: phase.label, Description: statement(phase.label+" implementation evidence", statementConfidence(0, phase.evidence), phase.evidence), OperationID: operation.ID, Evidence: cloneEvidence(phase.evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: EdgeTypeSuccess, Source: previous, Target: id, Description: statement("Implementation flow", statementConfidence(0, phase.evidence), phase.evidence), Evidence: cloneEvidence(phase.evidence)})
		previous = id
	}
	for index, access := range accessDecisions(operation.Access) {
		id := operationNodeID(operation, fmt.Sprintf("decision:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeDecision, Label: access.name, Description: access.statement, OperationID: operation.ID, Evidence: cloneEvidence(access.statement.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: EdgeTypeSuccess, Source: previous, Target: id, Description: access.statement, Evidence: cloneEvidence(access.statement.Evidence)})
		previous = id
	}
	for index, data := range operation.DataAccess {
		id := operationNodeID(operation, fmt.Sprintf("data:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeDatastore, Label: nonEmpty(data.Resource, "unknown datastore"), Description: data.Description, OperationID: operation.ID, Evidence: cloneEvidence(data.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: dataEdgeType(data.Access), Source: previous, Target: id, Label: data.Access, Description: data.Description, Evidence: cloneEvidence(data.Description.Evidence)})
	}
	for index, interaction := range operation.ExternalInteractions {
		id := operationNodeID(operation, fmt.Sprintf("external:%03d", index))
		typeValue := NodeTypeExternalService
		if normalized(interaction.Transport) == "nats" {
			typeValue = NodeTypeEventQueue
		}
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: typeValue, Label: nonEmpty(interaction.Target, "unknown external target"), Description: interaction.Description, OperationID: operation.ID, Evidence: cloneEvidence(interaction.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: interactionEdgeType(interaction), Source: previous, Target: id, Label: interactionLabel(interaction), Description: interaction.Description, Evidence: cloneEvidence(interaction.Description.Evidence)})
	}
	for index, event := range operation.Output.EmittedEvents {
		id := operationNodeID(operation, fmt.Sprintf("event:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeEventQueue, Label: nonEmpty(event.Code, "unknown event"), Description: event.Description, OperationID: operation.ID, Evidence: cloneEvidence(event.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: EdgeTypeEventPublish, Source: previous, Target: id, Label: event.Transport, Description: event.Description, Evidence: cloneEvidence(event.Description.Evidence)})
	}
	output := operationNodeID(operation, "output")
	graph.Nodes = append(graph.Nodes, DiagramNode{ID: output, Type: NodeTypeInputOutput, Label: "Output", Description: businessExplanation(operation).Result, OperationID: operation.ID, Evidence: cloneEvidence(outputResultEvidence(operation.Output))})
	graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + output, Type: EdgeTypeSuccess, Source: previous, Target: output, Description: businessExplanation(operation).Result, Evidence: cloneEvidence(outputResultEvidence(operation.Output))})
	for index, failure := range operation.Errors {
		id := operationNodeID(operation, fmt.Sprintf("error:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeInputOutput, Label: nonEmpty(failure.Code, "Error"), Description: failure.Description, OperationID: operation.ID, Evidence: cloneEvidence(failure.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: previous + ":" + id, Type: EdgeTypeFailure, Source: previous, Target: id, Label: fmt.Sprintf("%d", failure.StatusCode), Description: failure.Description, Evidence: cloneEvidence(failure.Description.Evidence)})
	}
	sortDiagram(&graph)
	return graph
}

// outputResultEvidence mirrors result schema provenance without projecting a
// new business claim. It is intentionally nil-safe.
func outputResultEvidence(output domain.ArchitectureOperationOutput) []domain.ArchitectureEvidence {
	if output.Result == nil {
		return []domain.ArchitectureEvidence{}
	}
	return output.Result.Description.Evidence
}

type implementationPhase struct {
	name, label string
	evidence    []domain.ArchitectureEvidence
}

func implementationPhases(flow domain.ArchitectureImplementationFlow) []implementationPhase {
	values := []implementationPhase{{"router", "Router", flow.Router}, {"handler", "Handler", flow.Handler}, {"use-case", "Use case", flow.UseCases}, {"domain-service", "Domain service", flow.DomainServices}, {"repository", "Repository", flow.Repositories}}
	result := make([]implementationPhase, 0, len(values))
	for _, value := range values {
		if len(value.evidence) > 0 {
			result = append(result, value)
		}
	}
	return result
}

type accessDecision struct {
	name      string
	statement domain.ArchitectureStatement
}

func accessDecisions(access domain.ArchitectureOperationAccess) []accessDecision {
	values := []accessDecision{{"Authentication", access.Authentication}, {"Authorization", access.Authorization}, {"Idempotency", access.Idempotency}}
	result := make([]accessDecision, 0, len(values))
	for _, value := range values {
		if normalized(value.statement.Value) != "" && normalized(value.statement.Value) != "unknown" {
			result = append(result, value)
		}
	}
	return result
}

func inputDescription(operation domain.ArchitectureOperationManifest) domain.ArchitectureStatement {
	if operation.Input.Body != nil {
		return operation.Input.Body.Description
	}
	if len(operation.Input.PathParams)+len(operation.Input.Query)+len(operation.Input.Headers) > 0 {
		return statement("Input fields are declared in the manifest", operation.Confidence, operation.Evidence)
	}
	return unknownStatement("Operation input is not explicitly described")
}

func operationSequence(operation domain.ArchitectureOperationManifest) Diagram {
	graph := emptyDiagram()
	caller := operationNodeID(operation, "sequence:caller")
	service := operationNodeID(operation, "sequence:service")
	graph.Nodes = append(graph.Nodes, DiagramNode{ID: caller, Type: NodeTypeExternalService, Label: "Caller", Description: nonUnknownStatement(operation.Trigger.Description, "Caller is unknown"), OperationID: operation.ID, Evidence: cloneEvidence(operation.Trigger.Description.Evidence)}, DiagramNode{ID: service, Type: NodeTypeServiceHub, Label: "Service", Description: operation.BusinessTask, OperationID: operation.ID, Evidence: cloneEvidence(operation.BusinessTask.Evidence)})
	graph.Edges = append(graph.Edges, DiagramEdge{ID: caller + ":" + service, Type: sequenceEdgeType(operation.Type), Source: caller, Target: service, Label: operationIdentity(operation), Description: operation.BusinessTask, Evidence: cloneEvidence(operation.BusinessTask.Evidence)})
	for index, interaction := range operation.ExternalInteractions {
		id := operationNodeID(operation, fmt.Sprintf("sequence:external:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeExternalService, Label: nonEmpty(interaction.Target, "unknown"), Description: interaction.Description, OperationID: operation.ID, Evidence: cloneEvidence(interaction.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: service + ":" + id, Type: interactionEdgeType(interaction), Source: service, Target: id, Label: interactionLabel(interaction), Description: interaction.Description, Evidence: cloneEvidence(interaction.Description.Evidence)})
	}
	for index, event := range operation.Output.EmittedEvents {
		id := operationNodeID(operation, fmt.Sprintf("sequence:event:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeEventQueue, Label: nonEmpty(event.Code, "unknown event"), Description: event.Description, OperationID: operation.ID, Evidence: cloneEvidence(event.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: service + ":" + id, Type: EdgeTypeEventPublish, Source: service, Target: id, Label: event.Transport, Description: event.Description, Evidence: cloneEvidence(event.Description.Evidence)})
	}
	sortDiagram(&graph)
	return graph
}

func operationContracts(operation domain.ArchitectureOperationManifest) Diagram {
	graph := emptyDiagram()
	operationNode := operationNodeID(operation, "contracts:operation")
	graph.Nodes = append(graph.Nodes, DiagramNode{ID: operationNode, Type: NodeTypeProcess, Label: operation.ID, Description: operation.BusinessTask, OperationID: operation.ID, Evidence: cloneEvidence(operation.BusinessTask.Evidence)})
	if identity := operationIdentity(operation); identity != "unknown" {
		id := operationNodeID(operation, "contracts:trigger")
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeContract, Label: identity, Description: operation.Trigger.Description, OperationID: operation.ID, Evidence: cloneEvidence(operation.Trigger.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: id + ":" + operationNode, Type: sequenceEdgeType(operation.Type), Source: id, Target: operationNode, Description: operation.Trigger.Description, Evidence: cloneEvidence(operation.Trigger.Description.Evidence)})
	}
	for index, event := range operation.Output.EmittedEvents {
		id := operationNodeID(operation, fmt.Sprintf("contracts:event:%03d", index))
		graph.Nodes = append(graph.Nodes, DiagramNode{ID: id, Type: NodeTypeContract, Label: event.Code, Description: event.Description, OperationID: operation.ID, Evidence: cloneEvidence(event.Description.Evidence)})
		graph.Edges = append(graph.Edges, DiagramEdge{ID: operationNode + ":" + id, Type: EdgeTypeEventPublish, Source: operationNode, Target: id, Label: event.Transport, Description: event.Description, Evidence: cloneEvidence(event.Description.Evidence)})
	}
	sortDiagram(&graph)
	return graph
}

func operationNodeID(operation domain.ArchitectureOperationManifest, suffix string) string {
	return "operation:" + operation.ID + ":" + suffix
}

func dataEdgeType(access string) EdgeType {
	if normalized(access) == "read" || normalized(access) == "select" {
		return EdgeTypeDataRead
	}
	return EdgeTypeDataWrite
}

func interactionEdgeType(value domain.ArchitectureExternalInteraction) EdgeType {
	if normalized(value.Transport) == "nats" {
		if relationInbound(value.Direction) {
			return EdgeTypeEventConsume
		}
		return EdgeTypeEventPublish
	}
	if normalized(value.Transport) == "http" {
		return EdgeTypeHTTPRequest
	}
	return EdgeTypeTechnical
}

func interactionLabel(value domain.ArchitectureExternalInteraction) string {
	return strings.TrimSpace(strings.Join([]string{value.Transport, value.Contract}, " "))
}

func sequenceEdgeType(kind domain.ArchitectureOperationType) EdgeType {
	switch kind {
	case domain.ArchitectureOperationHTTP:
		return EdgeTypeHTTPRequest
	case domain.ArchitectureOperationNATSRequestReply:
		return EdgeTypeNATSRequestReply
	case domain.ArchitectureOperationNATSEventSubscriber:
		return EdgeTypeEventConsume
	default:
		return EdgeTypeTechnical
	}
}
