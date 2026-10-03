package git

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ArchitectureBlobResolver reads only immutable regular-file Git objects.
// It never checks out files, follows a branch tip, invokes hooks or reads .env.
type ArchitectureBlobResolver struct {
	AllowedRoots []string
	GitBinary    string
	MaxBytes     int
}

func (r ArchitectureBlobResolver) Resolve(ctx context.Context, root, identity, commit, file string) (domain.ArchitectureGraphPin, error) {
	pin, _, err := r.Read(ctx, root, identity, commit, file)
	return pin, err
}

// Read additionally returns the exact immutable bytes for owner inventory validation.
func (r ArchitectureBlobResolver) Read(ctx context.Context, root, identity, commit, file string) (domain.ArchitectureGraphPin, []byte, error) {
	pin := domain.ArchitectureGraphPin{SourceIdentity: identity, CommitSHA: commit, Path: file, BlobOID: strings.Repeat("0", 40), ContentSHA256: strings.Repeat("0", 64)}
	if !architecturecatalog.ValidGraphPin(pin) {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("invalid immutable blob request: %w", domain.ErrValidation)
	}
	for _, component := range strings.Split(file, "/") {
		if component == ".git" || component == ".env" || strings.HasPrefix(component, ".env.") {
			return domain.ArchitectureGraphPin{}, nil, domain.ErrForbidden
		}
	}
	canonical, err := canonicalExistingPath(root)
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	allowed := false
	for _, allowedRoot := range r.AllowedRoots {
		if pathWithinCanonical(allowedRoot, canonical) {
			allowed = true
			break
		}
	}
	if !allowed {
		return domain.ArchitectureGraphPin{}, nil, domain.ErrForbidden
	}
	// Ensure the configured directory is the actual Git repository root.
	top, err := r.read(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	topRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil || filepath.Clean(topRoot) != canonical {
		return domain.ArchitectureGraphPin{}, nil, domain.ErrForbidden
	}
	// Compare the persisted identity with the source adapter, using its existing
	// canonical remote/common-directory normalization without reading file bytes.
	remote, err := r.read(ctx, canonical, "remote", "get-url", "origin")
	actualIdentity := ""
	if err == nil {
		actualIdentity, _, _, _ = (ProjectSource{AllowedRoots: r.AllowedRoots, GitBinary: r.GitBinary}).normalizeGitURL(strings.TrimSpace(string(remote)))
	}
	if actualIdentity == "" {
		common, commonErr := r.read(ctx, canonical, "rev-parse", "--git-common-dir")
		if commonErr != nil {
			return domain.ArchitectureGraphPin{}, nil, commonErr
		}
		directory := strings.TrimSpace(string(common))
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(canonical, directory)
		}
		directory, commonErr = filepath.EvalSymlinks(directory)
		if commonErr != nil {
			return domain.ArchitectureGraphPin{}, nil, commonErr
		}
		actualIdentity = "local:" + filepath.Clean(directory)
	}
	if actualIdentity != identity {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("canonical source identity differs: %w", domain.ErrConflict)
	}
	resolved, err := r.read(ctx, canonical, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	if strings.TrimSpace(string(resolved)) != commit {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("full commit object required: %w", domain.ErrValidation)
	}
	tree, err := r.read(ctx, canonical, "ls-tree", "-z", commit, "--", file)
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	record := strings.TrimSuffix(string(tree), "\x00")
	parts := strings.SplitN(record, "\t", 2)
	if len(parts) != 2 || parts[1] != file || strings.Contains(record, "\x00") {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("declaration is absent at pinned commit: %w", domain.ErrNotFound)
	}
	fields := strings.Fields(parts[0])
	if len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`).MatchString(fields[2]) {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("regular declaration blob required: %w", domain.ErrValidation)
	}
	pin.BlobOID = fields[2]
	sizeBytes, err := r.read(ctx, canonical, "cat-file", "-s", pin.BlobOID)
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(sizeBytes)))
	limit := r.limit()
	if err != nil || size < 0 || size > limit {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("declaration exceeds immutable read bound: %w", domain.ErrValidation)
	}
	data, err := r.read(ctx, canonical, "cat-file", "blob", pin.BlobOID)
	if err != nil {
		return domain.ArchitectureGraphPin{}, nil, err
	}
	if len(data) != size {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("declaration blob size differs: %w", domain.ErrConflict)
	}
	object := append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...)
	computedOID := ""
	if len(pin.BlobOID) == 40 {
		digest := sha1.Sum(object)
		computedOID = hex.EncodeToString(digest[:])
	} else {
		digest := sha256.Sum256(object)
		computedOID = hex.EncodeToString(digest[:])
	}
	if computedOID != pin.BlobOID {
		return domain.ArchitectureGraphPin{}, nil, fmt.Errorf("immutable blob object digest differs: %w", domain.ErrConflict)
	}
	sum := sha256.Sum256(data)
	pin.ContentSHA256 = hex.EncodeToString(sum[:])
	return pin, data, nil
}
func (r ArchitectureBlobResolver) limit() int {
	if r.MaxBytes > 0 {
		return r.MaxBytes
	}
	return 2 << 20
}
func (r ArchitectureBlobResolver) read(ctx context.Context, root string, args ...string) ([]byte, error) {
	binary := r.GitBinary
	if binary == "" {
		binary = "git"
	}
	safe := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "--literal-pathspecs"}, args...)
	command := exec.CommandContext(ctx, binary, safe...)
	command.Dir = root
	// Remove inherited repository/object/config overrides and replacement refs.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1")
	stdout := limitedBuffer{limit: r.limit()}
	stderr := limitedBuffer{limit: 4096}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("immutable Git object read failed: %w", err)
	}
	if stdout.truncated {
		return nil, fmt.Errorf("immutable Git output exceeds bound: %w", domain.ErrValidation)
	}
	return stdout.buffer.Bytes(), nil
}
