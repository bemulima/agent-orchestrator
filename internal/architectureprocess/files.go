package architectureprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadCurrent loads only confirmed, version-controlled process manifests from
// .ai/architecture/processes. A missing directory means that no explicitly
// authored process has been declared yet. Candidate/unverified definitions are
// rejected here so callers cannot accidentally present them as CURRENT.
func LoadCurrent(root string) ([]Manifest, error) {
	root, err := secureRoot(root)
	if err != nil {
		return nil, err
	}
	directory, err := secureDirectory(root, ManifestDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return []Manifest{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read business process manifests: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, invalid("symbolic links are not allowed in business process manifests")
		}
		if entry.IsDir() {
			return nil, invalid("business process manifest directory cannot contain subdirectories")
		}
		if !entry.Type().IsRegular() {
			return nil, invalid("business process manifest is not a regular file")
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil, invalid("business process manifest directory contains unsupported file %q", name)
		}
		paths = append(paths, filepath.Join(directory, name))
	}
	sort.Strings(paths)

	manifests := make([]Manifest, 0, len(paths))
	ids := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read business process manifest %q: %w", filepath.Base(path), err)
		}
		manifest, err := Parse(content)
		if err != nil {
			return nil, fmt.Errorf("parse business process manifest %q: %w", filepath.Base(path), err)
		}
		if !manifest.Provenance.IsConfirmed() {
			return nil, invalid("candidate process %q cannot be loaded as CURRENT", manifest.ID)
		}
		if _, exists := ids[manifest.ID]; exists {
			return nil, invalid("duplicate business process id %q", manifest.ID)
		}
		ids[manifest.ID] = struct{}{}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func secureRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", invalid("repository root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect repository root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", invalid("repository root must be a non-symbolic-link directory")
	}
	return abs, nil
}

func secureDirectory(root, relative string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", invalid("unsafe business process directory")
	}
	current := root
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return "", invalid("unsafe business process directory")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", invalid("business process manifest directory is unsafe")
		}
	}
	return current, nil
}
