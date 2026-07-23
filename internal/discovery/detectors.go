package discovery

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func (s Scanner) detectFile(state *detectorState, file analyzedFile) {
	path := filepath.ToSlash(file.path)
	base := strings.ToLower(filepath.Base(path))
	content := string(file.content)
	s.detectPurpose(state, path, base, content)
	if !isNonProductionEvidencePath(path) && !isDocumentationMarkdownPath(path) {
		s.detectStack(state, path, base, content)
		s.extractCapabilities(state, path, content)
		s.extractOwnership(state, path, content)
		s.extractContracts(state, path, base, content)
		s.extractGatewayRelations(state, path, content)
		s.extractFrontendConsumers(state, path, content)
		s.extractInfrastructure(state, path, base, content)
		s.extractCommands(state, path, base, content)
	}
	s.analyzePrompts(state, path, file.content)
	s.analyzeApprovedSemanticReport(state, path, file.content)

	if strings.Contains(path, "/") && (base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "yarn.lock" || base == "bun.lockb") ||
		base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "yarn.lock" || base == "bun.lockb" {
		state.lockFiles = append(state.lockFiles, path)
	}
	if strings.HasPrefix(path, ".ai/") && (base == "service.yaml" || base == "service.yml") {
		if match := aiServiceKindPattern.FindStringSubmatch(content); match != nil {
			state.aiKind = match[1]
			state.collector.fact("instruction", "existing_service_manifest", path, 1, path,
				"An existing .ai service manifest declares service kind "+match[1]+".")
		}
	}
}

func isDocumentationMarkdownPath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	return strings.HasPrefix(path, "docs/") && strings.HasSuffix(path, ".md")
}

func (s Scanner) analyzeApprovedSemanticReport(state *detectorState, path string, content []byte) {
	if filepath.ToSlash(path) != ".ai/discovery/semantic-report.json" {
		return
	}
	var analysis domain.SemanticAnalysis
	if err := json.Unmarshal(content, &analysis); err != nil || analysis.SchemaVersion != 1 ||
		analysis.ProjectName != state.project.Name || len(analysis.Facts) > 200 {
		state.collector.conflict("invalid_semantic_report", path, 1, path,
			"The approved semantic report is invalid, mismatched, or unsupported.")
		return
	}
	allowed := map[string]struct{}{
		"purpose": {}, "capability": {}, "ownership": {}, "relation": {}, "contract": {},
		"business_rule": {}, "business_process": {}, "entity": {}, "infrastructure": {}, "command": {},
	}
	for _, fact := range analysis.Facts {
		sourcePath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(fact.SourcePath))))
		sourceContent, sourceExists := state.filesByPath[sourcePath]
		if _, exists := allowed[fact.Category]; !exists || strings.TrimSpace(fact.Name) == "" ||
			strings.TrimSpace(fact.Value) == "" || fact.Confidence < .5 || fact.Confidence > .95 ||
			len(fact.Name) > 128 || len(fact.Value) > 1000 || len(fact.Explanation) > 2000 ||
			fact.Category == "command" && (sanitizeCommand(fact.Value) != fact.Value || !isApprovedSemanticCommandSource(fact.SourcePath)) ||
			fact.Category == "relation" && isSemanticSelfReference(fact.Value, state.project.Name) ||
			!safeManifestReference(fact.SourcePath) || sourcePath == path || !sourceExists ||
			len(fact.EvidenceQuote) < 8 || len(fact.EvidenceQuote) > 500 ||
			!strings.Contains(normalizedSemanticEvidence(string(sourceContent)), normalizedSemanticEvidence(fact.EvidenceQuote)) {
			state.collector.conflict("invalid_semantic_fact", fact.Category+":"+fact.Name, 1, path,
				"A semantic fact is malformed, stale, or has no verifiable source quote and was not imported.")
			continue
		}
		state.collector.fact(fact.Category, fact.Name, fact.Value, fact.Confidence, path,
			"Approved semantic evidence references "+sourcePath+". "+fact.Explanation)
	}
	for _, question := range analysis.OpenQuestions {
		if strings.TrimSpace(question.Question) == "" {
			continue
		}
		state.collector.conflict("semantic_open_question", question.Question, .8, path, question.Reason)
	}
}

func safeManifestReference(value string) bool {
	value = strings.TrimSpace(value)
	cleaned := filepath.Clean(filepath.FromSlash(value))
	return value != "" && !strings.Contains(value, "\\") && !filepath.IsAbs(cleaned) && cleaned != "." && cleaned != ".." &&
		!strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func normalizedSemanticEvidence(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func isSemanticSelfReference(value, projectName string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	projectName = strings.ToLower(strings.TrimSpace(projectName))
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if colon := strings.IndexByte(value, ':'); colon >= 0 {
		value = value[:colon]
	}
	return value != "" && value == projectName
}

func isApprovedSemanticCommandSource(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch filepath.Base(path) {
	case "makefile", "taskfile.yml", "taskfile.yaml", "package.json", "pyproject.toml", "composer.json", "readme.md", "agents.md":
		return true
	default:
		return false
	}
}

func (s Scanner) detectStack(state *detectorState, path, base, content string) {
	switch base {
	case "go.mod":
		state.goDetected = true
		state.collector.fact("stack", "language", "go", 1, path, "go.mod is the canonical Go module manifest.")
		frameworks := map[string]string{
			"github.com/go-chi/chi": "chi", "github.com/gin-gonic/gin": "gin",
			"github.com/labstack/echo": "echo", "go.temporal.io/sdk": "temporal",
			"github.com/nats-io/nats.go": "nats", "github.com/jackc/pgx": "pgx",
		}
		for dependency, framework := range frameworks {
			if strings.Contains(content, dependency) {
				state.collector.fact("stack", "framework", framework, .98, path,
					"go.mod declares dependency "+dependency+".")
			}
		}
	case "package.json":
		state.nodeDetected = true
		state.collector.fact("stack", "runtime", "node", .98, path, "package.json declares a Node.js project.")
		var manifest struct {
			Name            string            `json:"name"`
			Scripts         map[string]string `json:"scripts"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal([]byte(content), &manifest); err != nil {
			state.collector.conflict("invalid_manifest", "package.json", 1, path, "package.json is not valid JSON.")
			return
		}
		if manifest.Name != "" {
			state.collector.fact("identity", "package_name", manifest.Name, .95, path,
				"package.json declares the package name.")
		}
		dependencies := make(map[string]string, len(manifest.Dependencies)+len(manifest.DevDependencies))
		for name, version := range manifest.Dependencies {
			dependencies[name] = version
		}
		for name, version := range manifest.DevDependencies {
			dependencies[name] = version
		}
		for dependency, framework := range map[string]string{
			"next": "nextjs", "react": "react", "express": "express", "@nestjs/core": "nestjs",
			"fastify": "fastify", "playwright": "playwright", "typescript": "typescript",
		} {
			if _, exists := dependencies[dependency]; exists {
				state.collector.fact("stack", "framework", framework, .98, path,
					"package.json declares dependency "+dependency+".")
				if dependency == "next" {
					state.nextDetected = true
				}
			}
		}
		for name, command := range manifest.Scripts {
			state.collector.fact("command", name, sanitizeCommand(command), .95, path,
				"package.json exposes this script as a repository command.")
		}
	default:
		if strings.HasPrefix(base, "dockerfile") {
			state.collector.fact("stack", "container", "docker", .95, path, "A Dockerfile defines a container build.")
		}
		if base == "nginx.conf" || strings.HasSuffix(base, ".nginx.conf") {
			state.nginxDetected = true
			state.collector.fact("stack", "framework", "nginx", .98, path, "Nginx configuration is present.")
		}
		if strings.HasSuffix(base, ".py") {
			state.pythonDetected = true
			state.collector.fact("stack", "language", "python", .82, path, "Python source code is present.")
		}
		if strings.HasSuffix(base, ".php") {
			state.phpDetected = true
			state.collector.fact("stack", "language", "php", .82, path, "PHP source code is present.")
		}
	}
}

func (s Scanner) detectPurpose(state *detectorState, path, base, content string) {
	if base != "readme.md" || strings.Contains(path, "/") {
		return
	}
	paragraphs := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" || strings.HasPrefix(paragraph, "#") || strings.HasPrefix(paragraph, "[") || strings.HasPrefix(paragraph, "!") {
			continue
		}
		paragraph = strings.Join(strings.Fields(paragraph), " ")
		if len(paragraph) > 500 {
			paragraph = paragraph[:500]
		}
		state.readmePurpose = paragraph
		state.collector.fact("purpose", "summary", paragraph, .85, path,
			"The first prose paragraph in the root README describes the repository purpose.")
		return
	}
}

func (s Scanner) extractCapabilities(state *detectorState, path, content string) {
	for _, match := range httpRoutePattern.FindAllStringSubmatch(content, -1) {
		method := strings.ToUpper(match[1])
		route := match[2]
		collectHTTPRoute(state, method, route, .84, path,
			"A route registration or controller decorator exposes this HTTP operation.")
	}
	s.extractGoHTTPRoutes(state, path, content)
	s.extractPythonHTTPRoutes(state, path, content)
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		publishCall := strings.Contains(lower, ".publish(") || strings.Contains(lower, "publish(")
		subscribeCall := strings.Contains(lower, ".subscribe(") || strings.Contains(lower, "subscribe(")
		if !strings.Contains(lower, "nats") && !strings.Contains(lower, "subject") &&
			!publishCall && !subscribeCall {
			continue
		}
		for _, match := range subjectPattern.FindAllStringSubmatch(line, -1) {
			state.collector.fact("capability", "event_subject", match[1], .72, path,
				"A NATS/event-related source line references this subject.")
			if publishCall {
				state.collector.fact("contract", "event_publish", match[1], .78, path,
					"An event publisher emits this subject.")
			}
			if subscribeCall {
				state.collector.fact("contract", "event_subscribe", match[1], .78, path,
					"An event subscriber consumes this subject.")
			}
		}
	}
}

func (s Scanner) extractGoHTTPRoutes(state *detectorState, path, content string) {
	if !strings.HasSuffix(strings.ToLower(path), ".go") {
		return
	}
	matches := goHandleFuncPattern.FindAllStringSubmatchIndex(content, -1)
	for index, match := range matches {
		route := content[match[2]:match[3]]
		end := len(content)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		block := content[match[0]:end]
		method := "ANY"
		confidence := .72
		explanation := "A net/http HandleFunc registration exposes this route without a single-method guard."
		if methodMatch := goHTTPMethodPattern.FindStringSubmatch(block); methodMatch != nil {
			method = strings.ToUpper(methodMatch[1])
			confidence = .86
			explanation = "A net/http HandleFunc registration and method guard expose this HTTP operation."
		} else if isHealthRoute(route) {
			method = "GET"
			explanation = "An unrestricted net/http health handler exposes the conventional GET health operation."
		}
		collectHTTPRoute(state, method, route, confidence, path, explanation)
	}
}

func (s Scanner) extractPythonHTTPRoutes(state *detectorState, path, content string) {
	if !strings.HasSuffix(strings.ToLower(path), ".py") {
		return
	}
	handlers := pythonHandlerPattern.FindAllStringSubmatchIndex(content, -1)
	for index, handler := range handlers {
		method := strings.ToUpper(content[handler[2]:handler[3]])
		end := len(content)
		if index+1 < len(handlers) {
			end = handlers[index+1][0]
		}
		block := content[handler[0]:end]
		for _, pathMatch := range pythonPathPattern.FindAllStringSubmatch(block, -1) {
			collectHTTPRoute(state, method, pathMatch[1], .86, path,
				"A Python BaseHTTPRequestHandler method checks and serves this HTTP path.")
		}
	}
}

func collectHTTPRoute(
	state *detectorState,
	method string,
	route string,
	confidence float64,
	path string,
	explanation string,
) {
	if !looksLikeRoute(route) {
		return
	}
	value := method + " " + route
	state.collector.fact("capability", "http_route", value, confidence, path, explanation)
	state.collector.fact("contract", "http_produce", value, confidence, path,
		"The service implementation provides this HTTP contract.")
}

func (s Scanner) extractOwnership(state *detectorState, path, content string) {
	if strings.HasSuffix(strings.ToLower(path), ".sql") {
		for _, match := range databaseTablePattern.FindAllStringSubmatch(content, -1) {
			state.collector.fact("ownership", "database_table", match[1], .96, path,
				"A checked-in SQL schema file creates this table, indicating schema ownership.")
		}
	}
	if isEnvironmentExample(strings.ToLower(filepath.Base(path))) {
		for _, match := range environmentKeyPattern.FindAllStringSubmatch(content, -1) {
			state.collector.fact("configuration", "environment_key", match[1], .95, path,
				"An environment example declares this key; values are intentionally not collected.")
		}
	}
}

func (s Scanner) extractContracts(state *detectorState, path, base, content string) {
	lowerPath := strings.ToLower(path)
	if strings.HasPrefix(lowerPath, "openapi/") || strings.HasPrefix(lowerPath, "swagger/") ||
		strings.Contains(lowerPath, "/openapi/") || strings.Contains(lowerPath, "/swagger/") {
		state.collector.fact("contract", "http_definition", path, .98, path,
			"The file is stored in an OpenAPI or Swagger contract directory.")
	}
	if strings.HasSuffix(base, ".proto") || strings.HasPrefix(lowerPath, "proto/") {
		state.collector.fact("contract", "grpc_definition", path, .98, path,
			"A Protocol Buffers definition declares a versionable service contract.")
	}
	if strings.HasSuffix(base, ".sql") && databaseTablePattern.MatchString(content) {
		state.collector.fact("contract", "database_schema", path, .96, path,
			"The SQL file creates database resources owned by the project.")
	}
}

func (s Scanner) extractGatewayRelations(state *detectorState, path, content string) {
	for _, match := range proxyPassPattern.FindAllStringSubmatch(content, -1) {
		state.nginxDetected = true
		if state.project.RepositoryRole == domain.RepositoryRoleFrontend {
			continue
		}
		state.collector.fact("relation", "gateway_routes_to", match[1], .92, path,
			"An nginx proxy_pass directive routes traffic to this upstream.")
	}
}

func (s Scanner) extractFrontendConsumers(state *detectorState, path, content string) {
	if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") &&
		!strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".jsx") {
		return
	}
	for _, match := range frontendCallPattern.FindAllStringSubmatch(content, -1) {
		endpoint := redactURLQuery(match[2])
		if !strings.Contains(endpoint, "/api/") && !strings.HasPrefix(endpoint, "http") {
			continue
		}
		method := "GET"
		if strings.HasPrefix(strings.ToLower(match[1]), "axios.") {
			method = strings.ToUpper(strings.TrimPrefix(strings.ToLower(match[1]), "axios."))
		}
		value := method + " " + endpoint
		state.collector.fact("relation", "frontend_consumes", value, .72, path,
			"A frontend HTTP client call references this endpoint.")
		state.collector.fact("contract", "http_consume", value, .72, path,
			"The frontend implementation consumes this HTTP contract.")
	}
}

func (s Scanner) extractInfrastructure(state *detectorState, path, base, content string) {
	if !isComposeFile(base) {
		return
	}
	state.composeDetected = true
	state.collector.fact("stack", "orchestration", "docker_compose", .98, path,
		"A Docker Compose manifest defines local infrastructure or runtime services.")
	var manifest struct {
		Services map[string]struct {
			Image     string      `yaml:"image"`
			DependsOn interface{} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(content), &manifest); err != nil {
		state.collector.conflict("invalid_manifest", "docker_compose", 1, path,
			"The Docker Compose manifest is not valid YAML.")
		return
	}
	names := make([]string, 0, len(manifest.Services))
	for name := range manifest.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := manifest.Services[name]
		value := name
		if service.Image != "" {
			value += "=" + service.Image
		}
		state.collector.fact("infrastructure", "compose_service", value, .96, path,
			"Docker Compose declares this service.")
		for _, dependency := range composeDependencies(service.DependsOn) {
			state.collector.fact("relation", "depends_on", dependency, .90, path,
				"Docker Compose declares this service dependency.")
		}
	}
}

func (s Scanner) extractCommands(state *detectorState, path, base, content string) {
	if base != "makefile" {
		return
	}
	for _, match := range makeTargetPattern.FindAllStringSubmatch(content, -1) {
		state.collector.fact("command", "make_target", match[1], .92, path,
			"The Makefile exposes this target.")
	}
}

func (s Scanner) analyzePrompts(state *detectorState, path string, content []byte) {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	isPrompt := strings.HasPrefix(lower, "prompts/") || strings.Contains(lower, "/prompts/") ||
		base == "agents.md" || strings.HasPrefix(lower, ".ai/")
	isPolicyDocument := state.project.RepositoryRole == domain.RepositoryRolePolicy && strings.HasSuffix(lower, ".md")
	if !isPrompt && !isPolicyDocument {
		return
	}
	hash := checksum(content)
	state.collector.fact("instruction", "instruction_file", hash, .98, path,
		"The file contains repository or agent instructions; only its checksum is collected.")
	if !isPrompt {
		return
	}
	state.promptChecksums[base] = append(state.promptChecksums[base], promptChecksum{path: path, checksum: hash})
}

func (s Scanner) detectDerivedFacts(state *detectorState) {
	kind, confidence, explanation := domain.ServiceKindUnknown, .45, "No stronger runtime service-kind signal was found."
	sourcePath := "."
	runtimeDetected := state.goDetected || state.nodeDetected || state.pythonDetected || state.phpDetected ||
		state.nextDetected || state.nginxDetected || state.composeDetected
	switch {
	case isNonRuntimeRepositoryRole(state.project.RepositoryRole):
		kind, confidence, explanation = domain.ServiceKindUnknown, .99,
			"This repository role is intentionally not a runtime service kind."
	case state.project.RepositoryRole == domain.RepositoryRoleFrontend || state.nextDetected:
		kind, confidence, explanation = domain.ServiceKindFrontendApplication, .96,
			"The repository role or detected Next.js dependencies identify a frontend application."
		sourcePath = firstSource(state.collector.facts, "stack", "framework", "nextjs")
	case state.project.RepositoryRole == domain.RepositoryRoleInfrastructure || state.composeDetected && !state.goDetected && !state.nodeDetected:
		kind, confidence, explanation = domain.ServiceKindInfrastructure, .93,
			"The repository role or Compose-only manifest identifies infrastructure."
		sourcePath = firstSource(state.collector.facts, "stack", "orchestration", "docker_compose")
	case state.nginxDetected || runtimeDetected && strings.Contains(state.project.Name, "gateway"):
		kind, confidence, explanation = domain.ServiceKindGateway, .94,
			"Nginx gateway configuration or the canonical repository name identifies a gateway."
		sourcePath = firstSource(state.collector.facts, "stack", "framework", "nginx")
	case runtimeDetected && (strings.Contains(state.project.Name, "ai-") || strings.Contains(state.project.Name, "-ai")):
		kind, confidence, explanation = domain.ServiceKindAIService, .82,
			"The canonical repository name and runtime manifests identify an AI-focused service."
	case runtimeDetected && (strings.Contains(state.project.Name, "filestorage") || strings.Contains(state.project.Name, "tarantool")):
		kind, confidence, explanation = domain.ServiceKindStorageService, .84,
			"The canonical repository name and detected database/container evidence identify a storage service."
	case state.goDetected || state.nodeDetected || state.pythonDetected || state.phpDetected:
		kind, confidence, explanation = domain.ServiceKindBackendService, .80,
			"Detected runtime source or manifests identify a backend service, with no stronger specialized kind signal."
	}
	if sourcePath == "" {
		sourcePath = "."
	}
	state.collector.fact("classification", "service_kind", string(kind), confidence, sourcePath, explanation)
}

func (s Scanner) detectConflicts(state *detectorState) {
	for base, entries := range state.promptChecksums {
		checksums := make(map[string]struct{})
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			checksums[entry.checksum] = struct{}{}
			paths = append(paths, entry.path)
		}
		if len(checksums) > 1 {
			sort.Strings(paths)
			state.collector.conflict("instruction_mismatch", base, .95, strings.Join(paths, ","),
				"Instruction files with the same basename have different checksums and require a user-reviewed merge.")
		}
	}
	packageManagers := make(map[string][]string)
	for _, path := range state.lockFiles {
		packageManagers[filepath.Base(path)] = append(packageManagers[filepath.Base(path)], path)
	}
	if len(packageManagers) > 1 {
		paths := append([]string(nil), state.lockFiles...)
		sort.Strings(paths)
		state.collector.conflict("multiple_package_managers", "node_lockfiles", .90, strings.Join(paths, ","),
			"Multiple Node.js lockfile formats were found; the canonical package manager is ambiguous.")
	}
	if state.aiKind != "" {
		inferred := firstValue(state.collector.facts, "classification", "service_kind")
		if inferred != "" && inferred != state.aiKind {
			state.collector.conflict("service_kind_mismatch", state.aiKind+" != "+inferred, .92, ".ai/service.yaml",
				"The existing .ai manifest conflicts with read-only service-kind discovery.")
		}
	}
}

func looksLikeRoute(value string) bool {
	return strings.HasPrefix(value, "/") && len(value) <= 512 && !strings.ContainsAny(value, "\n\r")
}

func isHealthRoute(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(value), "/")
	return value == "/health" || value == "/ready" || value == "/readiness" || value == "/liveness"
}

func isComposeFile(base string) bool {
	base = strings.ToLower(strings.TrimSpace(base))
	if !strings.HasSuffix(base, ".yml") && !strings.HasSuffix(base, ".yaml") {
		return false
	}
	return base == "docker-compose.yml" || base == "docker-compose.yaml" ||
		base == "compose.yml" || base == "compose.yaml" ||
		strings.HasPrefix(base, "docker-compose.") || strings.HasPrefix(base, "compose.")
}

func composeDependencies(value interface{}) []string {
	dependencies := make([]string, 0)
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			if name, ok := item.(string); ok {
				dependencies = append(dependencies, name)
			}
		}
	case map[string]interface{}:
		for name := range typed {
			dependencies = append(dependencies, name)
		}
	case map[interface{}]interface{}:
		for name := range typed {
			dependencies = append(dependencies, fmt.Sprint(name))
		}
	}
	sort.Strings(dependencies)
	return dependencies
}

func firstSource(facts []domain.Evidence, category, name, value string) string {
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name && fact.Value == value {
			return fact.SourcePath
		}
	}
	return ""
}

func firstValue(facts []domain.Evidence, category, name string) string {
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name {
			return fact.Value
		}
	}
	return ""
}

func sanitizeCommand(command string) string {
	lower := strings.ToLower(command)
	for _, marker := range []string{"token=", "token ", "password=", "password ", "secret=", "secret ", "authorization:"} {
		if strings.Contains(lower, marker) {
			return "[redacted: command contains a credential-like argument]"
		}
	}
	if len(command) > 1000 {
		return command[:1000] + "…"
	}
	return command
}

func redactURLQuery(value string) string {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		return value[:index] + "?[redacted]"
	}
	return value
}
