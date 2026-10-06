package planning

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingCoverageCompatibilityBaseline(t *testing.T) {
	output := validRoutedOutput(t, plannerControlPlane(t))
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "..", "test", "fixtures", "context-retrieval", "routing")
	old, err := os.ReadFile(filepath.Join(fixture, "legacy-planner-output.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(old) {
		t.Fatal("routing/ContractPlan/PlannerOutput serialization changed from pre-R1 baseline")
	}
	fingerprint, err := os.ReadFile(filepath.Join(fixture, "legacy-planner-fingerprint.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if domain.PlannerFingerprint([]byte("routing-coverage-legacy-input"), raw) != string(fingerprint) {
		t.Fatal("pre-R1 approval fingerprint changed")
	}
}

func coverageBuild(t *testing.T, root, text string) (domain.RoutingResult, RoutingCoverageReport) {
	t.Helper()
	routing, _, report, err := BuildRoutingMetadataWithCoverage(text,
		domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}},
		[]domain.Project{{ID: "project", SourceIdentity: "source:synthetic", LocalPath: &root}}, plannerControlPlane(t))
	if err != nil {
		t.Fatal(err)
	}
	return routing, report
}

func hasCoverageReason(coverage RoutingRepositoryCoverage, reason string) bool {
	for _, omission := range coverage.Omissions {
		if omission.Reason == reason {
			return true
		}
	}
	return false
}

func TestRoutingCoverageInventoryExactAndOverLimits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count, bytes      int
		extension, reason string
		indexed           int
	}{
		{"visited exact", maxRoutingFilesVisited, 1, ".txt", "", 0},
		{"visited over", maxRoutingFilesVisited + 1, 1, ".txt", "visited_files_limit", 0},
		{"evidence exact", maxRoutingEvidence, 1, ".go", "", maxRoutingEvidence},
		{"evidence over", maxRoutingEvidence + 1, 1, ".go", "inventory_evidence_limit", maxRoutingEvidence},
		{"file bytes exact", 1, maxRoutingFileBytes, ".go", "", 1},
		{"file bytes over", 1, maxRoutingFileBytes + 1, ".go", "file_bytes_limit", 0},
		{"inventory bytes exact", 32, maxRoutingFileBytes, ".go", "", 32},
		{"inventory bytes over", 33, maxRoutingFileBytes, ".go", "inventory_bytes_limit", 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < tc.count; i++ {
				writeFixtureFiles(t, root, map[string]string{fmt.Sprintf("f%04d%s", i, tc.extension): strings.Repeat(" ", tc.bytes)})
			}
			inventory, err := indexRepository(root, "project")
			if err != nil {
				t.Fatal(err)
			}
			coverage := inventory.coverage
			if coverage.IndexedFiles != tc.indexed {
				t.Fatalf("indexed = %d want %d", coverage.IndexedFiles, tc.indexed)
			}
			if tc.reason == "" {
				if coverage.Status != "COMPLETE" || coverage.UnscannedRemainder {
					t.Fatalf("exact cap: %#v", coverage)
				}
			} else if coverage.Status != "PARTIAL" || !hasCoverageReason(*coverage, tc.reason) {
				t.Fatalf("over cap missing %s: %#v", tc.reason, coverage)
			}
			if coverage.UnscannedRemainder && coverage.TerminationReason == "" {
				t.Fatal("termination has no explicit reason")
			}
		})
	}
}

func TestRoutingCoverageLateMandatoryFile(t *testing.T) {
	root := t.TempDir()
	files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module test\n", "internal/usecase/change.go": "package usecase\nfunc Change() {}"})
	for i := 0; i < maxRoutingEvidence; i++ {
		files[fmt.Sprintf(".ai/contracts/a%04d.yaml", i)] = "repository-port\n"
	}
	files["z/late_test.go"] = "package z\nfunc TestLate() {}"
	writeFixtureFiles(t, root, files)
	_, report := coverageBuild(t, root, "change business process")
	if report.Status != "PARTIAL" || !report.Projects[0].UnscannedRemainder || report.Projects[0].RemainderCountKnown {
		t.Fatalf("late file looked complete: %#v", report)
	}
	if len(report.Requirements) == 0 || report.Requirements[0].Status != "REQUIRED_NOT_VERIFIED" {
		t.Fatalf("unresolved profile is not verified: %#v", report.Requirements)
	}
	if report.Projects[0].IndexedFiles != maxRoutingEvidence {
		t.Fatal("selection cap changed")
	}
}

func TestRoutingCoverageOversizedRequiredSource(t *testing.T) {
	root := t.TempDir()
	files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module test\n", "internal/usecase/change.go": strings.Repeat(" ", maxRoutingFileBytes+1)})
	writeFixtureFiles(t, root, files)
	_, report := coverageBuild(t, root, "change business process")
	if report.Status == "COMPLETE" || !hasCoverageReason(report.Projects[0], "file_bytes_limit") {
		t.Fatalf("oversize omission missing: %#v", report)
	}
	found := false
	for _, requirement := range report.Requirements {
		if requirement.Kind == "source" && requirement.Status == "REQUIRED_NOT_VERIFIED" {
			found = true
		}
	}
	if !found {
		t.Fatal("oversized requested route source was not marked REQUIRED_NOT_VERIFIED")
	}
}

func TestRoutingCoverageUnreadableRequiredSource(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{"go.mod": "module test\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}"}))
	file := filepath.Join(root, "internal", "usecase", "change.go")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0600) })
	if _, err := os.ReadFile(file); err == nil {
		t.Skip("process can read files without permission; unreadable case requires unprivileged runner")
	}
	_, report := coverageBuild(t, root, "change business process")
	if report.Status == "COMPLETE" || !hasCoverageReason(report.Projects[0], "read_error") || report.Projects[0].ReadErrors != 1 {
		t.Fatalf("unreadable omission missing: %#v", report)
	}
	found := false
	for _, requirement := range report.Requirements {
		if requirement.Kind == "source" && requirement.Status == "REQUIRED_NOT_VERIFIED" {
			found = true
		}
	}
	if !found {
		t.Fatal("unreadable required source claimed found")
	}
}

func TestRoutingCoverageTargetCandidateAndSymbolClips(t *testing.T) {
	for _, count := range []int{maxRouteTargets, maxRouteTargets + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := t.TempDir()
			files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module test\n"})
			for i := 0; i < count; i++ {
				files[fmt.Sprintf("internal/usecase/file%02d.go", i)] = "package usecase\nfunc Change() {}"
			}
			writeFixtureFiles(t, root, files)
			routing, report := coverageBuild(t, root, "change business process")
			if len(routing.Routes) != 1 || len(routing.Routes[0].Paths) != min(count, maxRouteTargets) {
				t.Fatalf("selection changed: %#v", routing.Routes)
			}
			coverage := report.Projects[0]
			if hasCoverageReason(coverage, "target_limit") != (count > maxRouteTargets) || hasCoverageReason(coverage, "candidate_evidence_limit") != (count > maxRouteTargets) {
				t.Fatalf("clip diagnostics: %#v", coverage)
			}
		})
	}
	for _, count := range []int{maxRoutingSymbolMatches, maxRoutingSymbolMatches + 1} {
		t.Run(fmt.Sprintf("symbols %d", count), func(t *testing.T) {
			root := t.TempDir()
			content := "package usecase\n"
			for i := 0; i < count; i++ {
				content += fmt.Sprintf("func Symbol%02d() {}\n", i)
			}
			writeFixtureFiles(t, root, map[string]string{"source.go": content})
			inventory, err := indexRepository(root, "project")
			if err != nil {
				t.Fatal(err)
			}
			observation := inventory.coverage.Extractions[0]
			if observation.SymbolMatches != count || observation.SelectedSymbols != min(count, maxRoutingSymbolMatches) || observation.OmittedMatches != max(0, count-maxRoutingSymbolMatches) {
				t.Fatalf("extraction=%#v", observation)
			}
			if hasCoverageReason(*inventory.coverage, "symbol_match_limit") != (count > maxRoutingSymbolMatches) {
				t.Fatal("symbol omission not observed")
			}
		})
	}
}

func TestRoutingCoverageFactsExactOverflowAndLaterOmission(t *testing.T) {
	for _, count := range []int{maxRepositoryFactsBytes, maxRepositoryFactsBytes + 1} {
		root := t.TempDir()
		writeFixtureFiles(t, root, map[string]string{"AGENTS.md": strings.Repeat("a", count)})
		inventory, err := indexRepository(root, "project")
		if err != nil {
			t.Fatal(err)
		}
		facts := repositoryFacts(inventory)
		if len(facts) == 0 && count == maxRepositoryFactsBytes {
			t.Fatal("exact facts limit changed")
		}
		if hasCoverageReason(*inventory.coverage, "facts_bytes_limit") != (count > maxRepositoryFactsBytes) {
			t.Fatalf("facts = %#v", inventory.coverage.Facts)
		}
	}
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{".ai/commands.yaml": strings.Repeat(" ", maxRepositoryFactsBytes-5), ".ai/contracts/required.yaml": "repository-port", "AGENTS.md": "rules"})
	inventory, err := indexRepository(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	facts := repositoryFacts(inventory)
	if len(facts) != 1 || len(inventory.coverage.Facts.OmittedPaths) != 2 {
		t.Fatalf("break semantics changed or omissions silent: %#v", inventory.coverage.Facts)
	}
	first, _ := json.Marshal(inventory.coverage.Facts)
	_ = repositoryFacts(inventory)
	second, _ := json.Marshal(inventory.coverage.Facts)
	if string(first) != string(second) {
		t.Fatal("facts diagnostics are not idempotent")
	}
}

func TestRoutingCoverageGlobalClipAndRequiredOmission(t *testing.T) {
	catalog := plannerControlPlane(t)
	var projects []domain.Project
	baseline := domain.PlannerOutput{}
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		id := fmt.Sprintf("project%d", i)
		files := canonicalGoFixtureFiles(map[string]string{"go.mod": "module test\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}"})
		for j := 0; j < 300; j++ {
			files[fmt.Sprintf("internal/domain/source%03d.go", j)] = "package z"
		}
		writeFixtureFiles(t, root, files)
		projects = append(projects, domain.Project{ID: id, SourceIdentity: "source:" + id, LocalPath: &root})
		baseline.Tasks = append(baseline.Tasks, domain.PlannedTask{ProjectID: id})
	}
	routing, _, report, err := BuildRoutingMetadataWithCoverage("repository inventory audit", baseline, projects, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(routing.EvidenceIndex) != maxRoutingEvidence || report.Status != "PARTIAL" || len(report.EvidenceProjection.OmittedEvidenceIDs) == 0 || len(report.EvidenceProjection.RequiredOmittedIDs) == 0 {
		t.Fatalf("global clipping not observed: %#v", report.EvidenceProjection)
	}
	// Required references alone can exceed the global cap; capture these before
	// the legacy router discards references from its serialized output.
	inventory := repositoryInventory{coverage: newRoutingRepositoryCoverage("project"), byPath: map[string]indexedEvidence{}}
	for i := 0; i < maxRoutingEvidence+1; i++ {
		evidence := makeEvidence("project", fmt.Sprintf("source%03d_test.go", i), "test", []byte("test"))
		inventory.evidence = append(inventory.evidence, indexedEvidence{value: evidence, content: []byte("test")})
	}
	routing = domain.RoutingResult{Profiles: []domain.ArchitectureProfileResolution{{ProjectID: "project"}}, Verification: []domain.RouteVerification{{RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"}}}}
	for _, item := range inventory.evidence {
		routing.Verification[0].EvidenceIDs = append(routing.Verification[0].EvidenceIDs, item.value.ID)
	}
	inventories := map[string]repositoryInventory{"project": inventory}
	recordRoutingRequirements(routing, domain.ContractPlan{}, inventories)
	routing.EvidenceIndex = buildBoundedEvidenceIndex(inventory.evidence, routing, domain.ContractPlan{})
	routing.Verification[0].EvidenceIDs = nil // actual old projection can lose references
	report = buildRoutingCoverageReport(routing, domain.ContractPlan{}, nil, inventories)
	if len(report.EvidenceProjection.RequiredOmittedIDs) != 1 {
		t.Fatalf("known requirement vanished: %#v", report.EvidenceProjection)
	}
	found := false
	for _, requirement := range report.Requirements {
		if requirement.Kind == "test" && requirement.Status == "REQUIRED_NOT_VERIFIED" && strings.Contains(requirement.Reason, "found_but_omitted") {
			found = true
		}
	}
	if !found {
		t.Fatal("omitted acquired required evidence was silently lost")
	}
}

func TestRoutingCoverageHistoricalUnknownDeterministicIdentity(t *testing.T) {
	root := goRoutingFixture(t)
	project := domain.Project{ID: "project", SourceIdentity: "source:fixture", LocalPath: &root}
	baseline := domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}
	catalog := plannerControlPlane(t)
	routing, plan, report, err := BuildRoutingMetadataWithCoverage("change HTTP handler", baseline, []domain.Project{project}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	historical := RoutingCoverageFromEvidence(routing, plan, []domain.Project{project})
	if historical.Status != "UNKNOWN" || historical.Projects[0].Status != "UNKNOWN" {
		t.Fatalf("historical coverage guessed: %#v", historical)
	}
	_, _, again, err := BuildRoutingMetadataWithCoverage("change HTTP handler", baseline, []domain.Project{project, {ID: "unselected"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if report.DiagnosticDigest != again.DiagnosticDigest || report.RoutingOutputDigest != again.RoutingOutputDigest {
		t.Fatal("same task/source/config diagnostics drifted")
	}
	// Absolute checkout location is excluded from semantic diagnostic identity.
	other := t.TempDir()
	writeFixtureFiles(t, other, canonicalGoFixtureFiles(map[string]string{"go.mod": "module example.test/service\n\ngo 1.24\n", "internal/domain/order.go": "package domain\n\ntype Order struct{}\n", "internal/usecase/checkout.go": "package usecase\n\ntype Checkout struct{}\n", "internal/adapters/http/handler.go": "package http\n\nfunc Handle() {}\n", "internal/adapters/http/handler_test.go": "package http\n\nfunc TestHandle() {}\n", "internal/adapters/postgres/repository.go": "package postgres\n\nfunc Query() {}\n", "internal/adapters/client/client.go": "package client\n\nfunc Call() {}\n", "internal/adapters/messaging/publisher.go": "package messaging\n\nfunc Publish() {}\n"}))
	project.LocalPath = &other
	_, _, relocated, err := BuildRoutingMetadataWithCoverage("change HTTP handler", baseline, []domain.Project{project}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if relocated.DiagnosticDigest != report.DiagnosticDigest {
		t.Fatal("host checkout path contaminated digest")
	}
	writeFixtureFiles(t, other, map[string]string{"internal/adapters/http/handler.go": "package http\nfunc Changed(){}"})
	_, _, changed, err := BuildRoutingMetadataWithCoverage("change HTTP handler", baseline, []domain.Project{project}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if changed.DiagnosticDigest == report.DiagnosticDigest || changed.RoutingOutputDigest == report.RoutingOutputDigest {
		t.Fatal("changed source did not change digest")
	}
	configChanged := report
	configChanged.Config.MaxEvidence--
	configChanged.DiagnosticDigest = ""
	encoded, _ := json.Marshal(configChanged)
	if coverageDigest(encoded) == report.DiagnosticDigest {
		t.Fatal("changed limits did not change digest")
	}
}

func TestRoutingCoveragePolicyExclusionsAndImportClip(t *testing.T) {
	root := t.TempDir()
	content := "package source\nimport (\n"
	for i := 0; i < maxRoutingSymbolMatches+1; i++ {
		content += fmt.Sprintf("\"example.test/p%02d\"\n", i)
	}
	content += ")\n"
	writeFixtureFiles(t, root, map[string]string{"source.go": content, ".env": "forbidden", "private.key": "forbidden", "node_modules/source.go": "forbidden"})
	if err := os.Symlink("/not-admitted", filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	inventory, err := indexRepository(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	coverage := inventory.coverage
	if coverage.ExcludedFiles != 2 || coverage.ExcludedDirectories != 1 || coverage.ExcludedSymlinks != 1 || coverage.IndexedFiles != 1 {
		t.Fatalf("exclusions: %#v", coverage)
	}
	if !hasCoverageReason(*coverage, "import_match_limit") {
		t.Fatal("import cap missing")
	}
	encoded, _ := json.Marshal(coverage)
	if strings.Contains(string(encoded), "forbidden") || strings.Contains(string(encoded), root) {
		t.Fatal("diagnostics expose content/host path")
	}
}

func TestRoutingCoverageInventoryByteOmissionContinues(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 31; i++ {
		writeFixtureFiles(t, root, map[string]string{fmt.Sprintf("a%02d.go", i): strings.Repeat(" ", maxRoutingFileBytes)})
	}
	writeFixtureFiles(t, root, map[string]string{"b.go": strings.Repeat(" ", maxRoutingFileBytes-1024), "c.go": strings.Repeat(" ", 2048), "d.go": " "})
	inventory, err := indexRepository(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.coverage.Status != "PARTIAL" || !hasCoverageReason(*inventory.coverage, "inventory_bytes_omission") || inventory.coverage.UnscannedRemainder {
		t.Fatalf("byte omission=%#v", inventory.coverage)
	}
	if _, found := inventory.byPath["d.go"]; !found {
		t.Fatal("old scan continues after an oversize remaining-byte candidate")
	}
}

func TestRoutingCoverageUnreadableSubtreeUnknownRemainder(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{"unreadable/source.go": "package unreadable"})
	directory := filepath.Join(root, "unreadable")
	if err := os.Chmod(directory, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
	if _, err := os.ReadDir(directory); err == nil {
		t.Skip("process can read directories without permission")
	}
	inventory, err := indexRepository(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.coverage.Status != "PARTIAL" || !inventory.coverage.UnscannedRemainder || inventory.coverage.RemainderCountKnown || !hasCoverageReason(*inventory.coverage, "walk_error") {
		t.Fatalf("unreadable subtree %#v", inventory.coverage)
	}
}

func TestValidateRoutingCoverageBindings(t *testing.T) {
	root := goRoutingFixture(t)
	route, plan, report, err := BuildRoutingMetadataWithCoverage("change HTTP handler", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}, []domain.Project{{ID: "project", SourceIdentity: "fixture", LocalPath: &root}}, plannerControlPlane(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoutingCoverage(report, route, plan); err != nil {
		t.Fatal(err)
	}
	// JSON round trips are how the offline CLI carries a full companion.
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip RoutingCoverageReport
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoutingCoverage(roundtrip, route, plan); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RoutingCoverageReport)
		resign bool
	}{
		{"schema", func(value *RoutingCoverageReport) { value.SchemaVersion = "routing-coverage.v0" }, true},
		{"config", func(value *RoutingCoverageReport) { value.Config.MaxEvidence-- }, true},
		{"catalog", func(value *RoutingCoverageReport) { value.CatalogDigest = "sha256:other" }, true},
		{"routing digest", func(value *RoutingCoverageReport) { value.RoutingOutputDigest = "sha256:other" }, true},
		{"diagnostic digest", func(value *RoutingCoverageReport) { value.DiagnosticDigest = "sha256:other" }, false},
		{"empty digest", func(value *RoutingCoverageReport) { value.DiagnosticDigest = "" }, false},
		{"edited diagnostics", func(value *RoutingCoverageReport) { value.EvidenceProjection.Selected++ }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered RoutingCoverageReport
			if err := json.Unmarshal(raw, &altered); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&altered)
			if tc.resign {
				altered.DiagnosticDigest = ""
				encoded, _ := json.Marshal(altered)
				altered.DiagnosticDigest = coverageDigest(encoded)
			}
			if err := ValidateRoutingCoverage(altered, route, plan); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("tampered binding accepted: %v", err)
			}
		})
	}
	changedRoute := route
	changedRoute.Classification = "changed"
	if err := ValidateRoutingCoverage(report, changedRoute, plan); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("different routing output accepted: %v", err)
	}
	changedPlan := plan
	changedPlan.Reason = "changed"
	if err := ValidateRoutingCoverage(report, route, changedPlan); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("different ContractPlan accepted: %v", err)
	}
	historical := RoutingCoverageFromEvidence(route, plan, nil)
	if historical.Status != "UNKNOWN" {
		t.Fatal("historical report status changed")
	}
	if err := ValidateRoutingCoverage(historical, route, plan); err != nil {
		t.Fatalf("valid UNKNOWN report rejected: %v", err)
	}
}
