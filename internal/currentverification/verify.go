// Package currentverification proves the read-only CURRENT projection for a
// supplied set of local backend repository roots. It deliberately has no
// persistence or mutation path: each invocation rescans the exact working
// trees supplied by the caller and reports the evidence used for that claim.
package currentverification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	"github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// Status is the verification state of the complete platform or one service.
// Failed means CURRENT is not proven. Stale means the scan was bounded by
// incomplete inventory evidence or changed source while it was being checked.
// Verified may still contain explicit unknown areas; those are evidence-bound
// limitations, not silently inferred architecture facts.
type Status string

const (
	StatusVerified Status = "verified"
	StatusStale    Status = "stale"
	StatusFailed   Status = "failed"
)

// Finding records a stable, non-secret explanation for an S4 conclusion.
// Paths are repository-relative. Private environment file names are redacted
// even though their contents are never opened by this package.
type Finding struct {
	Status  Status `json:"status"`
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// GitChange is a sanitized porcelain-v1 status entry. It never includes file
// contents. Architecture rollout artifacts are retained separately so an
// expected untracked .ai/architecture tree does not make code CURRENT stale.
type GitChange struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// GitEvidence pins a CURRENT scan to its Git source state. Dirty reports the
// raw Git state; CodeDirty excludes architecture rollout artifacts because
// those artifacts are audited as the subject of this verification.
type GitEvidence struct {
	HeadCommit                   string      `json:"head_commit"`
	Branch                       string      `json:"branch,omitempty"`
	Dirty                        bool        `json:"dirty"`
	CodeDirty                    bool        `json:"code_dirty"`
	ArchitectureRolloutChanges   []GitChange `json:"architecture_rollout_changes"`
	UnexpectedWorkingTreeChanges []GitChange `json:"unexpected_working_tree_changes"`
}

// DiscoveryEvidence identifies the bounded source scan used by the strict
// manifest auditor. It contains no raw repository files or environment data.
type DiscoveryEvidence struct {
	SchemaVersion   int      `json:"schema_version"`
	ContentChecksum string   `json:"content_checksum"`
	FilesVisited    int      `json:"files_visited"`
	FilesAnalyzed   int      `json:"files_analyzed"`
	Truncated       bool     `json:"truncated"`
	Warnings        []string `json:"warnings"`
	ConflictCount   int      `json:"conflict_count"`
	ConflictCodes   []string `json:"conflict_codes"`
}

// ServiceReport is the per-root CURRENT verification result. Audit is the
// strict manifest/operation/Mermaid completeness result used by S3 and S4.
type ServiceReport struct {
	InputRoot     string                             `json:"input_root"`
	CanonicalRoot string                             `json:"canonical_root,omitempty"`
	ServiceID     string                             `json:"service_id,omitempty"`
	Status        Status                             `json:"status"`
	Git           GitEvidence                        `json:"git"`
	Discovery     *DiscoveryEvidence                 `json:"discovery,omitempty"`
	Audit         *architecturemanifest.ServiceAudit `json:"audit,omitempty"`
	Findings      []Finding                          `json:"findings"`
}

// Report is the deterministic global CURRENT verification projection. The
// audit counters make the result usable by automation without reverse
// engineering individual service records.
type Report struct {
	Status   Status                           `json:"status"`
	Audit    architecturemanifest.AuditReport `json:"audit"`
	Services []ServiceReport                  `json:"services"`
	Findings []Finding                        `json:"findings"`
}

// Verify validates each canonical repository root, takes a bounded discovery
// snapshot, performs a strict local architecture audit, and verifies that Git
// HEAD did not move during the work. It reads no .env file and never writes to
// any supplied repository. Invalid roots become per-service failures so one
// bad service cannot hide results for the rest of the platform.
func Verify(ctx context.Context, roots []string) (Report, error) {
	if len(roots) == 0 {
		return Report{}, fmt.Errorf("at least one backend repository root is required: %w", domain.ErrValidation)
	}

	result := Report{Services: make([]ServiceReport, 0, len(roots)), Findings: []Finding{}}
	inputs := make([]architecturemanifest.AuditInput, 0, len(roots))
	byCanonicalRoot := make(map[string]int, len(roots))

	for _, inputRoot := range roots {
		service := ServiceReport{InputRoot: inputRoot, Status: StatusFailed, Findings: []Finding{}}
		canonicalRoot, err := canonicalRoot(inputRoot)
		if err != nil {
			service.Findings = append(service.Findings, failure("invalid_canonical_root", "", err.Error()))
			result.Services = append(result.Services, service)
			continue
		}
		service.CanonicalRoot = canonicalRoot
		if prior, exists := byCanonicalRoot[canonicalRoot]; exists {
			service.Findings = append(service.Findings, failure("duplicate_canonical_root", "", "repository root duplicates input at index "+fmt.Sprint(prior)))
			result.Services = append(result.Services, service)
			continue
		}
		byCanonicalRoot[canonicalRoot] = len(result.Services)

		manager := gitadapter.ProjectSource{AllowedRoots: []string{canonicalRoot}}
		before, err := manager.ConnectLocal(ctx, canonicalRoot)
		if err != nil {
			service.Findings = append(service.Findings, failure("git_source_unavailable", "", "inspect canonical Git source: "+err.Error()))
			result.Services = append(result.Services, service)
			continue
		}
		if before.LocalPath != canonicalRoot {
			service.Findings = append(service.Findings, failure("git_root_mismatch", "", "canonical input is not the Git worktree root"))
			result.Services = append(result.Services, service)
			continue
		}

		projectID := verificationProjectID(canonicalRoot)
		discoveryReport, err := discovery.NewScanner(discovery.Config{}).Scan(ctx, domain.Project{
			ID: projectID, Name: filepath.Base(canonicalRoot), RepositoryRole: domain.RepositoryRoleService,
		}, before)
		if err != nil {
			service.Git = gitEvidence(before, GitStatus{})
			service.Findings = append(service.Findings, failure("discovery_failed", "", err.Error()))
			result.Services = append(result.Services, service)
			continue
		}
		inputs = append(inputs, architecturemanifest.AuditInput{ServiceRoot: canonicalRoot, Report: discoveryReport})
		service.Discovery = &DiscoveryEvidence{
			SchemaVersion: discoveryReport.SchemaVersion, ContentChecksum: discoveryReport.ContentChecksum,
			FilesVisited: discoveryReport.Inventory.FilesVisited, FilesAnalyzed: discoveryReport.Inventory.FilesAnalyzed,
			Truncated: discoveryReport.Inventory.Truncated, Warnings: append([]string(nil), discoveryReport.Inventory.Warnings...),
			ConflictCount: len(discoveryReport.Conflicts), ConflictCodes: conflictCodes(discoveryReport.Conflicts),
		}

		after, err := manager.ConnectLocal(ctx, canonicalRoot)
		if err != nil {
			service.Findings = append(service.Findings, failure("git_source_unavailable_after_scan", "", "inspect canonical Git source after scan: "+err.Error()))
			result.Services = append(result.Services, service)
			continue
		}
		status, err := readGitStatus(ctx, canonicalRoot)
		if err != nil {
			service.Findings = append(service.Findings, failure("git_status_unavailable", "", err.Error()))
			result.Services = append(result.Services, service)
			continue
		}
		service.Git = gitEvidence(after, status)
		if before.HeadCommit != after.HeadCommit {
			service.Findings = append(service.Findings, stale("head_changed_during_scan", "", "Git HEAD changed while CURRENT was being rescanned"))
		}
		if status.CodeDirty {
			service.Findings = append(service.Findings, Finding{Status: StatusVerified, Code: "working_tree_dirty_rescanned", Message: "CURRENT was freshly rescanned from a working tree with changes outside .ai/architecture"})
		}
		if discoveryReport.Inventory.Truncated {
			service.Findings = append(service.Findings, stale("discovery_inventory_truncated", "", "bounded discovery did not inspect the complete repository inventory"))
		}
		for _, warning := range discoveryReport.Inventory.Warnings {
			service.Findings = append(service.Findings, stale("discovery_inventory_warning", "", warning))
		}
		if len(discoveryReport.Conflicts) > 0 {
			service.Findings = append(service.Findings, Finding{Status: StatusVerified, Code: "discovery_conflicts", Message: "discovery reported conflicting source evidence; inspect conflict_codes"})
		}
		result.Services = append(result.Services, service)
	}

	audit, err := architecturemanifest.Audit(inputs)
	if err != nil {
		return Report{}, fmt.Errorf("strict architecture audit: %w", err)
	}
	result.Audit = audit
	auditByRoot := make(map[string]architecturemanifest.ServiceAudit, len(audit.Services))
	for _, serviceAudit := range audit.Services {
		auditByRoot[serviceAudit.ServiceRoot] = serviceAudit
	}
	for index := range result.Services {
		service := &result.Services[index]
		if service.CanonicalRoot == "" {
			continue
		}
		serviceAudit, exists := auditByRoot[service.CanonicalRoot]
		if !exists {
			continue
		}
		service.Audit = &serviceAudit
		service.ServiceID = serviceAudit.ServiceID
		for _, finding := range serviceAudit.ValidationErrors {
			service.Findings = append(service.Findings, failure("manifest_validation_error", finding.Path, finding.Message))
		}
		if serviceAudit.Blocked || serviceAudit.OperationsBlocked > 0 {
			service.Findings = append(service.Findings, failure("manifest_operations_blocked", "", "strict audit contains blocked service or operation records"))
		}
		if !serviceAudit.HasArchitectureManifest || serviceAudit.OperationsMissingManifests > 0 {
			service.Findings = append(service.Findings, failure("manifest_operations_missing", "", "strict audit contains a missing service or operation manifest"))
		}
		for _, unknown := range serviceAudit.UnknownAreas {
			service.Findings = append(service.Findings, Finding{Status: StatusVerified, Code: "explicit_unknown", Path: unknown.Path, Message: unknown.Message})
		}
		service.Findings = sortFindings(service.Findings)
		service.Status = classify(service.Findings)
	}

	result.Services = sortServices(result.Services)
	result.Findings = collectFindings(result.Services)
	result.Status = classify(result.Findings)
	return result, nil
}

func canonicalRoot(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("repository root is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(input))
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve canonical repository root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat canonical repository root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("canonical repository root is not a directory")
	}
	return filepath.Clean(canonical), nil
}

func verificationProjectID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return "current-" + hex.EncodeToString(sum[:8])
}

type GitStatus struct {
	ArchitectureRolloutChanges   []GitChange
	UnexpectedWorkingTreeChanges []GitChange
	CodeDirty                    bool
}

func readGitStatus(ctx context.Context, root string) (GitStatus, error) {
	command := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false")
	output, err := command.Output()
	if err != nil {
		return GitStatus{}, fmt.Errorf("read Git working-tree status: %w", err)
	}
	if len(output) > 1<<20 {
		return GitStatus{}, errors.New("Git working-tree status exceeds bounded output")
	}
	changes, err := parsePorcelainV1Z(output)
	if err != nil {
		return GitStatus{}, err
	}
	status := GitStatus{ArchitectureRolloutChanges: []GitChange{}, UnexpectedWorkingTreeChanges: []GitChange{}}
	for _, change := range changes {
		if isArchitectureRolloutPath(change.Path) {
			status.ArchitectureRolloutChanges = append(status.ArchitectureRolloutChanges, change)
			continue
		}
		status.UnexpectedWorkingTreeChanges = append(status.UnexpectedWorkingTreeChanges, change)
	}
	status.CodeDirty = len(status.UnexpectedWorkingTreeChanges) > 0
	return status, nil
}

func parsePorcelainV1Z(output []byte) ([]GitChange, error) {
	if len(output) == 0 {
		return []GitChange{}, nil
	}
	parts := strings.Split(string(output), "\x00")
	changes := make([]GitChange, 0, len(parts))
	for index := 0; index < len(parts); index++ {
		record := parts[index]
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, fmt.Errorf("parse Git porcelain status: malformed record")
		}
		change := GitChange{Status: record[:2], Path: redactPrivateEnvironmentPath(record[3:])}
		changes = append(changes, change)
		if record[0] == 'R' || record[0] == 'C' || record[1] == 'R' || record[1] == 'C' {
			if index+1 >= len(parts) || parts[index+1] == "" {
				return nil, fmt.Errorf("parse Git porcelain status: missing rename source")
			}
			index++
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Status < changes[j].Status
		}
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func isArchitectureRolloutPath(path string) bool {
	return path == ".ai/architecture" || strings.HasPrefix(path, ".ai/architecture/")
}

func redactPrivateEnvironmentPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return "<private-environment-file>"
	}
	return filepath.ToSlash(path)
}

func gitEvidence(source domain.RepositorySource, status GitStatus) GitEvidence {
	return GitEvidence{
		HeadCommit: source.HeadCommit, Branch: source.CurrentBranch,
		Dirty: source.IsDirty, CodeDirty: status.CodeDirty,
		ArchitectureRolloutChanges:   append([]GitChange(nil), status.ArchitectureRolloutChanges...),
		UnexpectedWorkingTreeChanges: append([]GitChange(nil), status.UnexpectedWorkingTreeChanges...),
	}
}

func conflictCodes(conflicts []domain.Evidence) []string {
	seen := make(map[string]struct{}, len(conflicts))
	for _, conflict := range conflicts {
		code := strings.TrimSpace(conflict.Name)
		if code != "" {
			seen[code] = struct{}{}
		}
	}
	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func failure(code, path, message string) Finding {
	return Finding{Status: StatusFailed, Code: code, Path: path, Message: message}
}
func stale(code, path, message string) Finding {
	return Finding{Status: StatusStale, Code: code, Path: path, Message: message}
}

func classify(findings []Finding) Status {
	for _, finding := range findings {
		if finding.Status == StatusFailed {
			return StatusFailed
		}
	}
	for _, finding := range findings {
		if finding.Status == StatusStale {
			return StatusStale
		}
	}
	return StatusVerified
}

func sortFindings(findings []Finding) []Finding {
	sorted := append([]Finding(nil), findings...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Status != sorted[j].Status {
			return sorted[i].Status < sorted[j].Status
		}
		if sorted[i].Code != sorted[j].Code {
			return sorted[i].Code < sorted[j].Code
		}
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Message < sorted[j].Message
	})
	return sorted
}

func sortServices(services []ServiceReport) []ServiceReport {
	sorted := append([]ServiceReport(nil), services...)
	sort.Slice(sorted, func(i, j int) bool {
		left, right := sorted[i].CanonicalRoot, sorted[j].CanonicalRoot
		if left == right {
			return sorted[i].InputRoot < sorted[j].InputRoot
		}
		return left < right
	})
	return sorted
}

func collectFindings(services []ServiceReport) []Finding {
	findings := make([]Finding, 0)
	for _, service := range services {
		root := service.CanonicalRoot
		if root == "" {
			root = service.InputRoot
		}
		for _, finding := range service.Findings {
			findings = append(findings, Finding{Status: finding.Status, Code: finding.Code, Path: finding.Path, Message: root + ": " + finding.Message})
		}
	}
	return sortFindings(findings)
}
