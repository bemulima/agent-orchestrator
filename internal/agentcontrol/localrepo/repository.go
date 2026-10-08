package localrepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
)

const (
	maxProvenanceBytes   = int64(2 << 20)
	maxManagedAssetBytes = int64(4 << 20)
)

type LocalRepository struct {
	Root string
}

func (repository LocalRepository) Inspect(ctx context.Context, catalog agentcontrol.Catalog) (agentcontrol.RepositorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return agentcontrol.RepositorySnapshot{}, err
	}
	return InspectRepository(repository.Root, catalog)
}

func (repository LocalRepository) Apply(ctx context.Context, snapshot agentcontrol.RepositorySnapshot, proposal agentcontrol.Proposal, approvedFingerprint string) (agentcontrol.ApplyResult, error) {
	if approvedFingerprint == "" || approvedFingerprint != proposal.Fingerprint {
		return agentcontrol.ApplyResult{}, agentcontrol.ErrApprovalRequired
	}
	for _, change := range proposal.Changes {
		if change.State == agentcontrol.AssetStateConflicting || change.State == agentcontrol.AssetStateLocallyModified {
			return agentcontrol.ApplyResult{}, fmt.Errorf("%w: %s is %s", agentcontrol.ErrApplyConflict, change.AssetID, change.State)
		}
	}
	if err := ctx.Err(); err != nil {
		return agentcontrol.ApplyResult{}, err
	}
	return applyProposalFiles(ctx, snapshot, proposal, approvedFingerprint)
}

func InspectRepository(root string, catalog agentcontrol.Catalog) (agentcontrol.RepositorySnapshot, error) {
	canonicalRoot, revision, err := canonicalGitRoot(root)
	if err != nil {
		return agentcontrol.RepositorySnapshot{}, err
	}
	snapshot := agentcontrol.RepositorySnapshot{
		Root:       canonicalRoot,
		Revision:   revision,
		Assets:     map[string]agentcontrol.TargetAsset{},
		Provenance: agentcontrol.DistributionManifest{},
	}
	manifestPath := filepath.Join(canonicalRoot, filepath.FromSlash(".agents/manifest.yaml"))
	manifestContent, exists, unsafe, err := readRegularNoFollow(canonicalRoot, ".agents/manifest.yaml", maxProvenanceBytes)
	if err != nil {
		return agentcontrol.RepositorySnapshot{}, fmt.Errorf("read target provenance: %w", err)
	}
	if unsafe {
		return agentcontrol.RepositorySnapshot{}, fmt.Errorf("target provenance %s is not a regular non-symlink file", manifestPath)
	}
	if exists {
		var provenance agentcontrol.DistributionManifest
		if err := agentcontrol.DecodeStrictYAML(manifestContent, &provenance); err != nil {
			return agentcontrol.RepositorySnapshot{}, fmt.Errorf("decode target provenance: %w", err)
		}
		if err := agentcontrol.ValidateDistributionManifest(provenance, true); err != nil {
			return agentcontrol.RepositorySnapshot{}, fmt.Errorf("validate target provenance: %w", err)
		}
		snapshot.Provenance = provenance
		snapshot.ProvenanceContent = manifestContent
		snapshot.HasProvenance = true
	}
	for _, asset := range catalog.Assets {
		if asset.Asset.Kind == agentcontrol.AssetKindGlobalPolicy {
			continue
		}
		target, err := readTargetAsset(canonicalRoot, asset)
		if err != nil {
			return agentcontrol.RepositorySnapshot{}, fmt.Errorf("inspect target asset %q: %w", asset.Asset.ID, err)
		}
		snapshot.Assets[asset.Asset.ID] = target
	}
	return snapshot, nil
}

func canonicalGitRoot(root string) (string, string, error) {
	if strings.TrimSpace(root) == "" {
		return "", "", errors.New("repository root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository symlinks: %w", err)
	}
	gitRoot, err := gitRead(canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git repository root: %w", err)
	}
	gitRoot, err = filepath.EvalSymlinks(gitRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve Git root symlinks: %w", err)
	}
	if filepath.Clean(gitRoot) != filepath.Clean(canonical) {
		return "", "", fmt.Errorf("target path %q is not the Git repository root %q", canonical, gitRoot)
	}
	revision, err := gitRead(canonical, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(revision) == "" {
		return "", "", fmt.Errorf("read target base revision: %w", err)
	}
	return canonical, strings.TrimSpace(revision), nil
}

func gitRead(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func readTargetAsset(root string, asset agentcontrol.ResolvedAsset) (agentcontrol.TargetAsset, error) {
	info, exists, unsafe, err := lstatPath(root, asset.Asset.TargetPath)
	if err != nil {
		return agentcontrol.TargetAsset{}, err
	}
	if !exists {
		return agentcontrol.TargetAsset{Exists: false, Files: map[string][]byte{}}, nil
	}
	if unsafe {
		return agentcontrol.TargetAsset{Exists: true, Unsafe: true}, nil
	}
	if isDirectoryAsset(asset) != info.IsDir() {
		return agentcontrol.TargetAsset{Exists: true, Unsafe: true}, nil
	}
	if !isDirectoryAsset(asset) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(asset.Asset.TargetPath)))
		if err != nil {
			return agentcontrol.TargetAsset{}, err
		}
		return agentcontrol.TargetAsset{Exists: true, Files: map[string][]byte{path.Base(asset.Asset.TargetPath): content}}, nil
	}
	files := map[string][]byte{}
	basePath := filepath.Join(root, filepath.FromSlash(asset.Asset.TargetPath))
	err = filepath.WalkDir(basePath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if current == basePath {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errUnsafeTargetPath
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errUnsafeTargetPath
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(basePath, current)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = content
		return nil
	})
	if errors.Is(err, errUnsafeTargetPath) {
		return agentcontrol.TargetAsset{Exists: true, Unsafe: true}, nil
	}
	if err != nil {
		return agentcontrol.TargetAsset{}, err
	}
	return agentcontrol.TargetAsset{Exists: true, Files: files}, nil
}

var errUnsafeTargetPath = errors.New("unsafe symlink or non-regular target entry")

func lstatPath(root, relative string) (os.FileInfo, bool, bool, error) {
	if err := agentcontrol.ValidateRelativePath(relative); err != nil {
		return nil, false, true, err
	}
	current := root
	parts := strings.Split(relative, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, false, nil
		}
		if err != nil {
			return nil, false, false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return info, true, true, nil
		}
		if index < len(parts)-1 && !info.IsDir() {
			return info, true, true, nil
		}
		if index == len(parts)-1 {
			return info, true, false, nil
		}
	}
	return nil, false, false, nil
}

func readRegularNoFollow(root, relative string, maxBytes int64) ([]byte, bool, bool, error) {
	info, exists, unsafe, err := lstatPath(root, relative)
	if err != nil || !exists || unsafe {
		return nil, exists, unsafe, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, true, true, nil
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	return content, true, false, err
}

func applyProposalFiles(ctx context.Context, snapshot agentcontrol.RepositorySnapshot, proposal agentcontrol.Proposal, approvedFingerprint string) (agentcontrol.ApplyResult, error) {
	for _, change := range proposal.Changes {
		if change.State == agentcontrol.AssetStateConflicting || change.State == agentcontrol.AssetStateLocallyModified {
			return agentcontrol.ApplyResult{}, fmt.Errorf("%w: %s is %s", agentcontrol.ErrApplyConflict, change.AssetID, change.State)
		}
	}
	if err := ctx.Err(); err != nil {
		return agentcontrol.ApplyResult{}, err
	}
	currentRoot, currentRevision, err := canonicalGitRoot(snapshot.Root)
	if err != nil {
		return agentcontrol.ApplyResult{}, err
	}
	if currentRevision != proposal.BaseRevision || filepath.Clean(currentRoot) != filepath.Clean(proposal.RepositoryRoot) {
		return agentcontrol.ApplyResult{}, errors.New("proposal changed since approval: target base revision or root changed")
	}
	fresh, err := InspectRepository(snapshot.Root, agentcontrol.Catalog{Assets: proposalAssets(proposal)})
	if err != nil {
		return agentcontrol.ApplyResult{}, err
	}
	if fresh.Revision != snapshot.Revision {
		return agentcontrol.ApplyResult{}, errors.New("proposal changed since approval: target revision changed")
	}
	if fresh.HasProvenance != snapshot.HasProvenance || string(fresh.ProvenanceContent) != string(snapshot.ProvenanceContent) {
		return agentcontrol.ApplyResult{}, errors.New("proposal changed since approval: target provenance changed")
	}
	for _, change := range proposal.Changes {
		if !sameFileChecksums(fresh.Assets[change.AssetID].Files, snapshot.Assets[change.AssetID].Files) || fresh.Assets[change.AssetID].Exists != snapshot.Assets[change.AssetID].Exists || fresh.Assets[change.AssetID].Unsafe != snapshot.Assets[change.AssetID].Unsafe {
			return agentcontrol.ApplyResult{}, fmt.Errorf("proposal changed since approval: target asset %q changed", change.AssetID)
		}
	}
	recomputed, err := agentcontrol.BuildDistributionProposal(agentcontrol.Catalog{
		Assets: proposalAssets(proposal), Digest: proposal.CatalogDigest,
	}, fresh)
	if err != nil {
		return agentcontrol.ApplyResult{}, err
	}
	if recomputed.Fingerprint != proposal.Fingerprint || approvedFingerprint != proposal.Fingerprint {
		return agentcontrol.ApplyResult{}, errors.New("proposal changed since approval: fingerprint does not match inspected state")
	}
	backups := map[string]fileBackup{}
	result := agentcontrol.ApplyResult{ProposalFingerprint: proposal.Fingerprint, ProvenancePath: ".agents/manifest.yaml"}
	for _, change := range proposal.Changes {
		if change.State == agentcontrol.AssetStateCurrent {
			continue
		}
		targetFiles := ensureAssetFiles(change.Source, change.TargetPath)
		for _, relativeFile := range sortedKeys(targetFiles) {
			relativePath := assetFilePath(change.Source, change.TargetPath, relativeFile)
			if _, recorded := backups[relativePath]; !recorded {
				backups[relativePath] = snapshotFile(snapshot, change.AssetID, relativeFile, change.Source)
			}
			backup := backups[relativePath]
			backup.Written = append([]byte(nil), targetFiles[relativeFile]...)
			backups[relativePath] = backup
			if err := writeProposalFile(snapshot.Root, relativePath, targetFiles[relativeFile]); err != nil {
				rollbackFiles(snapshot.Root, backups, targetFiles)
				return agentcontrol.ApplyResult{}, fmt.Errorf("write managed asset %q: %w", change.AssetID, err)
			}
			result.Written = append(result.Written, relativePath)
		}
		prior, hasPrior := provenanceByID(snapshot.Provenance)[change.AssetID]
		if hasPrior && change.State == agentcontrol.AssetStateStale {
			for _, oldFile := range prior.Files {
				if _, stillPresent := targetFiles[oldFile.Path]; stillPresent {
					continue
				}
				obsolete := assetFilePath(change.Source, change.TargetPath, oldFile.Path)
				if _, recorded := backups[obsolete]; !recorded {
					backups[obsolete] = snapshotFile(snapshot, change.AssetID, oldFile.Path, change.Source)
				}
				if err := removeManagedFile(snapshot.Root, obsolete, backups[obsolete]); err != nil {
					rollbackFiles(snapshot.Root, backups, targetFiles)
					return agentcontrol.ApplyResult{}, fmt.Errorf("remove superseded managed file %q: %w", obsolete, err)
				}
				result.Removed = append(result.Removed, obsolete)
			}
		}
	}
	manifestBytes, err := agentcontrol.MarshalDistributionManifest(proposal.NextManifest)
	if err != nil {
		rollbackFiles(snapshot.Root, backups, nil)
		return agentcontrol.ApplyResult{}, err
	}
	backups[".agents/manifest.yaml"] = fileBackup{
		Exists: snapshot.HasProvenance, Content: append([]byte(nil), snapshot.ProvenanceContent...),
		Written: append([]byte(nil), manifestBytes...),
	}
	if !snapshot.HasProvenance || string(manifestBytes) != string(snapshot.ProvenanceContent) {
		if err := writeProposalFile(snapshot.Root, ".agents/manifest.yaml", manifestBytes); err != nil {
			rollbackFiles(snapshot.Root, backups, nil)
			return agentcontrol.ApplyResult{}, fmt.Errorf("write managed provenance: %w", err)
		}
	}
	if err := verifyAppliedDistribution(snapshot.Root, proposal, manifestBytes); err != nil {
		rollbackFiles(snapshot.Root, backups, nil)
		return agentcontrol.ApplyResult{}, fmt.Errorf("verify managed distribution after apply: %w", err)
	}
	sort.Strings(result.Written)
	sort.Strings(result.Removed)
	return result, nil
}

func verifyAppliedDistribution(root string, proposal agentcontrol.Proposal, expectedManifest []byte) error {
	verified, err := InspectRepository(root, agentcontrol.Catalog{Assets: proposalAssets(proposal)})
	if err != nil {
		return err
	}
	if !verified.HasProvenance || string(verified.ProvenanceContent) != string(expectedManifest) {
		return errors.New("generated provenance checksum or content does not match proposal")
	}
	for _, change := range proposal.Changes {
		actual := verified.Assets[change.AssetID]
		if !actual.Exists || actual.Unsafe || agentcontrol.ChecksumFiles(actual.Files) != change.DesiredChecksum {
			return fmt.Errorf("asset %q checksum does not match approved catalog", change.AssetID)
		}
	}
	return nil
}

type fileBackup struct {
	Exists  bool
	Content []byte
	Written []byte
}

func snapshotFile(snapshot agentcontrol.RepositorySnapshot, assetID, relative string, asset agentcontrol.ResolvedAsset) fileBackup {
	target := snapshot.Assets[assetID]
	file := fileBackup{Exists: false}
	if target.Exists && !target.Unsafe {
		key := relative
		if !isDirectoryAsset(asset) {
			key = path.Base(asset.Asset.TargetPath)
		}
		if content, ok := target.Files[key]; ok {
			file.Exists = true
			file.Content = append([]byte(nil), content...)
		}
	}
	return file
}

func assetFilePath(asset agentcontrol.ResolvedAsset, targetPath, relative string) string {
	if !isDirectoryAsset(asset) {
		return targetPath
	}
	return path.Join(targetPath, relative)
}

func provenanceByID(manifest agentcontrol.DistributionManifest) map[string]agentcontrol.ProvenanceEntry {
	entries := make(map[string]agentcontrol.ProvenanceEntry, len(manifest.Assets))
	for _, entry := range manifest.Assets {
		entries[entry.AssetID] = entry
	}
	return entries
}

func proposalAssets(proposal agentcontrol.Proposal) []agentcontrol.ResolvedAsset {
	assets := make([]agentcontrol.ResolvedAsset, 0, len(proposal.Changes))
	for _, change := range proposal.Changes {
		assets = append(assets, change.Source)
	}
	return assets
}

func removeManagedFile(root, relative string, backup fileBackup) error {
	if !backup.Exists {
		return nil
	}
	content, exists, unsafe, err := readRegularNoFollow(root, relative, maxManagedAssetBytes)
	if err != nil {
		return err
	}
	if !exists || unsafe || string(content) != string(backup.Content) {
		return fmt.Errorf("managed file changed after inspection")
	}
	return os.Remove(filepath.Join(root, filepath.FromSlash(relative)))
}

func rollbackFiles(root string, backups map[string]fileBackup, desired map[string][]byte) {
	paths := make([]string, 0, len(backups))
	for relative := range backups {
		paths = append(paths, relative)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, relative := range paths {
		backup := backups[relative]
		current, exists, unsafe, err := readRegularNoFollow(root, relative, maxManagedAssetBytes)
		if err != nil || unsafe {
			continue
		}
		if backup.Exists {
			if exists && string(current) == string(backup.Content) {
				continue
			}
			if exists && string(current) != string(backup.Written) {
				continue
			}
			_ = writeFileAtomic(root, relative, backup.Content)
			continue
		}
		if !exists {
			continue
		}
		if string(current) == string(backup.Written) {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(relative)))
		}
	}
}

func writeProposalFile(root, relative string, content []byte) error {
	if !strings.HasPrefix(relative, ".agents/") {
		return fmt.Errorf("refusing write outside .agents/**: %q", relative)
	}
	return writeFileAtomic(root, relative, content)
}

func writeFileAtomic(root, relative string, content []byte) error {
	if err := agentcontrol.ValidateRelativePath(relative); err != nil {
		return err
	}
	directory := filepath.Dir(filepath.Join(root, filepath.FromSlash(relative)))
	if err := ensureNoSymlinkDirectories(root, filepath.ToSlash(filepath.Dir(relative))); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if _, exists, unsafe, err := lstatPath(root, relative); err != nil {
		return err
	} else if unsafe {
		return fmt.Errorf("refusing to write through symlink or non-directory path %q", relative)
	} else if exists {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular path %q", relative)
		}
	}
	temporary, err := os.CreateTemp(directory, ".agentcontrol-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		return err
	}
	return nil
}

func isDirectoryAsset(asset agentcontrol.ResolvedAsset) bool {
	return asset.Asset.Kind == agentcontrol.AssetKindSkill
}

func ensureAssetFiles(asset agentcontrol.ResolvedAsset, targetPath string) map[string][]byte {
	files := make(map[string][]byte, len(asset.Files))
	for name, content := range asset.Files {
		relative := name
		if !isDirectoryAsset(asset) {
			relative = path.Base(targetPath)
		}
		files[relative] = append([]byte(nil), content...)
	}
	return files
}

func sameFileChecksums(left, right map[string][]byte) bool {
	return agentcontrol.ChecksumFiles(left) == agentcontrol.ChecksumFiles(right)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ensureNoSymlinkDirectories(root, relativeDir string) error {
	if relativeDir == "." || relativeDir == "" {
		return nil
	}
	if err := agentcontrol.ValidateRelativePath(relativeDir); err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(relativeDir, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("refusing unsafe parent path %q", current)
		}
	}
	return nil
}
