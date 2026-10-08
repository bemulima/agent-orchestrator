package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// MaterializeVerifiedSources reproduces owned production bytes from an immutable
// verified predecessor without moving HEAD, the branch, or the execution base.
func (w TaskWorktree) MaterializeVerifiedSources(ctx context.Context, project domain.Project, source, destination domain.TaskWorkspace, commit, base string, changedFiles, paths []string) (map[string]string, error) {
	if err := w.VerifyCommit(ctx, project, source, commit, base, changedFiles); err != nil {
		return nil, err
	}
	if err := w.validateWorkspace(ctx, project, destination); err != nil {
		return nil, err
	}
	sourceInspection := source
	sourceInspection.BaseCommit = commit
	sourceState, err := w.Inspect(ctx, project, sourceInspection)
	if err != nil || sourceState.HeadCommit != commit || len(sourceState.ChangedFiles) != 0 {
		return nil, fmt.Errorf("preimage source differs from immutable verified commit: %w", domain.ErrConflict)
	}
	destinationState, err := w.Inspect(ctx, project, destination)
	if err != nil || destination.BaseCommit != base || destinationState.HeadCommit != base || len(destinationState.ChangedFiles) != 0 {
		return nil, fmt.Errorf("preimage destination is not clean at frozen baseline: %w", domain.ErrConflict)
	}
	sources := map[string]string{}
	for _, path := range paths {
		owned := false
		for _, changed := range changedFiles {
			if changed == path {
				owned = true
			}
		}
		if !owned || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil, fmt.Errorf("preimage permits only verified production Go paths: %w", domain.ErrWriteScope)
		}
		data, err := w.ReadArtifact(ctx, source, path, 1<<20)
		if err != nil {
			return nil, err
		}
		relative, err := taskRelativePath(path)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(destination.Path, relative)
		parent, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err != nil || !pathWithin(destination.Path, parent) {
			return nil, fmt.Errorf("preimage destination escaped workspace: %w", domain.ErrForbidden)
		}
		if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("preimage destination must be existing regular source: %w", domain.ErrForbidden)
		}
		sources[path] = string(data)
	}
	for path, data := range sources {
		if err := os.WriteFile(filepath.Join(destination.Path, path), []byte(data), 0644); err != nil {
			return nil, err
		}
	}
	return sources, nil
}
