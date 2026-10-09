package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func contextCommandTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return root
}

func contextCommandJSON(t *testing.T, root, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	path := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestContextCommandInputSafety(t *testing.T) {
	root := contextCommandTempDir(t)
	valid := contextCommandJSON(t, root, "request.json", map[string]string{"task": "safe"})
	var value struct {
		Task string `json:"task"`
	}
	require.NoError(t, readContextCommandJSON(valid, &value))
	require.Equal(t, "safe", value.Task)
	for _, raw := range []string{"null", "[]", `{"task":"one"}{"task":"two"}`, `{"unexpected":true}`} {
		path := filepath.Join(root, "invalid.json")
		require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
		require.Error(t, readContextCommandJSON(path, &value), raw)
	}
	for _, name := range []string{".env", ".env.example", "config.env", "credentials.json", "private_key.json", "api-token.json", "api-key.json", "api_key.json", "access_token.json", "token.json", "id_rsa", "id_ecdsa", "data.pem"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(path, []byte(`{"task":"secret"}`), 0o600))
		err := readContextCommandJSON(path, &value)
		require.ErrorContains(t, err, "excluded command input path", name)
	}
	symlink := filepath.Join(root, "link.json")
	require.NoError(t, os.Symlink(valid, symlink))
	require.Error(t, readContextCommandJSON(symlink, &value))
	parentLink := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(root, parentLink))
	require.Error(t, readContextCommandJSON(filepath.Join(parentLink, "request.json"), &value))
	hardlink := filepath.Join(root, "hardlink.json")
	require.NoError(t, os.Link(valid, hardlink))
	require.ErrorContains(t, readContextCommandJSON(hardlink, &value), "hardlinked")
	fifo := filepath.Join(root, "pipe.json")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	require.Error(t, readContextCommandJSON(fifo, &value))
	require.Error(t, readContextCommandJSON(root, &value))
	unreadable := contextCommandJSON(t, root, "unreadable.json", map[string]string{"task": "no"})
	require.NoError(t, os.Chmod(unreadable, 0))
	require.Error(t, readContextCommandJSON(unreadable, &value))
	large := filepath.Join(root, "large.json")
	require.NoError(t, os.WriteFile(large, []byte("{"+strings.Repeat(" ", maxContextCommandJSONBytes)+"}"), 0o600))
	require.ErrorContains(t, readContextCommandJSON(large, &value), "bounded input")
}

func TestContextCommandFlagsRequireExplicitAdmission(t *testing.T) {
	_, err := parseContextCommandFlags("context-prepare", nil, false)
	require.ErrorIs(t, err, domain.ErrValidation)
	for _, root := range []string{"", "repo=relative", "= /tmp", "repo="} {
		var roots contextRootFlags
		require.Error(t, roots.Set(root))
	}
	var roots contextRootFlags
	require.NoError(t, roots.Set("repo=/private/tmp"))
	require.Error(t, roots.Set("repo=/private/tmp/second"))
	args := []string{"--request-json", "request.json", "--route-json", "route.json", "--root", "repo=/private/tmp"}
	_, err = parseContextCommandFlags("context-prepare", append(args, "--format", "other"), false)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = parseContextCommandFlags("context-expand", args, true)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = parseContextCommandFlags("context-prepare", append(args, "unexpected"), false)
	require.ErrorIs(t, err, domain.ErrValidation)
	var output strings.Builder
	require.ErrorIs(t, runContextEvaluate(nil, &output), domain.ErrValidation)
}

func contextCommandFixture(t *testing.T) (string, []string) {
	t.Helper()
	root := contextCommandTempDir(t)
	source := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(source, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "main.go"), []byte("package fixture\nfunc Hello() string { return \"hello\" }\n"), 0o600))
	for path, content := range map[string]string{
		"go.mod":                           "module example.test/context\n\ngo 1.24\n",
		"internal/domain/shape.go":         "package domain\n",
		"internal/transport/shape.go":      "package transport\n",
		"internal/infrastructure/shape.go": "package infrastructure\n",
		"internal/usecase/change.go":       "package usecase\nfunc Change() {}\n",
		"internal/usecase/change_test.go":  "package usecase\nfunc TestChange() {}\n",
	} {
		full := filepath.Join(source, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	request := contextretrieval.RetrievalRequest{
		RequestID: "cli-fixture", Task: "Locate Hello", Purpose: "implementation",
		Sources:        []contextretrieval.SourceAdmission{{Identity: "repo", RouteIdentity: "project-uuid", ReadPaths: []string{"."}}},
		RequiredFacets: []contextretrieval.Facet{{ID: "definition", QueryKind: contextretrieval.QueryDefinition, SourceIdentity: "repo", Path: "main.go", Symbol: "Hello", ClaimType: contextretrieval.ImplementationBehavior, Required: true}},
		Budget:         contextretrieval.Budget{MaxSourceBytes: 4096, MaxContextTokens: 32768},
	}
	catalog, err := agentcontrol.LoadCatalog(os.DirFS("../.."))
	require.NoError(t, err)
	route, contract, coverage, err := planning.BuildRoutingMetadataWithCoverage("change business process", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project-uuid"}}}, []domain.Project{{ID: "project-uuid", SourceIdentity: "repo", LocalPath: &source}}, catalog)
	require.NoError(t, err)
	require.Equal(t, "COMPLETE", coverage.Status)
	requestPath := contextCommandJSON(t, root, "request.json", request)
	routePath := contextCommandJSON(t, root, "route.json", route)
	contractPath := contextCommandJSON(t, root, "contract.json", contract)
	coveragePath := contextCommandJSON(t, root, "coverage.json", coverage)
	return root, []string{"--request-json", requestPath, "--route-json", routePath, "--contract-plan-json", contractPath, "--routing-coverage-json", coveragePath, "--root", "repo=" + source}
}

func TestContextPrepareAndExpandOfflineAndDeterministic(t *testing.T) {
	root, args := contextCommandFixture(t)
	var first, second strings.Builder
	require.NoError(t, runContextPrepare(args, &first))
	require.NoError(t, runContextPrepare(args, &second))
	require.Equal(t, first.String(), second.String())
	var pack contextretrieval.ContextPack
	require.NoError(t, json.Unmarshal([]byte(first.String()), &pack))
	require.NoError(t, contextretrieval.VerifyDigest(pack))
	require.NotEmpty(t, pack.Evidence)
	require.NotContains(t, first.String(), root)
	var markdown strings.Builder
	require.NoError(t, runContextPrepare(append(args, "--format", "markdown"), &markdown))
	require.Contains(t, markdown.String(), pack.ContentDigest)
	basePath := contextCommandJSON(t, root, "base.json", pack)
	expansionPath := contextCommandJSON(t, root, "expand.json", contextretrieval.ExpandRequest{
		Facet:  contextretrieval.Facet{ID: "caller", SourceIdentity: "repo", QueryKind: contextretrieval.QueryCallers, Path: "main.go", Symbol: "Hello", ClaimType: contextretrieval.ImplementationBehavior},
		Reason: "Check caller capability", RemainingBudget: contextretrieval.Budget{
			MaxSourceBytes:   pack.RetrievalPlan.Budget.MaxSourceBytes - pack.BudgetUsed.CumulativeSourceBytes,
			MaxContextTokens: pack.RetrievalPlan.Budget.MaxContextTokens - pack.BudgetUsed.CumulativeContextTokens - pack.BudgetUsed.ReservedPromptTokens,
		},
	})
	var expanded strings.Builder
	require.NoError(t, runContextExpand(append(args, "--base-pack-json", basePath, "--expand-json", expansionPath), &expanded))
	var delta contextretrieval.ContextDelta
	require.NoError(t, json.Unmarshal([]byte(expanded.String()), &delta))
	require.Equal(t, pack.ContentDigest, delta.BaseDigest)
	require.NotEmpty(t, delta.Diagnostics)
	original := pack
	pack.ContentDigest = "tampered"
	contextCommandJSON(t, root, "base.json", pack)
	var rejected strings.Builder
	require.ErrorContains(t, runContextExpand(append(args, "--base-pack-json", basePath, "--expand-json", expansionPath), &rejected), "verify base pack")
	requestRoot := strings.TrimPrefix(args[len(args)-1], "repo=")
	require.NoError(t, os.WriteFile(filepath.Join(requestRoot, "main.go"), []byte("package fixture\nfunc Renamed() {}\n"), 0o600))
	contextCommandJSON(t, root, "base.json", original)
	require.ErrorIs(t, runContextExpand(append(args, "--base-pack-json", basePath, "--expand-json", expansionPath), &rejected), contextretrieval.ErrStaleBase)
}

func TestContextCommandRejectsExtraRootAndSerializedRoots(t *testing.T) {
	_, args := contextCommandFixture(t)
	var output strings.Builder
	require.ErrorContains(t, runContextPrepare(append(args, "--root", "extra=/private/tmp"), &output), "not present in current admission")
	requestPath := args[1]
	require.NoError(t, os.WriteFile(requestPath, []byte(`{"sources":[{"identity":"repo","root":"/private/tmp"}]}`), 0o600))
	require.ErrorContains(t, runContextPrepare(args, &output), "unknown field")
}

func TestContextPrepareRoutingCoverageCompanionBinding(t *testing.T) {
	root, args := contextCommandFixture(t)
	var historicalArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--routing-coverage-json" {
			i++
			continue
		}
		historicalArgs = append(historicalArgs, args[i])
	}
	var historical strings.Builder
	require.NoError(t, runContextPrepare(historicalArgs, &historical))
	var pack contextretrieval.ContextPack
	require.NoError(t, json.Unmarshal([]byte(historical.String()), &pack))
	require.Equal(t, contextretrieval.Partial, pack.Status)
	foundUnknown := false
	for _, diagnostic := range pack.UnresolvedQuestions {
		if diagnostic.Code == "ROUTING_COVERAGE_UNKNOWN" {
			foundUnknown = true
		}
	}
	require.True(t, foundUnknown)
	var coverage planning.RoutingCoverageReport
	coveragePath := filepath.Join(root, "coverage.json")
	require.NoError(t, readContextCommandJSON(coveragePath, &coverage))
	coverage.RoutingOutputDigest = "sha256:changed-route"
	contextCommandJSON(t, root, "coverage.json", coverage)
	var rejected strings.Builder
	require.ErrorContains(t, runContextPrepare(args, &rejected), "bind routing coverage")
	require.Empty(t, rejected.String())
}

func TestContextExpandMarkdownPreservesBlockedDeltaWithCompleteBase(t *testing.T) {
	for _, scenario := range []string{"remaining_budget", "depth"} {
		t.Run(scenario, func(t *testing.T) {
			root, args := contextCommandFixture(t)
			var request contextretrieval.RetrievalRequest
			require.NoError(t, readContextCommandJSON(args[1], &request))
			request.Budget.MaxContextTokens = 32768
			if scenario == "depth" {
				request.Limits.MaxExpandDepth = 1
			}
			contextCommandJSON(t, root, "request.json", request)
			var prepared strings.Builder
			require.NoError(t, runContextPrepare(args, &prepared))
			var pack contextretrieval.ContextPack
			require.NoError(t, json.Unmarshal([]byte(prepared.String()), &pack))
			require.Equal(t, contextretrieval.Complete, pack.Status, "diagnostics: %+v", pack.UnresolvedQuestions)
			basePath := contextCommandJSON(t, root, "base.json", pack)
			expansion := contextretrieval.ExpandRequest{
				Facet:  contextretrieval.Facet{ID: "bounded-expansion", SourceIdentity: "repo", QueryKind: contextretrieval.QueryExact, Path: "main.go", ClaimType: contextretrieval.ImplementationBehavior},
				Reason: "Exercise bounded expansion",
			}
			expectedCode := "BUDGET_UNSATISFIED"
			if scenario == "remaining_budget" {
				expansion.RemainingBudget.MaxContextTokens = 1
			} else {
				firstPath := contextCommandJSON(t, root, "expand-first.json", expansion)
				var first strings.Builder
				require.NoError(t, runContextExpand(append(args, "--base-pack-json", basePath, "--expand-json", firstPath), &first))
				var delta contextretrieval.ContextDelta
				require.NoError(t, json.Unmarshal([]byte(first.String()), &delta))
				require.Equal(t, contextretrieval.Complete, delta.Status)
				pack = delta.Pack
				contextCommandJSON(t, root, "base.json", pack)
				expansion.Facet.ID = "depth-exceeded"
				expectedCode = "EXPAND_DEPTH_LIMIT"
			}
			expansionPath := contextCommandJSON(t, root, "expand.json", expansion)
			expandArgs := append(args, "--base-pack-json", basePath, "--expand-json", expansionPath, "--format", "markdown")
			var first, second strings.Builder
			require.NoError(t, runContextExpand(expandArgs, &first))
			require.NoError(t, runContextExpand(expandArgs, &second))
			require.Equal(t, first.String(), second.String())
			packStart := strings.Index(first.String(), "# Context pack")
			require.Positive(t, packStart)
			deltaMarkdown := first.String()[:packStart]
			require.Contains(t, deltaMarkdown, "# Context expansion\n\nStatus: **BLOCKED**")
			require.Contains(t, deltaMarkdown, expectedCode)
			require.Contains(t, deltaMarkdown, "Base digest: `"+pack.ContentDigest+"`")
			require.Contains(t, deltaMarkdown, "Content digest: `"+pack.ContentDigest+"`")
			require.Contains(t, first.String()[packStart:], "Status: **COMPLETE**")
		})
	}
}

func TestContextAnalysisFlagCompatibility(t *testing.T) {
	args := []string{"--request-json", "request.json", "--route-json", "route.json", "--root", "local:fixture=/tmp/fixture"}
	values, e := parseContextCommandFlags("context-prepare", args, false)
	if e != nil || values.analysisScope {
		t.Fatal("legacy default changed", e)
	}
	if _, e = parseContextCommandFlags("context-prepare", append(args, "--analysis-report-json", "new-report.json"), false); e == nil {
		t.Fatal("analysis output without opt-in accepted")
	}
	values, e = parseContextCommandFlags("context-prepare", append(args, "--analysis-scope", "--analysis-report-json", "new-report.json"), false)
	if e != nil || !values.analysisScope || values.analysisReportPath != "new-report.json" {
		t.Fatal("analysis flag binding", e)
	}
}
