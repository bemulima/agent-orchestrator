package architecturemanifest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ScaffoldResult lists the YAML files created by ScaffoldHTTPManifests. The
// paths are absolute and sorted. Existing manifests are deliberately omitted.
type ScaffoldResult struct {
	CreatedPaths []string
}

type discoveredHTTPOperation struct {
	method   string
	path     string
	evidence []domain.ArchitectureEvidence
}

type scaffoldOperation struct {
	relativePath string
	manifest     domain.ArchitectureOperationManifest
}

// ScaffoldHTTPManifests creates the smallest evidence-backed architecture
// manifests that can be supported by code-discovered HTTP routes. It never
// replaces YAML or Mermaid files, and it intentionally does not scaffold
// NATS, worker, or scheduled operations.
//
// All files are parsed and all cross-file checks are completed before a write
// is attempted. In particular, an invalid service document or a discovered
// route that conflicts with an existing endpoint leaves the repository
// untouched.
func ScaffoldHTTPManifests(root string, report domain.DiscoveryReport) (ScaffoldResult, error) {
	root, err := secureRoot(root)
	if err != nil {
		return ScaffoldResult{}, err
	}

	discovered, err := scaffoldDiscoveredHTTP(report.Operations)
	if err != nil {
		return ScaffoldResult{}, err
	}
	if len(discovered) == 0 {
		return ScaffoldResult{}, nil
	}

	service, serviceExists, servicePath, err := scaffoldService(root)
	if err != nil {
		return ScaffoldResult{}, err
	}
	serviceID := scaffoldServiceID(report, service, serviceExists)
	if !validID(serviceID) {
		return ScaffoldResult{}, validationError("cannot derive a canonical service id")
	}

	existing, err := scaffoldExistingEndpoints(root, serviceID, discovered)
	if err != nil {
		return ScaffoldResult{}, err
	}

	operations := make([]scaffoldOperation, 0, len(discovered))
	for _, operation := range discovered {
		key := operation.method + " " + operation.path
		if endpoint, exists := existing[key]; exists {
			operations = append(operations, endpoint)
			continue
		}
		id := scaffoldOperationID(operation.method, operation.path)
		relativePath := "endpoints/" + id + ".yaml"
		if err := scaffoldMissingTarget(root, filepath.ToSlash(filepath.Join(".ai", "architecture", relativePath))); err != nil {
			return ScaffoldResult{}, err
		}
		manifest := scaffoldHTTPManifest(serviceID, id, operation)
		if err := ValidateOperation(manifest); err != nil {
			return ScaffoldResult{}, fmt.Errorf("validate scaffolded HTTP operation %q: %w", id, err)
		}
		operations = append(operations, scaffoldOperation{relativePath: relativePath, manifest: manifest})
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].relativePath < operations[j].relativePath })

	if serviceExists {
		if err := validateExistingScaffoldService(service, operations); err != nil {
			return ScaffoldResult{}, err
		}
	} else {
		service = scaffoldHTTPService(serviceID, operations, discoveredEvidence(discovered))
		if err := ValidateService(service); err != nil {
			return ScaffoldResult{}, fmt.Errorf("validate scaffolded service manifest: %w", err)
		}
		if err := scaffoldMissingTarget(root, servicePath); err != nil {
			return ScaffoldResult{}, err
		}
	}

	planned := make([]scaffoldFile, 0, len(operations)+1)
	for _, operation := range operations {
		path := filepath.ToSlash(filepath.Join(".ai", "architecture", operation.relativePath))
		if _, exists := existing[operation.manifest.Identity.HTTP.Method+" "+operation.manifest.Identity.HTTP.Path]; exists {
			continue
		}
		content, err := yaml.Marshal(operation.manifest)
		if err != nil {
			return ScaffoldResult{}, fmt.Errorf("marshal scaffolded HTTP operation %q: %w", operation.manifest.ID, err)
		}
		planned = append(planned, scaffoldFile{relativePath: path, content: content})
	}
	if !serviceExists {
		content, err := yaml.Marshal(service)
		if err != nil {
			return ScaffoldResult{}, fmt.Errorf("marshal scaffolded service manifest: %w", err)
		}
		planned = append(planned, scaffoldFile{relativePath: servicePath, content: content})
	}

	// Validate output paths and contents before creating directories or files.
	for _, file := range planned {
		if err := scaffoldMissingTarget(root, file.relativePath); err != nil {
			return ScaffoldResult{}, err
		}
	}
	for _, file := range planned {
		if err := scaffoldEnsureParent(root, file.relativePath); err != nil {
			return ScaffoldResult{}, err
		}
	}

	created := make([]string, 0, len(planned))
	for _, file := range planned {
		path, err := scaffoldCreateNew(root, file.relativePath, file.content)
		if err != nil {
			return ScaffoldResult{}, err
		}
		created = append(created, path)
	}
	sort.Strings(created)
	return ScaffoldResult{CreatedPaths: created}, nil
}

// ScaffoldHTTP is a compact alias for callers that do not need to distinguish
// HTTP scaffolding from the package's other manifest helpers.
func ScaffoldHTTP(root string, report domain.DiscoveryReport) (ScaffoldResult, error) {
	return ScaffoldHTTPManifests(root, report)
}

type scaffoldFile struct {
	relativePath string
	content      []byte
}

func scaffoldDiscoveredHTTP(values []domain.DiscoveredOperation) ([]discoveredHTTPOperation, error) {
	byIdentity := map[string]discoveredHTTPOperation{}
	for _, value := range values {
		if value.Type != domain.ArchitectureOperationHTTP {
			continue
		}
		if value.Protocol != "http" || !validHTTPMethod(value.Method) || !validHTTPPath(value.Path) {
			return nil, validationError("discovered HTTP operation has an unsupported identity")
		}
		evidence := domain.ArchitectureEvidence{SourcePath: filepath.ToSlash(strings.TrimSpace(value.SourcePath))}
		if err := validateEvidence([]domain.ArchitectureEvidence{evidence}); err != nil {
			return nil, fmt.Errorf("discovered HTTP operation %s %s: %w", value.Method, value.Path, err)
		}
		key := value.Method + " " + value.Path
		operation := byIdentity[key]
		operation.method, operation.path = value.Method, value.Path
		if !containsScaffoldEvidence(operation.evidence, evidence) {
			operation.evidence = append(operation.evidence, evidence)
		}
		byIdentity[key] = operation
	}
	result := make([]discoveredHTTPOperation, 0, len(byIdentity))
	for _, operation := range byIdentity {
		sort.Slice(operation.evidence, func(i, j int) bool { return operation.evidence[i].SourcePath < operation.evidence[j].SourcePath })
		result = append(result, operation)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].method+" "+result[i].path < result[j].method+" "+result[j].path
	})
	return result, nil
}

func scaffoldService(root string) (domain.ArchitectureServiceManifest, bool, string, error) {
	const yamlPath = ".ai/architecture/service.yaml"
	const ymlPath = ".ai/architecture/service.yml"
	yamlContent, yamlExists, err := scaffoldReadOptional(root, yamlPath)
	if err != nil {
		return domain.ArchitectureServiceManifest{}, false, "", err
	}
	ymlContent, ymlExists, err := scaffoldReadOptional(root, ymlPath)
	if err != nil {
		return domain.ArchitectureServiceManifest{}, false, "", err
	}
	if yamlExists && ymlExists {
		return domain.ArchitectureServiceManifest{}, false, "", validationError("both service.yaml and service.yml exist")
	}
	if !yamlExists && !ymlExists {
		return domain.ArchitectureServiceManifest{}, false, yamlPath, nil
	}
	content, path := yamlContent, yamlPath
	if ymlExists {
		content, path = ymlContent, ymlPath
	}
	if len(content) == 0 {
		return domain.ArchitectureServiceManifest{}, false, "", validationError("existing service manifest %q is empty", path)
	}
	service, err := ParseService(content)
	if err != nil {
		return domain.ArchitectureServiceManifest{}, false, "", fmt.Errorf("parse existing service manifest %q: %w", path, err)
	}
	return service, true, path, nil
}

func scaffoldServiceID(report domain.DiscoveryReport, service domain.ArchitectureServiceManifest, exists bool) string {
	if exists {
		return service.ID
	}
	for _, manifest := range report.ArchitectureManifests {
		if manifest.Kind == "service" && validID(manifest.ID) {
			return manifest.ID
		}
	}
	return scaffoldIdentifier(report.ProjectName, "service")
}

func scaffoldExistingEndpoints(root, serviceID string, discovered []discoveredHTTPOperation) (map[string]scaffoldOperation, error) {
	directory := filepath.Join(root, ".ai", "architecture", "endpoints")
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]scaffoldOperation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect endpoint manifest directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, validationError("endpoint manifest directory is not a non-symbolic-link directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read endpoint manifest directory: %w", err)
	}
	known := make(map[string]struct{}, len(discovered))
	for _, operation := range discovered {
		known[operation.method+" "+operation.path] = struct{}{}
	}
	result := map[string]scaffoldOperation{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, validationError("symbolic links are not allowed in endpoint manifest directory")
		}
		if entry.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("read endpoint manifest %q: %w", name, err)
		}
		operation, err := ParseOperation(content)
		if err != nil {
			return nil, fmt.Errorf("parse existing endpoint manifest %q: %w", name, err)
		}
		if operation.Type != domain.ArchitectureOperationHTTP {
			continue
		}
		if operation.ServiceID != serviceID || operation.Identity.HTTP == nil {
			return nil, validationError("existing HTTP endpoint manifest %q conflicts with service %q", name, serviceID)
		}
		key := operation.Identity.HTTP.Method + " " + operation.Identity.HTTP.Path
		if _, exists := known[key]; !exists {
			return nil, validationError("existing HTTP endpoint manifest %q is not code-discovered", name)
		}
		if _, exists := result[key]; exists {
			return nil, validationError("multiple endpoint manifests describe %s", key)
		}
		result[key] = scaffoldOperation{relativePath: "endpoints/" + name, manifest: operation}
	}
	return result, nil
}

func validateExistingScaffoldService(service domain.ArchitectureServiceManifest, operations []scaffoldOperation) error {
	references := map[string]struct{}{}
	for _, path := range service.OperationManifests {
		references[path] = struct{}{}
	}
	grouped := map[string]struct{}{}
	for _, group := range service.EndpointGroups {
		for _, id := range group.Operations {
			grouped[id] = struct{}{}
		}
	}
	for _, operation := range operations {
		if _, exists := references[operation.relativePath]; !exists {
			return validationError("existing service manifest does not reference endpoint %q", operation.relativePath)
		}
		if _, exists := grouped[operation.manifest.ID]; !exists {
			return validationError("existing service manifest does not group endpoint %q", operation.manifest.ID)
		}
	}
	return nil
}

func scaffoldHTTPService(serviceID string, operations []scaffoldOperation, evidence []domain.ArchitectureEvidence) domain.ArchitectureServiceManifest {
	paths := make([]string, 0, len(operations))
	ids := make([]string, 0, len(operations))
	for _, operation := range operations {
		paths = append(paths, operation.relativePath)
		ids = append(ids, operation.manifest.ID)
	}
	unknown := scaffoldUnknown(evidence)
	return domain.ArchitectureServiceManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: serviceID, ManifestRevision: 1,
		Identity:         domain.ArchitectureServiceIdentity{Name: serviceID, Kind: "unknown"},
		Purpose:          unknown,
		Responsibilities: []domain.ArchitectureStatement{unknown},
		Capabilities:     []domain.ArchitectureStatement{unknown},
		OwnedResources: []domain.ArchitectureOwnedResource{{
			Type: "unknown", Name: "unknown", Description: unknown,
		}},
		InboundInterfaces: []domain.ArchitectureInterface{{
			Transport: "unknown", Name: "unknown", Description: unknown,
		}},
		OutboundDependencies: []domain.ArchitectureExternalInteraction{{
			ID: "unknown", Transport: "unknown", Target: "unknown", Direction: "unknown", Description: unknown,
		}},
		EndpointGroups: []domain.ArchitectureEndpointGroup{{
			ID: "http", Name: "HTTP endpoints", Description: unknown, Operations: ids,
		}},
		ProducedContracts: []domain.ArchitectureContractReference{{
			Transport: "unknown", Code: "unknown", Direction: "unknown", Description: unknown,
		}},
		ConsumedContracts: []domain.ArchitectureContractReference{{
			Transport: "unknown", Code: "unknown", Direction: "unknown", Description: unknown,
		}},
		PublishedEvents: []domain.ArchitectureContractReference{{
			Transport: "unknown", Code: "unknown", Direction: "unknown", Description: unknown,
		}},
		SubscribedEvents: []domain.ArchitectureContractReference{{
			Transport: "unknown", Code: "unknown", Direction: "unknown", Description: unknown,
		}},
		BusinessRules:      []domain.ArchitectureStatement{unknown},
		OperationManifests: paths,
		Evidence:           evidence,
		Confidence:         0,
	}
}

func scaffoldHTTPManifest(serviceID, id string, operation discoveredHTTPOperation) domain.ArchitectureOperationManifest {
	unknown := scaffoldUnknown(operation.evidence)
	return domain.ArchitectureOperationManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: id, ManifestRevision: 1,
		ServiceID: serviceID, Type: domain.ArchitectureOperationHTTP,
		Identity: domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{
			Method: operation.method, Path: operation.path,
		}},
		Access: domain.ArchitectureOperationAccess{
			Audience: unknown, Authentication: unknown, Authorization: unknown, Idempotency: unknown,
		},
		Trigger:      domain.ArchitectureOperationTrigger{Description: unknown},
		BusinessTask: unknown,
		BusinessProcess: []domain.ArchitectureOperationStep{{
			ID: "unknown", Description: unknown,
		}},
		BusinessRules:  []domain.ArchitectureStatement{unknown},
		Implementation: domain.ArchitectureImplementationFlow{Router: append([]domain.ArchitectureEvidence(nil), operation.evidence...)},
		DataAccess: []domain.ArchitectureDataAccess{{
			Resource: "unknown", Access: "unknown", Description: unknown,
		}},
		ExternalInteractions: []domain.ArchitectureExternalInteraction{{
			ID: "unknown", Transport: "unknown", Target: "unknown", Direction: "unknown", Description: unknown,
		}},
		SideEffects: []domain.ArchitectureSideEffect{{
			Type: "unknown", Description: unknown,
		}},
		Output: domain.ArchitectureOperationOutput{
			Result: &domain.ArchitectureSchemaRef{Name: "unknown", Description: unknown},
			EmittedEvents: []domain.ArchitectureContractReference{{
				Transport: "unknown", Code: "unknown", Direction: "unknown", Description: unknown,
			}},
		},
		Errors:     []domain.ArchitectureOperationError{{Code: "unknown", Description: unknown}},
		Evidence:   operation.evidence,
		Confidence: 0,
	}
}

func scaffoldUnknown(evidence []domain.ArchitectureEvidence) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: "unknown", Confidence: 0, Evidence: append([]domain.ArchitectureEvidence(nil), evidence...)}
}

func discoveredEvidence(operations []discoveredHTTPOperation) []domain.ArchitectureEvidence {
	var result []domain.ArchitectureEvidence
	for _, operation := range operations {
		for _, evidence := range operation.evidence {
			if !containsScaffoldEvidence(result, evidence) {
				result = append(result, evidence)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourcePath < result[j].SourcePath })
	return result
}

func containsScaffoldEvidence(values []domain.ArchitectureEvidence, wanted domain.ArchitectureEvidence) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func scaffoldOperationID(method, path string) string {
	sum := sha256.Sum256([]byte(method + " " + path))
	base := scaffoldIdentifier(strings.ToLower(method)+"-"+strings.Trim(path, "/"), "root")
	if len(base) > 110 {
		base = base[:110]
	}
	return base + "-" + fmt.Sprintf("%x", sum[:6])
}

func scaffoldIdentifier(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := true
	for _, runeValue := range value {
		if runeValue >= 'a' && runeValue <= 'z' || runeValue >= '0' && runeValue <= '9' {
			builder.WriteRune(runeValue)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" || result[0] < 'a' || result[0] > 'z' {
		result = fallback + "-" + result
	}
	if len(result) > 128 {
		result = strings.Trim(result[:128], "-.")
	}
	return result
}

func scaffoldReadOptional(root, relativePath string) ([]byte, bool, error) {
	path, err := scaffoldExistingPath(root, relativePath, true)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect architecture manifest %q: %w", relativePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, validationError("architecture manifest %q is not a regular file", relativePath)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read architecture manifest %q: %w", relativePath, err)
	}
	return content, true, nil
}

func scaffoldMissingTarget(root, relativePath string) error {
	path, err := scaffoldExistingPath(root, relativePath, true)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect scaffold target %q: %w", relativePath, err)
	}
	return validationError("scaffold target %q already exists", relativePath)
}

func scaffoldEnsureParent(root, relativePath string) error {
	directory := filepath.Dir(relativePath)
	if directory == "." {
		return validationError("scaffold target %q has no architecture directory", relativePath)
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(filepath.FromSlash(directory)), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create architecture manifest directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect architecture manifest directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return validationError("architecture manifest parent %q is not a non-symbolic-link directory", directory)
		}
	}
	return nil
}

func scaffoldCreateNew(root, relativePath string, content []byte) (string, error) {
	if err := scaffoldMissingTarget(root, relativePath); err != nil {
		return "", err
	}
	path, err := scaffoldExistingPath(root, relativePath, true)
	if err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".architecture-scaffold-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create scaffold temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("set scaffold temporary permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write scaffold temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync scaffold temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close scaffold temporary file: %w", err)
	}
	// Link is an atomic create-only publication: unlike Rename it cannot replace
	// a file that appeared after planning.
	if err := os.Link(temporaryPath, path); err != nil {
		return "", fmt.Errorf("create scaffold target %q: %w", relativePath, err)
	}
	return path, nil
}

func scaffoldExistingPath(root, relativePath string, allowMissingFinal bool) (string, error) {
	relativePath = filepath.Clean(filepath.FromSlash(relativePath))
	if relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", validationError("unsafe scaffold path %q", relativePath)
	}
	parts := strings.Split(relativePath, string(filepath.Separator))
	current := root
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", validationError("unsafe scaffold path %q", relativePath)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if allowMissingFinal || index < len(parts)-1 {
				return filepath.Join(append([]string{root}, parts...)...), nil
			}
			return "", fmt.Errorf("inspect scaffold path %q: %w", relativePath, err)
		}
		if err != nil {
			return "", fmt.Errorf("inspect scaffold path %q: %w", relativePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", validationError("symbolic links are not allowed in scaffold path %q", relativePath)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", validationError("scaffold parent %q is not a directory", relativePath)
		}
	}
	return current, nil
}
