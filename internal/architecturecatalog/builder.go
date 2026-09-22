// Package architecturecatalog builds the manifest-backed Architecture CURRENT
// catalog. It has no Mermaid, discovery, persistence, or topology mutation
// responsibilities.
package architecturecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/contractref"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type Builder struct{}

// Build creates a deterministic CURRENT projection. Input ordering is never
// retained, and the supplied source values are not modified.
func (Builder) Build(ctx context.Context, sources []domain.ArchitectureCatalogSource) (domain.ArchitectureCatalog, error) {
	sources = append([]domain.ArchitectureCatalogSource(nil), sources...)
	sort.Slice(sources, func(i, j int) bool {
		return sourceKey(sources[i]) < sourceKey(sources[j])
	})

	catalog := domain.ArchitectureCatalog{
		Mode: domain.ArchitectureCatalogModeCurrent,
		Platform: domain.ArchitectureCatalogPlatform{
			Services:     []domain.ArchitectureCatalogService{},
			Relations:    []domain.ArchitectureCatalogRelation{},
			Completeness: emptyCompleteness(),
		},
	}
	seenProjects := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		if err := validateSource(source); err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		projectID := source.Topology.Project.ID
		if _, exists := seenProjects[projectID]; exists {
			return domain.ArchitectureCatalog{}, fmt.Errorf("duplicate architecture catalog project %q: %w", projectID, domain.ErrConflict)
		}
		seenProjects[projectID] = struct{}{}

		service, err := buildService(source)
		if err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		catalog.Platform.Services = append(catalog.Platform.Services, service)
		addCompleteness(&catalog.Platform.Completeness, service.Completeness)
	}

	sort.Slice(catalog.Platform.Services, func(i, j int) bool {
		left, right := catalog.Platform.Services[i].Source, catalog.Platform.Services[j].Source
		return stableKey(left.ProjectName, left.ProjectID) < stableKey(right.ProjectName, right.ProjectID)
	})
	catalog.Platform.Completeness.OperationKinds = operationKindCounts(catalog.Platform.Completeness.OperationKinds)
	catalog.Platform.Relations = buildRelations(catalog.Platform.Services)

	fingerprint, err := fingerprint(catalog)
	if err != nil {
		return domain.ArchitectureCatalog{}, err
	}
	catalog.Fingerprint = fingerprint
	return catalog, nil
}

func validateSource(source domain.ArchitectureCatalogSource) error {
	topology := source.Topology
	if topology.Project.ID == "" || topology.Snapshot.ID == "" ||
		topology.Snapshot.ProjectID != topology.Project.ID ||
		topology.Report.ProjectID != topology.Project.ID ||
		topology.Report.CommitSHA != topology.Snapshot.CommitSHA {
		return fmt.Errorf("architecture catalog source does not match project snapshot: %w", domain.ErrConflict)
	}
	if source.ServiceManifest.Schema == "" {
		if len(source.Operations) != 0 {
			return fmt.Errorf("uncovered project %q includes operation manifests: %w", topology.Project.ID, domain.ErrConflict)
		}
		return nil
	}
	if source.ServiceManifest.ID == "" {
		return fmt.Errorf("architecture catalog service manifest has no id: %w", domain.ErrValidation)
	}
	for _, operation := range source.Operations {
		if operation.ID == "" || operation.ServiceID != source.ServiceManifest.ID {
			return fmt.Errorf("architecture catalog operation does not belong to service manifest %q: %w", source.ServiceManifest.ID, domain.ErrConflict)
		}
		if !isOperationKind(operation.Type) {
			return fmt.Errorf("unsupported architecture operation type %q: %w", operation.Type, domain.ErrValidation)
		}
	}
	return nil
}

func buildService(source domain.ArchitectureCatalogSource) (domain.ArchitectureCatalogService, error) {
	status := sourceStatus(source.Topology)
	result := domain.ArchitectureCatalogService{
		Source:       status,
		Groups:       []domain.ArchitectureCatalogEndpointGroup{},
		Ungrouped:    []domain.ArchitectureCatalogOperation{},
		Completeness: emptyCompleteness(),
	}
	result.Completeness.SourceCount = 1
	if source.ServiceManifest.Schema == "" {
		result.Completeness.UncoveredServiceCount = 1
		applyDiscoveredOperations(&result.Completeness, source.DiscoveredOperations, nil, nil)
		result.Completeness.OperationKinds = operationKindCounts(result.Completeness.OperationKinds)
		return result, nil
	}

	manifest, err := cloneServiceManifest(source.ServiceManifest)
	if err != nil {
		return domain.ArchitectureCatalogService{}, err
	}
	result.Covered, result.Manifest = true, &manifest
	result.Completeness.CoveredServiceCount = 1
	result.Completeness.DeclaredOperationManifestCount = len(manifest.OperationManifests)
	result.Completeness.ParsedOperationManifestCount = len(source.Operations)
	if expected, observed := len(manifest.OperationManifests), len(source.Operations); expected > observed {
		result.Completeness.MissingDeclaredOperationManifestCount = expected - observed
	} else {
		result.Completeness.UnexpectedParsedOperationManifestCount = observed - expected
	}

	operations := make(map[string]domain.ArchitectureCatalogOperation, len(source.Operations))
	for _, operation := range source.Operations {
		if _, exists := operations[operation.ID]; exists {
			return domain.ArchitectureCatalogService{}, fmt.Errorf("duplicate operation manifest %q for project %q: %w", operation.ID, status.ProjectID, domain.ErrConflict)
		}
		cloned, err := cloneOperationManifest(operation)
		if err != nil {
			return domain.ArchitectureCatalogService{}, err
		}
		operations[operation.ID] = domain.ArchitectureCatalogOperation{Manifest: cloned}
	}
	manifestSignatures := make(map[string]int, len(operations))
	manifestBySourceType := make(map[string][]string)
	for _, operation := range operations {
		if signature, ok := manifestOperationSignature(operation.Manifest); ok {
			manifestSignatures[signature]++
			if operation.Manifest.Type == domain.ArchitectureOperationWorker || operation.Manifest.Type == domain.ArchitectureOperationScheduled {
				for _, evidence := range operation.Manifest.Evidence {
					key := catalogOperationSourceTypeKey(operation.Manifest.Type, evidence.SourcePath)
					if key != "" && !containsCatalogSignature(manifestBySourceType[key], signature) {
						manifestBySourceType[key] = append(manifestBySourceType[key], signature)
					}
				}
			}
		}
	}
	applyDiscoveredOperations(&result.Completeness, source.DiscoveredOperations, manifestSignatures, manifestBySourceType)

	grouped := make(map[string]struct{}, len(operations))
	for _, declared := range manifest.EndpointGroups {
		group := domain.ArchitectureCatalogEndpointGroup{
			ID: declared.ID, Name: declared.Name, Description: declared.Description,
			Operations: []domain.ArchitectureCatalogOperation{}, MissingOperationIDs: []string{},
		}
		for _, operationID := range declared.Operations {
			result.Completeness.GroupOperationReferenceCount++
			operation, exists := operations[operationID]
			if !exists {
				group.MissingOperationIDs = append(group.MissingOperationIDs, operationID)
				result.Completeness.MissingGroupOperationCount++
				continue
			}
			grouped[operationID] = struct{}{}
			group.Operations = append(group.Operations, operation)
		}
		sort.Slice(group.Operations, func(i, j int) bool { return operationKey(group.Operations[i]) < operationKey(group.Operations[j]) })
		sort.Strings(group.MissingOperationIDs)
		result.Groups = append(result.Groups, group)
	}
	for id, operation := range operations {
		if _, exists := grouped[id]; !exists {
			result.Ungrouped = append(result.Ungrouped, operation)
		}
	}
	sort.Slice(result.Groups, func(i, j int) bool {
		return stableKey(result.Groups[i].ID, result.Groups[i].Name) < stableKey(result.Groups[j].ID, result.Groups[j].Name)
	})
	sort.Slice(result.Ungrouped, func(i, j int) bool { return operationKey(result.Ungrouped[i]) < operationKey(result.Ungrouped[j]) })
	result.Completeness.OperationKinds = operationKindCounts(result.Completeness.OperationKinds)
	return result, nil
}

func sourceStatus(source domain.TopologySource) domain.ArchitectureCatalogSourceStatus {
	project, snapshot, report := source.Project, source.Snapshot, source.Report
	// CURRENT is bound to the immutable persisted snapshot and its discovery
	// checksum. A local source may be dirty because generated architecture
	// artifacts are intentionally untracked, or because an owner is inspecting
	// a stable local change before committing it. Keep that state visible below,
	// but do not turn an otherwise exact snapshot/topology match into a false
	// non-CURRENT classification.
	current := snapshot.CommitSHA == report.CommitSHA && snapshot.ContentChecksum == report.ContentChecksum
	if project.HeadCommit != "" {
		current = current && project.HeadCommit == snapshot.CommitSHA
	}
	return domain.ArchitectureCatalogSourceStatus{
		ProjectID: project.ID, ProjectName: project.Name, ProjectStatus: project.Status, RepositoryRole: project.RepositoryRole,
		SnapshotID: snapshot.ID, SnapshotStatus: snapshot.Status, CommitSHA: snapshot.CommitSHA, Branch: snapshot.Branch,
		ContentChecksum: snapshot.ContentChecksum, DiscoverySchemaVersion: report.SchemaVersion,
		SourceCurrent: current, IsDirty: project.IsDirty || snapshot.IsDirty || report.IsDirty,
	}
}

func emptyCompleteness() domain.ArchitectureCatalogCompleteness {
	return domain.ArchitectureCatalogCompleteness{OperationKinds: operationKindCounts(nil)}
}

func addCompleteness(total *domain.ArchitectureCatalogCompleteness, value domain.ArchitectureCatalogCompleteness) {
	total.SourceCount += value.SourceCount
	total.CoveredServiceCount += value.CoveredServiceCount
	total.UncoveredServiceCount += value.UncoveredServiceCount
	total.TotalDiscoveredOperations += value.TotalDiscoveredOperations
	total.HTTPOperations += value.HTTPOperations
	total.NATSRequestReplyOperations += value.NATSRequestReplyOperations
	total.EventOperations += value.EventOperations
	total.WorkerOperations += value.WorkerOperations
	total.ScheduledOperations += value.ScheduledOperations
	total.OperationsWithManifests += value.OperationsWithManifests
	total.OperationsMissingManifests += value.OperationsMissingManifests
	total.OperationsBlocked += value.OperationsBlocked
	total.DeclaredOperationManifestCount += value.DeclaredOperationManifestCount
	total.ParsedOperationManifestCount += value.ParsedOperationManifestCount
	total.MissingDeclaredOperationManifestCount += value.MissingDeclaredOperationManifestCount
	total.UnexpectedParsedOperationManifestCount += value.UnexpectedParsedOperationManifestCount
	total.ManifestOperationsWithoutDiscovery += value.ManifestOperationsWithoutDiscovery
	total.GroupOperationReferenceCount += value.GroupOperationReferenceCount
	total.MissingGroupOperationCount += value.MissingGroupOperationCount
	for _, count := range value.OperationKinds {
		incrementKindBy(total, count.Type, count.Count)
	}
}

func incrementKind(completeness *domain.ArchitectureCatalogCompleteness, kind domain.ArchitectureOperationType) {
	incrementKindBy(completeness, kind, 1)
}

func applyDiscoveredOperations(completeness *domain.ArchitectureCatalogCompleteness, discovered []domain.DiscoveredOperation, manifests map[string]int, manifestsBySourceType map[string][]string) {
	matched := make(map[string]struct{}, len(manifests))
	for _, operation := range discovered {
		completeness.TotalDiscoveredOperations++
		incrementDiscoveredKind(completeness, operation.Type)
		signature, valid := discoveredOperationSignature(operation)
		if !valid {
			completeness.OperationsBlocked++
			continue
		}
		matches := manifests[signature]
		switch matches {
		case 0:
			if fallback := uniqueCatalogSourceOperationManifest(operation, manifestsBySourceType, manifests, matched); fallback != "" {
				completeness.OperationsWithManifests++
				matched[fallback] = struct{}{}
				continue
			}
			completeness.OperationsMissingManifests++
		case 1:
			completeness.OperationsWithManifests++
			matched[signature] = struct{}{}
		default:
			// Multiple manifests make this code observation ambiguous. Keep it
			// visible as blocked rather than choosing a declaration arbitrarily.
			completeness.OperationsBlocked++
			matched[signature] = struct{}{}
		}
	}
	for signature, count := range manifests {
		if _, exists := matched[signature]; !exists {
			completeness.ManifestOperationsWithoutDiscovery += count
		}
	}
}

// uniqueCatalogSourceOperationManifest carries the strict audit fallback into
// the persisted CURRENT projection. Worker and scheduled implementation names
// can differ from the evidence-backed business identity in a manifest; when a
// source path identifies exactly one unused declaration, it is a proven match.
// HTTP and NATS contracts remain signature-only.
func uniqueCatalogSourceOperationManifest(operation domain.DiscoveredOperation, bySourceType map[string][]string, manifests map[string]int, matched map[string]struct{}) string {
	if operation.Type != domain.ArchitectureOperationWorker && operation.Type != domain.ArchitectureOperationScheduled {
		return ""
	}
	candidates := bySourceType[catalogOperationSourceTypeKey(operation.Type, operation.SourcePath)]
	result := ""
	for _, candidate := range candidates {
		if manifests[candidate] != 1 {
			continue
		}
		if _, exists := matched[candidate]; exists {
			continue
		}
		if result != "" {
			return ""
		}
		result = candidate
	}
	return result
}

func catalogOperationSourceTypeKey(kind domain.ArchitectureOperationType, sourcePath string) string {
	sourcePath = filepath.ToSlash(strings.TrimSpace(sourcePath))
	if sourcePath == "" {
		return ""
	}
	return stableKey(string(kind), sourcePath)
}

func containsCatalogSignature(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func incrementDiscoveredKind(completeness *domain.ArchitectureCatalogCompleteness, kind domain.ArchitectureOperationType) {
	incrementKind(completeness, kind)
	switch kind {
	case domain.ArchitectureOperationHTTP:
		completeness.HTTPOperations++
	case domain.ArchitectureOperationNATSRequestReply:
		completeness.NATSRequestReplyOperations++
	case domain.ArchitectureOperationNATSEventSubscriber:
		completeness.EventOperations++
	case domain.ArchitectureOperationWorker:
		completeness.WorkerOperations++
	case domain.ArchitectureOperationScheduled:
		completeness.ScheduledOperations++
	}
}

func discoveredOperationSignature(operation domain.DiscoveredOperation) (string, bool) {
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		method, path, ok := contractref.HTTP(operation.Method + " " + operation.Path)
		if !ok {
			return "", false
		}
		return stableKey(string(operation.Type), method, path), true
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		subject, ok := contractref.EventSubject(operation.Subject)
		if !ok {
			return "", false
		}
		return stableKey(string(operation.Type), subject), true
	case domain.ArchitectureOperationWorker:
		name := strings.TrimSpace(operation.Name)
		return stableKey(string(operation.Type), name), name != ""
	case domain.ArchitectureOperationScheduled:
		name, schedule := strings.TrimSpace(operation.Name), strings.TrimSpace(operation.Schedule)
		return stableKey(string(operation.Type), name, schedule), name != "" && schedule != ""
	default:
		return "", false
	}
}

func manifestOperationSignature(operation domain.ArchitectureOperationManifest) (string, bool) {
	identity := operation.Identity
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		if identity.HTTP == nil {
			return "", false
		}
		method, path, ok := contractref.HTTP(identity.HTTP.Method + " " + identity.HTTP.Path)
		if !ok {
			return "", false
		}
		return stableKey(string(operation.Type), method, path), true
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		if identity.NATS == nil {
			return "", false
		}
		subject, ok := contractref.EventSubject(identity.NATS.Subject)
		if !ok {
			return "", false
		}
		return stableKey(string(operation.Type), subject), true
	case domain.ArchitectureOperationWorker:
		if identity.Worker == nil {
			return "", false
		}
		name := strings.TrimSpace(identity.Worker.Name)
		return stableKey(string(operation.Type), name), name != ""
	case domain.ArchitectureOperationScheduled:
		if identity.Scheduled == nil {
			return "", false
		}
		name, schedule := strings.TrimSpace(identity.Scheduled.Name), strings.TrimSpace(identity.Scheduled.Schedule)
		return stableKey(string(operation.Type), name, schedule), name != "" && schedule != ""
	default:
		return "", false
	}
}

func incrementKindBy(completeness *domain.ArchitectureCatalogCompleteness, kind domain.ArchitectureOperationType, delta int) {
	for i := range completeness.OperationKinds {
		if completeness.OperationKinds[i].Type == kind {
			completeness.OperationKinds[i].Count += delta
			return
		}
	}
	completeness.OperationKinds = append(completeness.OperationKinds, domain.ArchitectureCatalogOperationCount{Type: kind, Count: delta})
}

func operationKindCounts(existing []domain.ArchitectureCatalogOperationCount) []domain.ArchitectureCatalogOperationCount {
	counts := make(map[domain.ArchitectureOperationType]int, len(existing))
	for _, count := range existing {
		counts[count.Type] += count.Count
	}
	result := make([]domain.ArchitectureCatalogOperationCount, 0, len(allOperationKinds))
	for _, kind := range allOperationKinds {
		result = append(result, domain.ArchitectureCatalogOperationCount{Type: kind, Count: counts[kind]})
	}
	return result
}

var allOperationKinds = []domain.ArchitectureOperationType{
	domain.ArchitectureOperationHTTP,
	domain.ArchitectureOperationNATSRequestReply,
	domain.ArchitectureOperationNATSEventSubscriber,
	domain.ArchitectureOperationWorker,
	domain.ArchitectureOperationScheduled,
}

func isOperationKind(value domain.ArchitectureOperationType) bool {
	for _, kind := range allOperationKinds {
		if value == kind {
			return true
		}
	}
	return false
}

// buildRelations projects only declarations present in the cloned manifest
// hierarchy. In particular, it does not use fuzzy service-name matching: a
// target is internal only when it exactly identifies one catalog service, and
// a contract/event is paired only on its exact transport/code tuple.
func buildRelations(services []domain.ArchitectureCatalogService) []domain.ArchitectureCatalogRelation {
	identities := make(map[string]map[string]struct{}, len(services)*4)
	produced := make(map[string][]manifestReference)
	consumed := make(map[string][]manifestReference)
	published := make(map[string][]manifestReference)
	subscribed := make(map[string][]manifestReference)

	for _, service := range services {
		if !service.Covered || service.Manifest == nil {
			continue
		}
		manifest := service.Manifest
		for _, value := range []string{service.Source.ProjectID, service.Source.ProjectName, manifest.ID, manifest.Identity.Name} {
			addIdentity(identities, value, service.Source.ProjectID)
		}
		for _, reference := range manifest.ProducedContracts {
			produced[referenceKey(reference)] = append(produced[referenceKey(reference)], serviceReference(service, reference, ""))
		}
		for _, reference := range manifest.ConsumedContracts {
			consumed[referenceKey(reference)] = append(consumed[referenceKey(reference)], serviceReference(service, reference, ""))
		}
		for _, reference := range manifest.PublishedEvents {
			published[referenceKey(reference)] = append(published[referenceKey(reference)], serviceReference(service, reference, ""))
		}
		for _, reference := range manifest.SubscribedEvents {
			subscribed[referenceKey(reference)] = append(subscribed[referenceKey(reference)], serviceReference(service, reference, ""))
		}
		for _, operation := range serviceOperations(service) {
			// A NATS event subscriber is itself a concrete operation-level
			// subscription. Add it to the same exact subject index even when the
			// service summary omitted a duplicate declaration.
			if operation.Manifest.Type == domain.ArchitectureOperationNATSEventSubscriber && operation.Manifest.Identity.NATS != nil {
				reference := domain.ArchitectureContractReference{
					Transport: "nats", Code: operation.Manifest.Identity.NATS.Subject, Direction: "consumed",
					Description: domain.ArchitectureStatement{Value: "NATS event subscriber", Confidence: operation.Manifest.Confidence, Evidence: append([]domain.ArchitectureEvidence(nil), operation.Manifest.Evidence...)},
				}
				subscribed[referenceKey(reference)] = append(subscribed[referenceKey(reference)], manifestReference{ProjectID: service.Source.ProjectID, OperationID: operation.Manifest.ID, Reference: reference, ServiceEvidence: append([]domain.ArchitectureEvidence(nil), manifest.Evidence...)})
			}
		}
	}

	relations := make([]domain.ArchitectureCatalogRelation, 0)
	seen := make(map[string]int)
	add := func(relation domain.ArchitectureCatalogRelation) {
		relation.Transport = strings.TrimSpace(relation.Transport)
		relation.Contract = strings.TrimSpace(relation.Contract)
		relation.Direction = relationDirection(relation.Direction, "outbound")
		if relation.TargetProjectID == "" {
			relation.ExternalTarget = explicitExternalTarget(relation.ExternalTarget)
		}
		relation.Evidence = uniqueEvidence(relation.Evidence)
		if len(relation.Evidence) == 0 {
			relation.Evidence = uniqueEvidence(relation.Description.Evidence)
		}
		relation.Confidence = relation.Description.Confidence
		key := relationKey(relation)
		if index, exists := seen[key]; exists {
			relations[index].Evidence = uniqueEvidence(append(relations[index].Evidence, relation.Evidence...))
			if relation.Confidence > relations[index].Confidence {
				relations[index].Confidence = relation.Confidence
			}
			return
		}
		relation.ID = relationID(key)
		seen[key] = len(relations)
		relations = append(relations, relation)
	}

	for _, service := range services {
		if !service.Covered || service.Manifest == nil {
			continue
		}
		manifest := service.Manifest
		for _, dependency := range manifest.OutboundDependencies {
			add(directManifestRelation(service, "", domain.ArchitectureCatalogRelationOutboundDependency, dependency, identities))
		}
		for _, operation := range serviceOperations(service) {
			for _, interaction := range operation.Manifest.ExternalInteractions {
				add(directManifestRelation(service, operation.Manifest.ID, domain.ArchitectureCatalogRelationOperationInteraction, interaction, identities))
			}
			for _, event := range operation.Manifest.Output.EmittedEvents {
				addEventProducerRelations(add, service, operation.Manifest.ID, event, subscribed)
			}
		}
		for _, reference := range manifest.ConsumedContracts {
			addConsumerRelations(add, serviceReference(service, reference, ""), produced, domain.ArchitectureCatalogRelationContract, "provider")
		}
		for _, reference := range manifest.ProducedContracts {
			if len(consumed[referenceKey(reference)]) == 0 {
				addProducerUnknownRelation(add, serviceReference(service, reference, ""), domain.ArchitectureCatalogRelationContract, "consumer")
			}
		}
		for _, reference := range manifest.SubscribedEvents {
			addConsumerRelations(add, serviceReference(service, reference, ""), published, domain.ArchitectureCatalogRelationEvent, "publisher")
		}
		for _, reference := range manifest.PublishedEvents {
			if len(subscribed[referenceKey(reference)]) == 0 {
				addProducerUnknownRelation(add, serviceReference(service, reference, ""), domain.ArchitectureCatalogRelationEvent, "subscriber")
			}
		}
	}

	sort.Slice(relations, func(i, j int) bool { return relationKey(relations[i]) < relationKey(relations[j]) })
	return relations
}

type manifestReference struct {
	ProjectID       string
	OperationID     string
	Reference       domain.ArchitectureContractReference
	ServiceEvidence []domain.ArchitectureEvidence
}

func serviceReference(service domain.ArchitectureCatalogService, reference domain.ArchitectureContractReference, operationID string) manifestReference {
	var evidence []domain.ArchitectureEvidence
	if service.Manifest != nil {
		evidence = append(evidence, service.Manifest.Evidence...)
	}
	return manifestReference{ProjectID: service.Source.ProjectID, OperationID: operationID, Reference: reference, ServiceEvidence: evidence}
}

func serviceOperations(service domain.ArchitectureCatalogService) []domain.ArchitectureCatalogOperation {
	byID := make(map[string]domain.ArchitectureCatalogOperation)
	for _, group := range service.Groups {
		for _, operation := range group.Operations {
			byID[operation.Manifest.ID] = operation
		}
	}
	for _, operation := range service.Ungrouped {
		byID[operation.Manifest.ID] = operation
	}
	result := make([]domain.ArchitectureCatalogOperation, 0, len(byID))
	for _, operation := range byID {
		result = append(result, operation)
	}
	sort.Slice(result, func(i, j int) bool { return operationKey(result[i]) < operationKey(result[j]) })
	return result
}

func addIdentity(index map[string]map[string]struct{}, value, projectID string) {
	key := identityKey(value)
	if key == "" {
		return
	}
	if index[key] == nil {
		index[key] = make(map[string]struct{})
	}
	index[key][projectID] = struct{}{}
}

func resolveProject(index map[string]map[string]struct{}, target string) string {
	projects := index[identityKey(target)]
	if len(projects) != 1 {
		return ""
	}
	for projectID := range projects {
		return projectID
	}
	return ""
}

func directManifestRelation(service domain.ArchitectureCatalogService, operationID string, kind domain.ArchitectureCatalogRelationType, interaction domain.ArchitectureExternalInteraction, identities map[string]map[string]struct{}) domain.ArchitectureCatalogRelation {
	target := resolveProject(identities, interaction.Target)
	return domain.ArchitectureCatalogRelation{
		Type: kind, SourceProjectID: service.Source.ProjectID, TargetProjectID: target,
		ExternalTarget: interaction.Target, OperationID: operationID, Transport: interaction.Transport,
		Contract: interaction.Contract, Direction: interaction.Direction, Description: interaction.Description,
		Evidence: append([]domain.ArchitectureEvidence(nil), interaction.Description.Evidence...),
	}
}

func addConsumerRelations(add func(domain.ArchitectureCatalogRelation), consumer manifestReference, providers map[string][]manifestReference, kind domain.ArchitectureCatalogRelationType, unknownRole string) {
	matches := providers[referenceKey(consumer.Reference)]
	if len(matches) == 0 {
		add(domain.ArchitectureCatalogRelation{
			Type: kind, SourceProjectID: consumer.ProjectID, ExternalTarget: unknownCounterpart(unknownRole, consumer.Reference.Code),
			OperationID: consumer.OperationID, Transport: consumer.Reference.Transport, Contract: consumer.Reference.Code,
			Direction: "inbound", Description: consumer.Reference.Description, Evidence: referenceEvidence(consumer),
		})
		return
	}
	for _, provider := range matches {
		add(domain.ArchitectureCatalogRelation{
			Type: kind, SourceProjectID: consumer.ProjectID, TargetProjectID: provider.ProjectID,
			OperationID: consumer.OperationID, Transport: consumer.Reference.Transport, Contract: consumer.Reference.Code,
			Direction: "inbound", Description: consumer.Reference.Description,
			Evidence: append(referenceEvidence(consumer), referenceEvidence(provider)...),
		})
	}
}

func addProducerUnknownRelation(add func(domain.ArchitectureCatalogRelation), producer manifestReference, kind domain.ArchitectureCatalogRelationType, unknownRole string) {
	add(domain.ArchitectureCatalogRelation{
		Type: kind, SourceProjectID: producer.ProjectID, ExternalTarget: unknownCounterpart(unknownRole, producer.Reference.Code),
		OperationID: producer.OperationID, Transport: producer.Reference.Transport, Contract: producer.Reference.Code,
		Direction: "outbound", Description: producer.Reference.Description, Evidence: referenceEvidence(producer),
	})
}

func addEventProducerRelations(add func(domain.ArchitectureCatalogRelation), service domain.ArchitectureCatalogService, operationID string, event domain.ArchitectureContractReference, subscribers map[string][]manifestReference) {
	producer := serviceReference(service, event, operationID)
	if len(subscribers[referenceKey(event)]) == 0 {
		addProducerUnknownRelation(add, producer, domain.ArchitectureCatalogRelationEvent, "subscriber")
		return
	}
	// The reciprocal subscription assertion emits the paired edge, keeping one
	// relation per provider/consumer pair. This branch remains for an
	// operation-level emitted event whose consumer does not have a service
	// summary counterpart.
	for _, subscriber := range subscribers[referenceKey(event)] {
		add(domain.ArchitectureCatalogRelation{
			Type: domain.ArchitectureCatalogRelationEvent, SourceProjectID: subscriber.ProjectID, TargetProjectID: producer.ProjectID,
			OperationID: subscriber.OperationID, Transport: event.Transport, Contract: event.Code, Direction: "inbound",
			Description: subscriber.Reference.Description, Evidence: append(referenceEvidence(subscriber), referenceEvidence(producer)...),
		})
	}
}

func referenceKey(reference domain.ArchitectureContractReference) string {
	return stableKey(strings.ToLower(strings.TrimSpace(reference.Transport)), strings.ToLower(strings.TrimSpace(reference.Code)))
}

func identityKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func relationDirection(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func explicitExternalTarget(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "unknown"
}

func unknownCounterpart(role, contract string) string {
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return "unknown " + role
	}
	return "unknown " + role + " for contract " + contract
}

func referenceEvidence(reference manifestReference) []domain.ArchitectureEvidence {
	evidence := append([]domain.ArchitectureEvidence(nil), reference.Reference.Description.Evidence...)
	if len(evidence) == 0 {
		evidence = append(evidence, reference.ServiceEvidence...)
	}
	return evidence
}

func uniqueEvidence(values []domain.ArchitectureEvidence) []domain.ArchitectureEvidence {
	result := make([]domain.ArchitectureEvidence, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := stableKey(value.SourcePath, value.Symbol, fmt.Sprint(value.StartLine), fmt.Sprint(value.EndLine), value.Checksum)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func relationKey(relation domain.ArchitectureCatalogRelation) string {
	return stableKey(string(relation.Type), relation.SourceProjectID, relation.TargetProjectID, relation.ExternalTarget, relation.OperationID, relation.Transport, relation.Contract, relation.Direction)
}

func relationID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "rel-" + hex.EncodeToString(sum[:8])
}

func fingerprint(catalog domain.ArchitectureCatalog) (string, error) {
	catalog.Fingerprint = ""
	content, err := json.Marshal(catalog)
	if err != nil {
		return "", fmt.Errorf("marshal architecture catalog fingerprint: %w", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

func sourceKey(source domain.ArchitectureCatalogSource) string {
	return stableKey(source.Topology.Project.Name, source.Topology.Project.ID)
}

func operationKey(operation domain.ArchitectureCatalogOperation) string {
	identity := operation.Manifest.Identity
	parts := []string{string(operation.Manifest.Type)}
	if identity.HTTP != nil {
		parts = append(parts, identity.HTTP.Method, identity.HTTP.Path)
	}
	if identity.NATS != nil {
		parts = append(parts, identity.NATS.Subject, identity.NATS.Role)
	}
	if identity.Worker != nil {
		parts = append(parts, identity.Worker.Name)
	}
	if identity.Scheduled != nil {
		parts = append(parts, identity.Scheduled.Name, identity.Scheduled.Schedule)
	}
	parts = append(parts, operation.Manifest.ID)
	return stableKey(parts...)
}

func stableKey(values ...string) string { return strings.Join(values, "\x00") }

func cloneServiceManifest(value domain.ArchitectureServiceManifest) (domain.ArchitectureServiceManifest, error) {
	var clone domain.ArchitectureServiceManifest
	if err := cloneManifest(value, &clone); err != nil {
		return domain.ArchitectureServiceManifest{}, err
	}
	return clone, nil
}

func cloneOperationManifest(value domain.ArchitectureOperationManifest) (domain.ArchitectureOperationManifest, error) {
	var clone domain.ArchitectureOperationManifest
	if err := cloneManifest(value, &clone); err != nil {
		return domain.ArchitectureOperationManifest{}, err
	}
	return clone, nil
}

func cloneManifest(value, target any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("copy architecture catalog manifest: %w", err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("copy architecture catalog manifest: %w", err)
	}
	return nil
}
