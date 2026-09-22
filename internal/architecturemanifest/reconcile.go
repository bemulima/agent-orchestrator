package architecturemanifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"gopkg.in/yaml.v3"
)

// ReconcileResult reports only files actually changed, in absolute sorted paths.
type ReconcileResult struct {
	DiscoveredHTTP int      `json:"discovered_http"`
	PreservedHTTP  int      `json:"preserved_http"`
	CreatedPaths   []string `json:"created_paths"`
	UpdatedPaths   []string `json:"updated_paths"`
	RemovedPaths   []string `json:"removed_paths"`
}

type reconcileFile struct {
	path            string
	before, after   []byte
	existed, remove bool
}

// ReconcileHTTPManifests explicitly reconciles a previously scaffolded service
// against fresh v19+ discovery from this exact checkout. It never derives an
// inventory from the manifests. Stale authored/enriched HTTP manifests are a
// hard error; current authored HTTP and every non-HTTP file are preserved.
//
// All manifests, targets, references and contents are preflighted before writes.
// Publication uses atomic per-file replacement, with optimistic concurrency
// checks and best-effort rollback on I/O failure. It is not a crash-atomic
// multi-file transaction. Callers must serialize changes to the checkout and
// rescan after success. Mermaid generation remains a separate explicit step.
func ReconcileHTTPManifests(root string, report domain.DiscoveryReport) (ReconcileResult, error) {
	root, err := secureRoot(root)
	if err != nil {
		return ReconcileResult{}, err
	}
	if root == filepath.VolumeName(root)+string(filepath.Separator) {
		return ReconcileResult{}, validationError("repository root cannot be filesystem root")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return ReconcileResult{}, validationError("repository root must not contain symbolic links")
	}
	reportRoot, err := filepath.Abs(report.RepositoryPath)
	if err != nil || report.RepositoryPath == "" || filepath.Clean(reportRoot) != root || report.SchemaVersion < 19 {
		return ReconcileResult{}, validationError("fresh v19+ discovery from this exact repository root is required")
	}
	if len(report.Inventory.Warnings) > 0 {
		return ReconcileResult{}, validationError("discovery inventory warnings must be resolved before reconciliation")
	}
	discovered, err := scaffoldDiscoveredHTTP(report.Operations)
	if err != nil {
		return ReconcileResult{}, err
	}
	if len(discovered) == 0 {
		return ReconcileResult{}, validationError("refusing to reconcile an empty HTTP discovery inventory")
	}
	service, exists, servicePath, err := scaffoldService(root)
	if err != nil {
		return ReconcileResult{}, err
	}
	if !exists || servicePath != serviceManifestPath {
		return ReconcileResult{}, validationError("an existing service.yaml is required")
	}
	if report.ProjectName != service.ID && report.ProjectName != service.Identity.Name {
		return ReconcileResult{}, validationError("discovery project does not match the service")
	}
	serviceBytes, err := readSecureFile(root, servicePath)
	if err != nil {
		return ReconcileResult{}, err
	}
	known := map[string]discoveredHTTPOperation{}
	for _, op := range discovered {
		known[op.method+" "+op.path] = op
	}
	oldHTTPIDs, oldHTTPPaths := map[string]bool{}, map[string]bool{}
	allIDs, listed := map[string]bool{}, map[string]bool{}
	preserved := map[string]scaffoldOperation{}
	var files []reconcileFile
	for _, relative := range service.OperationManifests {
		full := ".ai/architecture/" + relative
		content, err := readSecureFile(root, full)
		if err != nil {
			return ReconcileResult{}, err
		}
		operation, err := ParseOperation(content)
		if err != nil {
			return ReconcileResult{}, err
		}
		if operation.ServiceID != service.ID || allIDs[operation.ID] {
			return ReconcileResult{}, validationError("mismatched service or duplicate operation id %q", operation.ID)
		}
		allIDs[operation.ID], listed[relative] = true, true
		if operation.Type != domain.ArchitectureOperationHTTP {
			continue
		}
		if !strings.HasPrefix(relative, "endpoints/") {
			return ReconcileResult{}, validationError("HTTP manifest must be in endpoints/: %q", relative)
		}
		oldHTTPIDs[operation.ID], oldHTTPPaths[relative] = true, true
		key := operation.Identity.HTTP.Method + " " + operation.Identity.HTTP.Path
		if _, present := known[key]; present {
			if _, duplicate := preserved[key]; duplicate {
				return ReconcileResult{}, validationError("duplicate HTTP identity %q", key)
			}
			preserved[key] = scaffoldOperation{relativePath: relative, manifest: operation}
			continue
		}
		if relative != "endpoints/"+operation.ID+".yaml" || !isReconcileScaffold(operation) {
			return ReconcileResult{}, validationError("stale authored HTTP manifest requires manual reconciliation: %q", relative)
		}
		files = append(files, reconcileFile{path: full, before: content, existed: true, remove: true})
		mermaidPath := operationMermaidRelativePath(relative)
		mermaid, hasMermaid, err := scaffoldReadOptional(root, mermaidPath)
		if err != nil {
			return ReconcileResult{}, err
		}
		if hasMermaid {
			if string(mermaid) != OperationMermaid(operation) {
				return ReconcileResult{}, validationError("stale HTTP Mermaid has authored changes: %q", mermaidPath)
			}
			files = append(files, reconcileFile{path: mermaidPath, before: mermaid, existed: true, remove: true})
		}
	}
	// Unlisted YAML could otherwise be silently replaced or left as a ghost
	// endpoint. Reject it, including unlisted non-HTTP manifests.
	for _, directory := range []string{"endpoints", "operations"} {
		dir, err := scaffoldExistingPath(root, ".ai/architecture/"+directory, true)
		if err != nil {
			return ReconcileResult{}, err
		}
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ReconcileResult{}, err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
				return ReconcileResult{}, validationError("unexpected directory or symbolic link in %s", directory)
			}
			if (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) && !listed[directory+"/"+entry.Name()] {
				return ReconcileResult{}, validationError("unlisted operation manifest %q", directory+"/"+entry.Name())
			}
		}
	}
	for _, group := range service.EndpointGroups {
		for _, id := range group.Operations {
			if !allIDs[id] {
				return ReconcileResult{}, validationError("endpoint group references unknown operation %q", id)
			}
		}
	}
	var httpOperations []scaffoldOperation
	for _, op := range discovered {
		if existing, ok := preserved[op.method+" "+op.path]; ok {
			httpOperations = append(httpOperations, existing)
			continue
		}
		id := scaffoldOperationID(op.method, op.path)
		if allIDs[id] {
			return ReconcileResult{}, validationError("generated HTTP id collides with existing operation %q", id)
		}
		relative := "endpoints/" + id + ".yaml"
		manifest := scaffoldHTTPManifest(service.ID, id, op)
		if err := ValidateOperation(manifest); err != nil {
			return ReconcileResult{}, err
		}
		content, err := yaml.Marshal(manifest)
		if err != nil {
			return ReconcileResult{}, err
		}
		full := ".ai/architecture/" + relative
		if err := scaffoldMissingTarget(root, full); err != nil {
			return ReconcileResult{}, err
		}
		if err := scaffoldMissingTarget(root, operationMermaidRelativePath(relative)); err != nil {
			return ReconcileResult{}, err
		}
		files = append(files, reconcileFile{path: full, after: content})
		httpOperations = append(httpOperations, scaffoldOperation{relativePath: relative, manifest: manifest})
	}
	paths := make([]string, 0, len(service.OperationManifests)+len(httpOperations))
	for _, relative := range service.OperationManifests {
		if !oldHTTPPaths[relative] {
			paths = append(paths, relative)
		}
	}
	ids := make([]string, 0, len(httpOperations))
	for _, op := range httpOperations {
		paths = append(paths, op.relativePath)
		ids = append(ids, op.manifest.ID)
	}
	sort.Strings(paths)
	sort.Strings(ids)
	groups := make([]domain.ArchitectureEndpointGroup, 0, len(service.EndpointGroups)+1)
	httpGroup := domain.ArchitectureEndpointGroup{ID: "http-reconciled", Name: "HTTP endpoints", Description: scaffoldUnknown(discoveredEvidence(discovered))}
	groupSelected := false
	for _, group := range service.EndpointGroups {
		retained := make([]string, 0, len(group.Operations))
		for _, id := range group.Operations {
			if !oldHTTPIDs[id] {
				retained = append(retained, id)
			}
		}
		if len(retained) == 0 && len(group.Operations) > 0 {
			if !groupSelected {
				httpGroup, groupSelected = group, true
			}
			continue
		}
		group.Operations = retained
		groups = append(groups, group)
	}
	for _, group := range groups {
		if group.ID == httpGroup.ID {
			return ReconcileResult{}, validationError("HTTP group id conflicts with preserved group %q", group.ID)
		}
	}
	httpGroup.Operations = ids
	groups = append(groups, httpGroup)
	service.OperationManifests, service.EndpointGroups = paths, groups
	if err := ValidateService(service); err != nil {
		return ReconcileResult{}, err
	}
	updatedService, err := reconcileServiceYAML(serviceBytes, paths, groups)
	if err != nil {
		return ReconcileResult{}, err
	}
	if _, err := ParseService(updatedService); err != nil {
		return ReconcileResult{}, err
	}
	if !bytes.Equal(serviceBytes, updatedService) {
		files = append(files, reconcileFile{path: servicePath, before: serviceBytes, after: updatedService, existed: true})
	}
	result := ReconcileResult{DiscoveredHTTP: len(discovered), PreservedHTTP: len(preserved)}
	if err := applyReconciliation(root, files); err != nil {
		return ReconcileResult{}, err
	}
	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file.path))
		switch {
		case file.remove:
			result.RemovedPaths = append(result.RemovedPaths, full)
		case file.existed:
			result.UpdatedPaths = append(result.UpdatedPaths, full)
		default:
			result.CreatedPaths = append(result.CreatedPaths, full)
		}
	}
	sort.Strings(result.CreatedPaths)
	sort.Strings(result.UpdatedPaths)
	sort.Strings(result.RemovedPaths)
	return result, nil
}

func isReconcileScaffold(operation domain.ArchitectureOperationManifest) bool {
	if operation.Identity.HTTP == nil || operation.ID != scaffoldOperationID(operation.Identity.HTTP.Method, operation.Identity.HTTP.Path) {
		return false
	}
	expected := scaffoldHTTPManifest(operation.ServiceID, operation.ID, discoveredHTTPOperation{method: operation.Identity.HTTP.Method, path: operation.Identity.HTTP.Path, evidence: operation.Evidence})
	// YAML normalizes omitted empty slices. Compare normalized documents rather
	// than treating nil/empty slices as proof of authored changes.
	left, _ := yaml.Marshal(operation)
	right, _ := yaml.Marshal(expected)
	return bytes.Equal(left, right)
}

func reconcileServiceYAML(content []byte, paths []string, groups []domain.ArchitectureEndpointGroup) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	for index := 0; index < len(root.Content); index += 2 {
		var replacement yaml.Node
		switch root.Content[index].Value {
		case "operation_manifests":
			if err := replacement.Encode(paths); err != nil {
				return nil, err
			}
		case "endpoint_groups":
			if err := replacement.Encode(groups); err != nil {
				return nil, err
			}
		default:
			continue
		}
		if reflect.DeepEqual(root.Content[index+1], &replacement) {
			continue
		}
		root.Content[index+1] = &replacement
	}
	return yaml.Marshal(&doc)
}

func applyReconciliation(root string, files []reconcileFile) error {
	// Check every original and every destination before even creating parents.
	for _, file := range files {
		if err := checkReconcileFile(root, file); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := scaffoldEnsureParent(root, file.path); err != nil {
			return err
		}
	}
	completed := make([]reconcileFile, 0, len(files))
	rollback := func(cause error) error {
		for index := len(completed) - 1; index >= 0; index-- {
			file := completed[index]
			// Never restore over a third party's intervening edit. The caller
			// gets an explicit rollback error requiring manual recovery instead.
			expected := reconcileFile{path: file.path, before: file.after, existed: !file.remove}
			if err := checkReconcileFile(root, expected); err != nil {
				cause = errors.Join(cause, fmt.Errorf("rollback %s: %w", file.path, err))
				continue
			}
			full, err := secureOutputPath(root, file.path)
			if err == nil {
				if file.existed {
					err = writeAtomic(full, string(file.before))
				} else {
					err = os.Remove(full)
				}
			}
			if err != nil {
				cause = errors.Join(cause, fmt.Errorf("rollback %s: %w", file.path, err))
			}
		}
		return cause
	}
	for _, file := range files {
		if err := checkReconcileFile(root, file); err != nil {
			return rollback(err)
		}
		full := filepath.Join(root, filepath.FromSlash(file.path))
		var err error
		if file.remove {
			err = os.Remove(full)
		} else if file.existed {
			err = writeAtomic(full, string(file.after))
		} else {
			_, err = scaffoldCreateNew(root, file.path, file.after)
		}
		if err != nil {
			return rollback(err)
		}
		completed = append(completed, file)
	}
	return nil
}

func checkReconcileFile(root string, file reconcileFile) error {
	content, exists, err := scaffoldReadOptional(root, file.path)
	if err != nil {
		return err
	}
	if exists != file.existed || !bytes.Equal(content, file.before) {
		return validationError("architecture file changed during reconciliation: %q", file.path)
	}
	return nil
}
