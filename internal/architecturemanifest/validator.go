// Package architecturemanifest validates service-owned Architecture Manifest
// v1 documents. It is deliberately independent of discovery and persistence:
// callers decide how immutable validated documents are snapshot and projected.
package architecturemanifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const maxManifestBytes = 512 << 10

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	checksumPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	httpMethodPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,31}$`)
)

// ParseService accepts exactly one bounded, strict YAML service manifest.
func ParseService(content []byte) (domain.ArchitectureServiceManifest, error) {
	if err := validateYAML(content); err != nil {
		return domain.ArchitectureServiceManifest{}, err
	}
	var manifest domain.ArchitectureServiceManifest
	if err := decode(content, &manifest); err != nil {
		return domain.ArchitectureServiceManifest{}, err
	}
	if err := ValidateService(manifest); err != nil {
		return domain.ArchitectureServiceManifest{}, err
	}
	return manifest, nil
}

// ParseOperation accepts exactly one bounded, strict YAML operation manifest.
func ParseOperation(content []byte) (domain.ArchitectureOperationManifest, error) {
	if err := validateYAML(content); err != nil {
		return domain.ArchitectureOperationManifest{}, err
	}
	var manifest domain.ArchitectureOperationManifest
	if err := decode(content, &manifest); err != nil {
		return domain.ArchitectureOperationManifest{}, err
	}
	if err := ValidateOperation(manifest); err != nil {
		return domain.ArchitectureOperationManifest{}, err
	}
	return manifest, nil
}

func decode(content []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode architecture manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("architecture manifest has multiple documents: %w", domain.ErrValidation)
		}
		return fmt.Errorf("decode trailing architecture manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	return nil
}

func validateYAML(content []byte) error {
	if len(content) == 0 || len(content) > maxManifestBytes {
		return fmt.Errorf("architecture manifest must be a bounded non-empty document: %w", domain.ErrValidation)
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("parse architecture manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("architecture manifest has multiple documents: %w", domain.ErrValidation)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse trailing architecture manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return fmt.Errorf("architecture manifest must contain one YAML document: %w", domain.ErrValidation)
	}
	return validateNode(document.Content[0])
}

func validateNode(node *yaml.Node) error {
	if node.Alias != nil || node.Anchor != "" || node.Tag == "!!merge" || node.Kind == yaml.AliasNode {
		return fmt.Errorf("architecture manifest forbids YAML aliases, anchors, and merge keys: %w", domain.ErrValidation)
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := map[string]struct{}{}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || isSecretKey(key.Value) {
				return fmt.Errorf("architecture manifest has an unsafe mapping key: %w", domain.ErrValidation)
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("architecture manifest has duplicate mapping key %q: %w", key.Value, domain.ErrValidation)
			}
			seen[key.Value] = struct{}{}
			if err := validateNode(value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if len(node.Content) > 1000 {
			return fmt.Errorf("architecture manifest sequence exceeds limit: %w", domain.ErrValidation)
		}
		for _, value := range node.Content {
			if err := validateNode(value); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" && node.Tag != "!!int" && node.Tag != "!!float" && node.Tag != "!!bool" && node.Tag != "!!null" {
			return fmt.Errorf("architecture manifest has unsupported scalar tag: %w", domain.ErrValidation)
		}
		if len(node.Value) > 16<<10 {
			return fmt.Errorf("architecture manifest scalar exceeds limit: %w", domain.ErrValidation)
		}
	default:
		return fmt.Errorf("architecture manifest has unsupported YAML node: %w", domain.ErrValidation)
	}
	return nil
}

func isSecretKey(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
	for _, forbidden := range []string{"secret", "token", "password", "credential", "cookie", "api_key"} {
		if strings.Contains(value, forbidden) {
			return true
		}
	}
	return false
}

func ValidateService(manifest domain.ArchitectureServiceManifest) error {
	if manifest.Schema != domain.ArchitectureManifestSchemaV1 || manifest.Kind != "service" || !validID(manifest.ID) || manifest.ManifestRevision < 1 {
		return fmt.Errorf("invalid service manifest identity: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(manifest.Identity.Name) == "" || strings.TrimSpace(manifest.Identity.Kind) == "" {
		return fmt.Errorf("service manifest requires service identity: %w", domain.ErrValidation)
	}
	if err := validateStatement(manifest.Purpose); err != nil {
		return fmt.Errorf("service purpose: %w", err)
	}
	if err := validateStatements(manifest.Responsibilities); err != nil {
		return fmt.Errorf("service responsibilities: %w", err)
	}
	if err := validateStatements(manifest.Capabilities); err != nil {
		return fmt.Errorf("service capabilities: %w", err)
	}
	if err := validateEvidence(manifest.Evidence); err != nil || !validConfidence(manifest.Confidence) {
		return fmt.Errorf("service evidence or confidence: %w", domain.ErrValidation)
	}
	paths := map[string]struct{}{}
	for _, path := range manifest.OperationManifests {
		if !safeOperationPath(path) {
			return fmt.Errorf("unsafe operation manifest path %q: %w", path, domain.ErrValidation)
		}
		if _, exists := paths[path]; exists {
			return fmt.Errorf("duplicate operation manifest path %q: %w", path, domain.ErrValidation)
		}
		paths[path] = struct{}{}
	}
	for _, group := range manifest.EndpointGroups {
		if !validID(group.ID) || strings.TrimSpace(group.Name) == "" || len(group.Operations) == 0 {
			return fmt.Errorf("invalid endpoint group: %w", domain.ErrValidation)
		}
		if err := validateStatement(group.Description); err != nil {
			return fmt.Errorf("endpoint group %q: %w", group.ID, err)
		}
	}
	for _, resource := range manifest.OwnedResources {
		if strings.TrimSpace(resource.Type) == "" || strings.TrimSpace(resource.Name) == "" {
			return fmt.Errorf("owned resource identity: %w", domain.ErrValidation)
		}
		if err := validateStatement(resource.Description); err != nil {
			return fmt.Errorf("owned resource %q: %w", resource.Name, err)
		}
	}
	return nil
}

func ValidateOperation(manifest domain.ArchitectureOperationManifest) error {
	if manifest.Schema != domain.ArchitectureManifestSchemaV1 || manifest.Kind != "operation" || !validID(manifest.ID) ||
		!validID(manifest.ServiceID) || manifest.ManifestRevision < 1 {
		return fmt.Errorf("invalid operation manifest identity: %w", domain.ErrValidation)
	}
	if err := validateOperationIdentity(manifest.Type, manifest.Identity); err != nil {
		return err
	}
	for _, access := range []domain.ArchitectureStatement{
		manifest.Access.Audience,
		manifest.Access.Authentication,
		manifest.Access.Authorization,
		manifest.Access.Idempotency,
	} {
		if err := validateStatement(access); err != nil {
			return fmt.Errorf("operation access policy: %w", err)
		}
	}
	if err := validateStatement(manifest.Trigger.Description); err != nil {
		return fmt.Errorf("operation trigger: %w", err)
	}
	if err := validateStatement(manifest.BusinessTask); err != nil {
		return fmt.Errorf("operation business task: %w", err)
	}
	if err := validateStatements(manifest.BusinessRules); err != nil {
		return fmt.Errorf("operation business rules: %w", err)
	}
	if err := validateEvidence(manifest.Evidence); err != nil || !validConfidence(manifest.Confidence) {
		return fmt.Errorf("operation evidence or confidence: %w", domain.ErrValidation)
	}
	seenSteps := map[string]struct{}{}
	for _, step := range manifest.BusinessProcess {
		if !validID(step.ID) {
			return fmt.Errorf("invalid operation step id: %w", domain.ErrValidation)
		}
		if _, exists := seenSteps[step.ID]; exists {
			return fmt.Errorf("duplicate operation step id %q: %w", step.ID, domain.ErrValidation)
		}
		seenSteps[step.ID] = struct{}{}
		if err := validateStatement(step.Description); err != nil {
			return fmt.Errorf("operation step %q: %w", step.ID, err)
		}
	}
	for _, interaction := range manifest.ExternalInteractions {
		if !validID(interaction.ID) || strings.TrimSpace(interaction.Transport) == "" || strings.TrimSpace(interaction.Target) == "" || strings.TrimSpace(interaction.Direction) == "" {
			return fmt.Errorf("invalid external interaction: %w", domain.ErrValidation)
		}
		if err := validateStatement(interaction.Description); err != nil {
			return fmt.Errorf("external interaction %q: %w", interaction.ID, err)
		}
	}
	for _, response := range manifest.Output.Responses {
		if response.StatusCode < 100 || response.StatusCode > 599 {
			return fmt.Errorf("invalid response status: %w", domain.ErrValidation)
		}
		if err := validateStatement(response.Description); err != nil {
			return fmt.Errorf("response %d: %w", response.StatusCode, err)
		}
	}
	for _, operationError := range manifest.Errors {
		if strings.TrimSpace(operationError.Code) == "" || operationError.StatusCode < 0 || operationError.StatusCode > 599 {
			return fmt.Errorf("invalid operation error: %w", domain.ErrValidation)
		}
		if err := validateStatement(operationError.Description); err != nil {
			return fmt.Errorf("operation error %q: %w", operationError.Code, err)
		}
	}
	return nil
}

func validateOperationIdentity(kind domain.ArchitectureOperationType, identity domain.ArchitectureOperationIdentity) error {
	transport := strings.TrimSpace(identity.Transport)
	switch kind {
	case domain.ArchitectureOperationHTTP:
		if transport != "http" || identity.HTTP == nil || identity.NATS != nil || identity.Worker != nil || identity.Scheduled != nil ||
			!validHTTPMethod(identity.HTTP.Method) || !validHTTPPath(identity.HTTP.Path) {
			return fmt.Errorf("invalid HTTP operation identity: %w", domain.ErrValidation)
		}
	case domain.ArchitectureOperationNATSRequestReply:
		if transport != "nats" || identity.NATS == nil || identity.HTTP != nil || identity.Worker != nil || identity.Scheduled != nil ||
			!validNATSSubject(identity.NATS.Subject) || identity.NATS.Role != "request_handler" {
			return fmt.Errorf("invalid NATS request/reply operation identity: %w", domain.ErrValidation)
		}
	case domain.ArchitectureOperationNATSEventSubscriber:
		if transport != "nats" || identity.NATS == nil || identity.HTTP != nil || identity.Worker != nil || identity.Scheduled != nil ||
			!validNATSSubject(identity.NATS.Subject) || identity.NATS.Role != "event_subscriber" {
			return fmt.Errorf("invalid NATS event subscriber operation identity: %w", domain.ErrValidation)
		}
	case domain.ArchitectureOperationWorker:
		if transport != "worker" || identity.Worker == nil || identity.HTTP != nil || identity.NATS != nil || identity.Scheduled != nil || strings.TrimSpace(identity.Worker.Name) == "" {
			return fmt.Errorf("invalid worker operation identity: %w", domain.ErrValidation)
		}
	case domain.ArchitectureOperationScheduled:
		if transport != "scheduled" || identity.Scheduled == nil || identity.HTTP != nil || identity.NATS != nil || identity.Worker != nil ||
			strings.TrimSpace(identity.Scheduled.Name) == "" || strings.TrimSpace(identity.Scheduled.Schedule) == "" {
			return fmt.Errorf("invalid scheduled operation identity: %w", domain.ErrValidation)
		}
	default:
		return fmt.Errorf("unsupported operation type %q: %w", kind, domain.ErrValidation)
	}
	return nil
}

func validateStatements(values []domain.ArchitectureStatement) error {
	for _, value := range values {
		if err := validateStatement(value); err != nil {
			return err
		}
	}
	return nil
}

func validateStatement(value domain.ArchitectureStatement) error {
	if strings.TrimSpace(value.Value) == "" || !validConfidence(value.Confidence) || len(value.Evidence) == 0 {
		return fmt.Errorf("statement requires value, confidence, and evidence: %w", domain.ErrValidation)
	}
	return validateEvidence(value.Evidence)
}

func validateEvidence(values []domain.ArchitectureEvidence) error {
	for _, value := range values {
		if !safeRelativePath(value.SourcePath) || value.StartLine < 0 || value.EndLine < 0 ||
			(value.EndLine > 0 && value.StartLine == 0) || (value.EndLine > 0 && value.EndLine < value.StartLine) ||
			(value.Checksum != "" && !checksumPattern.MatchString(value.Checksum)) {
			return fmt.Errorf("invalid evidence: %w", domain.ErrValidation)
		}
	}
	return nil
}

func validConfidence(value float64) bool { return value >= 0 && value <= 1 }
func validID(value string) bool          { return identifierPattern.MatchString(value) }

func safeRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	cleaned := filepath.Clean(filepath.FromSlash(value))
	return value != "" && !filepath.IsAbs(cleaned) && cleaned != "." && cleaned != ".." &&
		!strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) && !strings.ContainsRune(value, '\x00')
}

func safeOperationPath(value string) bool {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if !safeRelativePath(value) || strings.Count(value, "/") != 1 ||
		(!strings.HasSuffix(value, ".yaml") && !strings.HasSuffix(value, ".yml")) {
		return false
	}
	return strings.HasPrefix(value, "endpoints/") || strings.HasPrefix(value, "operations/")
}

func validHTTPMethod(value string) bool {
	return httpMethodPattern.MatchString(strings.TrimSpace(value))
}

func validHTTPPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "?#\r\n\t ") && len(value) <= 1024
}

func validNATSSubject(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, token := range strings.Split(value, ".") {
		if token == "" {
			return false
		}
	}
	return true
}

// StableOperationPaths returns a sorted copy, which callers can use for
// deterministic manifest-to-file traversal.
func StableOperationPaths(manifest domain.ArchitectureServiceManifest) []string {
	result := append([]string(nil), manifest.OperationManifests...)
	sort.Strings(result)
	return result
}
