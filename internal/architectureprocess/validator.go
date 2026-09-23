package architectureprocess

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
)

// Parse accepts exactly one strict, bounded YAML business-process manifest.
func Parse(content []byte) (Manifest, error) {
	if err := validateYAML(content); err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode business process manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, fmt.Errorf("business process manifest has multiple documents: %w", domain.ErrValidation)
		}
		return Manifest{}, fmt.Errorf("decode trailing business process manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate enforces explicit participation and provenance. It never attempts
// to prove a process by following service dependencies.
func Validate(manifest Manifest) error {
	if manifest.Schema != SchemaV1 || manifest.Kind != KindV1 || !validID(manifest.ID) || manifest.ManifestRevision < 1 || strings.TrimSpace(manifest.Identity.Name) == "" {
		return invalid("invalid business process identity")
	}
	if err := validateStatement(manifest.Goal); err != nil {
		return wrap("goal", err)
	}
	if err := validateStatement(manifest.Trigger); err != nil {
		return wrap("trigger", err)
	}
	if err := validateStatement(manifest.Outcome); err != nil {
		return wrap("outcome", err)
	}
	if !manifest.Provenance.IsConfirmed() && manifest.Provenance != ProvenanceCandidateUnverified {
		return invalid("unsupported process provenance %q", manifest.Provenance)
	}
	if err := validateEvidence(manifest.Evidence); err != nil || len(manifest.Evidence) == 0 || !validConfidence(manifest.Confidence) {
		return invalid("process evidence and confidence are required")
	}
	if len(manifest.Steps) == 0 {
		return invalid("business process requires ordered steps")
	}
	if len(manifest.Services) == 0 || len(manifest.Operations) == 0 {
		return invalid("business process requires explicit participating services and operations")
	}

	services := make(map[string]struct{}, len(manifest.Services))
	for _, participant := range manifest.Services {
		if !validID(participant.ServiceID) {
			return invalid("invalid participating service %q", participant.ServiceID)
		}
		if _, exists := services[participant.ServiceID]; exists {
			return invalid("duplicate participating service %q", participant.ServiceID)
		}
		services[participant.ServiceID] = struct{}{}
		if err := validateStatement(participant.Role); err != nil {
			return wrap("participating service role", err)
		}
	}

	operations := make(map[string]struct{}, len(manifest.Operations))
	for _, participant := range manifest.Operations {
		key := participant.ServiceID + "\x00" + participant.OperationID
		if !validID(participant.ServiceID) || !validID(participant.OperationID) {
			return invalid("invalid participating operation")
		}
		if _, exists := services[participant.ServiceID]; !exists {
			return invalid("operation %q references undeclared service %q", participant.OperationID, participant.ServiceID)
		}
		if _, exists := operations[key]; exists {
			return invalid("duplicate participating operation %q", participant.OperationID)
		}
		operations[key] = struct{}{}
		if err := validateStatement(participant.Role); err != nil {
			return wrap("participating operation role", err)
		}
	}

	contracts, err := validateContracts(manifest.Contracts, "contract")
	if err != nil {
		return err
	}
	events, err := validateEvents(manifest.Events)
	if err != nil {
		return err
	}

	steps := make(map[string]struct{}, len(manifest.Steps))
	for _, step := range manifest.Steps {
		if !validID(step.ID) || strings.TrimSpace(step.Name) == "" || !validID(step.ServiceID) || !validID(step.OperationID) {
			return invalid("invalid process step")
		}
		if _, exists := steps[step.ID]; exists {
			return invalid("duplicate process step %q", step.ID)
		}
		steps[step.ID] = struct{}{}
		if _, exists := operations[step.ServiceID+"\x00"+step.OperationID]; !exists {
			return invalid("step %q references undeclared operation %q", step.ID, step.OperationID)
		}
		if err := validateStatement(step.Description); err != nil {
			return wrap("step description", err)
		}
		if err := validateEvidence(step.Evidence); err != nil || len(step.Evidence) == 0 || !validConfidence(step.Confidence) {
			return invalid("step %q requires evidence and confidence", step.ID)
		}
		if err := validateReferences(step.Contracts, contracts, "contract", step.ID); err != nil {
			return err
		}
		if err := validateReferences(step.Events, events, "event", step.ID); err != nil {
			return err
		}
	}
	return nil
}

func validateContracts(values []ContractReference, name string) (map[string]struct{}, error) {
	ids := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value.ID) || strings.TrimSpace(value.Transport) == "" || strings.TrimSpace(value.Direction) == "" {
			return nil, invalid("invalid %s", name)
		}
		if _, exists := ids[value.ID]; exists {
			return nil, invalid("duplicate %s %q", name, value.ID)
		}
		ids[value.ID] = struct{}{}
		if err := validateStatement(value.Description); err != nil {
			return nil, wrap(name+" description", err)
		}
	}
	return ids, nil
}

func validateEvents(values []EventReference) (map[string]struct{}, error) {
	ids := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value.ID) || strings.TrimSpace(value.Transport) == "" || strings.TrimSpace(value.Direction) == "" {
			return nil, invalid("invalid event")
		}
		if _, exists := ids[value.ID]; exists {
			return nil, invalid("duplicate event %q", value.ID)
		}
		ids[value.ID] = struct{}{}
		if err := validateStatement(value.Description); err != nil {
			return nil, wrap("event description", err)
		}
	}
	return ids, nil
}

func validateReferences(values []string, known map[string]struct{}, kind, step string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value) {
			return invalid("step %q has invalid %s reference", step, kind)
		}
		if _, exists := known[value]; !exists {
			return invalid("step %q references undeclared %s %q", step, kind, value)
		}
		if _, exists := seen[value]; exists {
			return invalid("step %q repeats %s %q", step, kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateStatement(value domain.ArchitectureStatement) error {
	if strings.TrimSpace(value.Value) == "" || !validConfidence(value.Confidence) || len(value.Evidence) == 0 {
		return invalid("statement requires value, evidence, and confidence")
	}
	return validateEvidence(value.Evidence)
}

func validateEvidence(values []domain.ArchitectureEvidence) error {
	for _, value := range values {
		if !safeRelativePath(value.SourcePath) || value.StartLine < 0 || value.EndLine < 0 ||
			(value.EndLine > 0 && value.StartLine == 0) || (value.EndLine > 0 && value.EndLine < value.StartLine) ||
			(value.Checksum != "" && !checksumPattern.MatchString(value.Checksum)) {
			return invalid("invalid evidence")
		}
	}
	return nil
}

func validateYAML(content []byte) error {
	if len(content) == 0 || len(content) > maxManifestBytes {
		return invalid("business process manifest must be a bounded non-empty document")
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("parse business process manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return invalid("business process manifest has multiple documents")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse trailing business process manifest: %w", errors.Join(domain.ErrValidation, err))
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return invalid("business process manifest must contain one YAML document")
	}
	return validateNode(document.Content[0])
}

func validateNode(node *yaml.Node) error {
	if node.Alias != nil || node.Anchor != "" || node.Tag == "!!merge" || node.Kind == yaml.AliasNode {
		return invalid("business process manifest forbids YAML aliases, anchors, and merge keys")
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := map[string]struct{}{}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || isSecretKey(key.Value) {
				return invalid("business process manifest has an unsafe mapping key")
			}
			if _, exists := seen[key.Value]; exists {
				return invalid("business process manifest has duplicate mapping key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
			if err := validateNode(value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if len(node.Content) > 1000 {
			return invalid("business process manifest sequence exceeds limit")
		}
		for _, value := range node.Content {
			if err := validateNode(value); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" && node.Tag != "!!int" && node.Tag != "!!float" && node.Tag != "!!bool" && node.Tag != "!!null" {
			return invalid("business process manifest has unsupported scalar tag")
		}
		if len(node.Value) > 16<<10 {
			return invalid("business process manifest scalar exceeds limit")
		}
	default:
		return invalid("business process manifest has unsupported YAML node")
	}
	return nil
}

func validID(value string) bool          { return identifierPattern.MatchString(strings.TrimSpace(value)) }
func validConfidence(value float64) bool { return value >= 0 && value <= 1 }

func safeRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	cleaned := filepath.Clean(filepath.FromSlash(value))
	return value != "" && !filepath.IsAbs(cleaned) && cleaned != "." && cleaned != ".." &&
		!strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) && !strings.ContainsRune(value, '\x00')
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

func invalid(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, domain.ErrValidation)...)
}

func wrap(field string, err error) error { return fmt.Errorf("%s: %w", field, err) }

// StableIDs returns a sorted copy for deterministic catalog indexing.
func StableIDs(manifests []Manifest) []string {
	values := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		values = append(values, manifest.ID)
	}
	sort.Strings(values)
	return values
}
