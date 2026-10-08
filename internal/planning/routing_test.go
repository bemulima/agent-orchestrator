package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestProfileResolutionGoNextVariantsAndUnsupported(t *testing.T) {
	catalog := plannerControlPlane(t)
	tests := []struct {
		name     string
		files    map[string]string
		profile  string
		variant  string
		resolved bool
	}{
		{
			name: "go canonical",
			files: canonicalGoFixtureFiles(map[string]string{
				"go.mod":                       "module example.test/service\n\ngo 1.24\n",
				"internal/usecase/checkout.go": "package usecase\n\ntype Checkout struct{}\n",
			}),
			profile: "go.canonical", resolved: true,
		},
		{
			name: "course-dev-orchestrator shape is not canonical by language alone",
			files: map[string]string{
				"go.mod":                              "module example.test/orchestrator\n\ngo 1.24\n",
				"internal/domain/model.go":            "package domain\n",
				"internal/usecase/service.go":         "package usecase\n",
				"internal/adapters/http/router.go":    "package http\n",
				"internal/adapters/postgres/store.go": "package postgres\n",
			},
		},
		{
			name: "ms-go-task-source shape lacks the canonical usecase layer",
			files: map[string]string{
				"go.mod":                                    "module example.test/task-source\n\ngo 1.24\n",
				"internal/domain/model.go":                  "package domain\n",
				"internal/transport/httpapi/handler.go":     "package httpapi\n",
				"internal/infrastructure/postgres/store.go": "package postgres\n",
			},
		},
		{
			name: "next student",
			files: map[string]string{
				"package.json":             "{\"dependencies\":{\"next\":\"15.0.0\"}}",
				"src/app/student/page.tsx": "export default function Page() { return null }",
			},
			profile: "nextjs.common", variant: "student", resolved: true,
		},
		{
			name: "next admin",
			files: map[string]string{
				"package.json":           "{\"dependencies\":{\"next\":\"15.0.0\"}}",
				"src/app/admin/page.tsx": "export default function Page() { return null }",
			},
			profile: "nextjs.common", variant: "admin", resolved: true,
		},
		{
			name:     "unsupported",
			files:    map[string]string{"README.md": "a repository without a supported stack"},
			resolved: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFiles(t, root, test.files)
			inventory, err := indexRepository(root, "project")
			if err != nil {
				t.Fatal(err)
			}
			resolution, _, err := resolveArchitectureProfile("student settings", "project", inventory, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if (resolution.Status == domain.ProfileResolutionResolved) != test.resolved ||
				resolution.ProfileID != test.profile || resolution.Variant != test.variant {
				t.Fatalf("resolution = %#v", resolution)
			}
			if !test.resolved && strings.TrimSpace(resolution.Reason) == "" {
				t.Fatal("unsupported repository did not retain an unresolved reason")
			}
		})
	}
}

func TestRoutingSelectsEvidenceBackedGoRoutes(t *testing.T) {
	catalog := plannerControlPlane(t)
	root := t.TempDir()
	writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{
		"go.mod":                                   "module example.test/service\n\ngo 1.24\n",
		"internal/domain/order.go":                 "package domain\n\ntype Order struct{}\n",
		"internal/usecase/checkout.go":             "package usecase\n\ntype Checkout struct{}\n",
		"internal/adapters/http/handler.go":        "package http\n\nfunc Handle() {}\n",
		"internal/adapters/postgres/repository.go": "package postgres\n\nfunc Query() {}\n",
		"internal/adapters/client/client.go":       "package client\n\nfunc Call() {}\n",
		"internal/adapters/messaging/publisher.go": "package messaging\n\nfunc Publish() {}\n",
	}))
	projectPath := root
	project := domain.Project{ID: "project", LocalPath: &projectPath}
	cases := []struct {
		request string
		route   string
		concern string
	}{
		{"SQL query is slow", "backend.infrastructure.persistence", "persistence"},
		{"business process validation", "backend.usecase", "business-process"},
		{"domain invariant is wrong", "backend.domain", "domain-invariant"},
		{"HTTP handler response mapping", "backend.transport.http", "http-transport"},
		{"external client timeout", "backend.infrastructure.client", "external-client"},
		{"messaging publisher retry", "backend.infrastructure.messaging", "messaging"},
	}
	for _, test := range cases {
		t.Run(test.route, func(t *testing.T) {
			output := domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}
			routing, _, _, err := buildRoutingMetadata(test.request, output, []domain.Project{project}, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if len(routing.Routes) != 1 || routing.Routes[0].RouteID != test.route ||
				routing.Classification != test.concern || routing.Status != domain.RoutingStatusResolved {
				t.Fatalf("routing = %#v", routing)
			}
			if len(routing.Routes[0].Paths) == 0 || len(routing.Routes[0].EvidenceIDs) == 0 {
				t.Fatalf("route has no evidence-backed target: %#v", routing.Routes[0])
			}
		})
	}
}

func TestRoutingPolarityControlsRouteSelectionAndContractPlanning(t *testing.T) {
	catalog := plannerControlPlane(t)
	tests := []struct {
		name         string
		request      string
		selected     []string
		excluded     []string
		ownerReview  bool
		contractPlan domain.ContractPlanState
	}{
		{
			name:    "canary exclusion does not select composition",
			request: "Implement HTTP boundary, usecase and PostgreSQL persistence. Keep route registration and composition out of scope.",
			selected: []string{
				"backend.infrastructure.persistence", "backend.transport.http", "backend.usecase",
			},
			excluded: []string{"backend.composition"}, contractPlan: domain.ContractPlanFreezeNeeded,
		},
		{
			name:    "direct PostgreSQL exclusion",
			request: "Do not modify PostgreSQL.", excluded: []string{"backend.infrastructure.persistence"},
			ownerReview: true, contractPlan: domain.ContractPlanPlanned,
		},
		{
			name:    "explicit composition responsibility",
			request: "Register the new handler in the HTTP router.", selected: []string{"backend.composition"},
			contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:         "HTTP implementation with serialized composition requires no registration contract",
			request:      "Implement the HTTP handler. Register the handler in the application router.",
			selected:     []string{"backend.transport.http", "backend.composition"},
			contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:         "three workers plus composition freeze only source interfaces",
			request:      "Implement HTTP boundary, usecase and PostgreSQL persistence. Register the handler in the application router.",
			selected:     []string{"backend.transport.http", "backend.usecase", "backend.infrastructure.persistence", "backend.composition"},
			contractPlan: domain.ContractPlanFreezeNeeded,
		},
		{
			name:    "adding an HTTP handler does not imply composition",
			request: "Add the HTTP handler.", selected: []string{"backend.transport.http"},
			contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:     "HTTP implementation and route exclusion",
			request:  "Implement the HTTP handler but do not register the route.",
			selected: []string{"backend.transport.http"}, excluded: []string{"backend.composition"},
			contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:     "comma-separated actions keep polarity scoped",
			request:  "Do not modify PostgreSQL, implement the HTTP handler.",
			selected: []string{"backend.transport.http"}, excluded: []string{"backend.infrastructure.persistence"},
			contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:     "contradictory route registration",
			request:  "Register the route, but do not change route registration.",
			excluded: []string{"backend.composition"}, ownerReview: true, contractPlan: domain.ContractPlanPlanned,
		},
		{
			name:     "explanatory composition mention",
			request:  "The existing composition currently wires the handler. Only change usecase validation.",
			selected: []string{"backend.usecase"}, contractPlan: domain.ContractPlanNotRequired,
		},
		{
			name:     "conditional composition responsibility",
			request:  "Change composition only if necessary.",
			excluded: []string{"backend.composition"}, ownerReview: true, contractPlan: domain.ContractPlanPlanned,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := goRoutingFixture(t)
			if err := os.MkdirAll(filepath.Join(root, "cmd", "service"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cmd", "service", "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			projectPath := root
			routing, plan, _, err := buildRoutingMetadata(
				test.request, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
				[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
			)
			if err != nil {
				t.Fatal(err)
			}
			var selected []string
			for _, route := range routing.Routes {
				selected = append(selected, route.RouteID)
			}
			if strings.Join(uniqueSorted(selected), "\x00") != strings.Join(uniqueSorted(test.selected), "\x00") {
				t.Fatalf("selected routes = %v, want %v; routing=%#v", selected, test.selected, routing)
			}
			for _, group := range plan.IndependentAfterFreeze {
				for _, ref := range group {
					if isCompositionRoute(ref.RouteID) {
						t.Fatal("serialized composition entered independent implementation group")
					}
				}
			}
			for _, candidate := range routing.SharedBoundaryCandidates {
				if candidate.Kind == "http-route-registration" || candidate.Kind == "message-consumer-registration" {
					t.Fatal("runtime registration became a source-interface freeze candidate")
				}
			}
			if plan.State != test.contractPlan {
				t.Fatalf("contract plan state = %s, want %s; plan=%#v", plan.State, test.contractPlan, plan)
			}
			if routing.OwnerReviewRequired != test.ownerReview {
				t.Fatalf("owner review required = %v, want %v; routing=%#v", routing.OwnerReviewRequired, test.ownerReview, routing)
			}
			for _, routeID := range test.excluded {
				for _, route := range routing.Routes {
					if route.RouteID == routeID {
						t.Fatalf("excluded route %q was selected: %#v", routeID, routing.Routes)
					}
				}
			}
			for _, routeID := range append(append([]string(nil), test.selected...), test.excluded...) {
				var diagnostic *domain.RoutingCandidateEvidence
				for index := range routing.RouteCandidates {
					if routing.RouteCandidates[index].RouteID == routeID {
						diagnostic = &routing.RouteCandidates[index]
						break
					}
				}
				if diagnostic == nil || diagnostic.MatchedPhrase == "" || diagnostic.TaskSpan == "" ||
					len(diagnostic.ArchitectureEvidence) == 0 || len(diagnostic.SourceEvidence) == 0 {
					t.Fatalf("route %q lacks deterministic routing evidence: %#v", routeID, diagnostic)
				}
				if containsValue(test.excluded, routeID) && diagnostic.Decision != domain.RouteDecisionExcluded &&
					diagnostic.Decision != domain.RouteDecisionOwnerReview {
					t.Fatalf("route %q decision = %s, want excluded or owner review", routeID, diagnostic.Decision)
				}
				if containsValue(test.selected, routeID) && diagnostic.Decision != domain.RouteDecisionSelected {
					t.Fatalf("route %q decision = %s, want selected", routeID, diagnostic.Decision)
				}
			}
			for _, boundary := range routing.SharedBoundaryCandidates {
				if boundary.Kind == "http-route-registration" {
					for _, route := range test.excluded {
						if route == "backend.composition" {
							t.Fatalf("excluded composition generated route-registration boundary: %#v", boundary)
						}
					}
				}
			}
		})
	}
}

func TestRoutingSelectsEvidenceBackedNextRoutes(t *testing.T) {
	catalog := plannerControlPlane(t)
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"package.json":                                          "{\"dependencies\":{\"next\":\"15.0.0\"}}",
		"src/app/student/page.tsx":                              "export default function Page() { return null }",
		"src/modules/student/internal/ui/form.tsx":              "export function StudentForm() { return null }",
		"src/modules/student/internal/api/student-api.ts":       "export async function loadStudent() { return null }",
		"src/modules/student/internal/usecases/load-student.ts": "export async function loadStudentUsecase() { return null }",
	})
	projectPath := root
	cases := []struct {
		request string
		route   string
	}{
		{"student module interface component", "frontend.module.ui"},
		{"student module API adapter request mapping", "frontend.module.api"},
		{"student use case behavior", "frontend.module.usecase"},
	}
	for _, test := range cases {
		t.Run(test.route, func(t *testing.T) {
			routing, _, _, err := buildRoutingMetadata(
				test.request, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
				[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
			)
			if err != nil {
				t.Fatal(err)
			}
			if routing.Profiles[0].ProfileID != "nextjs.common" || routing.Profiles[0].Variant != "student" ||
				len(routing.Routes) != 1 || routing.Routes[0].RouteID != test.route {
				t.Fatalf("routing = %#v", routing)
			}
		})
	}
}

func TestTaskRouteInstructionsTakePrecedenceOverTaskKeywords(t *testing.T) {
	catalog := plannerControlPlane(t)
	root := t.TempDir()
	writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{
		"go.mod":                       "module example.test/service\n\ngo 1.24\n",
		"AGENTS.md":                    "backend.infrastructure.persistence owns SQL and repository query changes.",
		"internal/usecase/checkout.go": "package usecase\n\ntype Checkout struct{}\n",
		"internal/adapters/postgres/repository.go": "package postgres\n\nfunc Query() {}\n",
	}))
	projectPath := root
	routing, _, _, err := buildRoutingMetadata(
		"business process query changes", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
		[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(routing.Routes) != 1 || routing.Routes[0].RouteID != "backend.infrastructure.persistence" {
		t.Fatalf("local instruction was not applied before keyword routing: %#v", routing.Routes)
	}
}

func TestRepositoryPortOwnershipSupportsKnownGoInterfaceLayouts(t *testing.T) {

	for _, interfacePath := range []string{
		"internal/domain/repository/ports.go",
		"internal/domain/ports.go",
		"internal/port/ports.go",
		"internal/usecase/ports.go",
	} {
		t.Run(interfacePath, func(t *testing.T) {
			catalog := plannerControlPlane(t)
			expectedOwner := "backend.domain"
			if interfacePath == "internal/usecase/ports.go" {
				// A different audited profile may own its ports in usecase only when
				// the implementation dependency is explicitly valid in that profile.
				expectedOwner = "backend.usecase"
				profile := catalog.Profiles["go.canonical"]
				for i := range profile.BoundaryOwnership {
					if profile.BoundaryOwnership[i].Kind == "repository-port" {
						profile.BoundaryOwnership[i].OwnerRoutes = []string{expectedOwner}
					}
				}
				for i := range profile.Routes {
					if profile.Routes[i].ID == "backend.infrastructure.persistence" {
						profile.Routes[i].AllowedDependencies = []string{"backend.domain", "backend.usecase"}
					}
				}
				catalog.Profiles[profile.ID] = profile
			}
			root := t.TempDir()
			writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{
				"go.mod":                       "module example.test/service\n\ngo 1.24\n",
				"internal/usecase/checkout.go": "package usecase\n\ntype Checkout struct{}\n",
				"internal/adapters/postgres/repository.go": "package postgres\n\nfunc Query() {}\n",
				interfacePath: "package ports\n\ntype CheckoutRepository interface { Find() }\n",
			}))
			projectPath := root
			routing, plan, _, err := buildRoutingMetadata(
				"SQL query change for business process", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
				[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
			)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, boundary := range plan.Boundaries {
				if boundary.Kind == "repository-port" {
					found = boundary.Existing && boundary.Owner.RouteID == expectedOwner
					evidenceFound := false
					for _, evidence := range routing.EvidenceIndex {
						if evidence.ID == boundary.SourceEvidenceID && evidence.Path == interfacePath {
							evidenceFound = true
						}
					}
					found = found && evidenceFound
				}
			}
			if !found {
				t.Fatalf("repository port was not found/owned using layout %q: plan=%#v", interfacePath, plan)
			}
		})
	}
}

func TestContractPlanAdaptiveDecisions(t *testing.T) {
	catalog := plannerControlPlane(t)
	t.Run("single persistence route skips", func(t *testing.T) {
		root := goRoutingFixture(t)
		projectPath := root
		routing, plan, _, err := buildRoutingMetadata(
			"SQL query performance", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
			[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(routing.Routes) != 1 || plan.State != domain.ContractPlanNotRequired || plan.Required || plan.FreezeRequired {
			t.Fatalf("routes=%#v plan=%#v", routing.Routes, plan)
		}
	})
	t.Run("shared backend boundary requires plan and freeze", func(t *testing.T) {
		root := goRoutingFixture(t)
		projectPath := root
		routing, plan, _, err := buildRoutingMetadata(
			"HTTP handler for business process", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
			[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(routing.Routes) != 2 || plan.State != domain.ContractPlanFreezeNeeded ||
			!plan.Required || !plan.FreezeRequired || len(plan.Boundaries) == 0 {
			t.Fatalf("routes=%#v plan=%#v", routing.Routes, plan)
		}
		for _, boundary := range plan.Boundaries {
			if boundary.Kind == "application-command-result" && boundary.Owner.RouteID != "backend.usecase" {
				t.Fatalf("application boundary owner = %#v, want usecase", boundary.Owner)
			}
		}
	})
	t.Run("multiple routes without shared boundary skip", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFiles(t, root, map[string]string{
			"package.json":             "{\"dependencies\":{\"next\":\"15.0.0\"}}",
			"src/app/student/page.tsx": "export default function Page() { return null }",
			"src/shared/ui/button.tsx": "export function Button() { return null }",
			"src/shared/bff/client.ts": "export async function request() { return null }",
		})
		projectPath := root
		routing, plan, _, err := buildRoutingMetadata(
			"shared UI component and BFF adapter", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
			[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(routing.Routes) != 2 || len(routing.SharedBoundaryCandidates) != 0 ||
			plan.State != domain.ContractPlanNotRequired || plan.Required || plan.FreezeRequired {
			t.Fatalf("routing=%#v plan=%#v", routing, plan)
		}
	})
}

func TestValidatorRejectsUnsupportedRoutingAndContractMetadata(t *testing.T) {
	catalog := plannerControlPlane(t)
	base := validRoutedOutput(t, catalog)
	cases := []struct {
		name   string
		mutate func(*domain.PlannerOutput)
	}{
		{name: "unknown profile", mutate: func(value *domain.PlannerOutput) { value.Routing.Profiles[0].ProfileID = "unknown.profile" }},
		{name: "unknown route", mutate: func(value *domain.PlannerOutput) { value.Routing.Routes[0].RouteID = "backend.transport.unsupported" }},
		{name: "incompatible path", mutate: func(value *domain.PlannerOutput) { value.Routing.Routes[0].Paths[0] = "outside/unverified.go" }},
		{name: "missing evidence", mutate: func(value *domain.PlannerOutput) { value.Routing.Routes[0].EvidenceIDs = nil }},
		{name: "impossible contract owner", mutate: func(value *domain.PlannerOutput) { value.ContractPlan.Boundaries[0].Owner.ProjectID = "foreign" }},
		{name: "unsupported boundary kind", mutate: func(value *domain.PlannerOutput) { value.ContractPlan.Boundaries[0].Kind = "private-helper" }},
		{name: "false existing boundary", mutate: func(value *domain.PlannerOutput) {
			value.ContractPlan.Boundaries[0].Existing = true
			value.ContractPlan.Boundaries[0].SourceEvidenceID = value.Routing.Routes[0].EvidenceIDs[0]
		}},
		{name: "foreign contract route", mutate: func(value *domain.PlannerOutput) { value.ContractPlan.AffectedRoutes[0].RouteID = "missing.route" }},
		{name: "freeze claim removed", mutate: func(value *domain.PlannerOutput) { value.ContractPlan.FreezeRequired = false }},
		{name: "unknown verification type", mutate: func(value *domain.PlannerOutput) { value.Routing.Verification[0].Boundary = "invented-global-command" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := clonePlannerOutput(t, base)
			test.mutate(&value)
			err := (Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3, ControlPlane: catalog}).Validate(context.Background(), value)
			if !errors.Is(err, domain.ErrValidation) && !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("Validate() error = %v, want invalid metadata rejected", err)
			}
		})
	}
}

func TestPlannerMetadataPersistenceCompatibilityAndFingerprint(t *testing.T) {
	catalog := plannerControlPlane(t)
	output := validRoutedOutput(t, catalog)
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "\"routing\"") || !strings.Contains(string(encoded), "\"contract_plan\"") {
		t.Fatalf("planner output omitted durable metadata: %s", encoded)
	}
	var roundTrip domain.PlannerOutput
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Routing == nil || roundTrip.ContractPlan == nil || roundTrip.PlanningMetadataVersion != domain.PlannerMetadataVersionV1 {
		t.Fatalf("planner output lost metadata on reload: %#v", roundTrip)
	}
	planJSON, err := json.Marshal(domain.Plan{PlannerOutput: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planJSON), "\"routing\"") || !strings.Contains(string(planJSON), "\"contract_plan\"") {
		t.Fatalf("existing plan serialization hid planner metadata: %s", planJSON)
	}
	var legacy domain.PlannerOutput
	if err := json.Unmarshal([]byte("{\"summary\":\"legacy\",\"tasks\":[],\"dependencies\":[]}"), &legacy); err != nil {
		t.Fatalf("old planner output did not decode: %v", err)
	}
	if legacy.PlanningMetadataVersion != 0 || legacy.Routing != nil || legacy.ContractPlan != nil {
		t.Fatalf("legacy planner output unexpectedly gained metadata: %#v", legacy)
	}
	input := []byte("{\"command_id\":\"command\"}")
	fingerprint, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	first := domain.PlannerFingerprint(input, fingerprint)
	changed := clonePlannerOutput(t, output)
	changed.ContractPlan.Boundaries[0].Kind = "changed-boundary"
	changedJSON, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	second := domain.PlannerFingerprint(input, changedJSON)
	if first == second {
		t.Fatal("changing contract metadata did not change owner approval fingerprint")
	}
	changed = clonePlannerOutput(t, output)
	changed.Routing.Routes[0].Paths[0] = "internal/usecase/another.go"
	changedJSON, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if domain.PlannerFingerprint(input, changedJSON) == first {
		t.Fatal("changing routed target did not change owner approval fingerprint")
	}
}

func TestPlannerRouteMetadataDoesNotCreateExecutableTasksOrFreezeSource(t *testing.T) {
	catalog := plannerControlPlane(t)
	output := validRoutedOutput(t, catalog)
	if len(output.Tasks) != 1 || len(output.Routing.Routes) != 2 {
		t.Fatalf("routed plan created architecture task shards: tasks=%d routes=%d", len(output.Tasks), len(output.Routing.Routes))
	}
	if len(output.Tasks[0].WriteScope) != 1 || output.Tasks[0].WriteScope[0] != "internal/**" {
		t.Fatalf("analysis metadata rewrote current executable scope: %#v", output.Tasks[0].WriteScope)
	}
	if !output.ContractPlan.FreezeRequired || output.ContractPlan.State != domain.ContractPlanFreezeNeeded {
		t.Fatalf("contract plan decision = %#v", output.ContractPlan)
	}
	encoded, err := json.Marshal(output.ContractPlan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "contracts_frozen") || strings.Contains(string(encoded), "frozen_baseline") {
		t.Fatalf("plan falsely represents a source freeze: %s", encoded)
	}
	if err := (Validator{ControlPlane: catalog}).Validate(context.Background(), output); err != nil {
		t.Fatalf("validator rejected analysis-only freeze requirement: %v", err)
	}
	if _, err := decodePlannerAgentResult([]byte("{\"contracts_frozen\":true}")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("agent decoder accepted a frozen-contract claim: %v", err)
	}
}

func TestRoutingInventorySkipsSecretsSymlinksAndLegacyAgentInputs(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod":                   "module example.test/service\n\ngo 1.24\n",
		"AGENTS.md":                "database owner is backend.infrastructure.persistence",
		".env":                     "TOP_SECRET=never-index",
		".ai/rules/common.md":      "legacy rule source",
		".ai/workflows/feature.md": "legacy workflow source",
		"internal/adapters/postgres/repository.go": "package postgres\nfunc Query() {}\n",
	})
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal", "adapters", "postgres", "linked.go")); err != nil {
		t.Fatal(err)
	}
	inventory, err := indexRepository(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, item := range inventory.evidence {
		paths[item.value.Path] = true
		if strings.Contains(item.value.Summary, "TOP_SECRET") {
			t.Fatal("secret content appeared in routing evidence")
		}
	}
	for _, skipped := range []string{".env", ".ai/rules/common.md", ".ai/workflows/feature.md", "internal/adapters/postgres/linked.go"} {
		if paths[skipped] {
			t.Fatalf("excluded file %q entered routing evidence", skipped)
		}
	}
}

func validRoutedOutput(t *testing.T, catalog agentcontrol.Catalog) domain.PlannerOutput {
	t.Helper()
	root := goRoutingFixture(t)
	projectPath := root
	routing, contractPlan, _, err := buildRoutingMetadata(
		"HTTP handler for business process", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
		[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	return domain.PlannerOutput{
		PlanningMetadataVersion: domain.PlannerMetadataVersionV1,
		Summary:                 "routed plan fixture", RiskLevel: domain.RiskLevelLow,
		Tasks: []domain.PlannedTask{{
			Key: "project", ProjectID: "project", Role: "backend-coder", Title: "change",
			Description: "fixture", AcceptanceCriteria: []string{"passes"},
			WriteScope: []string{"internal/**"}, ModelProfile: config.ModelProfileStandard,
			RiskLevel: domain.RiskLevelLow, VerificationCommands: []string{"go test ./..."},
		}},
		Routing: &routing, ContractPlan: &contractPlan,
	}
}

func goRoutingFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{
		"go.mod":                                   "module example.test/service\n\ngo 1.24\n",
		"internal/domain/order.go":                 "package domain\n\ntype Order struct{}\n",
		"internal/usecase/checkout.go":             "package usecase\n\ntype Checkout struct{}\n",
		"internal/adapters/http/handler.go":        "package http\n\nfunc Handle() {}\n",
		"internal/adapters/http/handler_test.go":   "package http\n\nfunc TestHandle() {}\n",
		"internal/adapters/postgres/repository.go": "package postgres\n\nfunc Query() {}\n",
		"internal/adapters/client/client.go":       "package client\n\nfunc Call() {}\n",
		"internal/adapters/messaging/publisher.go": "package messaging\n\nfunc Publish() {}\n",
	}))
	return root
}

func canonicalGoFixtureFiles(files map[string]string) map[string]string {
	files["internal/domain/shape.go"] = "package domain\n"
	files["internal/transport/shape.go"] = "package transport\n"
	files["internal/infrastructure/shape.go"] = "package infrastructure\n"
	return files
}

func writeFixtureFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func clonePlannerOutput(t *testing.T, output domain.PlannerOutput) domain.PlannerOutput {
	t.Helper()
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var clone domain.PlannerOutput
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestRoutePolarityRecognizesBoundedNegationAndTaskSignals(t *testing.T) {
	tests := []struct {
		name    string
		routeID string
		request string
		want    domain.RoutePolarity
	}{
		{"do not", "backend.infrastructure.persistence", "Do not modify PostgreSQL.", domain.RoutePolarityNegative},
		{"without", "backend.transport.http", "without changing HTTP", domain.RoutePolarityNegative},
		{"keep out of scope", "backend.composition", "Keep composition out of scope", domain.RoutePolarityNegative},
		{"leave unchanged", "backend.composition", "Leave route registration unchanged", domain.RoutePolarityNegative},
		{"no changes", "backend.composition", "No composition changes", domain.RoutePolarityNegative},
		{"must not", "backend.composition", "Must not modify DI", domain.RoutePolarityNegative},
		{"should not", "backend.composition", "Should not wire the endpoint yet", domain.RoutePolarityNegative},
		{"exclude", "backend.infrastructure.persistence", "Exclude SQL", domain.RoutePolarityNegative},
		{"noun mention is neutral", "backend.composition", "The composition boundary", domain.RoutePolarityNeutral},
		{"current-state explanation is neutral", "backend.composition", "The existing composition currently wires the handler.", domain.RoutePolarityNeutral},
		{"HTTP implementation is positive", "backend.transport.http", "Add the HTTP handler", domain.RoutePolarityPositive},
		{"explicit bootstrap owns composition", "backend.composition", "Bootstrap the component", domain.RoutePolarityPositive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := analyzeRouteCandidate(test.routeID, test.request, "project", nil, nil)
			if got.Polarity != test.want {
				t.Fatalf("polarity = %s, want %s; evidence=%#v", got.Polarity, test.want, got)
			}
		})
	}
}

func TestPlannerCannotOverrideDeterministicRouteExclusion(t *testing.T) {
	catalog := plannerControlPlane(t)
	root := goRoutingFixture(t)
	projectPath := root
	routing, _, _, err := buildRoutingMetadata(
		"Implement the HTTP handler but do not register the route.",
		domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
		[]domain.Project{{ID: "project", LocalPath: &projectPath}}, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []domain.PlannedTask{{ProjectID: "project", Key: "canary", ArchitecturalRoutes: []string{"backend.composition"}}}
	if err := validateArchitecturalRouteSelection(tasks, routing); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("excluded composition override error = %v, want validation error", err)
	}
}

func TestMessageRegistrationIsWiringNotFrozenSourceContract(t *testing.T) {
	catalog := plannerControlPlane(t)
	candidates := sharedBoundaryCandidates([]domain.RoutedTarget{
		{RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.transport.message"}},
		{RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.composition"}},
	}, map[string]agentcontrol.Profile{"project": catalog.Profiles["go.canonical"]})
	if len(candidates) != 0 {
		t.Fatalf("consumer registration became a source contract: %#v", candidates)
	}
}

func TestWave1GoldFairScan(t *testing.T) {
	files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module fixture\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}\n"})
	for i := 0; i < maxRoutingEvidence; i++ {
		files[fmt.Sprintf(".ai/contracts/f%04d.yaml", i)] = "contract: fixture\n"
	}
	root := t.TempDir()
	writeFixtureFiles(t, root, files)
	inventory, err := indexRepository(root, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	resolution, _, err := resolveArchitectureProfile("change business process", "fixture", inventory, plannerControlPlane(t))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != domain.ProfileResolutionResolved {
		t.Fatalf("existing required layers hidden by metadata: %s", resolution.Reason)
	}
	if inventory.coverage.IndexedFiles > maxRoutingEvidence || !inventory.coverage.UnscannedRemainder || inventory.coverage.RemainderCountKnown {
		t.Fatal("bounded acquisition uncertainty lost")
	}
}
func TestWave1GoldOutboundHTTPRoot(t *testing.T) {
	root := t.TempDir()
	files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module fixture\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}\n", ".ai/architecture.yaml": "outbound_client: internal/infrastructure/http/remote\n", "internal/infrastructure/http/remote/client.go": "package remote\nimport \"net/http\"\nfunc Call(){http.Get(\"https://remote.example.test\")}\n"})
	writeFixtureFiles(t, root, files)
	route, _ := coverageBuild(t, root, "Change external client timeout")
	found := false
	for _, r := range route.Routes {
		found = found || r.RouteID == "backend.infrastructure.client"
	}
	if !found {
		t.Fatalf("owner-declared outbound root unresolved: %+v", route.RouteCandidates)
	}
}

func TestWave1GoldOutboundRejectsInboundAndComments(t *testing.T) {
	for _, declaration := range []string{"inbound_http: internal/infrastructure/http/server\n", "outbound_client: internal/infrastructure/http/server\n"} {
		root := t.TempDir()
		files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module fixture\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}\n", ".ai/architecture.yaml": declaration, "internal/infrastructure/http/server/handler.go": "package server\nimport \"net/http\"\n// client.Do( is prohibited here\nfunc Serve(){http.HandleFunc(\"/\",func(http.ResponseWriter,*http.Request){})}\n"})
		writeFixtureFiles(t, root, files)
		route, _ := coverageBuild(t, root, "Change external client timeout")
		for _, r := range route.Routes {
			if r.RouteID == "backend.infrastructure.client" {
				t.Fatal("inbound/comment became outbound implementation")
			}
		}
	}
}
