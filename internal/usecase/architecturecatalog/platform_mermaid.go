package architecturecatalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// PlatformMermaid renders the whole manifest-backed CURRENT interaction graph.
// It is generated on demand from Current and is never a second source of
// truth. TARGET or proposal state is intentionally absent from this use case.
type PlatformMermaid struct{ Current Current }

func (uc PlatformMermaid) Handle(ctx context.Context) (string, error) {
	catalog, err := uc.Current.Handle(ctx)
	if err != nil {
		return "", err
	}
	return RenderPlatformMermaid(catalog), nil
}

// RenderPlatformMermaid is deterministic for an ArchitectureCatalog value.
// Relations with unresolved counterparts deliberately render a distinct
// external node whose label is the literal evidence-backed target (or an
// explicit unknown counterpart); they are never attached to a guessed service.
func RenderPlatformMermaid(catalog domain.ArchitectureCatalog) string {
	services := append([]domain.ArchitectureCatalogService(nil), catalog.Platform.Services...)
	sort.Slice(services, func(i, j int) bool {
		return platformServiceKey(services[i]) < platformServiceKey(services[j])
	})
	relations := append([]domain.ArchitectureCatalogRelation(nil), catalog.Platform.Relations...)
	sort.Slice(relations, func(i, j int) bool { return platformRelationKey(relations[i]) < platformRelationKey(relations[j]) })

	serviceNodes := make(map[string]string, len(services))
	var b strings.Builder
	b.WriteString("%% GENERATED — READ-ONLY CURRENT PRESENTATION\n")
	b.WriteString("%% Source of truth: validated service and operation manifests.\n")
	b.WriteString("flowchart LR\n")
	for index, service := range services {
		node := fmt.Sprintf("service_%d", index)
		serviceNodes[service.Source.ProjectID] = node
		label := service.Source.ProjectName
		if service.Manifest != nil && strings.TrimSpace(service.Manifest.Identity.Name) != "" {
			label = service.Manifest.Identity.Name
		}
		if !service.Covered {
			label += " (manifest unavailable)"
		}
		b.WriteString(fmt.Sprintf("  %s[\"service: %s\"]\n", node, mermaidEscape(label)))
	}

	externalNodes := make(map[string]string)
	externalLabels := make([]string, 0)
	for _, relation := range relations {
		if relation.TargetProjectID == "" {
			externalLabels = append(externalLabels, explicitMermaidExternal(relation.ExternalTarget))
		}
	}
	sort.Strings(externalLabels)
	for _, label := range uniqueStrings(externalLabels) {
		node := fmt.Sprintf("external_%d", len(externalNodes))
		externalNodes[label] = node
		b.WriteString(fmt.Sprintf("  %s[\"external: %s\"]\n", node, mermaidEscape(label)))
	}

	for _, relation := range relations {
		source := serviceNodes[relation.SourceProjectID]
		if source == "" {
			// This is defensive only: Builder creates relations from catalog
			// services. Keeping the unexpected source visible is safer than
			// dropping an evidence-bearing relation in a presentation.
			label := "unknown source " + relation.SourceProjectID
			source = externalNodes[label]
			if source == "" {
				source = fmt.Sprintf("external_%d", len(externalNodes))
				externalNodes[label] = source
				b.WriteString(fmt.Sprintf("  %s[\"external: %s\"]\n", source, mermaidEscape(label)))
			}
		}
		target := serviceNodes[relation.TargetProjectID]
		if target == "" {
			target = externalNodes[explicitMermaidExternal(relation.ExternalTarget)]
		}
		if target == "" {
			// Same defensive treatment for an invalid hand-built catalog value.
			label := "unknown"
			target = externalNodes[label]
			if target == "" {
				target = fmt.Sprintf("external_%d", len(externalNodes))
				externalNodes[label] = target
				b.WriteString(fmt.Sprintf("  %s[\"external: %s\"]\n", target, label))
			}
		}
		label := relationLabel(relation)
		if relationInbound(relation.Direction) {
			b.WriteString(fmt.Sprintf("  %s -->|%s| %s\n", target, mermaidEscape(label), source))
			continue
		}
		b.WriteString(fmt.Sprintf("  %s -->|%s| %s\n", source, mermaidEscape(label), target))
	}
	return b.String()
}

func platformServiceKey(service domain.ArchitectureCatalogService) string {
	return strings.Join([]string{service.Source.ProjectName, service.Source.ProjectID}, "\x00")
}

func platformRelationKey(relation domain.ArchitectureCatalogRelation) string {
	return strings.Join([]string{string(relation.Type), relation.SourceProjectID, relation.TargetProjectID, relation.ExternalTarget, relation.OperationID, relation.Transport, relation.Contract, relation.Direction, relation.ID}, "\x00")
}

func relationLabel(relation domain.ArchitectureCatalogRelation) string {
	parts := []string{string(relation.Type)}
	if value := strings.TrimSpace(relation.Transport); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(relation.Contract); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(relation.OperationID); value != "" {
		parts = append(parts, "operation "+value)
	}
	if value := strings.TrimSpace(relation.Direction); value != "" && value != "outbound" && value != "inbound" {
		parts = append(parts, value)
	}
	return strings.Join(parts, " · ")
}

func relationInbound(direction string) bool {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "inbound", "consumed", "subscribed", "receive", "received":
		return true
	default:
		return false
	}
}

func explicitMermaidExternal(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func mermaidEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;", "\n", "<br/>").Replace(value)
}
