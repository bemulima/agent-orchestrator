package architecturecatalog

import (
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"sort"
)

// These exact owner literals were reviewed against committed declaration
// evidence. Unknown targets and configurable provider aggregates stay unresolved.
var fleetExternalTargets = map[string]bool{
	"postgres": true, "nats": true, "clamav": true, "configured OpenAI-compatible API": true, "Codex CLI": true, "PostgreSQL": true, "Temporal": true, "Codex SDK runner": true, "configured GitHub work-item gateway": true, "Telegram Bot API": true, "redis": true, "elasticsearch": true, "docker": true, "course database": true, "NATS JetStream": true, "sql-runtime": true, "tarantool": true, "MinIO": true, "local Git repository": true, "workspace runtime command": true, "LEARNING_PLANNER_EVENTS": true, "validation workspace": true, "request command array": true, "practice catalog database": true, "PHP CLI": true, "repository analyze.php": true, "PHP CLI and analyze.php": true, "shared S3-compatible object store": true, "Docker Compose": true, "Docker Compose and Traefik preview": true, "Tarantool": true, "NATS": true, "clickhouse": true, "learning_action.closed": true, "Student-owned database": true, "NATS and JetStream": true, "configured_tarantool_endpoint": true, "telegram-bot-api": true,
}

// ResolveFleetExternals resolves only direct, evidence-backed owner assertions.
// It never promotes an unmatched provider/consumer inference into a resource.
func ResolveFleetExternals(graph *domain.ArchitectureGraph, catalog domain.ArchitectureCatalog) {
	authored := map[string]bool{}
	for _, relation := range catalog.Platform.Relations {
		if relation.TargetReferenceID == "" && fleetExternalTargets[relation.ExternalTarget] && relation.Confidence > 0 && len(relation.Evidence) > 0 && relation.Transport != "" && relation.Transport != "unknown" && (relation.Type == domain.ArchitectureCatalogRelationOutboundDependency || relation.Type == domain.ArchitectureCatalogRelationOperationInteraction) {
			authored[relation.EdgeID] = true
		}
	}
	external := map[string]domain.ArchitectureGraphReference{}
	resolvedEdges := map[string]bool{}
	for i := range graph.Edges {
		edge := &graph.Edges[i]
		if !authored[edge.EdgeID] {
			continue
		}
		resolvedEdges[edge.EdgeID] = true
		identity := "external:" + edge.Transport + ":" + edge.ExternalTarget
		refID := StableReferenceID(identity, edge.ExternalTarget)
		ref := external[refID]
		ref.ReferenceID = refID
		ref.ReferenceKind = "external"
		ref.SourceIdentity = identity
		ref.ManifestID = edge.ExternalTarget
		ref.RepositoryRole = domain.RepositoryRoleInfrastructure
		ref.Covered = true
		ref.SourceCurrent = true
		ref.DeclarationPins = sortedGraphPins(append(ref.DeclarationPins, edge.DeclarationPins...))
		if len(ref.DeclarationPins) > 0 {
			ref.CommitSHA = ref.DeclarationPins[0].CommitSHA
		}
		external[refID] = ref
		edge.TargetReferenceID = refID
		edge.EdgeID = StableEdgeID(edge.SourceReferenceID, string(edge.Relation), edge.TargetReferenceID, edge.ExternalTarget, edge.OperationID, edge.Transport, edge.Contract, edge.Direction)
	}
	for _, ref := range external {
		graph.References = append(graph.References, ref)
	}
	diagnostics := graph.Diagnostics[:0]
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "EDGE_TARGET_UNRESOLVED" && resolvedEdges[diagnostic.EdgeID] {
			continue
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	graph.Diagnostics = diagnostics
	sort.Slice(graph.References, func(i, j int) bool { return graph.References[i].ReferenceID < graph.References[j].ReferenceID })
	sort.Slice(graph.Edges, func(i, j int) bool { return graph.Edges[i].EdgeID < graph.Edges[j].EdgeID })
}

// CountUnmatchedInterfaceMetadata preserves opaque interface descriptors as debt,
// rather than treating a missing matching label as an asserted unknown endpoint.
func CountUnmatchedInterfaceMetadata(sources []domain.ArchitectureCatalogSource) int {
	produced, consumed, published, subscribed := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	for _, source := range sources {
		for _, r := range source.ServiceManifest.ProducedContracts {
			produced[referenceKey(r)]++
		}
		for _, r := range source.ServiceManifest.ConsumedContracts {
			consumed[referenceKey(r)]++
		}
		for _, r := range source.ServiceManifest.PublishedEvents {
			published[referenceKey(r)]++
		}
		for _, r := range source.ServiceManifest.SubscribedEvents {
			subscribed[referenceKey(r)]++
		}
		for _, op := range source.Operations {
			for _, r := range op.Output.EmittedEvents {
				published[referenceKey(r)]++
			}
			if op.Type == domain.ArchitectureOperationNATSEventSubscriber && op.Identity.NATS != nil {
				subscribed[referenceKey(domain.ArchitectureContractReference{Transport: "nats", Code: op.Identity.NATS.Subject})]++
			}
		}
	}
	count := 0
	for k, n := range produced {
		if consumed[k] == 0 {
			count += n
		}
	}
	for k, n := range consumed {
		if produced[k] == 0 {
			count += n
		}
	}
	for k, n := range published {
		if subscribed[k] == 0 {
			count += n
		}
	}
	for k, n := range subscribed {
		if published[k] == 0 {
			count += n
		}
	}
	return count
}
