package shardexecution

import (
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const compositionEmptySource = `package main
import("net/http";"github.com/jackc/pgx/v5/pgxpool")
func main(){}
func BuildAvailabilityRouter(pool *pgxpool.Pool) http.Handler { return http.NewServeMux() }
`
const compositionWiredSource = `package main
import("net/http";"github.com/jackc/pgx/v5/pgxpool";"fixture/internal/infrastructure/persistence";"fixture/internal/usecase"; transport "fixture/internal/transport/http")
func main(){}
func BuildAvailabilityRouter(pool *pgxpool.Pool) http.Handler {
 repository:=persistence.NewAvailabilityStore(pool)
 application:=usecase.NewAvailabilityUsecase(repository)
 handler:=transport.NewAvailabilityHandler(application)
 mux:=http.NewServeMux()
 mux.Handle("/availability",handler)
 return mux
}
`
const compositionBehaviorTest = `package main
import("net/http/httptest";"testing"
 "time"
 "github.com/bemulima/agent-orchestrator/internal/domain";"fixture/internal/testsupport/postgresfixture")
func TestCompositionAvailabilityRoute(t *testing.T){
 pool:=postgresfixture.Open(t)
 pool.Exec(nil,"INSERT seed")
 router:=BuildAvailabilityRouter(pool)
 request:=httptest.NewRequest("GET","/availability",nil)
 response:=httptest.NewRecorder()
 router.ServeHTTP(response,request)
 if response.Code!=200 {t.Fatalf("semantic RED: availability route status=%d expected200",response.Code)}
}
`

func TestCompositionWiringAcceptsEmptySetupAndProvidedConstructorAssembly(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	wp, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	wp.WriteScope.CompositionOnly = []string{"cmd/"}
	path := "cmd/availability-service/main.go"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0755); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		source string
		setup  bool
	}{{compositionEmptySource, true}, {compositionWiredSource, false}, {strings.Replace(compositionWiredSource, "func main(){}", "func main(){ http.ListenAndServe(\":8080\", BuildAvailabilityRouter(nil)) }", 1), false}} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(value.source), 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCompositionWiring(root, wp, []string{path}, value.setup); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompositionWiringRejectsBehaviorQueriesAndFakeConstruction(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	wp, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	wp.WriteScope.CompositionOnly = []string{"cmd/"}
	path := "cmd/availability-service/main.go"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0755); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		strings.Replace(compositionWiredSource, "mux.Handle", "pool.Query", 1),
		strings.Replace(compositionWiredSource, "\"/availability\"", "\"/unapproved\"", 1),
		strings.Replace(compositionWiredSource, "return mux", "for {} ; return mux", 1),
		strings.Replace(compositionWiredSource, "return mux", "if pool == nil {} ; return mux", 1),
		strings.Replace(compositionWiredSource, "return mux", "return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){})", 1),
		strings.Replace(compositionWiredSource, "fixture/internal/usecase", "fixture/fake/usecase", 1),
		strings.Replace(compositionWiredSource, "repository:=", "repository:=FakeRepository(); unused:=", 1),
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCompositionWiring(root, wp, []string{path}, false); err == nil {
			t.Fatalf("accepted forbidden wiring: %s", value)
		}
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(strings.Replace(compositionEmptySource, "return http.NewServeMux()", "return nil", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompositionWiring(root, wp, []string{path}, true); err == nil {
		t.Fatal("setup did not require callable real empty mux")
	}
	if err := ValidateCompositionWiring(root, wp, []string{"internal/usecase/availability.go"}, false); err == nil {
		t.Fatal("accepted sibling layer edits")
	}
}

func TestCompositionRegressionRequiresActualRouterAndPostgresFixture(t *testing.T) {
	for _, value := range []struct {
		source string
		valid  bool
	}{
		{compositionBehaviorTest, true},
		{strings.Replace(compositionBehaviorTest, "BuildAvailabilityRouter(pool)", "fakeRouter(pool)", 1), false},
		{strings.Replace(compositionBehaviorTest, "postgresfixture.Open(t)", "fakePool(t)", 1), false},
		{strings.Replace(compositionBehaviorTest, "pool.Exec(nil,\"INSERT seed\")", "", 1), false},
		{compositionBehaviorTest + "\ntype FakeRepository struct{}", false},
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), "main_test.go", value.source, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = validateCompositionBehaviorTest(parsed)
		if (err == nil) != value.valid {
			t.Fatalf("valid=%v err=%v", value.valid, err)
		}
	}
}

func TestCompositionPacketPromptForbidsLayerRepairAndRequiresActual404Red(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	wp, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	prompt := compositionPrompt(wp, "red", nil)
	for _, required := range []string{"READ-ONLY", "No business logic", "real PostgreSQL", "actual HTTP404", "postgresfixture.Open", "pool.Exec", "Do not run tests", "DEPENDENCY_NOT_READY"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("missing %q", required)
		}
	}
	if compositionScopeAllows(wp, []string{"cmd/availability-service/main.go", "cmd/availability-service/main_test.go"}) == false {
		t.Fatal("approved exact scope rejected")
	}
	if compositionScopeAllows(wp, []string{"cmd/other/main.go"}) {
		t.Fatal("out-of-scope root allowed")
	}
}

func TestCompositionPhaseDecoderIgnoresAgentTimestampButRemainsStrict(t *testing.T) {
	for _, stamp := range []string{"", "agent-placeholder", "2026-10-04T00:00:00Z"} {
		value := `{"status":"completed","shard_id":"composition","baseline_revision":"frozen","changed_files":["main.go"],"blockers":[],"contract_change_requested":false,"red":{"recorded_at":"` + stamp + `","semantic":false,"test_paths":[],"command":"","expected_failure":"","observed_failure":""}}`
		decoded, err := decodeCompositionPhaseResult([]byte(value))
		if err != nil || decoded.Status != "completed" || !decoded.Red.RecordedAt.IsZero() {
			t.Fatalf("decode=%+v err=%v", decoded, err)
		}
	}
	for _, value := range []string{
		`{"unknown":"bad"}`,
		`{"red":{"recorded_at":"","unexpected":"bad"}}`,
		`{"red":{"recorded_at":3}}`,
		`{"contract_change_requested":"false"}`,
		`{} {}`,
	} {
		if _, err := decodeCompositionPhaseResult([]byte(value)); err == nil {
			t.Fatalf("accepted invalid wire %s", value)
		}
	}
}

func TestCompositionFormattingRecoveryRejectsSemanticOrBoundaryFailures(t *testing.T) {
	done := time.Now()
	original := domain.ShardFanoutExecution{State: "COMPOSITION_BLOCKED", Composition: "COMPOSITION_BLOCKED", BarrierReasons: []string{`TEST_BOUNDARY_MISSING: strict JSON decode: parsing time "" as "2006-01-02T15:04:05Z07:00": cannot parse "" as "2006"`}, CompositionAttempt: &domain.CompositionAttempt{ID: "same-attempt", WorkerThreadID: "same-thread", Model: "actual-model", ReasoningEffort: "low", StartedAt: done, PhaseHistory: []domain.ShardPhaseEvent{{Phase: domain.ShardPhasePrepared}, {Phase: domain.ShardPhaseREDSetup}}, Phase: domain.ShardPhaseREDSetup, Status: domain.ShardAttemptBlocked, FinishedAt: &done, Blockers: []string{"TEST_BOUNDARY_MISSING"}}}
	before := compactJSON(original)
	if !compositionFormattingRecoveryAllowed(original) {
		t.Fatal("exact wire failure not recoverable")
	}
	for _, mutate := range []func(*domain.ShardFanoutExecution){
		func(e *domain.ShardFanoutExecution) { e.State = "COMPOSITION_RUNNING" },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Red = &domain.ShardREDEvidence{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Green = &domain.ShardGreenEvidence{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Implementation = &domain.WorkerResult{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.CommitSHA = "commit" },
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.PhaseHistory = append(e.CompositionAttempt.PhaseHistory, domain.ShardPhaseEvent{Phase: domain.ShardPhaseImplementing})
		},
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.REDSetup = &domain.ShardREDSetupEvidence{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Phase = domain.ShardPhaseREDVerified },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.WorkerThreadID = "" },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Status = domain.ShardAttemptContractChange },
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.Blockers = []string{"CONTRACT_CHANGE_REQUIRED"}
		},
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.RecoveryHistory = []domain.CompositionRecoveryEvidence{{Reason: "already recovered"}}
		},
		func(e *domain.ShardFanoutExecution) { e.BarrierReasons = []string{"fixture failed"} },
		func(e *domain.ShardFanoutExecution) {
			e.BarrierReasons = []string{`strict JSON decode: parsing time "bad"`}
		},
		func(e *domain.ShardFanoutExecution) { e.BarrierReasons = append(e.BarrierReasons, "upstream bug") },
	} {
		changed := original
		attempt := *original.CompositionAttempt
		changed.CompositionAttempt = &attempt
		mutate(&changed)
		if compositionFormattingRecoveryAllowed(changed) {
			t.Fatalf("accepted unsafe recovery: %+v", changed)
		}
	}
	if compactJSON(original) != before {
		t.Fatal("classification mutated persisted audit")
	}
}

func TestCompositionRecoveryOnlyAdoptsStrictUnwiredProductionScaffold(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	wp, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	path := "cmd/availability-service/main.go"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(compositionEmptySource), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCompositionRecoveryScaffold(root, wp, []string{path}); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{}, {path, "cmd/availability-service/main_test.go"}, {"internal/usecase/availability.go"}} {
		if err := validateCompositionRecoveryScaffold(root, wp, paths); err == nil {
			t.Fatalf("accepted dirty recovery %v", paths)
		}
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(compositionWiredSource), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCompositionRecoveryScaffold(root, wp, []string{path}); err == nil {
		t.Fatal("accepted implemented behavior before RED")
	}
}
