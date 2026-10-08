package shardexecution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const compositionBootstrap = `func main(){
 pool,err:=pgxpool.New(context.Background(),os.Getenv("DATABASE_URL"))
 if err!=nil {log.Fatal(err)}
 defer pool.Close()
 address:=os.Getenv("HTTP_ADDR")
 if address=="" {address=":8080"}
 log.Fatal(http.ListenAndServe(address,BuildAvailabilityRouter(pool)))
}`

func TestCompositionBootstrapAllowsOnlyBoundedMainEnvironmentFallback(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	wp, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	path := "cmd/availability-service/main.go"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0755); err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(compositionWiredSource, `"net/http"`, `"net/http";"os";"context";"log"`, 1)
	source = strings.Replace(source, "func main(){}", compositionBootstrap, 1)
	for _, tc := range []struct {
		kind, source string
		valid        bool
	}{
		{"bounded main bootstrap", source, true},
		{"different environment", strings.Replace(source, `os.Getenv("HTTP_ADDR")`, `os.Getenv("RESOURCE_ID")`, 1), false},
		{"different variable assignment", strings.Replace(source, `address=":8080"`, `other=":8080"`, 1), false},
		{"arbitrary conditional body", strings.Replace(source, `address=":8080"`, `address=":8080";log.Fatal("extra")`, 1), false},
		{"computed fallback", strings.Replace(source, `address=":8080"`, `address=os.Getenv("OTHER")`, 1), false},
		{"different comparison", strings.Replace(source, `address==""`, `address==":9090"`, 1), false},
		{"else branch", strings.Replace(source, `if address=="" {address=":8080"}`, `if address=="" {address=":8080"} else {address=":9090"}`, 1), false},
		{"lost provenance", strings.Replace(source, `if address==""`, `address="fixed";if address==""`, 1), false},
		{"router conditional", strings.Replace(source, `repository:=`, `address:=os.Getenv("HTTP_ADDR");if address=="" {address=":8080"};repository:=`, 1), false},
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(tc.source), 0644); err != nil {
			t.Fatal(err)
		}
		err := ValidateCompositionWiring(root, wp, []string{path}, false)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: err=%v", tc.kind, err)
		}
	}
}

func compositionVerificationRecoveryFixture() domain.ShardFanoutExecution {
	now := time.Now()
	return domain.ShardFanoutExecution{State: "COMPOSITION_BLOCKED", Composition: "COMPOSITION_BLOCKED", BarrierReasons: []string{"OUT_OF_SCOPE_CHANGE_REQUIRED: composition only allows startup error checks"}, CompositionAttempt: &domain.CompositionAttempt{ID: "same-attempt", WorkerThreadID: "same-thread", Model: "actual", ReasoningEffort: "low", StartedAt: now, FinishedAt: &now, Phase: domain.ShardPhaseImplementing, Status: domain.ShardAttemptBlocked, Blockers: []string{"OUT_OF_SCOPE_CHANGE_REQUIRED"}, Red: &domain.ShardREDEvidence{Semantic: true, RecordedAt: now, Command: "go test ./... -count=1", TestPaths: []string{"main_test.go"}}, REDSetup: &domain.ShardREDSetupEvidence{MechanicalReview: "PASS"}, FixtureReplay: &domain.CompositionFixtureReplay{}, WorkPackage: domain.CompositionWorkPackage{Verification: domain.WorkPackageVerification{TestPaths: []string{"main_test.go"}}}}}
}

func TestCompositionVerificationOnlyRecoveryRejectsAllOtherFailureStates(t *testing.T) {
	original := compositionVerificationRecoveryFixture()
	before := compactJSON(original)
	if !compositionWiringVerificationRecoveryAllowed(original) {
		t.Fatal("exact mechanical false positive not recoverable")
	}
	for _, mutate := range []func(*domain.ShardFanoutExecution){
		func(e *domain.ShardFanoutExecution) {
			e.BarrierReasons = []string{"OUT_OF_SCOPE_CHANGE_REQUIRED: arbitrary failure"}
		},
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.Blockers = []string{"CONTRACT_CHANGE_REQUIRED"}
		},
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Green = &domain.ShardGreenEvidence{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Implementation = &domain.WorkerResult{} },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.CommitSHA = "commit" },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.Red = nil },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.FixtureReplay = nil },
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.REDSetup = nil },
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.BudgetBlocker = &domain.CompositionBudgetBlocker{}
		},
		func(e *domain.ShardFanoutExecution) {
			e.CompositionAttempt.RecoveryHistory = []domain.CompositionRecoveryEvidence{{}}
		},
		func(e *domain.ShardFanoutExecution) { e.CompositionAttempt.WorkerThreadID = "" },
	} {
		changed := original
		copy := *original.CompositionAttempt
		changed.CompositionAttempt = &copy
		mutate(&changed)
		if compositionWiringVerificationRecoveryAllowed(changed) {
			t.Fatalf("unsafe recovery accepted %+v", changed)
		}
	}
	if compactJSON(original) != before {
		t.Fatal("eligibility mutated failed audit")
	}
}
