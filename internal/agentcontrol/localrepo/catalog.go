package localrepo

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
)

func LoadCatalogFromDirectory(root string) (agentcontrol.Catalog, error) {
	if strings.TrimSpace(root) == "" {
		return agentcontrol.Catalog{}, fmt.Errorf("repository root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return agentcontrol.Catalog{}, fmt.Errorf("resolve canonical source root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return agentcontrol.Catalog{}, fmt.Errorf("resolve canonical source root: %w", err)
	}
	agentSystem := filepath.Join(canonical, "agent-system")
	info, err := os.Lstat(agentSystem)
	if err != nil {
		return agentcontrol.Catalog{}, fmt.Errorf("inspect agent-system source tree: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return agentcontrol.Catalog{}, fmt.Errorf("agent-system source must be a regular directory")
	}
	err = filepath.WalkDir(agentSystem, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("canonical source tree contains symlink %q", current)
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("canonical source tree contains non-regular file %q", current)
		}
		return nil
	})
	if err != nil {
		return agentcontrol.Catalog{}, fmt.Errorf("validate agent-system source tree: %w", err)
	}
	return agentcontrol.LoadCatalog(os.DirFS(canonical))
}

// ReadGlobalPolicyTarget is deliberately read-only and requires an explicit
// .codex/AGENTS.md target. It rejects symlink components so a plan cannot be
// redirected outside the named user-config location.
func ReadGlobalPolicyTarget(target string) (string, []byte, bool, error) {
	if strings.TrimSpace(target) == "" {
		return "", nil, false, fmt.Errorf("explicit global policy target is required")
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", nil, false, err
	}
	if filepath.Base(absolute) != "AGENTS.md" || filepath.Base(filepath.Dir(absolute)) != ".codex" {
		return "", nil, false, fmt.Errorf("global policy target must be an explicit .codex/AGENTS.md path")
	}
	parent := filepath.Dir(absolute)
	if err := rejectSymlinkComponents(parent); err != nil {
		return "", nil, false, err
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return absolute, nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return "", nil, false, fmt.Errorf("global policy target must be a regular non-symlink file no larger than 2 MiB")
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		return "", nil, false, err
	}
	return absolute, content, true, nil
}

func rejectSymlinkComponents(pathname string) error {
	volume := filepath.VolumeName(pathname)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(pathname, current)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("global policy target contains symlink component %q", current)
		}
	}
	return nil
}
