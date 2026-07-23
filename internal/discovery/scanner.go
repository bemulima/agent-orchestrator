package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const reportSchemaVersion = 11

var excludedDirectories = map[string]struct{}{
	".git": {}, ".cache": {}, ".gocache": {}, ".idea": {}, ".vscode": {},
	"node_modules": {}, "vendor": {}, "dist": {}, "build": {}, ".next": {},
	"coverage": {}, "tmp": {}, "temp": {}, "__pycache__": {}, ".pytest_cache": {},
}

var (
	httpRoutePattern      = regexp.MustCompile(`(?i)(?:\.|@)(get|post|put|patch|delete|options|head)\s*\(\s*["` + "`" + `']([^"` + "`" + `']+)["` + "`" + `']`)
	goHandleFuncPattern   = regexp.MustCompile(`(?m)(?:[a-zA-Z_][a-zA-Z0-9_]*\.)?HandleFunc\s*\(\s*["` + "`" + `']([^"` + "`" + `']+)["` + "`" + `']`)
	goHTTPMethodPattern   = regexp.MustCompile(`(?i)\.Method\s*!=\s*http\.Method(Get|Post|Put|Patch|Delete|Options|Head)`)
	pythonHandlerPattern  = regexp.MustCompile(`(?m)^\s*def\s+do_(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\s*\(`)
	pythonPathPattern     = regexp.MustCompile(`self\.path\s*(?:==|!=)\s*["']([^"']+)["']`)
	databaseTablePattern  = regexp.MustCompile(`(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?(?:["` + "`" + `]?[a-zA-Z0-9_-]+["` + "`" + `]?\.)?["` + "`" + `]?([a-zA-Z][a-zA-Z0-9_-]*)`)
	environmentKeyPattern = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)\s*=`)
	makeTargetPattern     = regexp.MustCompile(`(?m)^([a-zA-Z0-9][a-zA-Z0-9_.-]*):(?:[^=]|$)`)
	proxyPassPattern      = regexp.MustCompile(`(?m)proxy_pass\s+([^;\s]+)`)
	frontendCallPattern   = regexp.MustCompile(`(?i)(fetch|axios\.(?:get|post|put|patch|delete))\s*\(\s*["` + "`" + `']([^"` + "`" + `']+)["` + "`" + `']`)
	subjectPattern        = regexp.MustCompile(`["` + "`" + `']([a-zA-Z][a-zA-Z0-9_-]*(?:\.[a-zA-Z0-9_*>-]+){1,})["` + "`" + `']`)
	aiServiceKindPattern  = regexp.MustCompile(`(?m)^\s*(?:service_kind|kind):\s*["']?([a-z_]+)`)
)

type Config struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxDepth      int
	Now           func() time.Time
}

// Scanner performs bounded, read-only filesystem discovery.
type Scanner struct {
	config Config
}

func NewScanner(config Config) Scanner {
	if config.MaxFiles <= 0 {
		config.MaxFiles = 10000
	}
	if config.MaxFileBytes <= 0 {
		config.MaxFileBytes = 1 << 20
	}
	if config.MaxTotalBytes <= 0 {
		config.MaxTotalBytes = 20 << 20
	}
	if config.MaxDepth <= 0 {
		config.MaxDepth = 24
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return Scanner{config: config}
}

func (s Scanner) Scan(
	ctx context.Context,
	project domain.Project,
	source domain.RepositorySource,
) (domain.DiscoveryReport, error) {
	startedAt := s.config.Now().UTC()
	files, inventory, err := s.inventory(ctx, source.LocalPath)
	if err != nil {
		return domain.DiscoveryReport{}, err
	}
	collector := newCollector()
	filesByPath := make(map[string][]byte, len(files))
	for _, file := range files {
		filesByPath[filepath.ToSlash(file.path)] = file.content
	}
	state := detectorState{
		project:         project,
		source:          source,
		collector:       collector,
		filesByPath:     filesByPath,
		promptChecksums: make(map[string][]promptChecksum),
		lockFiles:       make([]string, 0),
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return domain.DiscoveryReport{}, err
		}
		s.detectFile(&state, file)
	}
	s.detectDerivedFacts(&state)
	s.detectConflicts(&state)
	if isNonRuntimeRepositoryRole(project.RepositoryRole) {
		collector.removeFactCategories("capability", "contract", "infrastructure", "ownership", "relation")
	}
	collector.sort()
	return domain.DiscoveryReport{
		SchemaVersion:   reportSchemaVersion,
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		RepositoryRole:  project.RepositoryRole,
		RepositoryPath:  source.LocalPath,
		CommitSHA:       source.HeadCommit,
		Branch:          source.CurrentBranch,
		IsDirty:         source.IsDirty,
		ContentChecksum: inventory.ContentChecksum,
		StartedAt:       startedAt,
		CompletedAt:     s.config.Now().UTC(),
		Inventory:       inventory,
		Facts:           collector.facts,
		Conflicts:       collector.conflicts,
	}, nil
}

type analyzedFile struct {
	path    string
	content []byte
}

func (s Scanner) inventory(ctx context.Context, root string) ([]analyzedFile, domain.InventorySummary, error) {
	root = filepath.Clean(root)
	files := make([]analyzedFile, 0)
	summary := domain.InventorySummary{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			summary.Warnings = append(summary.Warnings, "unreadable path: "+relativePath(root, path))
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := relativePath(root, path)
		if relative == "." {
			return nil
		}
		depth := strings.Count(relative, "/") + 1
		if depth > s.config.MaxDepth {
			summary.Truncated = true
			summary.ExcludedPaths++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, excluded := excludedDirectories[entry.Name()]; excluded {
				summary.ExcludedPaths++
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			summary.ExcludedPaths++
			return nil
		}
		if summary.FilesVisited >= s.config.MaxFiles {
			summary.Truncated = true
			return fs.SkipAll
		}
		summary.FilesVisited++
		if !shouldAnalyze(relative) {
			return nil
		}
		content, tooLarge, err := readBounded(path, s.config.MaxFileBytes)
		if err != nil {
			summary.Warnings = append(summary.Warnings, "cannot read: "+relative)
			return nil
		}
		if tooLarge {
			summary.SkippedLarge++
			return nil
		}
		if summary.BytesAnalyzed+int64(len(content)) > s.config.MaxTotalBytes {
			summary.Truncated = true
			return fs.SkipAll
		}
		if isBinary(content) {
			summary.ExcludedPaths++
			return nil
		}
		summary.FilesAnalyzed++
		summary.BytesAnalyzed += int64(len(content))
		files = append(files, analyzedFile{path: relative, content: content})
		return nil
	})
	if err != nil {
		return nil, summary, fmt.Errorf("inventory repository: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "%d\x00%d\x00%d\x00%d\x00%d\x00%t\x00",
		summary.FilesVisited, summary.FilesAnalyzed, summary.BytesAnalyzed,
		summary.ExcludedPaths, summary.SkippedLarge, summary.Truncated)
	for _, file := range files {
		_, _ = hasher.Write([]byte(file.path))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(file.content)
		_, _ = hasher.Write([]byte{0})
	}
	summary.ContentChecksum = hex.EncodeToString(hasher.Sum(nil))
	sort.Strings(summary.Warnings)
	return files, summary, nil
}

func readBounded(path string, limit int64) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(content)) > limit {
		return nil, true, nil
	}
	return content, false, nil
}

func shouldAnalyze(path string) bool {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	lowerBase := strings.ToLower(base)
	if isSecretEnvironmentFile(lowerBase) || strings.HasSuffix(lowerBase, ".pem") ||
		strings.HasSuffix(lowerBase, ".key") || strings.HasSuffix(lowerBase, ".p12") ||
		strings.HasSuffix(lowerBase, ".pfx") {
		return false
	}
	if isEnvironmentExample(lowerBase) {
		return true
	}
	exact := map[string]struct{}{
		"readme.md": {}, "agents.md": {}, "go.mod": {}, "go.sum": {},
		"package.json": {}, "package-lock.json": {}, "pnpm-lock.yaml": {},
		"yarn.lock": {}, "bun.lockb": {}, "makefile": {}, "taskfile.yml": {},
		"taskfile.yaml": {}, ".gitlab-ci.yml": {}, ".gitlab-ci.yaml": {},
	}
	if _, ok := exact[lowerBase]; ok {
		return true
	}
	if strings.HasPrefix(lowerBase, "dockerfile") || strings.HasPrefix(lowerBase, "docker-compose") ||
		strings.HasPrefix(lowerBase, "compose.") || strings.HasSuffix(lowerBase, ".nginx.conf") || lowerBase == "nginx.conf" {
		return true
	}
	for _, prefix := range []string{
		"prompts/", ".ai/", ".github/workflows/", "openapi/", "swagger/", "proto/",
		"migrations/", "db/migrations/", "routes/", "router/", "handlers/", "controllers/",
		"internal/", "src/", "app/", "config/", "configs/", "deploy/", "infra/", "k8s/",
	} {
		if strings.HasPrefix(strings.ToLower(path), prefix) {
			return hasAnalyzableExtension(lowerBase)
		}
	}
	return hasAnalyzableExtension(lowerBase)
}

func isNonProductionEvidencePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	base := filepath.Base(path)
	for _, suffix := range []string{
		"_test.go", "_test.py", ".test.ts", ".test.tsx", ".test.js", ".test.jsx",
		".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "test", "tests", "testdata", "__tests__", "fixture", "fixtures", "example", "examples":
			return true
		}
	}
	return false
}

func hasAnalyzableExtension(name string) bool {
	for _, extension := range []string{
		".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".php",
		".sql", ".proto", ".yaml", ".yml", ".json", ".toml", ".md", ".conf",
	} {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

func isSecretEnvironmentFile(name string) bool {
	if isEnvironmentExample(name) {
		return false
	}
	return name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".env")
}

func isEnvironmentExample(name string) bool {
	return name == ".env.example" || name == ".env.dist" || name == ".env.sample" ||
		name == ".env.template" || strings.HasSuffix(name, ".env.example") ||
		strings.HasSuffix(name, ".env.sample") || strings.HasSuffix(name, ".env.template")
}

func isBinary(content []byte) bool {
	limit := len(content)
	if limit > 8000 {
		limit = 8000
	}
	for _, value := range content[:limit] {
		if value == 0 {
			return true
		}
	}
	return false
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

type collector struct {
	facts      []domain.Evidence
	conflicts  []domain.Evidence
	seenFacts  map[string]struct{}
	seenIssues map[string]struct{}
}

func newCollector() *collector {
	return &collector{seenFacts: make(map[string]struct{}), seenIssues: make(map[string]struct{})}
}

func (c *collector) fact(category, name, value string, confidence float64, sourcePath, explanation string) {
	evidence := domain.Evidence{
		Category: category, Name: name, Value: value, Confidence: confidence,
		SourcePath: sourcePath, Explanation: explanation,
	}
	key := category + "\x00" + name + "\x00" + value + "\x00" + sourcePath
	if _, exists := c.seenFacts[key]; exists {
		return
	}
	c.seenFacts[key] = struct{}{}
	c.facts = append(c.facts, evidence)
}

func (c *collector) conflict(name, value string, confidence float64, sourcePath, explanation string) {
	evidence := domain.Evidence{
		Category: "conflict", Name: name, Value: value, Confidence: confidence,
		SourcePath: sourcePath, Explanation: explanation,
	}
	key := name + "\x00" + value + "\x00" + sourcePath
	if _, exists := c.seenIssues[key]; exists {
		return
	}
	c.seenIssues[key] = struct{}{}
	c.conflicts = append(c.conflicts, evidence)
}

func (c *collector) removeFactCategories(categories ...string) {
	excluded := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		excluded[category] = struct{}{}
	}
	filtered := c.facts[:0]
	for _, fact := range c.facts {
		if _, remove := excluded[fact.Category]; remove {
			continue
		}
		filtered = append(filtered, fact)
	}
	c.facts = filtered
}

func (c *collector) sort() {
	sortEvidence := func(values []domain.Evidence) {
		sort.Slice(values, func(i, j int) bool {
			left := values[i].Category + values[i].Name + values[i].Value + values[i].SourcePath
			right := values[j].Category + values[j].Name + values[j].Value + values[j].SourcePath
			return left < right
		})
	}
	sortEvidence(c.facts)
	sortEvidence(c.conflicts)
}

type promptChecksum struct {
	path     string
	checksum string
}

type detectorState struct {
	project         domain.Project
	source          domain.RepositorySource
	collector       *collector
	filesByPath     map[string][]byte
	promptChecksums map[string][]promptChecksum
	lockFiles       []string
	readmePurpose   string
	goDetected      bool
	nodeDetected    bool
	pythonDetected  bool
	phpDetected     bool
	nextDetected    bool
	nginxDetected   bool
	composeDetected bool
	aiKind          string
}

func isNonRuntimeRepositoryRole(role domain.RepositoryRole) bool {
	switch role {
	case domain.RepositoryRoleContent, domain.RepositoryRolePolicy,
		domain.RepositoryRoleDocumentation, domain.RepositoryRoleArchive:
		return true
	default:
		return false
	}
}

func checksum(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

var _ repository.DiscoveryScanner = Scanner{}
