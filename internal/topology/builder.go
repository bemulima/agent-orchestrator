package topology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/contractref"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

var (
	versionPathPattern    = regexp.MustCompile(`(?i)/(v[0-9]+)(?:/|$)`)
	versionSubjectPattern = regexp.MustCompile(`(?i)(?:^|\.)(v[0-9]+)(?:\.|$)`)
)

type Builder struct{}

func (Builder) Build(ctx context.Context, sources []domain.TopologySource) (domain.TopologyCatalog, error) {
	sources = append([]domain.TopologySource(nil), sources...)
	sort.Slice(sources, func(i, j int) bool {
		return stableSortKey(sources[i].Project.Name, sources[i].Project.ID) <
			stableSortKey(sources[j].Project.Name, sources[j].Project.ID)
	})
	catalog := domain.TopologyCatalog{
		Services: []domain.TopologyService{}, Capabilities: []domain.ServiceCapability{},
		Ownership: []domain.ServiceOwnership{}, Contracts: []domain.Contract{},
		Relations: []domain.ServiceRelation{}, Drifts: []domain.ContractDrift{},
	}
	runtimeSources := make([]domain.TopologySource, 0, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return domain.TopologyCatalog{}, err
		}
		if err := validateSource(source); err != nil {
			return domain.TopologyCatalog{}, err
		}
		if !isTopologyProject(source.Project) {
			continue
		}
		runtimeSources = append(runtimeSources, source)
		catalog.Services = append(catalog.Services, buildService(source))
		for _, fact := range source.Report.Facts {
			switch fact.Category {
			case "capability", "business_rule", "business_process", "entity":
				catalog.Capabilities = append(catalog.Capabilities, buildCapability(source, fact))
			case "ownership":
				catalog.Ownership = append(catalog.Ownership, buildOwnership(source, fact))
			}
		}
		catalog.Contracts = append(catalog.Contracts, buildContracts(source)...)
	}
	catalog.Capabilities = deduplicateCapabilities(catalog.Capabilities)
	catalog.Ownership = deduplicateOwnership(catalog.Ownership)
	catalog.Contracts = deduplicateContracts(catalog.Contracts)
	catalog.Relations = buildRelations(runtimeSources, catalog.Contracts)
	catalog.Drifts = buildDrifts(catalog.Contracts)
	sortCatalog(&catalog)
	catalog.Revision = domain.TopologyRevision{
		ProjectCount: len(sources), ServiceCount: len(catalog.Services),
		CapabilityCount: len(catalog.Capabilities), OwnershipCount: len(catalog.Ownership),
		ContractCount: len(catalog.Contracts), RelationCount: len(catalog.Relations), DriftCount: len(catalog.Drifts),
	}
	fingerprint, err := catalogFingerprint(catalog)
	if err != nil {
		return domain.TopologyCatalog{}, err
	}
	catalog.Revision.Fingerprint = fingerprint
	return catalog, nil
}

func validateSource(source domain.TopologySource) error {
	if source.Project.ID == "" || source.Snapshot.ID == "" || source.Snapshot.ProjectID != source.Project.ID ||
		source.Report.ProjectID != source.Project.ID || source.Report.CommitSHA != source.Snapshot.CommitSHA {
		return fmt.Errorf("topology source does not match project snapshot: %w", domain.ErrConflict)
	}
	return nil
}

func isTopologyProject(project domain.Project) bool {
	switch project.RepositoryRole {
	case domain.RepositoryRoleContent, domain.RepositoryRolePolicy, domain.RepositoryRoleDocumentation, domain.RepositoryRoleArchive:
		return false
	default:
		return true
	}
}

func buildService(source domain.TopologySource) domain.TopologyService {
	stack := filterEvidence(source.Report.Facts, "stack", "")
	return domain.TopologyService{
		ProjectID: source.Project.ID, SnapshotID: source.Snapshot.ID, Name: source.Project.Name,
		RepositoryRole: source.Project.RepositoryRole, ServiceKind: source.Snapshot.ServiceKind,
		Purpose: source.Snapshot.Purpose, Stack: stack,
	}
}

func buildCapability(source domain.TopologySource, fact domain.Evidence) domain.ServiceCapability {
	return domain.ServiceCapability{
		ProjectID: source.Project.ID, SnapshotID: source.Snapshot.ID,
		Code: stableCode("capability:"+fact.Name, fact.Value, 255), Name: bounded(fact.Value, 255),
		Description: fact.Explanation, Confidence: fact.Confidence, Source: fact.SourcePath,
	}
}

func buildOwnership(source domain.TopologySource, fact domain.Evidence) domain.ServiceOwnership {
	return domain.ServiceOwnership{
		ProjectID: source.Project.ID, SnapshotID: source.Snapshot.ID,
		ResourceType: bounded(fact.Name, 64), ResourceName: bounded(fact.Value, 255),
		Confidence: fact.Confidence, Source: fact.SourcePath,
	}
}

type contractShape struct {
	Kind       string  `json:"kind"`
	Method     string  `json:"method,omitempty"`
	Path       string  `json:"path,omitempty"`
	Subject    string  `json:"subject,omitempty"`
	Resource   string  `json:"resource,omitempty"`
	SourcePath string  `json:"source_path"`
	Confidence float64 `json:"confidence"`
}

func buildContracts(source domain.TopologySource) []domain.Contract {
	contracts := make([]domain.Contract, 0)
	for _, fact := range source.Report.Facts {
		var contractType domain.ContractType
		var direction string
		var code, version string
		shape := contractShape{SourcePath: fact.SourcePath, Confidence: fact.Confidence}
		switch {
		case fact.Category == "capability" && fact.Name == "http_route",
			fact.Category == "contract" && fact.Name == "http_produce":
			method, path, valid := contractref.HTTP(fact.Value)
			if !valid {
				continue
			}
			contractType, direction = domain.ContractTypeHTTP, domain.ContractDirectionProvides
			code, version = httpContractCode(method, path), versionFromPath(path)
			shape.Kind, shape.Method, shape.Path = "http", method, path
		case fact.Category == "relation" && fact.Name == "frontend_consumes",
			fact.Category == "contract" && fact.Name == "http_consume":
			method, path, valid := contractref.HTTP(fact.Value)
			if !valid {
				continue
			}
			contractType, direction = domain.ContractTypeHTTP, domain.ContractDirectionConsumes
			code, version = httpContractCode(method, path), versionFromPath(path)
			shape.Kind, shape.Method, shape.Path = "http", method, path
		case fact.Category == "contract" && fact.Name == "event_publish":
			subject, valid := contractref.EventSubject(fact.Value)
			if !valid {
				continue
			}
			contractType, direction = domain.ContractTypeEvent, domain.ContractDirectionPublishes
			code, version = eventContractCode(subject), versionFromSubject(subject)
			shape.Kind, shape.Subject = "event", subject
		case fact.Category == "contract" && fact.Name == "event_subscribe":
			subject, valid := contractref.EventSubject(fact.Value)
			if !valid {
				continue
			}
			contractType, direction = domain.ContractTypeEvent, domain.ContractDirectionSubscribes
			code, version = eventContractCode(subject), versionFromSubject(subject)
			shape.Kind, shape.Subject = "event", subject
		case fact.Category == "ownership" && fact.Name == "database_table":
			contractType, direction = domain.ContractTypeDatabase, domain.ContractDirectionOwns
			code, version = stableCode("database", strings.ToLower(fact.Value), 255), "unversioned"
			shape.Kind, shape.Resource = "database", fact.Value
		case fact.Category == "contract" && fact.Name == "http_definition":
			contractType, direction = domain.ContractTypeHTTP, domain.ContractDirectionProvides
			code, version = stableCode("http-definition", fact.Value, 255), versionFromPath(fact.Value)
			shape.Kind, shape.Resource = "http_definition", fact.Value
		case fact.Category == "contract" && fact.Name == "grpc_definition":
			contractType, direction = domain.ContractTypeGRPC, domain.ContractDirectionProvides
			code, version = stableCode("grpc", fact.Value, 255), versionFromPath(fact.Value)
			shape.Kind, shape.Resource = "grpc", fact.Value
		default:
			continue
		}
		definition, _ := json.Marshal(shape)
		contracts = append(contracts, domain.Contract{
			ProjectID: source.Project.ID, SnapshotID: source.Snapshot.ID,
			Code: code, Type: contractType, Version: version, Direction: direction,
			Definition: definition, SourcePath: fact.SourcePath, Checksum: checksum(definition),
		})
	}
	return contracts
}

func buildRelations(sources []domain.TopologySource, contracts []domain.Contract) []domain.ServiceRelation {
	aliases := projectAliases(sources)
	producers := make(map[string][]domain.Contract)
	for _, contract := range contracts {
		if isProducer(contract.Direction) {
			producers[contract.Code] = append(producers[contract.Code], contract)
		}
	}
	relations := make([]domain.ServiceRelation, 0)
	for _, source := range sources {
		for _, fact := range source.Report.Facts {
			var targetID string
			var relationType domain.RelationType
			var contractCode *string
			switch {
			case fact.Category == "relation" && fact.Name == "gateway_routes_to":
				targetID = aliases[referenceName(fact.Value)]
				relationType = domain.RelationRoutesTo
			case fact.Category == "relation" && fact.Name == "depends_on":
				targetID = aliases[referenceName(fact.Value)]
				relationType = domain.RelationDependsOn
			case fact.Category == "relation" && fact.Name == "authenticates_through":
				targetID = aliases[referenceName(fact.Value)]
				relationType = domain.RelationAuthenticatesThrough
			case fact.Category == "relation" && fact.Name == "stores_in":
				targetID = aliases[referenceName(fact.Value)]
				relationType = domain.RelationStoresIn
			case fact.Category == "relation" && fact.Name == "deploys":
				targetID = aliases[referenceName(fact.Value)]
				relationType = domain.RelationDeploys
			case fact.Category == "relation" && fact.Name == "frontend_consumes",
				fact.Category == "contract" && fact.Name == "http_consume":
				method, path, valid := contractref.HTTP(fact.Value)
				if !valid {
					continue
				}
				code := httpContractCode(method, path)
				for _, producer := range producers[code] {
					if producer.ProjectID == source.Project.ID {
						continue
					}
					codeCopy := code
					relations = append(relations, domain.ServiceRelation{
						SourceProjectID: source.Project.ID, TargetProjectID: producer.ProjectID,
						SnapshotID: source.Snapshot.ID, RelationType: domain.RelationConsumes,
						ContractCode: &codeCopy, Confidence: fact.Confidence, Source: fact.SourcePath,
					})
				}
				continue
			case fact.Category == "contract" && fact.Name == "event_subscribe":
				subject, valid := contractref.EventSubject(fact.Value)
				if !valid {
					continue
				}
				code := eventContractCode(subject)
				for _, producer := range producers[code] {
					if producer.ProjectID == source.Project.ID {
						continue
					}
					codeCopy := code
					relations = append(relations, domain.ServiceRelation{
						SourceProjectID: source.Project.ID, TargetProjectID: producer.ProjectID,
						SnapshotID: source.Snapshot.ID, RelationType: domain.RelationSubscribes,
						ContractCode: &codeCopy, Confidence: fact.Confidence, Source: fact.SourcePath,
					})
				}
				continue
			default:
				continue
			}
			if targetID == "" || targetID == source.Project.ID {
				continue
			}
			relations = append(relations, domain.ServiceRelation{
				SourceProjectID: source.Project.ID, TargetProjectID: targetID,
				SnapshotID: source.Snapshot.ID, RelationType: relationType,
				ContractCode: contractCode, Confidence: fact.Confidence, Source: fact.SourcePath,
			})
		}
	}
	return deduplicateRelations(relations)
}

func buildDrifts(contracts []domain.Contract) []domain.ContractDrift {
	producers := make(map[string][]domain.Contract)
	consumers := make(map[string][]domain.Contract)
	for _, contract := range contracts {
		if isProducer(contract.Direction) {
			producers[contract.Code] = append(producers[contract.Code], contract)
		} else if isConsumer(contract.Direction) {
			consumers[contract.Code] = append(consumers[contract.Code], contract)
		}
	}
	drifts := make([]domain.ContractDrift, 0)
	for code, contractConsumers := range consumers {
		contractProducers := producers[code]
		for _, consumer := range contractConsumers {
			consumerID := consumer.ProjectID
			if len(contractProducers) == 0 {
				difference, _ := json.Marshal(map[string]any{"missing_producer": true, "consumer_definition": json.RawMessage(consumer.Definition)})
				drifts = append(drifts, domain.ContractDrift{
					ConsumerProjectID: &consumerID, ContractCode: code, ContractType: consumer.Type,
					ConsumerVersion: consumer.Version, Difference: difference, Severity: domain.DriftSeverityCritical,
					SuggestedAction: "Connect or identify the producer before changing this consumer contract.",
				})
				continue
			}
			for _, producer := range contractProducers {
				if producer.ProjectID == consumer.ProjectID {
					continue
				}
				severity := domain.DriftSeverityInfo
				differenceFields := map[string]any{}
				suggestedAction := "Producer and consumer contract descriptions are aligned."
				if producer.Version != consumer.Version {
					severity = domain.DriftSeverityError
					differenceFields["version_mismatch"] = map[string]string{"producer": producer.Version, "consumer": consumer.Version}
					suggestedAction = "Align producer and consumer versions or add an explicit compatibility migration."
				} else if len(contractProducers) > 1 {
					severity = domain.DriftSeverityWarning
					differenceFields["multiple_producers"] = len(contractProducers)
					suggestedAction = "Confirm the canonical producer and remove ambiguous duplicate ownership."
				}
				if severity == domain.DriftSeverityInfo {
					continue
				}
				producerID := producer.ProjectID
				difference, _ := json.Marshal(differenceFields)
				drifts = append(drifts, domain.ContractDrift{
					ProducerProjectID: &producerID, ConsumerProjectID: &consumerID,
					ContractCode: code, ContractType: producer.Type,
					ProducerVersion: producer.Version, ConsumerVersion: consumer.Version,
					Difference: difference, Severity: severity, SuggestedAction: suggestedAction,
				})
			}
		}
	}
	return deduplicateDrifts(drifts)
}

func httpContractCode(method, path string) string {
	canonical := versionPathPattern.ReplaceAllStringFunc(path, func(match string) string {
		suffix := ""
		if strings.HasSuffix(match, "/") {
			suffix = "/"
		}
		return "/{version}" + suffix
	})
	return stableCode("http:"+strings.ToUpper(method), canonical, 255)
}

func eventContractCode(subject string) string {
	canonical := versionSubjectPattern.ReplaceAllString(subject, ".{version}.")
	canonical = strings.Trim(canonical, ".")
	return stableCode("event", canonical, 255)
}

func versionFromPath(value string) string {
	match := versionPathPattern.FindStringSubmatch(value)
	if len(match) > 1 {
		return strings.ToLower(match[1])
	}
	return "unversioned"
}

func versionFromSubject(value string) string {
	match := versionSubjectPattern.FindStringSubmatch(value)
	if len(match) > 1 {
		return strings.ToLower(match[1])
	}
	return "unversioned"
}

func projectAliases(sources []domain.TopologySource) map[string]string {
	aliases := make(map[string]string)
	for _, source := range sources {
		name := strings.ToLower(source.Project.Name)
		for _, alias := range []string{name, strings.TrimPrefix(name, "ms-go-"), strings.TrimPrefix(name, "ms-"),
			strings.TrimSuffix(name, "-service")} {
			if alias != "" {
				if _, exists := aliases[alias]; !exists {
					aliases[alias] = source.Project.ID
				}
			}
		}
	}
	return aliases
}

func referenceName(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	value = strings.TrimSpace(strings.Split(value, "=")[0])
	value = strings.TrimPrefix(value, "//")
	if index := strings.IndexAny(value, ":/"); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(value)
}

func isProducer(direction string) bool {
	return direction == domain.ContractDirectionProvides || direction == domain.ContractDirectionPublishes || direction == domain.ContractDirectionOwns
}

func isConsumer(direction string) bool {
	return direction == domain.ContractDirectionConsumes || direction == domain.ContractDirectionSubscribes
}

func filterEvidence(facts []domain.Evidence, category, name string) []domain.Evidence {
	result := make([]domain.Evidence, 0)
	for _, fact := range facts {
		if fact.Category == category && (name == "" || fact.Name == name) {
			result = append(result, fact)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return stableSortKey(result[i].Name, result[i].Value, result[i].SourcePath, result[i].Explanation) <
			stableSortKey(result[j].Name, result[j].Value, result[j].SourcePath, result[j].Explanation)
	})
	return result
}

func stableCode(prefix, value string, limit int) string {
	code := strings.ToLower(strings.TrimSpace(prefix + ":" + value))
	code = strings.Join(strings.Fields(code), " ")
	if len(code) <= limit {
		return code
	}
	hash := sha256.Sum256([]byte(code))
	suffix := ":sha256:" + hex.EncodeToString(hash[:8])
	return code[:limit-len(suffix)] + suffix
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func checksum(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func deduplicateCapabilities(values []domain.ServiceCapability) []domain.ServiceCapability {
	best := make(map[string]domain.ServiceCapability)
	for _, value := range values {
		key := value.ProjectID + "\x00" + value.Code + "\x00" + value.Source
		if existing, exists := best[key]; !exists || value.Confidence > existing.Confidence ||
			(value.Confidence == existing.Confidence && capabilityChoiceKey(value) < capabilityChoiceKey(existing)) {
			best[key] = value
		}
	}
	result := make([]domain.ServiceCapability, 0, len(best))
	for _, value := range best {
		result = append(result, value)
	}
	return result
}

func deduplicateOwnership(values []domain.ServiceOwnership) []domain.ServiceOwnership {
	best := make(map[string]domain.ServiceOwnership)
	for _, value := range values {
		key := value.ProjectID + "\x00" + value.ResourceType + "\x00" + value.ResourceName + "\x00" + value.Source
		if existing, exists := best[key]; !exists || value.Confidence > existing.Confidence ||
			(value.Confidence == existing.Confidence && ownershipChoiceKey(value) < ownershipChoiceKey(existing)) {
			best[key] = value
		}
	}
	result := make([]domain.ServiceOwnership, 0, len(best))
	for _, value := range best {
		result = append(result, value)
	}
	return result
}

func deduplicateContracts(values []domain.Contract) []domain.Contract {
	best := make(map[string]domain.Contract)
	for _, value := range values {
		key := value.ProjectID + "\x00" + value.Code + "\x00" + string(value.Type) + "\x00" + value.Version + "\x00" + value.Direction
		if existing, exists := best[key]; !exists || contractChoiceKey(value) < contractChoiceKey(existing) {
			best[key] = value
		}
	}
	result := make([]domain.Contract, 0, len(best))
	for _, value := range best {
		result = append(result, value)
	}
	return result
}

func deduplicateRelations(values []domain.ServiceRelation) []domain.ServiceRelation {
	best := make(map[string]domain.ServiceRelation)
	for _, value := range values {
		contractCode := relationContractCode(value)
		key := value.SourceProjectID + "\x00" + value.TargetProjectID + "\x00" + string(value.RelationType) + "\x00" + contractCode + "\x00" + value.Source
		if existing, exists := best[key]; !exists || value.Confidence > existing.Confidence ||
			(value.Confidence == existing.Confidence && relationChoiceKey(value) < relationChoiceKey(existing)) {
			best[key] = value
		}
	}
	result := make([]domain.ServiceRelation, 0, len(best))
	for _, value := range best {
		result = append(result, value)
	}
	return result
}

func deduplicateDrifts(values []domain.ContractDrift) []domain.ContractDrift {
	best := make(map[string]domain.ContractDrift)
	for _, value := range values {
		producer, consumer := "", ""
		if value.ProducerProjectID != nil {
			producer = *value.ProducerProjectID
		}
		if value.ConsumerProjectID != nil {
			consumer = *value.ConsumerProjectID
		}
		key := producer + "\x00" + consumer + "\x00" + value.ContractCode
		if existing, exists := best[key]; !exists || severityRank(value.Severity) > severityRank(existing.Severity) ||
			(severityRank(value.Severity) == severityRank(existing.Severity) && driftChoiceKey(value) < driftChoiceKey(existing)) {
			best[key] = value
		}
	}
	result := make([]domain.ContractDrift, 0, len(best))
	for _, value := range best {
		result = append(result, value)
	}
	return result
}

func severityRank(value domain.DriftSeverity) int {
	switch value {
	case domain.DriftSeverityCritical:
		return 4
	case domain.DriftSeverityError:
		return 3
	case domain.DriftSeverityWarning:
		return 2
	default:
		return 1
	}
}

func sortCatalog(catalog *domain.TopologyCatalog) {
	sort.Slice(catalog.Services, func(i, j int) bool {
		return stableSortKey(catalog.Services[i].Name, catalog.Services[i].ProjectID) <
			stableSortKey(catalog.Services[j].Name, catalog.Services[j].ProjectID)
	})
	sort.Slice(catalog.Capabilities, func(i, j int) bool {
		return stableSortKey(catalog.Capabilities[i].ProjectID, catalog.Capabilities[i].Code, catalog.Capabilities[i].Source) <
			stableSortKey(catalog.Capabilities[j].ProjectID, catalog.Capabilities[j].Code, catalog.Capabilities[j].Source)
	})
	sort.Slice(catalog.Ownership, func(i, j int) bool {
		return stableSortKey(catalog.Ownership[i].ProjectID, catalog.Ownership[i].ResourceType, catalog.Ownership[i].ResourceName, catalog.Ownership[i].Source) <
			stableSortKey(catalog.Ownership[j].ProjectID, catalog.Ownership[j].ResourceType, catalog.Ownership[j].ResourceName, catalog.Ownership[j].Source)
	})
	sort.Slice(catalog.Contracts, func(i, j int) bool {
		return contractSortKey(catalog.Contracts[i]) < contractSortKey(catalog.Contracts[j])
	})
	sort.Slice(catalog.Relations, func(i, j int) bool {
		return relationSortKey(catalog.Relations[i]) < relationSortKey(catalog.Relations[j])
	})
	sort.Slice(catalog.Drifts, func(i, j int) bool {
		return driftSortKey(catalog.Drifts[i]) < driftSortKey(catalog.Drifts[j])
	})
}

func driftSortKey(value domain.ContractDrift) string {
	producer, consumer := "", ""
	if value.ProducerProjectID != nil {
		producer = *value.ProducerProjectID
	}
	if value.ConsumerProjectID != nil {
		consumer = *value.ConsumerProjectID
	}
	return stableSortKey(fmt.Sprintf("%02d", 10-severityRank(value.Severity)), value.ContractCode, producer, consumer,
		string(value.ContractType), value.ProducerVersion, value.ConsumerVersion, string(value.Difference), value.SuggestedAction)
}

func stableSortKey(fields ...string) string {
	return strings.Join(fields, "\x00")
}

func capabilityChoiceKey(value domain.ServiceCapability) string {
	return stableSortKey(value.SnapshotID, value.Name, value.Description, value.Source)
}

func ownershipChoiceKey(value domain.ServiceOwnership) string {
	return stableSortKey(value.SnapshotID, value.ResourceType, value.ResourceName, value.Source)
}

func contractChoiceKey(value domain.Contract) string {
	return stableSortKey(value.SourcePath, value.Checksum, string(value.Definition), value.SnapshotID)
}

func contractSortKey(value domain.Contract) string {
	return stableSortKey(value.Code, value.ProjectID, value.Direction, string(value.Type), value.Version,
		value.SourcePath, value.Checksum, value.SnapshotID)
}

func relationContractCode(value domain.ServiceRelation) string {
	if value.ContractCode == nil {
		return ""
	}
	return *value.ContractCode
}

func relationChoiceKey(value domain.ServiceRelation) string {
	return stableSortKey(value.SnapshotID, relationContractCode(value), value.Source)
}

func relationSortKey(value domain.ServiceRelation) string {
	return stableSortKey(value.SourceProjectID, value.TargetProjectID, string(value.RelationType),
		relationContractCode(value), value.Source, value.SnapshotID)
}

func driftChoiceKey(value domain.ContractDrift) string {
	return stableSortKey(string(value.ContractType), value.ProducerVersion, value.ConsumerVersion,
		string(value.Difference), value.SuggestedAction)
}

func catalogFingerprint(catalog domain.TopologyCatalog) (string, error) {
	catalog.Revision = domain.TopologyRevision{}
	content, err := json.Marshal(catalog)
	if err != nil {
		return "", fmt.Errorf("marshal topology fingerprint: %w", err)
	}
	return checksum(content), nil
}

var _ repository.TopologyBuilder = Builder{}
