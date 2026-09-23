package architecturepresentation

import (
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// Classifier resolves only explicit owner data and small deterministic rules.
// It deliberately has no capability-, purpose-, relation-, or AI-based path.
type Classifier struct{ rules ClassificationRules }

func NewClassifier(rules ClassificationRules) Classifier {
	defaults := DefaultClassificationRules()
	if rules.Explicit == nil {
		rules.Explicit = defaults.Explicit
	} else {
		copied := make(map[string]ExplicitClassification, len(rules.Explicit))
		for projectID, value := range rules.Explicit {
			copied[projectID] = value
		}
		rules.Explicit = copied
	}
	if rules.TechnicalManifestKinds == nil {
		rules.TechnicalManifestKinds = defaults.TechnicalManifestKinds
	} else {
		copied := make(map[string]struct{}, len(rules.TechnicalManifestKinds))
		for value := range rules.TechnicalManifestKinds {
			copied[normalized(value)] = struct{}{}
		}
		rules.TechnicalManifestKinds = copied
	}
	if rules.TechnicalNameSuffixes == nil {
		rules.TechnicalNameSuffixes = defaults.TechnicalNameSuffixes
	} else {
		rules.TechnicalNameSuffixes = append([]string{}, rules.TechnicalNameSuffixes...)
	}
	for projectID, value := range rules.Explicit {
		value.Kind = normalizeClassification(value.Kind)
		value.DomainID = strings.TrimSpace(value.DomainID)
		value.DomainName = strings.TrimSpace(value.DomainName)
		if value.Kind == "" {
			value.Kind = ServiceClassificationUnclassified
		}
		rules.Explicit[projectID] = value
	}
	return Classifier{rules: rules}
}

func (c Classifier) Classify(service domain.ArchitectureCatalogService) ServiceClassification {
	if explicit, ok := c.rules.Explicit[service.Source.ProjectID]; ok {
		reason := explicit.Reason
		if strings.TrimSpace(reason.Value) == "" {
			reason = statement("Owner-authored classification", 1, nil)
		}
		return ServiceClassification{
			Kind:       explicit.Kind,
			DomainID:   nonEmpty(explicit.DomainID, classificationDomainID(explicit.Kind)),
			DomainName: nonEmpty(explicit.DomainName, classificationDomainName(explicit.Kind)),
			Source:     ClassificationSourceOwnerMetadata,
			Rule:       "owner-authored metadata for project " + service.Source.ProjectID,
			Reason:     nonUnknownStatement(reason, "owner-authored classification"),
		}
	}

	if service.Manifest != nil {
		kind := normalized(service.Manifest.Identity.Kind)
		if _, ok := c.rules.TechnicalManifestKinds[kind]; ok {
			return ServiceClassification{
				Kind:       ServiceClassificationTechnical,
				DomainID:   "technical",
				DomainName: "Technical",
				Source:     ClassificationSourceManifestKind,
				Rule:       "manifest.identity.kind=" + kind,
				Reason: domain.ArchitectureStatement{
					Value:      "Technical classification is declared by manifest identity kind " + kind,
					Confidence: statementConfidence(service.Manifest.Confidence, service.Manifest.Evidence),
					Evidence:   cloneEvidence(service.Manifest.Evidence),
				},
			}
		}
	}

	for _, suffix := range sortedNormalized(c.rules.TechnicalNameSuffixes) {
		if suffix != "" && strings.HasSuffix(normalized(serviceName(service)), suffix) {
			return ServiceClassification{
				Kind:       ServiceClassificationTechnical,
				DomainID:   "technical",
				DomainName: "Technical",
				Source:     ClassificationSourceNamingRule,
				Rule:       "service name suffix " + suffix,
				Reason: domain.ArchitectureStatement{
					Value:      "Technical classification follows the deterministic service-name suffix " + suffix,
					Confidence: 0.70,
					Evidence:   manifestEvidence(service),
				},
			}
		}
	}

	return ServiceClassification{
		Kind:       ServiceClassificationUnclassified,
		DomainID:   "unclassified",
		DomainName: "Unclassified",
		Source:     ClassificationSourceUnknown,
		Reason:     unknownStatement("Business/Technical classification is not proven by explicit metadata or deterministic rules"),
	}
}

func normalizeClassification(value ServiceClassificationKind) ServiceClassificationKind {
	switch ServiceClassificationKind(normalized(string(value))) {
	case ServiceClassificationBusiness:
		return ServiceClassificationBusiness
	case ServiceClassificationTechnical:
		return ServiceClassificationTechnical
	default:
		return ServiceClassificationUnclassified
	}
}

func classificationDomainID(kind ServiceClassificationKind) string {
	if kind == ServiceClassificationBusiness {
		return "business"
	}
	if kind == ServiceClassificationTechnical {
		return "technical"
	}
	return "unclassified"
}

func classificationDomainName(kind ServiceClassificationKind) string {
	if kind == ServiceClassificationBusiness {
		return "Business"
	}
	if kind == ServiceClassificationTechnical {
		return "Technical"
	}
	return "Unclassified"
}

func serviceName(service domain.ArchitectureCatalogService) string {
	if service.Manifest != nil && strings.TrimSpace(service.Manifest.Identity.Name) != "" {
		return service.Manifest.Identity.Name
	}
	return service.Source.ProjectName
}

func manifestEvidence(service domain.ArchitectureCatalogService) []domain.ArchitectureEvidence {
	if service.Manifest == nil {
		return []domain.ArchitectureEvidence{}
	}
	return cloneEvidence(service.Manifest.Evidence)
}

func nonUnknownStatement(value domain.ArchitectureStatement, fallback string) domain.ArchitectureStatement {
	if strings.TrimSpace(value.Value) == "" {
		return unknownStatement(fallback)
	}
	value.Evidence = cloneEvidence(value.Evidence)
	return value
}

func unknownStatement(reason string) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: "unknown", Confidence: 0, Evidence: []domain.ArchitectureEvidence{}}
}

func statementConfidence(value float64, evidence []domain.ArchitectureEvidence) float64 {
	if value > 0 {
		return value
	}
	if len(evidence) > 0 {
		return 1
	}
	return 0.70
}

func cloneEvidence(value []domain.ArchitectureEvidence) []domain.ArchitectureEvidence {
	return append([]domain.ArchitectureEvidence{}, value...)
}

func normalized(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func sortedNormalized(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalized(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
