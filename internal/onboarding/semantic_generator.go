package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	semanticSchemaVersion = 1
	semanticGeneratorName = "stage3-semantic-v1"
	maxSemanticFacts      = 200
	maxSemanticQuestions  = 30
	maxEvidenceFileBytes  = 1 << 20
)

type SemanticGenerator struct {
	Base     Generator
	Runner   repository.AgentRunner
	Projects repository.ProjectRepository
	Model    string
}

func (g SemanticGenerator) Generate(
	ctx context.Context,
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	report domain.DiscoveryReport,
) (domain.OnboardingProposal, string, error) {
	if g.Runner == nil || project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
		return domain.OnboardingProposal{}, "", fmt.Errorf("semantic onboarding generator is incomplete: %w", domain.ErrInvalidStatus)
	}
	connectedProjects := make([]string, 0)
	if g.Projects != nil {
		projects, listErr := g.Projects.List(ctx)
		if listErr != nil {
			return domain.OnboardingProposal{}, "", fmt.Errorf("list connected projects for semantic analysis: %w", listErr)
		}
		for _, connected := range projects {
			connectedProjects = append(connectedProjects, connected.Name)
		}
		sort.Strings(connectedProjects)
	}
	prompt, err := semanticPrompt(project, snapshot, report, connectedProjects)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	threadID := ""
	request := domain.AgentRunRequest{
		Role: domain.AgentRunAnalyst, WorkingDirectory: *project.LocalPath, Model: g.Model,
		Prompt: prompt, OutputSchema: semanticOutputSchema(),
	}
	var response domain.AgentRunResponse
	for attempt := 0; attempt < 2; attempt++ {
		response, err = g.Runner.Run(ctx, request, func(_ context.Context, value string) error {
			threadID = value
			return nil
		})
		if response.ThreadID != "" {
			threadID = response.ThreadID
		}
		if err == nil {
			break
		}
		if !errors.Is(err, domain.ErrTransient) || attempt == 1 || threadID == "" {
			return domain.OnboardingProposal{}, "", fmt.Errorf("run semantic analyst: %w", err)
		}
		request.ThreadID = threadID
	}
	if threadID == "" || response.ThreadID != threadID {
		return domain.OnboardingProposal{}, "", fmt.Errorf("semantic analyst thread was not captured: %w", domain.ErrConflict)
	}
	analysis, err := validateSemanticAnalysis(*project.LocalPath, project, snapshot, response.Result, connectedProjects)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	enriched := report
	enriched.Facts = mergeSemanticFacts(report.Facts, analysis.Facts)
	proposal, _, err := g.Base.Generate(ctx, project, snapshot, enriched)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	proposal.Generator = semanticGeneratorName
	proposal.Semantic = &domain.SemanticProposalMetadata{
		AgentThreadID: threadID, ModelProfile: "deep", Analysis: analysis,
	}
	if err := g.addSemanticReport(&proposal, *project.LocalPath, analysis); err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	sortProposal(&proposal)
	proposal.Checksum, err = domain.OnboardingProposalChecksum(proposal)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	existing := make(map[string]string, len(proposal.Files))
	for _, file := range proposal.Files {
		content, _, readErr := g.Base.readExisting(*project.LocalPath, file.Path)
		if readErr != nil {
			return domain.OnboardingProposal{}, "", readErr
		}
		existing[file.Path] = content
	}
	diff, err := buildUnifiedDiff(proposal.Files, existing)
	if err != nil {
		return domain.OnboardingProposal{}, "", err
	}
	return proposal, diff, nil
}

func semanticPrompt(
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	report domain.DiscoveryReport,
	connectedProjects []string,
) (string, error) {
	roleGuidance := ""
	if isNonRuntimeSemanticRole(project.RepositoryRole) {
		roleGuidance = `
This repository has a non-runtime role (content, policy, documentation, or archive).
Use only purpose, business_rule, business_process, entity, and command facts.
Describe platform knowledge as rules or processes with the owning service named in the fact value when the source does so.
Do not emit capability, ownership, relation, contract, or infrastructure facts: those would incorrectly make this repository a runtime owner, producer, consumer, or deployment component.
`
	}
	contextPayload := struct {
		ProjectName       string                `json:"project_name"`
		RepositoryRole    domain.RepositoryRole `json:"repository_role"`
		ServiceKind       domain.ServiceKind    `json:"service_kind"`
		Commit            string                `json:"commit"`
		ConnectedProjects []string              `json:"connected_projects"`
		KnownFacts        []domain.Evidence     `json:"known_facts"`
	}{project.Name, project.RepositoryRole, snapshot.ServiceKind, snapshot.CommitSHA, connectedProjects, report.Facts}
	content, err := json.Marshal(contextPayload)
	if err != nil {
		return "", fmt.Errorf("encode semantic analyst context: %w", err)
	}
	return `You are a read-only repository analyst preparing a user-reviewed onboarding proposal.
Inspect README.md, AGENTS.md, prompts, .ai files, source code, API/event contracts, migrations, Docker, CI, and configuration examples.
Do not edit files, use the network, inspect .env or credential files, or report secret values.
Return only facts directly supported by repository text. Every fact must include an exact evidence_quote of 8 to 500 characters copied from source_path; include surrounding source context when the value itself is shorter.
Use confidence between 0.50 and 0.95. Put ambiguity in open_questions instead of guessing.
Runtime capability, ownership, relation, contract, and infrastructure facts must come from production code or authoritative runtime documentation, never tests, fixtures, examples, or testdata.
Database-table ownership must use a checked-in .sql source; models, ORM tags, and prose may describe entities but cannot establish schema ownership.
AGENTS.md and prompts are instructions, not runtime topology evidence. They may support working rules, but not capability, ownership, relation, contract, or infrastructure facts. A command documented only in README.md or AGENTS.md is valid only when the repository contains the corresponding runtime or command manifest (for example go.mod for go, Makefile for make, Taskfile for task, package.json for npm, or a Compose manifest for docker compose).
Use these category/name conventions when applicable:
- purpose: summary
- capability: business capability or http_route
- ownership: domain_entity or database_table
- relation: depends_on, gateway_routes_to, frontend_consumes, authenticates_through, stores_in, deploys
- contract: http_produce, http_consume, event_publish, event_subscribe, http_definition, grpc_definition, database_schema
- business_rule: stable rule identifier
- business_process: stable process identifier
- entity: domain entity identifier
- infrastructure: dependency identifier
- command: stable command name; value must be the exact developer-facing command documented in Makefile, Taskfile, package/pyproject/composer manifest, README, or AGENTS.md; never classify a Dockerfile RUN or CI step as a local command
Keep values concise, use repository-relative paths, return at most 200 facts and 30 open questions.
For relation facts, value must be one exact name from connected_projects and repository text must identify that project. Networks, containers, platforms, libraries, URLs, and the current project are infrastructure facts, not relations.
authenticates_through means the current project delegates authentication or token/JWT verification to the target; a caller allowlist or permission check is not authentication delegation.
Do not infer runtime relations from Makefile, Taskfile, or package-manager command manifests. Use gateway_routes_to only when the current repository is the gateway, and frontend_consumes only when it is a frontend.
Never use .ai/discovery/semantic-report.json itself as evidence.
For an open question with no specific source file, return an empty source_paths array; never use "." as a path.
` + roleGuidance + `

Deterministic discovery context:
` + string(content), nil
}

func validateSemanticAnalysis(
	root string,
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	content []byte,
	connectedProjects []string,
) (domain.SemanticAnalysis, error) {
	var result struct {
		Summary       string                        `json:"summary"`
		Facts         []domain.SemanticFact         `json:"facts"`
		OpenQuestions []domain.SemanticOpenQuestion `json:"open_questions"`
	}
	if len(content) == 0 || len(content) > 512<<10 || !json.Valid(content) {
		return domain.SemanticAnalysis{}, fmt.Errorf("semantic result is not bounded valid JSON: %w", domain.ErrValidation)
	}
	if err := json.Unmarshal(content, &result); err != nil {
		return domain.SemanticAnalysis{}, fmt.Errorf("decode semantic result: %w", err)
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" || len(result.Summary) > 4000 || len(result.Facts) > maxSemanticFacts ||
		len(result.OpenQuestions) > maxSemanticQuestions {
		return domain.SemanticAnalysis{}, fmt.Errorf("semantic result exceeds content limits: %w", domain.ErrValidation)
	}
	allowedCategories := map[string]struct{}{
		"purpose": {}, "capability": {}, "ownership": {}, "relation": {}, "contract": {},
		"business_rule": {}, "business_process": {}, "entity": {}, "infrastructure": {}, "command": {},
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return domain.SemanticAnalysis{}, fmt.Errorf("resolve semantic repository root: %w", err)
	}
	seen := make(map[string]struct{}, len(result.Facts))
	connectedNames := make(map[string]struct{}, len(connectedProjects))
	for _, name := range connectedProjects {
		connectedNames[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	validated := make([]domain.SemanticFact, 0, len(result.Facts))
	rejected := make([]domain.SemanticRejectedFact, 0)
	for _, fact := range result.Facts {
		fact.Category = strings.TrimSpace(fact.Category)
		fact.Name = strings.TrimSpace(fact.Name)
		fact.Value = strings.TrimSpace(fact.Value)
		fact.SourcePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(fact.SourcePath))))
		fact.EvidenceQuote = strings.TrimSpace(fact.EvidenceQuote)
		fact.Explanation = strings.TrimSpace(fact.Explanation)
		if _, exists := allowedCategories[fact.Category]; !exists || fact.Name == "" || fact.Value == "" ||
			strings.Contains(fact.SourcePath, "\\") || fact.SourcePath == ".ai/discovery/semantic-report.json" ||
			len(fact.Name) > 128 || len(fact.Value) > 1000 || len(fact.Explanation) > 2000 ||
			fact.Confidence < .5 || fact.Confidence > .95 {
			return domain.SemanticAnalysis{}, fmt.Errorf("invalid semantic fact %s/%s: %w", fact.Category, fact.Name, domain.ErrValidation)
		}
		if isNonRuntimeSemanticRole(project.RepositoryRole) && isRuntimeSemanticCategory(fact.Category) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "runtime_category_not_allowed_for_repository_role",
			})
			continue
		}
		if isRuntimeSemanticCategory(fact.Category) && isNonProductionSemanticEvidencePath(fact.SourcePath) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "non_production_evidence_not_allowed_for_runtime_category",
			})
			continue
		}
		if isRuntimeSemanticCategory(fact.Category) && isInstructionSemanticEvidencePath(fact.SourcePath) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "runtime_category_not_allowed_from_instruction_source",
			})
			continue
		}
		if fact.Category == "ownership" && fact.Name == "database_table" &&
			!strings.HasSuffix(strings.ToLower(fact.SourcePath), ".sql") {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "database_ownership_requires_sql_source",
			})
			continue
		}
		if fact.Category == "relation" && isOperationalSemanticRelationSource(fact.SourcePath) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "relation_source_is_operational_manifest",
			})
			continue
		}
		if fact.Category == "relation" && !semanticRelationAllowedForSource(fact.Name, project, snapshot) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "relation_type_not_allowed_for_source_kind",
			})
			continue
		}
		if fact.Category == "relation" && !semanticRelationEvidenceSupportsName(fact) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "relation_evidence_does_not_support_relation_type",
			})
			continue
		}
		if fact.Category == "command" && (containsCredentialLikeCommand(fact.Value) || !isSemanticCommandSource(fact.SourcePath)) {
			reason := "command_source_not_approved"
			if containsCredentialLikeCommand(fact.Value) {
				reason = "credential_like_command_not_allowed"
			}
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath, Reason: reason,
			})
			continue
		}
		if fact.Category == "command" && !semanticCommandPathExists(root, fact.Value) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "command_path_not_found_from_repository_root",
			})
			continue
		}
		if fact.Category == "command" && !semanticInstructionCommandSupported(root, fact.SourcePath, fact.Value) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "instruction_command_has_no_repository_manifest",
			})
			continue
		}
		if fact.Category == "relation" && isSelfSemanticRelation(fact.Value, project.Name) {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "self_relation_not_allowed",
			})
			continue
		}
		if fact.Category == "relation" && len(connectedNames) > 0 {
			if _, exists := connectedNames[strings.ToLower(fact.Value)]; !exists {
				rejected = append(rejected, domain.SemanticRejectedFact{
					Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
					Reason: "relation_target_not_connected",
				})
				continue
			}
		}
		if err := verifyEvidenceQuote(root, fact.SourcePath, fact.EvidenceQuote); err != nil {
			rejected = append(rejected, domain.SemanticRejectedFact{
				Category: fact.Category, Name: fact.Name, SourcePath: fact.SourcePath,
				Reason: "evidence_quote_not_verified_against_current_source",
			})
			continue
		}
		key := fact.Category + "\x00" + fact.Name + "\x00" + fact.Value + "\x00" + fact.SourcePath
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		validated = append(validated, fact)
	}
	validatedQuestions := make([]domain.SemanticOpenQuestion, 0, len(result.OpenQuestions))
	for _, question := range result.OpenQuestions {
		question.Question = strings.TrimSpace(question.Question)
		question.Reason = strings.TrimSpace(question.Reason)
		if question.Question == "" || question.Reason == "" ||
			len(question.Question) > 1000 || len(question.Reason) > 2000 {
			return domain.SemanticAnalysis{}, fmt.Errorf("invalid semantic open question: %w", domain.ErrValidation)
		}
		paths := make([]string, 0, len(question.SourcePaths))
		for _, path := range question.SourcePaths {
			path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
			if path == "." {
				continue
			}
			if strings.Contains(path, "\\") || path == ".ai/discovery/semantic-report.json" {
				return domain.SemanticAnalysis{}, fmt.Errorf("invalid semantic question path: %w", domain.ErrValidation)
			}
			if _, err := resolveSemanticEvidencePath(root, path); err != nil {
				return domain.SemanticAnalysis{}, fmt.Errorf("invalid semantic question path: %w", err)
			}
			paths = append(paths, path)
		}
		question.SourcePaths = uniqueSorted(paths)
		validatedQuestions = append(validatedQuestions, question)
	}
	sort.Slice(validated, func(i, j int) bool {
		return validated[i].Category+validated[i].Name+validated[i].Value+validated[i].SourcePath <
			validated[j].Category+validated[j].Name+validated[j].Value+validated[j].SourcePath
	})
	sort.Slice(rejected, func(i, j int) bool {
		return rejected[i].Category+rejected[i].Name+rejected[i].SourcePath <
			rejected[j].Category+rejected[j].Name+rejected[j].SourcePath
	})
	return domain.SemanticAnalysis{
		SchemaVersion: semanticSchemaVersion, ProjectID: project.ID, ProjectName: project.Name,
		BaseCommit: snapshot.CommitSHA, Summary: result.Summary, Facts: validated, RejectedFacts: rejected,
		OpenQuestions: validatedQuestions,
	}, nil
}

func isNonRuntimeSemanticRole(role domain.RepositoryRole) bool {
	switch role {
	case domain.RepositoryRoleContent, domain.RepositoryRolePolicy,
		domain.RepositoryRoleDocumentation, domain.RepositoryRoleArchive:
		return true
	default:
		return false
	}
}

func isRuntimeSemanticCategory(category string) bool {
	switch category {
	case "capability", "ownership", "relation", "contract", "infrastructure":
		return true
	default:
		return false
	}
}

func isNonProductionSemanticEvidencePath(path string) bool {
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

func isOperationalSemanticRelationSource(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(filepath.Clean(path))))
	return base == "makefile" || base == "taskfile.yml" || base == "taskfile.yaml" ||
		base == "package.json" || base == "pyproject.toml" || base == "composer.json"
}

func isInstructionSemanticEvidencePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	return filepath.Base(path) == "agents.md" || strings.HasPrefix(path, "prompts/") ||
		strings.Contains(path, "/prompts/")
}

func semanticRelationAllowedForSource(
	name string,
	project domain.Project,
	snapshot domain.ServiceSnapshot,
) bool {
	switch name {
	case "gateway_routes_to":
		return snapshot.ServiceKind == domain.ServiceKindGateway
	case "frontend_consumes":
		return project.RepositoryRole == domain.RepositoryRoleFrontend ||
			snapshot.ServiceKind == domain.ServiceKindFrontendApplication
	default:
		return true
	}
}

func semanticRelationEvidenceSupportsName(fact domain.SemanticFact) bool {
	evidence := strings.ToLower(fact.EvidenceQuote + " " + fact.Explanation)
	for _, marker := range []string{"allowedservices", "allowed services", "allowed caller", "allowlisted caller"} {
		if strings.Contains(evidence, marker) {
			return false
		}
	}
	if fact.Name != "authenticates_through" {
		return true
	}
	for _, marker := range []string{"authenticat", "jwt", "verify token", "token verification", "bearer token"} {
		if strings.Contains(evidence, marker) {
			return true
		}
	}
	return false
}

func verifyEvidenceQuote(root, path, quote string) error {
	if len(quote) < 8 || len(quote) > 500 {
		return fmt.Errorf("evidence quote length is invalid: %w", domain.ErrValidation)
	}
	resolved, err := resolveSemanticEvidencePath(root, path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxEvidenceFileBytes {
		return fmt.Errorf("semantic evidence file is unavailable or too large: %w", domain.ErrValidation)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read semantic evidence: %w", err)
	}
	if !strings.Contains(normalizedEvidence(string(content)), normalizedEvidence(quote)) {
		return fmt.Errorf("evidence quote is not present in %s: %w", path, domain.ErrValidation)
	}
	return nil
}

func resolveSemanticEvidencePath(root, value string) (string, error) {
	value = strings.TrimSpace(value)
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if value == "" || filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe semantic evidence path %q: %w", value, domain.ErrWriteScope)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, cleaned))
	if err != nil {
		return "", fmt.Errorf("resolve semantic evidence path %q: %w", value, err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("semantic evidence escaped repository: %w", domain.ErrForbidden)
	}
	return resolved, nil
}

func normalizedEvidence(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func mergeSemanticFacts(existing []domain.Evidence, semantic []domain.SemanticFact) []domain.Evidence {
	result := append([]domain.Evidence(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(semantic))
	for _, fact := range result {
		seen[fact.Category+"\x00"+fact.Name+"\x00"+fact.Value+"\x00"+fact.SourcePath] = struct{}{}
	}
	for _, fact := range semantic {
		key := fact.Category + "\x00" + fact.Name + "\x00" + fact.Value + "\x00" + fact.SourcePath
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, domain.Evidence{
			Category: fact.Category, Name: fact.Name, Value: fact.Value, Confidence: fact.Confidence,
			SourcePath:  fact.SourcePath,
			Explanation: "Semantic proposal with a verified repository quote. " + fact.Explanation,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Category+result[i].Name+result[i].Value+result[i].SourcePath <
			result[j].Category+result[j].Name+result[j].Value+result[j].SourcePath
	})
	return result
}

func (g SemanticGenerator) addSemanticReport(
	proposal *domain.OnboardingProposal,
	root string,
	analysis domain.SemanticAnalysis,
) error {
	content, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal semantic report: %w", err)
	}
	text := normalizeText(string(content))
	if int64(len(text)) > g.Base.config.MaxFileBytes {
		return fmt.Errorf("semantic report exceeds configured size: %w", domain.ErrValidation)
	}
	path := ".ai/discovery/semantic-report.json"
	existing, exists, err := g.Base.readExisting(root, path)
	if err != nil {
		return err
	}
	action := domain.ProposalFileCreate
	if exists {
		action = domain.ProposalFileUpdate
		if normalizeText(existing) == text {
			action = domain.ProposalFileUnchanged
		} else {
			proposal.Conflicts = append(proposal.Conflicts, domain.OnboardingConflict{
				Path: path, Field: "semantic_report", Existing: "existing semantic report",
				Discovered:  "new evidence-backed semantic proposal",
				Explanation: "The semantic report changed and requires explicit onboarding approval.",
			})
		}
	}
	evidencePaths := make([]string, 0, len(analysis.Facts))
	for _, fact := range analysis.Facts {
		evidencePaths = append(evidencePaths, fact.SourcePath)
	}
	proposal.Files = append(proposal.Files, domain.ProposedFile{
		Path: path, Content: text, Action: action, Checksum: contentChecksum(text),
		Explanation:   "Store user-reviewable semantic facts backed by exact repository quotes.",
		EvidencePaths: uniqueSorted(evidencePaths),
	})
	var total int64
	for _, file := range proposal.Files {
		total += int64(len(file.Content))
	}
	if total > g.Base.config.MaxTotalBytes {
		return fmt.Errorf("semantic onboarding proposal exceeds configured size: %w", domain.ErrValidation)
	}
	return nil
}

func semanticOutputSchema() map[string]any {
	stringField := func(maxLength int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength}
	}
	fact := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"category", "name", "value", "confidence", "source_path", "evidence_quote", "explanation"},
		"properties": map[string]any{
			"category": map[string]any{"type": "string", "enum": []string{
				"purpose", "capability", "ownership", "relation", "contract",
				"business_rule", "business_process", "entity", "infrastructure", "command",
			}},
			"name": stringField(128), "value": stringField(1000),
			"confidence":  map[string]any{"type": "number", "minimum": .5, "maximum": .95},
			"source_path": stringField(1000),
			"evidence_quote": map[string]any{
				"type": "string", "minLength": 8, "maxLength": 500,
			},
			"explanation": stringField(2000),
		},
	}
	question := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"question", "reason", "source_paths"},
		"properties": map[string]any{
			"question": stringField(1000), "reason": stringField(2000),
			"source_paths": map[string]any{"type": "array", "maxItems": 20, "items": stringField(1000)},
		},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"summary", "facts", "open_questions"},
		"properties": map[string]any{
			"summary":        stringField(4000),
			"facts":          map[string]any{"type": "array", "maxItems": maxSemanticFacts, "items": fact},
			"open_questions": map[string]any{"type": "array", "maxItems": maxSemanticQuestions, "items": question},
		},
	}
}

func containsCredentialLikeCommand(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"token=", "token ", "password=", "password ", "secret=", "secret ", "authorization:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func semanticCommandPathExists(root, value string) bool {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "./") {
		return true
	}
	relative := strings.TrimPrefix(fields[0], "./")
	path, err := resolveSemanticEvidencePath(root, relative)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func semanticInstructionCommandSupported(root, sourcePath, value string) bool {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(sourcePath))))
	if base != "readme.md" && base != "agents.md" {
		return true
	}
	fields := strings.Fields(strings.TrimSpace(value))
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "./") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	has := func(paths ...string) bool {
		for _, path := range paths {
			if info, err := os.Stat(filepath.Join(root, path)); err == nil && !info.IsDir() {
				return true
			}
		}
		return false
	}
	switch fields[0] {
	case "go", "gofmt", "goimports", "golangci-lint":
		return has("go.mod", "go.work")
	case "make", "gmake":
		return has("Makefile", "makefile", "GNUmakefile")
	case "task":
		return has("Taskfile.yml", "Taskfile.yaml", "taskfile.yml", "taskfile.yaml")
	case "npm", "npx", "node", "pnpm", "yarn", "bun":
		return has("package.json")
	case "docker-compose":
		return has("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")
	case "docker":
		if len(fields) > 1 && fields[1] == "compose" {
			return has("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")
		}
		return has("Dockerfile", "dockerfile")
	case "python", "python3", "pytest", "pip", "pip3", "poetry", "uv":
		return has("pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile")
	case "php", "composer":
		return has("composer.json")
	case "buf":
		return has("buf.yaml", "buf.work.yaml", "buf.gen.yaml")
	case "helm":
		return has("Chart.yaml")
	case "terraform", "tofu":
		matches, _ := filepath.Glob(filepath.Join(root, "*.tf"))
		return len(matches) > 0
	default:
		return strings.HasPrefix(fields[0], "./") && semanticCommandPathExists(root, value)
	}
}

func isSemanticCommandSource(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	base := filepath.Base(path)
	switch base {
	case "makefile", "taskfile.yml", "taskfile.yaml", "package.json", "pyproject.toml", "composer.json", "readme.md", "agents.md":
		return true
	default:
		return false
	}
}

func isSelfSemanticRelation(value, projectName string) bool {
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

var _ repository.OnboardingGenerator = SemanticGenerator{}
