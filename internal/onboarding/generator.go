package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	proposalSchemaVersion = 1
	generatorVersion      = "stage3-v1"
	managedStart          = "<!-- agent-orchestrator:start -->"
	managedEnd            = "<!-- agent-orchestrator:end -->"
)

type GeneratorConfig struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	Now           func() time.Time
}

type Generator struct {
	config GeneratorConfig
}

func NewGenerator(config GeneratorConfig) Generator {
	if config.MaxFileBytes <= 0 {
		config.MaxFileBytes = 2 << 20
	}
	if config.MaxTotalBytes <= 0 {
		config.MaxTotalBytes = 10 << 20
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return Generator{config: config}
}

func (g Generator) Generate(
	ctx context.Context,
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	report domain.DiscoveryReport,
) (domain.OnboardingProposal, string, error) {
	if project.LocalPath == nil || *project.LocalPath == "" {
		return domain.OnboardingProposal{}, "", fmt.Errorf("project has no local checkout: %w", domain.ErrInvalidStatus)
	}
	if report.ProjectID != project.ID || snapshot.ProjectID != project.ID || report.CommitSHA != snapshot.CommitSHA {
		return domain.OnboardingProposal{}, "", fmt.Errorf("discovery report does not match project snapshot: %w", domain.ErrConflict)
	}
	if len(report.Facts) == 0 {
		return domain.OnboardingProposal{}, "", fmt.Errorf("discovery report has no evidence: %w", domain.ErrValidation)
	}
	root, err := filepath.EvalSymlinks(*project.LocalPath)
	if err != nil {
		return domain.OnboardingProposal{}, "", fmt.Errorf("resolve project path: %w", err)
	}

	generated, err := g.buildGeneratedFiles(project, snapshot, report)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	proposal := domain.OnboardingProposal{
		SchemaVersion: proposalSchemaVersion,
		Generator:     generatorVersion,
		ProjectID:     project.ID,
		SnapshotID:    snapshot.ID,
		BaseCommit:    snapshot.CommitSHA,
		Files:         make([]domain.ProposedFile, 0, len(generated)),
		GeneratedAt:   g.config.Now().UTC(),
	}
	existingContents := make(map[string]string, len(generated))
	var totalBytes int64
	for _, candidate := range generated {
		if err := ctx.Err(); err != nil {
			return domain.OnboardingProposal{}, "", err
		}
		existing, exists, readErr := g.readExisting(root, candidate.path)
		if readErr != nil {
			return domain.OnboardingProposal{}, "", readErr
		}
		existingContents[candidate.path] = existing
		content := candidate.content
		conflicts := make([]domain.OnboardingConflict, 0)
		if exists {
			switch candidate.format {
			case formatYAML:
				content, conflicts, err = mergeYAML(existing, content, candidate.path)
			case formatMarkdown:
				content = mergeManagedText(existing, content)
			case formatJSON:
				if strings.TrimSpace(existing) != strings.TrimSpace(content) {
					conflicts = append(conflicts, domain.OnboardingConflict{
						Path: candidate.path, Field: "generated_report", Existing: "existing generated report",
						Discovered: "latest discovery report", Explanation: "The generated discovery artifact will be refreshed.",
					})
				}
			}
			if err != nil {
				return domain.OnboardingProposal{}, "", err
			}
		} else if candidate.format == formatMarkdown {
			content = mergeManagedText("", content)
		}
		content = normalizeText(content)
		action := domain.ProposalFileCreate
		if exists {
			action = domain.ProposalFileUpdate
			if normalizeText(existing) == content {
				action = domain.ProposalFileUnchanged
			}
		}
		totalBytes += int64(len(content))
		if int64(len(content)) > g.config.MaxFileBytes || totalBytes > g.config.MaxTotalBytes {
			return domain.OnboardingProposal{}, "", fmt.Errorf("onboarding proposal exceeds configured size: %w", domain.ErrValidation)
		}
		proposal.Files = append(proposal.Files, domain.ProposedFile{
			Path: candidate.path, Content: content, Action: action, Checksum: contentChecksum(content),
			Explanation: candidate.explanation, EvidencePaths: candidate.evidencePaths,
		})
		proposal.Conflicts = append(proposal.Conflicts, conflicts...)
	}
	for _, conflict := range report.Conflicts {
		proposal.Conflicts = append(proposal.Conflicts, domain.OnboardingConflict{
			Path: conflict.SourcePath, Field: conflict.Name, Existing: "repository evidence",
			Discovered: conflict.Value, Explanation: conflict.Explanation,
		})
	}
	sortProposal(&proposal)
	proposal.Checksum, err = domain.OnboardingProposalChecksum(proposal)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	unifiedDiff, err := buildUnifiedDiff(proposal.Files, existingContents)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	return proposal, unifiedDiff, nil
}

type fileFormat int

const (
	formatYAML fileFormat = iota
	formatMarkdown
	formatJSON
)

type generatedFile struct {
	path          string
	content       string
	format        fileFormat
	explanation   string
	evidencePaths []string
}

func (g Generator) buildGeneratedFiles(
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	report domain.DiscoveryReport,
) ([]generatedFile, error) {
	allEvidence := evidencePaths(report.Facts)
	serviceContent, err := yaml.Marshal(buildServiceManifest(project, snapshot, report))
	if err != nil {
		return nil, fmt.Errorf("marshal service manifest: %w", err)
	}
	architectureContent, err := yaml.Marshal(buildArchitectureManifest(report))
	if err != nil {
		return nil, fmt.Errorf("marshal architecture manifest: %w", err)
	}
	commands := buildCommandsManifest(report)
	commandsContent, err := yaml.Marshal(commands)
	if err != nil {
		return nil, fmt.Errorf("marshal commands manifest: %w", err)
	}
	portableReport := report
	portableReport.RepositoryPath = "."
	reportContent, err := json.MarshalIndent(portableReport, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal latest discovery report: %w", err)
	}
	files := []generatedFile{
		{path: "AGENTS.md", content: agentsManagedBlock(), format: formatMarkdown,
			explanation: "Expose the repository-local AI manifest and preserve all existing agent instructions.", evidencePaths: allEvidence},
		{path: ".ai/service.yaml", content: string(serviceContent), format: formatYAML,
			explanation: "Describe the discovered service identity, ownership, capabilities, dependencies, and instruction references.", evidencePaths: allEvidence},
		{path: ".ai/discovery/latest-report.json", content: string(reportContent), format: formatJSON,
			explanation: "Store the exact discovery report used to generate this proposal.", evidencePaths: allEvidence},
		{path: ".ai/agents/reviewer.md", content: reviewerAgent(), format: formatMarkdown,
			explanation: "Add repository-scoped review instructions based on the generated manifest.", evidencePaths: allEvidence},
	}
	architecturePaths := architectureEvidencePaths(report)
	if len(architecturePaths) > 0 {
		files = append(files, generatedFile{path: ".ai/architecture.yaml", content: string(architectureContent), format: formatYAML,
			explanation: "Record evidence-backed stack, ownership, and dependency architecture.", evidencePaths: architecturePaths})
	}
	if len(commands.Commands) > 0 {
		files = append(files, generatedFile{path: ".ai/commands.yaml", content: string(commandsContent), format: formatYAML,
			explanation: "Expose only commands discovered from repository manifests.", evidencePaths: commandEvidencePaths(report)})
	}

	if isBackendKind(snapshot.ServiceKind) {
		files = append(files, generatedFile{path: ".ai/agents/backend-coder.md", content: backendAgent(len(commands.Commands) > 0), format: formatMarkdown,
			explanation: "Add backend implementation guidance for the detected runtime service.", evidencePaths: allEvidence})
	}
	if hasFact(report.Facts, "ownership", "database_table") || hasFact(report.Facts, "contract", "database_schema") {
		files = append(files, generatedFile{path: ".ai/agents/migration-agent.md", content: migrationAgent(), format: formatMarkdown,
			explanation: "Add migration safety guidance because database ownership was discovered.", evidencePaths: factPaths(report.Facts, "ownership", "database_table")})
	}
	contractGroups := buildContractManifests(report)
	for path, manifest := range contractGroups {
		content, marshalErr := yaml.Marshal(manifest)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal %s: %w", path, marshalErr)
		}
		files = append(files, generatedFile{path: path, content: string(content), format: formatYAML,
			explanation: "Describe discovered contract evidence without inventing missing schema details.", evidencePaths: manifest.EvidencePaths})
	}
	if len(commands.Commands) > 0 {
		content, marshalErr := yaml.Marshal(buildTestWorkflow(commands))
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal test workflow: %w", marshalErr)
		}
		files = append(files, generatedFile{path: ".ai/workflows/test.yaml", content: string(content), format: formatYAML,
			explanation: "Run discovered verification commands.", evidencePaths: commandEvidencePaths(report)})
	}
	if snapshot.ServiceKind != domain.ServiceKindUnknown {
		content, marshalErr := yaml.Marshal(buildFeatureWorkflow())
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal feature workflow: %w", marshalErr)
		}
		files = append(files, generatedFile{path: ".ai/workflows/implement-feature.yaml", content: string(content), format: formatYAML,
			explanation: "Provide an approval-aware feature workflow for the detected runtime service.", evidencePaths: allEvidence})
	}
	if len(contractGroups) > 0 {
		content, marshalErr := yaml.Marshal(buildContractWorkflow())
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal contract workflow: %w", marshalErr)
		}
		files = append(files, generatedFile{path: ".ai/workflows/change-contract.yaml", content: string(content), format: formatYAML,
			explanation: "Require drift and consumer checks before contract changes.", evidencePaths: contractEvidencePaths(report)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func (g Generator) readExisting(root, relative string) (string, bool, error) {
	path, err := safeTargetPath(root, relative)
	if err != nil {
		return "", false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect existing onboarding file %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("onboarding target %s is not a regular file: %w", relative, domain.ErrForbidden)
	}
	if info.Size() > g.config.MaxFileBytes {
		return "", false, fmt.Errorf("existing onboarding file %s is too large: %w", relative, domain.ErrValidation)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read existing onboarding file %s: %w", relative, err)
	}
	return string(content), true, nil
}

func safeTargetPath(root, relative string) (string, error) {
	relative = filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe onboarding target path: %w", domain.ErrForbidden)
	}
	if relative != "AGENTS.md" && !strings.HasPrefix(filepath.ToSlash(relative), ".ai/") {
		return "", fmt.Errorf("onboarding target is outside AGENTS.md/.ai: %w", domain.ErrWriteScope)
	}
	current := root
	parent := filepath.Dir(relative)
	if parent != "." {
		for _, component := range strings.Split(parent, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			info, statErr := os.Lstat(current)
			if errors.Is(statErr, os.ErrNotExist) {
				break
			}
			if statErr != nil {
				return "", fmt.Errorf("inspect onboarding target parent: %w", statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("onboarding target parent is unsafe: %w", domain.ErrForbidden)
			}
		}
	}
	target := filepath.Join(root, relative)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("onboarding target escaped repository: %w", domain.ErrForbidden)
	}
	return target, nil
}

func mergeYAML(existing, generated, path string) (string, []domain.OnboardingConflict, error) {
	var existingValue map[string]any
	if err := yaml.Unmarshal([]byte(existing), &existingValue); err != nil {
		return "", nil, fmt.Errorf("existing %s is invalid YAML: %w", path, domain.ErrConflict)
	}
	var generatedValue map[string]any
	if err := yaml.Unmarshal([]byte(generated), &generatedValue); err != nil {
		return "", nil, fmt.Errorf("generated %s is invalid YAML: %w", path, err)
	}
	conflicts := make([]domain.OnboardingConflict, 0)
	mergeMaps(existingValue, generatedValue, path, "", &conflicts)
	content, err := yaml.Marshal(existingValue)
	if err != nil {
		return "", nil, fmt.Errorf("marshal merged %s: %w", path, err)
	}
	return string(content), conflicts, nil
}

func mergeMaps(existing, generated map[string]any, path, prefix string, conflicts *[]domain.OnboardingConflict) {
	keys := make([]string, 0, len(generated))
	for key := range generated {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		generatedValue := generated[key]
		existingValue, exists := existing[key]
		field := key
		if prefix != "" {
			field = prefix + "." + key
		}
		if !exists {
			existing[key] = generatedValue
			continue
		}
		existingMap, existingIsMap := stringMap(existingValue)
		generatedMap, generatedIsMap := stringMap(generatedValue)
		if existingIsMap && generatedIsMap {
			mergeMaps(existingMap, generatedMap, path, field, conflicts)
			existing[key] = existingMap
			continue
		}
		if valuesEqual(existingValue, generatedValue) {
			continue
		}
		*conflicts = append(*conflicts, domain.OnboardingConflict{
			Path: path, Field: field, Existing: safeConflictValue(field, existingValue),
			Discovered:  safeConflictValue(field, generatedValue),
			Explanation: "The existing user-authored value is preserved and differs from discovery.",
		})
	}
}

func stringMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func valuesEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func safeConflictValue(field string, value any) string {
	lower := strings.ToLower(field)
	for _, marker := range []string{"token", "password", "secret", "credential", "private_key"} {
		if strings.Contains(lower, marker) {
			return "[redacted]"
		}
	}
	result := fmt.Sprint(value)
	if len(result) > 500 {
		result = result[:500] + "…"
	}
	return result
}

func mergeManagedText(existing, managed string) string {
	existing = strings.ReplaceAll(existing, "\r\n", "\n")
	managed = strings.TrimSpace(managed)
	block := managedStart + "\n" + managed + "\n" + managedEnd
	start := strings.Index(existing, managedStart)
	end := strings.Index(existing, managedEnd)
	if start >= 0 && end >= start {
		end += len(managedEnd)
		return strings.TrimSpace(existing[:start]+block+existing[end:]) + "\n"
	}
	if strings.TrimSpace(existing) == "" {
		return block + "\n"
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n"
}

func normalizeText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n")) + "\n"
}

func contentChecksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func buildUnifiedDiff(files []domain.ProposedFile, existing map[string]string) (string, error) {
	var result strings.Builder
	for _, file := range files {
		if file.Action == domain.ProposalFileUnchanged {
			continue
		}
		fromName := "a/" + file.Path
		if file.Action == domain.ProposalFileCreate {
			fromName = "/dev/null"
		}
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: difflib.SplitLines(normalizeOptional(existing[file.Path])), B: difflib.SplitLines(file.Content),
			FromFile: fromName, ToFile: "b/" + file.Path, Context: 3,
		})
		if err != nil {
			return "", fmt.Errorf("build diff for %s: %w", file.Path, err)
		}
		result.WriteString(diff)
	}
	return result.String(), nil
}

func normalizeOptional(value string) string {
	if value == "" {
		return ""
	}
	return normalizeText(value)
}

func sortProposal(proposal *domain.OnboardingProposal) {
	sort.Slice(proposal.Files, func(i, j int) bool { return proposal.Files[i].Path < proposal.Files[j].Path })
	for index := range proposal.Files {
		proposal.Files[index].EvidencePaths = uniqueSorted(proposal.Files[index].EvidencePaths)
	}
	sort.Slice(proposal.Conflicts, func(i, j int) bool {
		return proposal.Conflicts[i].Path+proposal.Conflicts[i].Field < proposal.Conflicts[j].Path+proposal.Conflicts[j].Field
	})
}

var _ repository.OnboardingGenerator = Generator{}
