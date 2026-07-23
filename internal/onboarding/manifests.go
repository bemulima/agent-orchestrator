package onboarding

import (
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type manifestFact struct {
	Name        string  `yaml:"name" json:"name"`
	Value       string  `yaml:"value" json:"value"`
	Confidence  float64 `yaml:"confidence" json:"confidence"`
	SourcePath  string  `yaml:"source_path" json:"source_path"`
	Explanation string  `yaml:"explanation" json:"explanation"`
}

type serviceRepository struct {
	GitURL        string `yaml:"git_url,omitempty"`
	LocalPath     string `yaml:"local_path,omitempty"`
	DefaultBranch string `yaml:"default_branch"`
	Commit        string `yaml:"commit"`
}

type discoveryMetadata struct {
	SnapshotID      string `yaml:"snapshot_id"`
	Commit          string `yaml:"commit"`
	Branch          string `yaml:"branch"`
	Dirty           bool   `yaml:"dirty"`
	ContentChecksum string `yaml:"content_checksum"`
}

type serviceManifest struct {
	SchemaVersion     int                   `yaml:"schema_version"`
	Name              string                `yaml:"name"`
	ServiceKind       domain.ServiceKind    `yaml:"service_kind"`
	RepositoryRole    domain.RepositoryRole `yaml:"repository_role"`
	Repository        serviceRepository     `yaml:"repository"`
	Purpose           string                `yaml:"purpose,omitempty"`
	Stack             []manifestFact        `yaml:"stack,omitempty"`
	Capabilities      []manifestFact        `yaml:"capabilities,omitempty"`
	Ownership         []manifestFact        `yaml:"ownership,omitempty"`
	Dependencies      []manifestFact        `yaml:"dependencies,omitempty"`
	Contracts         []manifestFact        `yaml:"contracts,omitempty"`
	Gateway           []manifestFact        `yaml:"gateway,omitempty"`
	Frontends         []manifestFact        `yaml:"frontends,omitempty"`
	Infrastructure    []manifestFact        `yaml:"infrastructure,omitempty"`
	BusinessRules     []manifestFact        `yaml:"business_rules,omitempty"`
	BusinessProcesses []manifestFact        `yaml:"business_processes,omitempty"`
	Entities          []manifestFact        `yaml:"entities,omitempty"`
	Instructions      []string              `yaml:"instructions,omitempty"`
	Commands          []string              `yaml:"commands,omitempty"`
	Discovery         discoveryMetadata     `yaml:"discovery"`
}

func buildServiceManifest(project domain.Project, snapshot domain.ServiceSnapshot, report domain.DiscoveryReport) serviceManifest {
	localPath := ""
	if project.LocalPath != nil {
		localPath = "."
	}
	gitURL := ""
	if project.GitURL != nil {
		gitURL = *project.GitURL
		localPath = ""
	}
	return serviceManifest{
		SchemaVersion:     1,
		Name:              project.Name,
		ServiceKind:       snapshot.ServiceKind,
		RepositoryRole:    project.RepositoryRole,
		Repository:        serviceRepository{GitURL: gitURL, LocalPath: localPath, DefaultBranch: project.DefaultBranch, Commit: snapshot.CommitSHA},
		Purpose:           snapshot.Purpose,
		Stack:             manifestFacts(report.Facts, "stack"),
		Capabilities:      manifestFacts(report.Facts, "capability"),
		Ownership:         manifestFacts(report.Facts, "ownership"),
		Dependencies:      manifestFacts(report.Facts, "relation", "depends_on"),
		Contracts:         manifestFacts(report.Facts, "contract"),
		Gateway:           manifestFacts(report.Facts, "relation", "gateway_routes_to"),
		Frontends:         manifestFacts(report.Facts, "relation", "frontend_consumes"),
		Infrastructure:    manifestFacts(report.Facts, "infrastructure"),
		BusinessRules:     manifestFacts(report.Facts, "business_rule"),
		BusinessProcesses: manifestFacts(report.Facts, "business_process"),
		Entities:          manifestFacts(report.Facts, "entity"),
		Instructions:      instructionPaths(report),
		Commands:          commandNames(report),
		Discovery: discoveryMetadata{SnapshotID: snapshot.ID, Commit: snapshot.CommitSHA, Branch: snapshot.Branch,
			Dirty: snapshot.IsDirty, ContentChecksum: snapshot.ContentChecksum},
	}
}

type architectureManifest struct {
	SchemaVersion  int            `yaml:"schema_version"`
	Stack          []manifestFact `yaml:"stack,omitempty"`
	Ownership      []manifestFact `yaml:"ownership,omitempty"`
	Relations      []manifestFact `yaml:"relations,omitempty"`
	Infrastructure []manifestFact `yaml:"infrastructure,omitempty"`
}

func buildArchitectureManifest(report domain.DiscoveryReport) architectureManifest {
	return architectureManifest{SchemaVersion: 1, Stack: manifestFacts(report.Facts, "stack"),
		Ownership: manifestFacts(report.Facts, "ownership"), Relations: manifestFacts(report.Facts, "relation"),
		Infrastructure: manifestFacts(report.Facts, "infrastructure")}
}

type commandEntry struct {
	Name             string  `yaml:"name"`
	Run              string  `yaml:"run"`
	SourcePath       string  `yaml:"source_path"`
	Confidence       float64 `yaml:"confidence"`
	RequiresApproval bool    `yaml:"requires_approval"`
	Risk             string  `yaml:"risk"`
}

type commandsManifest struct {
	SchemaVersion int            `yaml:"schema_version"`
	Commands      []commandEntry `yaml:"commands,omitempty"`
}

func buildCommandsManifest(report domain.DiscoveryReport) commandsManifest {
	commands := make([]commandEntry, 0)
	for _, fact := range report.Facts {
		if fact.Category != "command" {
			continue
		}
		entry := commandEntry{Name: fact.Name, SourcePath: fact.SourcePath, Confidence: fact.Confidence}
		if fact.Name == "make_target" {
			entry.Name = fact.Value
			entry.Run = "make " + fact.Value
		} else if strings.HasSuffix(fact.SourcePath, "package.json") {
			entry.Run = "npm run " + fact.Name
		} else {
			entry.Run = fact.Value
		}
		entry.RequiresApproval, entry.Risk = classifyCommandRisk(entry.Name, entry.Run)
		commands = append(commands, entry)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commandsManifest{SchemaVersion: 1, Commands: commands}
}

type contractManifest struct {
	SchemaVersion int            `yaml:"schema_version"`
	Contracts     []manifestFact `yaml:"contracts"`
	EvidencePaths []string       `yaml:"-"`
}

func buildContractManifests(report domain.DiscoveryReport) map[string]contractManifest {
	result := make(map[string]contractManifest)
	httpFacts := append(manifestFacts(report.Facts, "contract", "http_definition"), manifestFacts(report.Facts, "capability", "http_route")...)
	if len(httpFacts) > 0 {
		result[".ai/contracts/http.yaml"] = contractManifest{SchemaVersion: 1, Contracts: httpFacts, EvidencePaths: pathsFromManifestFacts(httpFacts)}
	}
	eventFacts := manifestFacts(report.Facts, "capability", "event_subject")
	if len(eventFacts) > 0 {
		result[".ai/contracts/events.yaml"] = contractManifest{SchemaVersion: 1, Contracts: eventFacts, EvidencePaths: pathsFromManifestFacts(eventFacts)}
	}
	databaseFacts := append(manifestFacts(report.Facts, "contract", "database_schema"), manifestFacts(report.Facts, "ownership", "database_table")...)
	if len(databaseFacts) > 0 {
		result[".ai/contracts/database.yaml"] = contractManifest{SchemaVersion: 1, Contracts: databaseFacts, EvidencePaths: pathsFromManifestFacts(databaseFacts)}
	}
	return result
}

type workflowManifest struct {
	SchemaVersion    int      `yaml:"schema_version"`
	Name             string   `yaml:"name"`
	RequiresApproval bool     `yaml:"requires_approval"`
	Steps            []string `yaml:"steps"`
}

func buildTestWorkflow(commands commandsManifest) workflowManifest {
	steps := make([]string, 0, len(commands.Commands))
	for _, command := range commands.Commands {
		if command.RequiresApproval {
			continue
		}
		lower := strings.ToLower(command.Name)
		if strings.Contains(lower, "test") || strings.Contains(lower, "lint") || strings.Contains(lower, "verify") ||
			strings.Contains(lower, "validate") || strings.Contains(lower, "check") {
			steps = append(steps, command.Run)
		}
	}
	if len(steps) == 0 {
		for _, command := range commands.Commands {
			if command.RequiresApproval {
				continue
			}
			steps = append(steps, command.Run)
		}
	}
	if len(steps) == 0 {
		steps = append(steps, "request owner approval before running repository commands")
	}
	return workflowManifest{SchemaVersion: 1, Name: "test", RequiresApproval: false, Steps: uniqueSorted(steps)}
}

func buildFeatureWorkflow() workflowManifest {
	return workflowManifest{SchemaVersion: 1, Name: "implement-feature", RequiresApproval: true,
		Steps: []string{"read .ai/service.yaml and linked instructions", "implement within approved write scope", "run .ai/workflows/test.yaml when present", "request independent review"}}
}

func buildContractWorkflow() workflowManifest {
	return workflowManifest{SchemaVersion: 1, Name: "change-contract", RequiresApproval: true,
		Steps: []string{"identify producers and consumers", "update versioned contract evidence", "check compatibility and drift", "run repository verification", "request independent review"}}
}

func agentsManagedBlock() string {
	return "## Agent Orchestrator (Managed)\n\nRead `.ai/service.yaml` before repository work. Follow every linked repository instruction and prompt. Do not edit files outside an explicitly approved write scope."
}

func reviewerAgent() string {
	return "# Reviewer\n\nReview the real Git diff independently. Verify write scope, discovered commands, contracts, migrations, and evidence-backed acceptance criteria. Do not reuse the coder thread."
}

func backendAgent(hasCommands bool) string {
	verification := "No repository commands have been approved. Do not invent or run project commands until the owner supplies evidence-backed commands."
	if hasCommands {
		verification = "Run only commands listed in `.ai/commands.yaml` with `requires_approval: false`. Request explicit owner approval before any command marked `requires_approval: true`."
	}
	return "# Backend Coder\n\nRead `.ai/service.yaml`, linked instructions, and relevant contracts before implementation. Keep domain, use-case, and adapter boundaries intact. " + verification
}

func classifyCommandRisk(name, command string) (bool, string) {
	name = strings.ToLower(strings.TrimSpace(name))
	command = strings.ToLower(strings.TrimSpace(command))
	if strings.HasPrefix(name, "pre") || strings.HasPrefix(name, "post") ||
		strings.Contains(name, ":ui") || strings.Contains(command, " --ui") ||
		strings.Contains(name, "watch") || strings.Contains(command, "--watch") {
		return true, "lifecycle"
	}
	if strings.Contains(command, "../") || strings.Contains(command, `..\`) {
		return true, "state_change"
	}
	padded := " " + command + " "
	for _, marker := range []string{" rm ", "rm -"} {
		if strings.Contains(padded, marker) {
			return true, "state_change"
		}
	}
	for _, marker := range []string{
		"delete", "destroy", "cleanup", "clean-up", "drop ", "truncate ",
		"reset", "rollback", "migrate", "migration", "create", "import", "insert", "seed",
		"compose down", " stop", "kill",
		"deploy", "publish", "release", "git push", "docker push", "kubectl", "helm ", "terraform",
		"curl ", "wget ", "sudo ", "go fmt ", "gofmt -w", "format",
	} {
		if strings.Contains(padded, marker) || strings.Contains(name, strings.TrimSpace(marker)) {
			return true, "state_change"
		}
	}
	if strings.HasPrefix(command, "docker ") || strings.HasPrefix(command, "docker-compose ") {
		return true, "external_runtime"
	}
	if strings.Contains(name, "integration") || strings.Contains(command, "integration") {
		return true, "external_runtime"
	}
	for _, marker := range []string{"test", "lint", "verify", "validate", "check", "vet", "build", "help"} {
		if strings.Contains(name, marker) {
			return false, "verification"
		}
	}
	return true, "lifecycle"
}

func migrationAgent() string {
	return "# Migration Agent\n\nTreat migrations as versioned contracts. Provide a reversible migration, preserve existing data, validate ordering, and run the repository's discovered migration checks."
}

func isBackendKind(kind domain.ServiceKind) bool {
	switch kind {
	case domain.ServiceKindBackendService, domain.ServiceKindGateway, domain.ServiceKindBackgroundWorker,
		domain.ServiceKindAIService, domain.ServiceKindStorageService:
		return true
	default:
		return false
	}
}

func manifestFacts(facts []domain.Evidence, filters ...string) []manifestFact {
	result := make([]manifestFact, 0)
	for _, fact := range facts {
		if len(filters) > 0 && fact.Category != filters[0] {
			continue
		}
		if len(filters) > 1 && fact.Name != filters[1] {
			continue
		}
		result = append(result, manifestFact{Name: fact.Name, Value: fact.Value, Confidence: fact.Confidence,
			SourcePath: fact.SourcePath, Explanation: fact.Explanation})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name+result[i].Value+result[i].SourcePath < result[j].Name+result[j].Value+result[j].SourcePath
	})
	return result
}

func hasFact(facts []domain.Evidence, category, name string) bool {
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name {
			return true
		}
	}
	return false
}

func factPaths(facts []domain.Evidence, category, name string) []string {
	paths := make([]string, 0)
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name {
			paths = append(paths, fact.SourcePath)
		}
	}
	return uniqueSorted(paths)
}

func evidencePaths(facts []domain.Evidence) []string {
	paths := make([]string, 0, len(facts))
	for _, fact := range facts {
		paths = append(paths, fact.SourcePath)
	}
	return uniqueSorted(paths)
}

func instructionPaths(report domain.DiscoveryReport) []string {
	return factPaths(report.Facts, "instruction", "instruction_file")
}

func commandEvidencePaths(report domain.DiscoveryReport) []string {
	paths := make([]string, 0)
	for _, fact := range report.Facts {
		if fact.Category == "command" {
			paths = append(paths, fact.SourcePath)
		}
	}
	return uniqueSorted(paths)
}

func commandNames(report domain.DiscoveryReport) []string {
	commands := buildCommandsManifest(report)
	names := make([]string, 0, len(commands.Commands))
	for _, command := range commands.Commands {
		names = append(names, command.Name)
	}
	return uniqueSorted(names)
}

func architectureEvidencePaths(report domain.DiscoveryReport) []string {
	paths := make([]string, 0)
	for _, fact := range report.Facts {
		if fact.Category == "stack" || fact.Category == "ownership" || fact.Category == "relation" || fact.Category == "infrastructure" {
			paths = append(paths, fact.SourcePath)
		}
	}
	return uniqueSorted(paths)
}

func contractEvidencePaths(report domain.DiscoveryReport) []string {
	paths := make([]string, 0)
	for _, manifest := range buildContractManifests(report) {
		paths = append(paths, manifest.EvidencePaths...)
	}
	return uniqueSorted(paths)
}

func pathsFromManifestFacts(facts []manifestFact) []string {
	paths := make([]string, 0, len(facts))
	for _, fact := range facts {
		paths = append(paths, fact.SourcePath)
	}
	return uniqueSorted(paths)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
