package contextretrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"golang.org/x/sys/unix"
)

// FilesystemLoader reads only explicit admitted roots, using the existing scanner.
// Every content read is anchored to an open directory, with no-follow on every
// component. It executes no shell, Git, network or model operation.
type FilesystemLoader struct{}

func (FilesystemLoader) Load(ctx context.Context, admissions []core.SourceAdmission, limits core.Limits) (core.SnapshotResult, error) {
	return loadFilesystem(ctx, admissions, limits, openRelativeRegular)
}

// The opener seam tests the pre-open admission invariant. Production always
// supplies the descriptor-anchored secure reader above.
func loadFilesystem(ctx context.Context, admissions []core.SourceAdmission, limits core.Limits, openRead func(*os.File, string) (*os.File, error)) (core.SnapshotResult, error) {
	out := core.SnapshotResult{}
	if limits.MaxFiles < 0 || limits.MaxFiles > 10000 || limits.MaxFileBytes < 0 || limits.MaxFileBytes > 1<<20 || limits.MaxTotalBytes < 0 || limits.MaxTotalBytes > 20<<20 || limits.MaxDepth < 0 || limits.MaxDepth > 64 {
		return out, fmt.Errorf("invalid source inventory limits")
	}
	if limits.MaxFiles == 0 {
		limits.MaxFiles = 10000
	}
	if limits.MaxFileBytes == 0 {
		limits.MaxFileBytes = 1 << 20
	}
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = 20 << 20
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = 24
	}
	ordered := append([]core.SourceAdmission(nil), admissions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Identity < ordered[j].Identity })
	seen := map[string]bool{}
	remaining := limits.MaxTotalBytes
	if remaining <= 0 {
		remaining = 20 << 20
	}
	for _, admission := range ordered {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if admission.Identity == "" || admission.Root == "" || seen[admission.Identity] || len(admission.ReadPaths) == 0 {
			return out, fmt.Errorf("source identity must be unique and nonempty")
		}
		seen[admission.Identity] = true
		for _, pattern := range append(append([]string(nil), admission.ReadPaths...), admission.ExcludePaths...) {
			if pattern != "." && !safeRelativePath(pattern) {
				return out, fmt.Errorf("unsafe admitted path pattern")
			}
		}
		// Reject secret-store ancestors before canonicalization, then repeat for
		// resolved aliases. Explicit root admission cannot bypass path exclusions.
		if secretPath(filepath.ToSlash(admission.Root)) {
			return out, fmt.Errorf("secret-store root excluded by policy")
		}
		root, err := filepath.Abs(admission.Root)
		if err != nil {
			return out, fmt.Errorf("invalid admitted root")
		}
		if secretPath(filepath.ToSlash(root)) {
			return out, fmt.Errorf("secret-store root excluded by policy")
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return out, fmt.Errorf("admitted root unavailable")
		}
		if secretPath(filepath.ToSlash(root)) {
			return out, fmt.Errorf("secret-store root excluded by policy")
		}
		if root == string(filepath.Separator) {
			return out, fmt.Errorf("filesystem root cannot be admitted")
		}
		dir, err := openAbsoluteDirectory(root)
		if err != nil {
			return out, fmt.Errorf("admitted root is not a safe directory")
		}
		coverage := core.CoverageResult{SourceIdentity: admission.Identity, Stage: "inventory", Status: core.Complete, Complete: true, DepthLimit: limits.MaxDepth, FileLimit: limits.MaxFiles, ByteLimit: remaining}
		diagnostics := []core.RetrievalDiagnostic{}
		denied := map[string]bool{}
		addDiagnostic := func(code, path string, status core.Status) {
			diagnostics = append(diagnostics, core.RetrievalDiagnostic{Code: code, Status: status, SourceIdentity: admission.Identity, RelativePath: path, Message: code})
		}
		admissionCopy := admission
		admissionCopy.Root = ""
		admissionCopy.ExpectedSnapshot = ""
		admissionDigest := hashJSON(admissionCopy)
		scanner := discovery.NewScanner(discovery.Config{MaxFiles: limits.MaxFiles, MaxDepth: limits.MaxDepth, MaxFileBytes: int64(limits.MaxFileBytes), MaxTotalBytes: int64(max(1, remaining)),
			AdmitPath: func(relative string, directory bool) error {
				if secretPath(relative) || excludedPath(admission.ExcludePaths, relative) || (!directory && !core.PathAdmitted(admission, relative)) || (directory && !admittedDirectory(admission.ReadPaths, relative)) {
					denied[relative] = true
					addDiagnostic("EXCLUDED_BY_POLICY", relative, core.Complete)
					return errors.New("excluded")
				}
				return nil
			},
			ReadFile: func(readCtx context.Context, _ string, relative string, limit int64) ([]byte, bool, error) {
				if err := readCtx.Err(); err != nil {
					return nil, false, err
				}
				file, err := openRead(dir, relative)
				if err != nil {
					addDiagnostic("UNREADABLE_SOURCE", relative, core.Partial)
					return nil, false, err
				}
				defer file.Close()
				info, err := file.Stat()
				if err != nil {
					return nil, false, err
				}
				coverage.BytesConsidered += int(min(info.Size(), int64(^uint(0)>>1)))
				if info.Size() > limit {
					addDiagnostic("SOURCE_TOO_LARGE", relative, core.Partial)
					return nil, true, nil
				}
				if info.Size() > int64(remaining-coverage.BytesSelected) {
					coverage.Complete = false
					coverage.TerminatedByLimit = true
					coverage.Omitted++
					coverage.Reasons = append(coverage.Reasons, "byte_limit")
					addDiagnostic("BYTE_LIMIT", relative, core.Partial)
					return nil, true, nil
				}
				content, err := io.ReadAll(io.LimitReader(file, min(limit, int64(remaining-coverage.BytesSelected))+1))
				if err != nil {
					return nil, false, err
				}
				if int64(len(content)) > limit {
					addDiagnostic("SOURCE_TOO_LARGE", relative, core.Partial)
					return nil, true, nil
				}
				if contractbaseline.ContainsSecretLikeContent(content) || core.ContainsSecretLikeContent(string(content)) {
					denied[relative] = true
					coverage.SkippedByPolicy++
					addDiagnostic("SECRET_CONTENT_EXCLUDED", relative, core.Complete)
					return nil, false, errors.New("secret content excluded")
				}
				return content, false, nil
			},
			ObserveInventory: func(event discovery.InventoryEvent) {
				switch event.Kind {
				case "visited":
					coverage.Visited++
				case "indexed":
					coverage.Indexed++
					coverage.BytesSelected += int(event.Bytes)
				case "skipped_by_policy":
					coverage.SkippedByPolicy++
					if !denied[event.Path] {
						addDiagnostic("EXCLUDED_BY_POLICY", event.Path, core.Complete)
					}
				case "nonregular":
					coverage.SkippedByPolicy++
					addDiagnostic("FILE_TYPE_EXCLUDED", event.Path, core.Complete)
				case "unreadable":
					if !denied[event.Path] {
						coverage.Unreadable++
						coverage.Errors++
						coverage.Complete = false
						addDiagnostic("UNREADABLE_SOURCE", event.Path, core.Partial)
					}
				case "skipped_large":
					coverage.SkippedLarge++
					coverage.Complete = false
				case "depth_limit", "file_limit", "byte_limit":
					coverage.Complete = false
					coverage.TerminatedByLimit = true
					coverage.Omitted++
					coverage.Reasons = append(coverage.Reasons, event.Kind)
					addDiagnostic(strings.ToUpper(event.Kind), event.Path, core.Partial)
				}
			},
		})
		var files []discovery.InventoryFile
		if remaining <= 0 {
			coverage.Complete = false
			coverage.TerminatedByLimit = true
			coverage.Reasons = []string{"global_byte_limit"}
			addDiagnostic("GLOBAL_BYTE_LIMIT", "", core.Partial)
		} else {
			files, _, err = scanner.Inventory(ctx, root)
		}
		_ = dir.Close()
		if err != nil {
			return out, err
		}
		if !coverage.Complete {
			coverage.Status = core.Partial
		}
		sort.Strings(coverage.Reasons)
		sort.Slice(diagnostics, func(i, j int) bool {
			return diagnostics[i].RelativePath+diagnostics[i].Code < diagnostics[j].RelativePath+diagnostics[j].Code
		})
		// The manifest pins analyzed content and all coverage gaps. Host paths and
		// mtime never participate. A declared Git label is not a verified Git pin.
		manifest := struct {
			Admission   string
			Limits      core.Limits
			Files       []struct{ Path, Hash string }
			Coverage    core.CoverageResult
			Diagnostics []core.RetrievalDiagnostic
		}{Admission: admissionDigest, Limits: limits, Coverage: coverage, Diagnostics: diagnostics}
		for _, f := range files {
			manifest.Files = append(manifest.Files, struct{ Path, Hash string }{f.Path, hashBytes(f.Content)})
		}
		snapshot := hashJSON(manifest)
		source := core.EvidenceSource{Identity: admission.Identity, Revision: "snapshot:" + snapshot, Snapshot: snapshot, Dirty: admission.Dirty, AdmissionDigest: admissionDigest}
		if admission.Revision != "" {
			addDiagnostic("REVISION_PIN_UNSUPPORTED", "", core.Unsupported)
		}
		if admission.ExpectedSnapshot != "" && admission.ExpectedSnapshot != snapshot {
			addDiagnostic("SOURCE_SNAPSHOT_CHANGED", "", core.Stale)
		}
		out.Sources = append(out.Sources, source)
		for _, f := range files {
			out.Documents = append(out.Documents, core.Document{Source: source, RelativePath: f.Path, Content: string(f.Content), ContentHash: hashBytes(f.Content), External: admission.External})
		}
		out.Coverage = append(out.Coverage, coverage)
		out.Diagnostics = append(out.Diagnostics, diagnostics...)
		remaining -= coverage.BytesSelected
	}
	return out, nil
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func hashJSON(value any) string     { raw, _ := json.Marshal(value); return hashBytes(raw) }

func openAbsoluteDirectory(root string) (*os.File, error) {
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(root, string(filepath.Separator)), string(filepath.Separator)) {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "admitted-root"), nil
}
func openRelativeRegular(root *os.File, relative string) (*os.File, error) {
	if !safeRelativePath(relative) || secretPath(relative) {
		return nil, errors.New("unsafe source path")
	}
	fd, err := unix.Dup(int(root.Fd()))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), "admitted-source")
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	// os.FileInfo.Sys uses syscall.Stat_t; Fstat is portable between Darwin/Linux.
	var descriptorStat unix.Stat_t
	if err = unix.Fstat(fd, &descriptorStat); err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || descriptorStat.Nlink != 1 || info.Mode().Perm()&0444 == 0 {
		_ = file.Close()
		return nil, errors.New("unsafe or unreadable source file")
	}
	return file, nil
}
func safeRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || filepath.IsAbs(value) {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func secretPath(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if core.SecretPathComponent(component) {
			return true
		}
	}
	return false
}

func excludedPath(patterns []string, value string) bool {
	for _, scope := range patterns {
		if scope == "." || value == scope || strings.HasPrefix(value, scope+"/") {
			return true
		}
	}
	return false
}
func admittedDirectory(scopes []string, value string) bool {
	for _, scope := range scopes {
		if scope == "." || scope == value || strings.HasPrefix(scope, value+"/") || strings.HasPrefix(value, scope+"/") {
			return true
		}
	}
	return false
}

var _ core.SourceLoader = FilesystemLoader{}
