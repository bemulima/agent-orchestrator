package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// MaterializeCompositionRegression copies an immutable old test and recorded
// unimplemented scaffold, never the prior composition implementation or result.
func (w TaskWorktree) MaterializeCompositionRegression(ctx context.Context, project domain.Project, prior domain.CompositionAttempt, destination domain.TaskWorkspace, wp domain.CompositionWorkPackage) (map[string]string, error) {
	if err := w.VerifyCommit(ctx, project, prior.Workspace, prior.CommitSHA, prior.WorkPackage.AssemblyCommit, prior.ChangedFiles); err != nil {
		return nil, err
	}
	sourceInspection := prior.Workspace
	sourceInspection.BaseCommit = prior.CommitSHA
	sourceState, err := w.Inspect(ctx, project, sourceInspection)
	if err != nil || sourceState.HeadCommit != prior.CommitSHA || len(sourceState.ChangedFiles) != 0 {
		return nil, fmt.Errorf("prior composition fixture source is dirty: %w", domain.ErrConflict)
	}
	targetState, err := w.Inspect(ctx, project, destination)
	if err != nil || targetState.HeadCommit != wp.AssemblyCommit || destination.BaseCommit != wp.AssemblyCommit || len(targetState.ChangedFiles) != 0 {
		return nil, fmt.Errorf("composition regression target is not pristine input assembly: %w", domain.ErrConflict)
	}
	if prior.REDSetup == nil || len(prior.REDSetup.Sources) != 1 || len(wp.Verification.TestPaths) != 1 || len(wp.WriteScope.Allow) != 2 {
		return nil, domain.ErrInvalidStatus
	}
	sources := map[string]string{}
	for path, source := range prior.REDSetup.Sources {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil, domain.ErrWriteScope
		}
		digest := sha256.Sum256([]byte(source))
		if prior.REDSetup.Files[path] != hex.EncodeToString(digest[:]) {
			return nil, fmt.Errorf("recorded composition scaffold hash differs: %w", domain.ErrConflict)
		}
		sources[path] = source
	}
	testPath := wp.Verification.TestPaths[0]
	if !strings.HasSuffix(testPath, "_test.go") {
		return nil, domain.ErrWriteScope
	}
	found := false
	for _, path := range prior.ChangedFiles {
		if path == testPath {
			found = true
		}
	}
	if !found {
		return nil, domain.ErrWriteScope
	}
	test, err := w.ReadArtifact(ctx, prior.Workspace, testPath, 1<<20)
	if err != nil {
		return nil, err
	}
	sources[testPath] = string(test)
	if len(sources) != 2 {
		return nil, domain.ErrWriteScope
	}
	for path := range sources {
		allowed := false
		for _, item := range wp.WriteScope.Allow {
			if path == item {
				allowed = true
			}
		}
		if !allowed {
			return nil, domain.ErrWriteScope
		}
		relative, err := taskRelativePath(path)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(destination.Path, relative)
		parent, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err != nil || !pathWithin(destination.Path, parent) {
			return nil, domain.ErrForbidden
		}
		if info, err := os.Lstat(target); err != nil && !os.IsNotExist(err) || err == nil && !info.Mode().IsRegular() {
			return nil, domain.ErrForbidden
		}
	}
	for path, source := range sources {
		if err := os.WriteFile(filepath.Join(destination.Path, path), []byte(source), 0644); err != nil {
			return nil, err
		}
	}
	return sources, nil
}
