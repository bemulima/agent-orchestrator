package architecturemanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/contractref"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// AuditInput joins a repository root with the immutable discovery report that
// belongs to that exact source. The auditor deliberately accepts the report as
// data: it never invokes discovery and therefore remains safe to use in a
// multi-repository batch without introducing a package cycle.
type AuditInput struct {
	ServiceRoot string
	Report      domain.DiscoveryReport
}

// AuditFinding is a deterministic, non-secret diagnostic. Unknown findings
// describe an area that cannot be proven from the supplied discovery report;
// validation findings describe a malformed or incomplete local artifact.
type AuditFinding struct {
	ServiceID string `json:"service_id"`
	Path      string `json:"path,omitempty"`
	Operation string `json:"operation,omitempty"`
	Message   string `json:"message"`
}

// ServiceAudit is the per-root audit projection. Counts are intentionally kept
// here as well as at the platform level so a caller can explain one service's
// status without reconstructing it from aggregate totals.
type ServiceAudit struct {
	ServiceRoot                    string         `json:"service_root"`
	ServiceID                      string         `json:"service_id"`
	HasArchitectureManifest        bool           `json:"has_architecture_manifest"`
	Blocked                        bool           `json:"blocked"`
	DiscoveredOperations           int            `json:"discovered_operations"`
	HTTPOperations                 int            `json:"http_operations"`
	NATSRequestReplyOperations     int            `json:"nats_request_reply_operations"`
	EventOperations                int            `json:"event_operations"`
	WorkerOperations               int            `json:"worker_operations"`
	ScheduledOperations            int            `json:"scheduled_operations"`
	OperationsWithManifests        int            `json:"operations_with_manifests"`
	OperationsMissingManifests     int            `json:"operations_missing_manifests"`
	OperationsBlocked              int            `json:"operations_blocked"`
	DeclaredOperationManifestCount int            `json:"declared_operation_manifest_count"`
	ParsedOperationManifestCount   int            `json:"parsed_operation_manifest_count"`
	GeneratedServiceMermaid        bool           `json:"generated_service_mermaid"`
	GeneratedOperationMermaidCount int            `json:"generated_operation_mermaid_count"`
	ValidationErrors               []AuditFinding `json:"validation_errors"`
	UnknownAreas                   []AuditFinding `json:"unknown_areas"`
}

// AuditReport is the complete S3 completeness projection. Mermaid counts mean
// generated presentation files present beside successfully parsed manifests;
// the auditor does not rewrite them.
type AuditReport struct {
	TotalBackendServices              int `json:"total_backend_services"`
	ServicesWithArchitectureManifests int `json:"services_with_architecture_manifests"`
	ServicesMissingManifests          int `json:"services_missing_manifests"`
	ServicesBlocked                   int `json:"services_blocked"`

	TotalDiscoveredOperations  int `json:"total_discovered_operations"`
	HTTPOperations             int `json:"http_operations"`
	NATSRequestReplyOperations int `json:"nats_request_reply_operations"`
	EventOperations            int `json:"event_operations"`
	WorkerOperations           int `json:"worker_operations"`
	ScheduledOperations        int `json:"scheduled_operations"`

	OperationsWithManifests    int `json:"operations_with_manifests"`
	OperationsMissingManifests int `json:"operations_missing_manifests"`
	OperationsBlocked          int `json:"operations_blocked"`

	GeneratedServiceMermaidCount   int            `json:"generated_service_mermaid_count"`
	GeneratedOperationMermaidCount int            `json:"generated_operation_mermaid_count"`
	ValidationErrors               []AuditFinding `json:"validation_errors"`
	UnknownAreas                   []AuditFinding `json:"unknown_areas"`
	Services                       []ServiceAudit `json:"services"`
}

// Audit reads and validates all supplied service roots in deterministic order.
// A malformed service is reported in the result and does not prevent other
// services from being audited. The returned error is reserved for invalid
// input to the audit itself (for example a duplicate root identity).
func Audit(inputs []AuditInput) (AuditReport, error) {
	ordered := append([]AuditInput(nil), inputs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := auditServiceKey(ordered[i])
		right := auditServiceKey(ordered[j])
		return left < right
	})

	result := AuditReport{Services: []ServiceAudit{}, ValidationErrors: []AuditFinding{}, UnknownAreas: []AuditFinding{}}
	seen := make(map[string]struct{}, len(ordered))
	for _, input := range ordered {
		key := auditServiceKey(input)
		if _, exists := seen[key]; exists {
			return AuditReport{}, fmt.Errorf("duplicate audit service %q: %w", key, domain.ErrConflict)
		}
		seen[key] = struct{}{}

		service := auditService(input)
		result.TotalBackendServices++
		if service.HasArchitectureManifest {
			result.ServicesWithArchitectureManifests++
		} else {
			result.ServicesMissingManifests++
		}
		if service.Blocked {
			result.ServicesBlocked++
		}
		result.TotalDiscoveredOperations += service.DiscoveredOperations
		result.HTTPOperations += service.HTTPOperations
		result.NATSRequestReplyOperations += service.NATSRequestReplyOperations
		result.EventOperations += service.EventOperations
		result.WorkerOperations += service.WorkerOperations
		result.ScheduledOperations += service.ScheduledOperations
		result.OperationsWithManifests += service.OperationsWithManifests
		result.OperationsMissingManifests += service.OperationsMissingManifests
		result.OperationsBlocked += service.OperationsBlocked
		if service.GeneratedServiceMermaid {
			result.GeneratedServiceMermaidCount++
		}
		result.GeneratedOperationMermaidCount += service.GeneratedOperationMermaidCount
		result.ValidationErrors = append(result.ValidationErrors, service.ValidationErrors...)
		result.UnknownAreas = append(result.UnknownAreas, service.UnknownAreas...)
		result.Services = append(result.Services, service)
	}
	return result, nil
}

func auditService(input AuditInput) ServiceAudit {
	service := ServiceAudit{ServiceRoot: filepath.Clean(input.ServiceRoot), ValidationErrors: []AuditFinding{}, UnknownAreas: []AuditFinding{}}
	if strings.TrimSpace(input.ServiceRoot) == "" {
		service.Blocked = true
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{Message: "service root is empty"})
		return service
	}
	root, err := filepath.Abs(filepath.Clean(input.ServiceRoot))
	if err != nil {
		service.Blocked = true
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{Message: "resolve service root: " + err.Error()})
		return service
	}
	service.ServiceRoot = root

	if info, rootErr := os.Lstat(root); rootErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		service.Blocked = true
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{Message: "service root is not a regular non-symlink directory"})
		return finalizeServiceAudit(service)
	}
	populateDiscoveredCounts(&service, input.Report.Operations)
	serviceBytes, err := readAuditFile(root, serviceManifestPath)
	if err != nil {
		service.Blocked = false
		service.OperationsMissingManifests = service.DiscoveredOperations
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{Path: serviceManifestPath, Message: err.Error()})
		service.UnknownAreas = append(service.UnknownAreas, AuditFinding{Path: serviceManifestPath, Message: "service architecture manifest is unavailable"})
		return finalizeServiceAudit(service)
	}
	manifest, err := ParseService(serviceBytes)
	if err != nil {
		service.Blocked = true
		service.OperationsMissingManifests = service.DiscoveredOperations
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{Path: serviceManifestPath, Message: err.Error()})
		return finalizeServiceAudit(service)
	}
	service.HasArchitectureManifest = true
	service.ServiceID = manifest.ID
	service.DeclaredOperationManifestCount = len(manifest.OperationManifests)

	loaded := make([]domain.ArchitectureOperationManifest, 0, len(manifest.OperationManifests))
	for _, relative := range StableOperationPaths(manifest) {
		operationBytes, readErr := readAuditFile(root, filepath.ToSlash(filepath.Join(".ai", "architecture", relative)))
		if readErr != nil {
			service.Blocked = true
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Path: filepath.ToSlash(filepath.Join(".ai", "architecture", relative)), Message: readErr.Error()})
			continue
		}
		operation, parseErr := ParseOperation(operationBytes)
		if parseErr != nil {
			service.Blocked = true
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Path: filepath.ToSlash(filepath.Join(".ai", "architecture", relative)), Message: parseErr.Error()})
			continue
		}
		if operation.ServiceID != manifest.ID {
			service.Blocked = true
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Operation: operation.ID, Path: relative, Message: fmt.Sprintf("operation belongs to service %q", operation.ServiceID)})
			continue
		}
		loaded = append(loaded, operation)
		mermaidPath := mermaidPathForOperation(relative)
		if mermaidOK, mermaidErr := auditGeneratedFile(root, mermaidPath); mermaidOK {
			service.GeneratedOperationMermaidCount++
		} else if mermaidErr != nil {
			service.Blocked = true
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Operation: operation.ID, Path: mermaidPath, Message: mermaidErr.Error()})
		} else {
			service.Blocked = true
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Operation: operation.ID, Path: mermaidPath, Message: "generated Mermaid file is missing"})
		}
	}
	service.ParsedOperationManifestCount = len(loaded)
	if ok, mermaidErr := auditGeneratedFile(root, serviceMermaidPath); ok {
		service.GeneratedServiceMermaid = true
	} else if mermaidErr != nil {
		service.Blocked = true
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Path: serviceMermaidPath, Message: mermaidErr.Error()})
	} else {
		service.Blocked = true
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Path: serviceMermaidPath, Message: "generated Mermaid file is missing"})
	}

	service.DiscoveredOperations = len(input.Report.Operations)
	manifestSignatures := make(map[string]int, len(loaded))
	manifestBySourceType := make(map[string][]string)
	for _, operation := range loaded {
		if signature, ok := manifestSignature(operation); ok {
			manifestSignatures[signature]++
			if operation.Type == domain.ArchitectureOperationWorker || operation.Type == domain.ArchitectureOperationScheduled {
				for _, evidence := range operation.Evidence {
					key := operationSourceTypeKey(operation.Type, evidence.SourcePath)
					if key != "" && !containsAuditSignature(manifestBySourceType[key], signature) {
						manifestBySourceType[key] = append(manifestBySourceType[key], signature)
					}
				}
			}
		} else {
			service.OperationsBlocked++
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Operation: operation.ID, Message: "operation identity cannot be normalized"})
		}
	}
	matched := make(map[string]struct{}, len(manifestSignatures))
	for _, operation := range input.Report.Operations {
		signature, ok := discoveredSignature(operation)
		if !ok {
			service.OperationsBlocked++
			service.UnknownAreas = append(service.UnknownAreas, AuditFinding{ServiceID: manifest.ID, Operation: discoveredOperationName(operation), Path: operation.SourcePath, Message: "discovered operation identity cannot be normalized"})
			continue
		}
		switch manifestSignatures[signature] {
		case 0:
			if fallback := uniqueSourceOperationManifest(operation, manifestBySourceType, manifestSignatures, matched); fallback != "" {
				service.OperationsWithManifests++
				matched[fallback] = struct{}{}
				continue
			}
			service.OperationsMissingManifests++
			service.UnknownAreas = append(service.UnknownAreas, AuditFinding{ServiceID: manifest.ID, Operation: discoveredOperationName(operation), Path: operation.SourcePath, Message: "discovered operation has no matching manifest"})
		case 1:
			service.OperationsWithManifests++
			matched[signature] = struct{}{}
		default:
			service.OperationsBlocked++
			service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Operation: discoveredOperationName(operation), Path: operation.SourcePath, Message: "multiple manifests match discovered operation"})
		}
	}
	for signature, count := range manifestSignatures {
		if _, ok := matched[signature]; !ok {
			service.UnknownAreas = append(service.UnknownAreas, AuditFinding{ServiceID: manifest.ID, Message: fmt.Sprintf("manifest identity %q has no proven discovery match", signature)})
			if count > 1 {
				service.Blocked = true
			}
		}
	}

	// The service manifest is the declaration boundary. Extra YAML files under
	// the operation directories are surfaced rather than silently counted.
	for _, extra := range unlistedOperationManifests(root, manifest.OperationManifests) {
		service.ValidationErrors = append(service.ValidationErrors, AuditFinding{ServiceID: manifest.ID, Path: extra, Message: "operation manifest is not declared by service.yaml"})
	}
	return finalizeServiceAudit(service)
}

func populateDiscoveredCounts(service *ServiceAudit, operations []domain.DiscoveredOperation) {
	service.DiscoveredOperations = len(operations)
	for _, operation := range operations {
		switch operation.Type {
		case domain.ArchitectureOperationHTTP:
			service.HTTPOperations++
		case domain.ArchitectureOperationNATSRequestReply:
			service.NATSRequestReplyOperations++
		case domain.ArchitectureOperationNATSEventSubscriber:
			service.EventOperations++
		case domain.ArchitectureOperationWorker:
			service.WorkerOperations++
		case domain.ArchitectureOperationScheduled:
			service.ScheduledOperations++
		}
	}
}

func finalizeServiceAudit(service ServiceAudit) ServiceAudit {
	sortAuditFindings(service.ValidationErrors)
	sortAuditFindings(service.UnknownAreas)
	return service
}

func auditServiceKey(input AuditInput) string {
	if strings.TrimSpace(input.Report.ProjectID) != "" {
		return input.Report.ProjectID
	}
	return filepath.Clean(input.ServiceRoot)
}

func readAuditFile(root, relative string) ([]byte, error) {
	path, err := auditPath(root, relative)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: file is not a regular non-symlink", relative)
	}
	return os.ReadFile(path)
}

func auditGeneratedFile(root, relative string) (bool, error) {
	path, err := auditPath(root, relative)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("inspect %s: file is not a regular non-symlink", relative)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", relative, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return false, fmt.Errorf("inspect %s: generated Mermaid file is empty", relative)
	}
	return true, nil
}

func auditPath(root, relative string) (string, error) {
	if strings.TrimSpace(root) == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("unsafe architecture path %q", relative)
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve architecture root: %w", err)
	}
	cleanRelative := filepath.Clean(filepath.FromSlash(relative))
	if cleanRelative == "." || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe architecture path %q", relative)
	}
	path := filepath.Join(cleanRoot, cleanRelative)
	if path != cleanRoot && !strings.HasPrefix(path, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("architecture path escapes root %q", relative)
	}
	current := cleanRoot
	parts := strings.Split(cleanRelative, string(filepath.Separator))
	for index, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			// A missing parent is reported by the eventual read/stat call. Do
			// not turn a normal missing manifest into an unsafe-path error.
			if os.IsNotExist(statErr) {
				return path, nil
			}
			return "", fmt.Errorf("inspect architecture path component %q: %w", strings.Join(parts[:index+1], string(filepath.Separator)), statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symbolic links are not allowed in architecture path %q", relative)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("architecture path component %q is not a directory", strings.Join(parts[:index+1], string(filepath.Separator)))
		}
	}
	return path, nil
}

func mermaidPathForOperation(relative string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(filepath.ToSlash(relative), ".yaml"), ".yml")
	return filepath.ToSlash(filepath.Join(".ai", "architecture", trimmed+".mmd"))
}

func manifestSignature(operation domain.ArchitectureOperationManifest) (string, bool) {
	identity := operation.Identity
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		if identity.HTTP == nil {
			return "", false
		}
		method, path, ok := contractref.HTTP(identity.HTTP.Method + " " + identity.HTTP.Path)
		return stableAuditKey(string(operation.Type), method, path), ok
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		if identity.NATS == nil {
			return "", false
		}
		subject, ok := contractref.EventSubject(identity.NATS.Subject)
		return stableAuditKey(string(operation.Type), subject), ok
	case domain.ArchitectureOperationWorker:
		if identity.Worker == nil || strings.TrimSpace(identity.Worker.Name) == "" {
			return "", false
		}
		return stableAuditKey(string(operation.Type), strings.TrimSpace(identity.Worker.Name)), true
	case domain.ArchitectureOperationScheduled:
		if identity.Scheduled == nil || strings.TrimSpace(identity.Scheduled.Name) == "" || strings.TrimSpace(identity.Scheduled.Schedule) == "" {
			return "", false
		}
		return stableAuditKey(string(operation.Type), strings.TrimSpace(identity.Scheduled.Name), strings.TrimSpace(identity.Scheduled.Schedule)), true
	default:
		return "", false
	}
}

func discoveredSignature(operation domain.DiscoveredOperation) (string, bool) {
	switch operation.Type {
	case domain.ArchitectureOperationHTTP:
		method, path, ok := contractref.HTTP(operation.Method + " " + operation.Path)
		return stableAuditKey(string(operation.Type), method, path), ok
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		subject, ok := contractref.EventSubject(operation.Subject)
		return stableAuditKey(string(operation.Type), subject), ok
	case domain.ArchitectureOperationWorker:
		name := strings.TrimSpace(operation.Name)
		return stableAuditKey(string(operation.Type), name), name != ""
	case domain.ArchitectureOperationScheduled:
		name, schedule := strings.TrimSpace(operation.Name), strings.TrimSpace(operation.Schedule)
		return stableAuditKey(string(operation.Type), name, schedule), name != "" && schedule != ""
	default:
		return "", false
	}
}

func stableAuditKey(values ...string) string { return strings.Join(values, "\x00") }

// uniqueSourceOperationManifest permits a worker/scheduled manifest whose
// stable business identity differs from the implementation function name to
// match a single source-evidenced discovery. HTTP and NATS identities remain
// strict transport contracts and never use this fallback.
func uniqueSourceOperationManifest(
	operation domain.DiscoveredOperation,
	bySourceType map[string][]string,
	manifestSignatures map[string]int,
	matched map[string]struct{},
) string {
	if operation.Type != domain.ArchitectureOperationWorker && operation.Type != domain.ArchitectureOperationScheduled {
		return ""
	}
	candidates := bySourceType[operationSourceTypeKey(operation.Type, operation.SourcePath)]
	result := ""
	for _, candidate := range candidates {
		if manifestSignatures[candidate] != 1 {
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

func operationSourceTypeKey(kind domain.ArchitectureOperationType, sourcePath string) string {
	sourcePath = filepath.ToSlash(strings.TrimSpace(sourcePath))
	if sourcePath == "" {
		return ""
	}
	return stableAuditKey(string(kind), sourcePath)
}

func containsAuditSignature(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func discoveredOperationName(operation domain.DiscoveredOperation) string {
	if operation.Type == domain.ArchitectureOperationHTTP {
		return strings.TrimSpace(operation.Method) + " " + strings.TrimSpace(operation.Path)
	}
	if operation.Type == domain.ArchitectureOperationNATSRequestReply || operation.Type == domain.ArchitectureOperationNATSEventSubscriber {
		return strings.TrimSpace(operation.Subject)
	}
	if operation.Type == domain.ArchitectureOperationScheduled {
		return strings.TrimSpace(operation.Name) + " " + strings.TrimSpace(operation.Schedule)
	}
	return strings.TrimSpace(operation.Name)
}

func sortAuditFindings(values []AuditFinding) {
	sort.Slice(values, func(i, j int) bool {
		left := stableAuditKey(values[i].ServiceID, values[i].Path, values[i].Operation, values[i].Message)
		right := stableAuditKey(values[j].ServiceID, values[j].Path, values[j].Operation, values[j].Message)
		return left < right
	})
}

func unlistedOperationManifests(root string, declared []string) []string {
	declaredSet := make(map[string]struct{}, len(declared))
	for _, path := range declared {
		declaredSet[filepath.ToSlash(filepath.Join(".ai", "architecture", path))] = struct{}{}
	}
	var result []string
	for _, directory := range []string{".ai/architecture/endpoints", ".ai/architecture/operations"} {
		directoryPath := filepath.Join(root, filepath.FromSlash(directory))
		if info, err := os.Lstat(directoryPath); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(directoryPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || (entry.Type()&os.ModeSymlink) != 0 {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			relative := filepath.ToSlash(filepath.Join(directory, name))
			if _, ok := declaredSet[relative]; !ok {
				result = append(result, relative)
			}
		}
	}
	sort.Strings(result)
	return result
}
