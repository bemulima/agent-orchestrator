package architecturemanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const (
	serviceManifestPath = ".ai/architecture/service.yaml"
	serviceMermaidPath  = ".ai/architecture/service.mmd"
)

// GenerateResult explicitly lists every Mermaid file created or replaced by
// GenerateFiles. Paths are absolute, sorted, and contain no source YAML paths.
type GenerateResult struct {
	OutputPaths []string
}

type plannedMermaidFile struct {
	relativePath string
	content      string
}

// GenerateFiles strictly loads the service-owned manifest files below root and
// materializes their Mermaid presentations. YAML is read only. Existing files
// are replaced only when they are the corresponding generated .mmd target.
//
// All manifest reads and generated writes reject path escapes and symbolic
// links. Parsing finishes before any output is modified, so invalid manifests
// cannot leave a partial generated representation behind.
func GenerateFiles(root string) (GenerateResult, error) {
	root, err := secureRoot(root)
	if err != nil {
		return GenerateResult{}, err
	}

	serviceBytes, err := readSecureFile(root, serviceManifestPath)
	if err != nil {
		return GenerateResult{}, err
	}
	service, err := ParseService(serviceBytes)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("parse %s: %w", serviceManifestPath, err)
	}

	type loadedOperation struct {
		manifest     domain.ArchitectureOperationManifest
		relativePath string
	}
	operations := make([]loadedOperation, 0, len(service.OperationManifests))
	planned := make([]plannedMermaidFile, 0, len(service.OperationManifests)+1)
	planned = append(planned, plannedMermaidFile{
		relativePath: serviceMermaidPath,
		content:      ServiceMermaid(service, nil),
	})

	operationIDs := make(map[string]struct{}, len(service.OperationManifests))
	for _, manifestPath := range StableOperationPaths(service) {
		operationRelativePath := filepath.ToSlash(filepath.Join(".ai", "architecture", manifestPath))
		operationBytes, err := readSecureFile(root, operationRelativePath)
		if err != nil {
			return GenerateResult{}, err
		}
		operation, err := ParseOperation(operationBytes)
		if err != nil {
			return GenerateResult{}, fmt.Errorf("parse .ai/architecture/%s: %w", filepath.ToSlash(manifestPath), err)
		}
		if operation.ServiceID != service.ID {
			return GenerateResult{}, validationError("operation %q belongs to service %q, not %q", operation.ID, operation.ServiceID, service.ID)
		}
		if _, exists := operationIDs[operation.ID]; exists {
			return GenerateResult{}, validationError("duplicate operation id %q", operation.ID)
		}
		operationIDs[operation.ID] = struct{}{}
		operations = append(operations, loadedOperation{manifest: operation, relativePath: manifestPath})
	}

	// The service diagram includes all operations. Rendering only starts after
	// every YAML document and cross-file invariant has been accepted.
	serviceOperations := make([]domain.ArchitectureOperationManifest, 0, len(operations))
	for _, operation := range operations {
		serviceOperations = append(serviceOperations, operation.manifest)
	}
	planned[0].content = ServiceMermaid(service, serviceOperations)
	for _, operation := range operations {
		planned = append(planned, plannedMermaidFile{
			relativePath: operationMermaidRelativePath(operation.relativePath),
			content:      OperationMermaid(operation.manifest),
		})
	}

	for _, file := range planned {
		if _, err := secureOutputPath(root, file.relativePath); err != nil {
			return GenerateResult{}, err
		}
	}
	for _, file := range planned {
		path, err := secureOutputPath(root, file.relativePath)
		if err != nil {
			return GenerateResult{}, err
		}
		if err := writeAtomic(path, file.content); err != nil {
			return GenerateResult{}, err
		}
	}

	outputPaths := make([]string, 0, len(planned))
	for _, file := range planned {
		path, err := secureOutputPath(root, file.relativePath)
		if err != nil {
			return GenerateResult{}, err
		}
		outputPaths = append(outputPaths, path)
	}
	sort.Strings(outputPaths)
	return GenerateResult{OutputPaths: outputPaths}, nil
}

// operationMermaidRelativePath is safe because every caller has already
// accepted the source through ValidateService. It deliberately preserves the
// source directory: HTTP manifests remain under endpoints/, while non-HTTP
// operations are generated under operations/.
func operationMermaidRelativePath(manifestPath string) string {
	return filepath.ToSlash(filepath.Join(".ai", "architecture", strings.TrimSuffix(strings.TrimSuffix(manifestPath, ".yaml"), ".yml")+".mmd"))
}

func secureRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", validationError("repository root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	info, err := os.Lstat(absRoot)
	if err != nil {
		return "", fmt.Errorf("inspect repository root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", validationError("repository root must be a non-symbolic-link directory")
	}
	return absRoot, nil
}

func readSecureFile(root, relativePath string) ([]byte, error) {
	path, err := secureExistingPath(root, relativePath, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func secureOutputPath(root, relativePath string) (string, error) {
	path, err := secureExistingPath(root, relativePath, true)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", validationError("generated Mermaid target %q is not a regular file", relativePath)
		}
		return path, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect generated Mermaid target %q: %w", relativePath, err)
	}
	return path, nil
}

// secureExistingPath verifies each path component through Lstat. It accepts a
// missing final component only when allowMissingFinal is true.
func secureExistingPath(root, relativePath string, allowMissingFinal bool) (string, error) {
	relativePath = filepath.Clean(filepath.FromSlash(relativePath))
	if relativePath == "." || filepath.IsAbs(relativePath) || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || relativePath == ".." {
		return "", validationError("unsafe architecture manifest path %q", relativePath)
	}
	parts := strings.Split(relativePath, string(filepath.Separator))
	current := root
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", validationError("unsafe architecture manifest path %q", relativePath)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingFinal && index == len(parts)-1 && os.IsNotExist(err) {
				return current, nil
			}
			return "", fmt.Errorf("inspect architecture manifest path %q: %w", relativePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", validationError("symbolic links are not allowed in architecture manifest path %q", relativePath)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", validationError("architecture manifest parent %q is not a directory", relativePath)
		}
		if index == len(parts)-1 && !allowMissingFinal && !info.Mode().IsRegular() {
			return "", validationError("architecture manifest %q is not a regular file", relativePath)
		}
	}
	return current, nil
}

func writeAtomic(path, content string) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".architecture-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create generated Mermaid temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set generated Mermaid file permissions: %w", err)
	}
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write generated Mermaid temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync generated Mermaid temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close generated Mermaid temporary file: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return validationError("generated Mermaid target %q changed to an unsafe file", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reinspect generated Mermaid target: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace generated Mermaid file: %w", err)
	}
	return nil
}

func validationError(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, domain.ErrValidation)...)
}
