package shardexecution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestSemanticREDRejectsMissingOrBuildOnlyFailure(t *testing.T) {
	red := domain.ShardREDEvidence{
		ExpectedFailure: "semantic RED: missing handler behavior", Semantic: true,
		TestPaths: []string{"internal/transport/http/availability_test.go"},
	}
	valid := domain.WorkspaceCheckResult{Command: "go test ./...", ExitCode: 1, Output: "--- FAIL: TestMissingBehavior\n    availability_test.go: semantic RED: missing handler behavior"}
	if err := ValidateSemanticRED(red, red.TestPaths, valid); err != nil {
		t.Fatalf("valid semantic RED was rejected: %v", err)
	}

	for name, result := range map[string]domain.WorkspaceCheckResult{
		"green":         {Command: "go test ./...", ExitCode: 0, Output: "ok"},
		"compile":       {Command: "go test ./...", ExitCode: 1, Output: "undefined: AvailabilityRequest"},
		"environment":   {Command: "go test ./...", ExitCode: 1, Output: "semantic RED: missing handler behavior\npermission denied"},
		"wrong command": {Command: "go vet ./...", ExitCode: 1, Output: "semantic RED: missing handler behavior"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSemanticRED(red, red.TestPaths, result); err == nil {
				t.Fatal("expected invalid RED evidence to be rejected")
			}
		})
	}
	if err := ValidateSemanticRED(red, []string{"internal/transport/http/unrelated_test.go"}, valid); err == nil {
		t.Fatal("expected changed test path mismatch to be rejected")
	}
	if err := ValidateSemanticRED(domain.ShardREDEvidence{Semantic: true, TestPaths: red.TestPaths}, red.TestPaths, valid); err == nil {
		t.Fatal("expected missing failure description to be rejected")
	}
}

func TestBarrierRequiresVerifiedRedGreenAndCommitForEveryWorker(t *testing.T) {
	base := strings.Repeat("a", 40)
	now := time.Now().UTC()
	attempt := verifiedAttempt("http", base, now, now.Add(time.Second))
	result := EvaluateBarrier([]string{"http", "usecase"}, []domain.ShardAttempt{attempt}, base)
	if result.State != domain.ShardBarrierBlocked || len(result.Reasons) == 0 {
		t.Fatalf("missing shard unexpectedly passed barrier: %+v", result)
	}
	other := verifiedAttempt("usecase", base, now, now.Add(2*time.Second))
	result = EvaluateBarrier([]string{"http", "usecase"}, []domain.ShardAttempt{attempt, other}, base)
	if result.State != domain.ShardBarrierReady {
		t.Fatalf("fully verified siblings did not pass barrier: %+v", result)
	}

	other.Red = nil
	result = EvaluateBarrier([]string{"http", "usecase"}, []domain.ShardAttempt{attempt, other}, base)
	if result.State != domain.ShardBarrierBlocked {
		t.Fatal("worker without semantic RED unexpectedly passed barrier")
	}
	other = verifiedAttempt("usecase", strings.Repeat("b", 40), now, now.Add(time.Second))
	result = EvaluateBarrier([]string{"http", "usecase"}, []domain.ShardAttempt{attempt, other}, base)
	if result.State != domain.ShardBarrierReplanRequired {
		t.Fatal("worker on stale baseline unexpectedly passed barrier")
	}
}

func TestParallelismEvidenceCountsOverlappingIntervals(t *testing.T) {
	start := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	attempts := []domain.ShardAttempt{
		{ShardID: "one", WorkerStartedAt: start, FinishedAt: timePointer(start.Add(30 * time.Second))},
		{ShardID: "two", WorkerStartedAt: start.Add(10 * time.Second), FinishedAt: timePointer(start.Add(40 * time.Second))},
		{ShardID: "three", WorkerStartedAt: start.Add(40 * time.Second), FinishedAt: timePointer(start.Add(50 * time.Second))},
	}
	if got := MaxSimultaneousWorkers(attempts); got != 2 {
		t.Fatalf("max concurrency=%d, want 2", got)
	}
	if got := MaxSimultaneousWorkers([]domain.ShardAttempt{
		{ShardID: "one", WorkerStartedAt: start, FinishedAt: timePointer(start.Add(time.Second))},
		{ShardID: "two", WorkerStartedAt: start.Add(time.Second), FinishedAt: timePointer(start.Add(2 * time.Second))},
	}); got != 1 {
		t.Fatalf("touching intervals counted as overlap: %d", got)
	}
}

func TestAssemblyOrderUsesRouteAndStableShardID(t *testing.T) {
	attempts := []domain.ShardAttempt{
		{ShardID: "z", WorkPackage: domain.WorkPackage{Route: "backend.usecase", ShardID: "z"}},
		{ShardID: "h", WorkPackage: domain.WorkPackage{Route: "backend.transport.http", ShardID: "h"}},
		{ShardID: "a", WorkPackage: domain.WorkPackage{Route: "backend.usecase", ShardID: "a"}},
	}
	ordered := AssemblyOrder(attempts)
	if ordered[0].ShardID != "h" || ordered[1].ShardID != "a" || ordered[2].ShardID != "z" {
		t.Fatalf("assembly order is not route/shard deterministic: %#v", ordered)
	}
}

func TestAssemblyRejectsBeforeEveryShardIsVerified(t *testing.T) {
	base := strings.Repeat("a", 40)
	started := time.Now().UTC()
	prepared := PreparedFanout{
		BaselineCommit: base,
		Workers: []PreparedShard{
			{WorkPackage: domain.WorkPackage{ShardID: "http"}},
			{WorkPackage: domain.WorkPackage{ShardID: "usecase"}},
		},
	}
	ready := domain.ShardFanoutExecution{
		Barrier: domain.ShardBarrierReady, Composition: "SKIPPED_NOT_REQUIRED",
		Attempts: []domain.ShardAttempt{verifiedAttempt("http", base, started, started.Add(time.Second)),
			verifiedAttempt("usecase", base, started, started.Add(2*time.Second))},
	}
	if err := validateAssemblyPreconditions(prepared, ready); err != nil {
		t.Fatalf("fully verified fan-out rejected assembly: %v", err)
	}

	ready.Attempts[1].Status = domain.ShardAttemptFailed
	if err := validateAssemblyPreconditions(prepared, ready); err == nil {
		t.Fatal("unverified shard incorrectly allowed integration assembly")
	}
	ready.Attempts = ready.Attempts[:1]
	if err := validateAssemblyPreconditions(prepared, ready); err == nil {
		t.Fatal("missing shard incorrectly allowed integration assembly")
	}
}

func TestWorkerScopeRejectsFrozenSiblingAndCompositionPaths(t *testing.T) {
	packageValue := domain.WorkPackage{WriteScope: domain.ShardWriteScope{
		Allow: []string{"internal/transport/http/**", "internal/transport/http/availability_test.go"},
		Deny:  []string{"internal/usecase/**"}, CompositionOnly: []string{"cmd/**"},
	}, Contracts: domain.WorkPackageContracts{ReadOnlyPaths: []string{"internal/usecase/repository_port.go"}}}
	for _, file := range []string{
		"internal/transport/http/availability.go",
		"internal/usecase/repository_port.go",
		"internal/usecase/availability.go",
		"cmd/server/main.go",
	} {
		wantAllowed := file == "internal/transport/http/availability.go"
		if got := scopeAllowsFiles(packageValue, []string{file}); got != wantAllowed {
			t.Fatalf("scopeAllowsFiles(%q)=%t, want %t", file, got, wantAllowed)
		}
	}
}

func TestConnectedSourcePreflightExplainsDirtyHeadAndInspectionFailures(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	clean := domain.RepositorySource{HeadCommit: head}
	if reason, err := validateConnectedSource(clean, head, nil); err != nil || reason != "" {
		t.Fatalf("clean source rejected: reason=%q err=%v", reason, err)
	}
	for name, source := range map[string]domain.RepositorySource{
		"dirty":         {HeadCommit: head, IsDirty: true},
		"head mismatch": {HeadCommit: strings.Repeat("b", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			reason, err := validateConnectedSource(source, head, nil)
			if err == nil || reason == "" {
				t.Fatalf("source drift was not explained: reason=%q err=%v", reason, err)
			}
		})
	}
	inspectFailure := errors.New("repository path cannot be inspected")
	reason, err := validateConnectedSource(domain.RepositorySource{}, head, inspectFailure)
	if !errors.Is(err, inspectFailure) || !strings.Contains(reason, "inspection failed") {
		t.Fatalf("inspection failure detail was lost: reason=%q err=%v", reason, err)
	}
}

func TestSourcePreflightStateSemantics(t *testing.T) {
	const base = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const drifted = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	newStore := func() *sourcePreflightStoreFake {
		return &sourcePreflightStoreFake{
			baseline: domain.ContractBaseline{ID: "baseline-1", PlanID: "plan-1", ExecutionState: domain.ContractBaselineFrozen,
				ContractBaselineCommit: strings.Repeat("c", 40), RepositoryRevision: base,
				Validation: domain.ContractBaselineValidation{Passed: true}},
			readiness: domain.FanoutReadinessEvidence{PlanID: "plan-1", State: domain.FanoutReadinessReady,
				RecordedAt: now},
		}
	}
	project := domain.Project{ID: "project-1", SourceIdentity: "local:/repo/.git", HeadCommit: base}
	path := "/canary/availability-service"
	project.LocalPath = &path

	t.Run("actual HEAD drift invalidates baseline and readiness", func(t *testing.T) {
		store := newStore()
		evidence, err := connectedSourcePreflight(project, store.baseline,
			domain.RepositorySource{LocalPath: path, Identity: project.SourceIdentity, HeadCommit: drifted}, nil, now)
		if err == nil || evidence.InspectionStatus != domain.SourcePreflightDrift || evidence.ActualRevision != drifted {
			t.Fatalf("HEAD drift was not represented: evidence=%+v err=%v", evidence, err)
		}
		if err := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, evidence); err != nil {
			t.Fatal(err)
		}
		if store.baseline.ExecutionState != domain.ContractBaselineInvalidated || store.readiness.State != domain.FanoutReadinessBlocked {
			t.Fatalf("proven HEAD drift did not invalidate/block: baseline=%s readiness=%s", store.baseline.ExecutionState, store.readiness.State)
		}
		if !store.fanoutExists || len(store.fanout.SourcePreflights) != 1 || store.fanout.SourcePreflights[0].ReasonCode != "HEAD_REVISION_MISMATCH" {
			t.Fatalf("drift evidence was not persisted: %+v", store.fanout)
		}
	})

	t.Run("dirty checkout and repository identity drift invalidate baseline", func(t *testing.T) {
		for name, source := range map[string]domain.RepositorySource{
			"dirty working tree":  {LocalPath: path, Identity: project.SourceIdentity, HeadCommit: base, IsDirty: true},
			"repository identity": {LocalPath: path, Identity: "local:/different/repository/.git", HeadCommit: base},
		} {
			t.Run(name, func(t *testing.T) {
				store := newStore()
				evidence, err := connectedSourcePreflight(project, store.baseline, source, nil, now)
				if err == nil || evidence.InspectionStatus != domain.SourcePreflightDrift || evidence.ActualStatus == "UNKNOWN" {
					t.Fatalf("proven source drift was not represented: evidence=%+v err=%v", evidence, err)
				}
				if err := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, evidence); err != nil {
					t.Fatal(err)
				}
				if store.baseline.ExecutionState != domain.ContractBaselineInvalidated || store.readiness.State != domain.FanoutReadinessBlocked {
					t.Fatalf("proven source drift did not invalidate/block: baseline=%s readiness=%s", store.baseline.ExecutionState, store.readiness.State)
				}
			})
		}
	})

	t.Run("contract hash drift invalidates baseline and readiness", func(t *testing.T) {
		store := newStore()
		evidence := domain.SourcePreflightEvidence{RepositoryPath: path, ProjectID: project.ID,
			ExpectedRevision: base, ActualRevision: base, ExpectedClean: true, ActualStatus: "CLEAN",
			ContractBaselineCommit: store.baseline.ContractBaselineCommit,
			InspectionStatus:       domain.SourcePreflightVerified, RecordedAt: now}
		evidence = contractBaselinePreflight(evidence, domain.ContractBaselineValidation{Passed: false,
			Reasons: []string{"persisted file hash does not match committed content: repository_port.go"}})
		if evidence.InspectionStatus != domain.SourcePreflightDrift || evidence.ReasonCode != "CONTRACT_HASH_MISMATCH" {
			t.Fatalf("contract hash mismatch was not classified as proven drift: %+v", evidence)
		}
		if err := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, evidence); err != nil {
			t.Fatal(err)
		}
		if store.baseline.ExecutionState != domain.ContractBaselineInvalidated || store.readiness.State != domain.FanoutReadinessBlocked {
			t.Fatalf("contract hash drift did not invalidate/block: baseline=%s readiness=%s", store.baseline.ExecutionState, store.readiness.State)
		}
	})

	t.Run("contract verification read failure preserves frozen baseline", func(t *testing.T) {
		store := newStore()
		evidence := contractBaselinePreflight(domain.SourcePreflightEvidence{RepositoryPath: path, ProjectID: project.ID,
			ExpectedRevision: base, ActualRevision: base, ExpectedClean: true, ActualStatus: "CLEAN",
			ContractBaselineCommit: store.baseline.ContractBaselineCommit, RecordedAt: now},
			domain.ContractBaselineValidation{Passed: false, InspectionFailed: true,
				Reasons: []string{"cannot inspect contract commit diff: temporary I/O error"}})
		if evidence.InspectionStatus != domain.SourcePreflightInspectionFailed {
			t.Fatalf("contract verification read failure was classified as drift: %+v", evidence)
		}
		if err := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, evidence); err != nil {
			t.Fatal(err)
		}
		if store.baseline.ExecutionState != domain.ContractBaselineFrozen || store.readiness.State != domain.FanoutReadinessReady {
			t.Fatalf("contract verification read failure changed lifecycle state: baseline=%s readiness=%s", store.baseline.ExecutionState, store.readiness.State)
		}
	})

	t.Run("Git status inspection failure blocks attempt but preserves frozen baseline for retry", func(t *testing.T) {
		store := newStore()
		inspectErr := errors.New("temporary repository read failure")
		evidence, err := connectedSourcePreflight(project, store.baseline, domain.RepositorySource{}, inspectErr, now)
		if err == nil || evidence.InspectionStatus != domain.SourcePreflightInspectionFailed || evidence.ActualStatus != "UNKNOWN" {
			t.Fatalf("inspection failure was not represented: evidence=%+v err=%v", evidence, err)
		}
		if applyErr := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, evidence); applyErr != nil {
			t.Fatal(applyErr)
		}
		if store.baseline.ExecutionState != domain.ContractBaselineFrozen || store.readiness.State != domain.FanoutReadinessReady {
			t.Fatalf("inspection failure changed lifecycle state: baseline=%s readiness=%s", store.baseline.ExecutionState, store.readiness.State)
		}
		if !store.fanoutExists || store.fanout.Barrier != domain.ShardBarrierBlocked || len(store.fanout.SourcePreflights) != 1 {
			t.Fatalf("inspection failure was not persisted as a blocked attempt: %+v", store.fanout)
		}

		clean, retryErr := connectedSourcePreflight(project, store.baseline,
			domain.RepositorySource{LocalPath: path, Identity: project.SourceIdentity, HeadCommit: base}, nil, now.Add(time.Second))
		if retryErr != nil || clean.InspectionStatus != domain.SourcePreflightVerified {
			t.Fatalf("successful source inspection retry remained blocked: evidence=%+v err=%v", clean, retryErr)
		}
		if applyErr := applySourcePreflight(context.Background(), store, store.baseline, &store.readiness, clean); applyErr != nil {
			t.Fatal(applyErr)
		}
		if store.baseline.ExecutionState != domain.ContractBaselineFrozen || store.readiness.State != domain.FanoutReadinessReady ||
			len(store.fanout.SourcePreflights) != 2 || store.fanout.SourcePreflights[1].InspectionStatus != domain.SourcePreflightVerified {
			t.Fatalf("successful retry did not retain baseline and append evidence: baseline=%+v readiness=%+v fanout=%+v", store.baseline, store.readiness, store.fanout)
		}
		history, historyErr := loadSourcePreflightHistory(context.Background(), store, store.baseline, []domain.SourcePreflightEvidence{clean})
		if historyErr != nil || len(history) != 2 || history[0].InspectionStatus != domain.SourcePreflightInspectionFailed {
			t.Fatalf("fan-out preparation would lose the failed inspection history: history=%+v err=%v", history, historyErr)
		}
	})
}

type sourcePreflightStoreFake struct {
	baseline     domain.ContractBaseline
	readiness    domain.FanoutReadinessEvidence
	fanout       domain.ShardFanoutExecution
	fanoutExists bool
}

func (f *sourcePreflightStoreFake) SaveContractBaseline(_ context.Context, baseline domain.ContractBaseline) error {
	f.baseline = baseline
	return nil
}

func (f *sourcePreflightStoreFake) SaveFanoutReadiness(_ context.Context, readiness domain.FanoutReadinessEvidence) error {
	f.readiness = readiness
	return nil
}

func (f *sourcePreflightStoreFake) GetShardFanout(_ context.Context, _ string) (domain.ShardFanoutExecution, error) {
	if !f.fanoutExists {
		return domain.ShardFanoutExecution{}, domain.ErrNotFound
	}
	return f.fanout, nil
}

func (f *sourcePreflightStoreFake) SaveShardFanout(_ context.Context, execution domain.ShardFanoutExecution) error {
	f.fanout = execution
	f.fanoutExists = true
	return nil
}

func TestIntegrationVerifierVerdictCannotOverrideMechanicalFailure(t *testing.T) {
	mechanical := domain.ShardFanoutExecution{
		State: "INTEGRATION_MECHANICALLY_VERIFIED", IntegrationVerification: []byte(`{"passed":true}`),
	}
	for verdict, wantState := range map[string]string{
		"PASS": "INTEGRATION_VERIFIED", "REJECT": "INTEGRATION_REJECTED", "REPLAN_REQUIRED": "REPLAN_REQUIRED",
	} {
		updated, err := applyIntegrationReviewerDecision(mechanical, IntegrationReviewerDecision{Verdict: verdict})
		if err != nil || updated.State != wantState {
			t.Fatalf("verdict %s produced state %s (err=%v), want %s", verdict, updated.State, err, wantState)
		}
	}
	failed := domain.ShardFanoutExecution{State: "INTEGRATION_VERIFICATION_FAILED"}
	if _, err := applyIntegrationReviewerDecision(failed, IntegrationReviewerDecision{Verdict: "PASS"}); err == nil {
		t.Fatal("independent PASS incorrectly overrode failed mechanical verification")
	}
}

func verifiedAttempt(id, baseline string, started, finished time.Time) domain.ShardAttempt {
	finishedCopy := finished
	return domain.ShardAttempt{
		ShardID: id, BaselineCommit: baseline,
		WorkPackage: domain.WorkPackage{ExecutionBase: domain.ShardExecutionBase{Revision: baseline}},
		CommitSHA:   strings.Repeat("c", 40), Status: domain.ShardAttemptVerified,
		Red:             &domain.ShardREDEvidence{Semantic: true},
		Green:           &domain.ShardGreenEvidence{Passed: true},
		WorkerStartedAt: started, FinishedAt: &finishedCopy,
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestNextShardAttemptPreservesFailedHistoryAndIdentity(t *testing.T) {
	value := domain.WorkPackage{ShardID: "http", ExecutionBase: domain.ShardExecutionBase{Revision: strings.Repeat("a", 40)}}
	finished := time.Now()
	previous := domain.ShardAttempt{ShardID: value.ShardID, AttemptNumber: 1, WorkPackage: value, BaselineCommit: value.ExecutionBase.Revision, Status: domain.ShardAttemptFailed, FinishedAt: &finished}
	if number, err := nextShardAttemptNumber(value, []domain.ShardAttempt{previous}); err != nil || number != 2 {
		t.Fatalf("failed attempt retry = %d, %v", number, err)
	}
	for _, status := range []domain.ShardAttemptStatus{domain.ShardAttemptRunning, domain.ShardAttemptVerified, domain.ShardAttemptReplan, domain.ShardAttemptContractChange} {
		invalid := previous
		invalid.Status = status
		if _, err := nextShardAttemptNumber(value, []domain.ShardAttempt{invalid}); err == nil {
			t.Fatalf("retry of %s allowed", status)
		}
	}
	changed := value
	changed.Route = "other"
	if _, err := nextShardAttemptNumber(changed, []domain.ShardAttempt{previous}); err == nil {
		t.Fatal("changed package accepted")
	}
	if previous.AttemptNumber != 1 {
		t.Fatal("historical attempt rewritten")
	}
}

func TestWorkerPhaseSchemaRequiresEveryDeclaredREDProperty(t *testing.T) {
	red := phaseSchema()["properties"].(map[string]any)["red"].(map[string]any)
	required := map[string]bool{}
	for _, key := range red["required"].([]string) {
		required[key] = true
	}
	for key := range red["properties"].(map[string]any) {
		if !required[key] {
			t.Errorf("strict response schema omits required RED property %s", key)
		}
	}
}

func TestWorkerSchemasDoNotAskAgentToInventRunnerThreadIdentity(t *testing.T) {
	for name, schema := range map[string]map[string]any{"RED": phaseSchema(), "implementation": claimSchema()} {
		if _, present := schema["properties"].(map[string]any)["worker_thread"]; present {
			t.Errorf("%s schema asks agent to invent runner-assigned thread ID", name)
		}
	}
}

func TestRunnerThreadIdentityUsesCallbackAndResponse(t *testing.T) {
	for _, valid := range [][3]string{{"", "real", "real"}, {"real", "real", "real"}} {
		if err := validateRunnerThread(valid[0], valid[1], valid[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range [][3]string{{"", "", "real"}, {"real", "other", "other"}, {"", "real", "other"}, {"", "", ""}} {
		if err := validateRunnerThread(invalid[0], invalid[1], invalid[2]); err == nil {
			t.Fatalf("invalid runner identity accepted: %v", invalid)
		}
	}
}

func TestREDPromptRequiresObservableBehaviorAndRealPersistenceBoundary(t *testing.T) {
	prompt := redPrompt(domain.WorkPackage{Route: "backend.infrastructure.persistence"})
	for _, requirement := range []string{"observable output", "interface existence alone", "real PostgreSQL fixture", "seed", "query result"} {
		if !strings.Contains(prompt, requirement) {
			t.Errorf("RED prompt missing %q", requirement)
		}
	}
}

func TestImplementationClaimIncludesPreservedREDDiffWithoutInventingPhaseEdits(t *testing.T) {
	red := []string{"internal/usecase/availability_test.go"}
	production := []string{"internal/usecase/availability.go"}
	full := append(append([]string(nil), red...), production...)
	if !implementationClaimMatches(production, full, red) {
		t.Fatal("implementation phase claim rejected because preserved RED tests remain in baseline diff")
	}
	if implementationClaimMatches(production, append(full, "internal/transport/http/sibling.go"), red) {
		t.Fatal("unclaimed sibling edit accepted")
	}
	if implementationClaimMatches(production, production, red) {
		t.Fatal("deleted RED test accepted")
	}
}

func TestSnapshotREDDeltaPreservesSetupProduction(t *testing.T) {
	before := domain.WorkspaceSnapshot{Files: map[string]string{"adapter.go": "shell", "port.go": "frozen"}}
	after := domain.WorkspaceSnapshot{Files: map[string]string{"adapter.go": "shell", "port.go": "frozen", "adapter_test.go": "red"}}
	if !samePaths(snapshotChangedFiles(before, after), []string{"adapter_test.go"}) {
		t.Fatal("setup production attributed to RED")
	}
	after.Files["adapter.go"] = "behavior"
	if pathsWithin(snapshotChangedFiles(before, after), []string{"adapter_test.go"}) {
		t.Fatal("RED production behavior accepted")
	}
	delete(after.Files, "port.go")
	if !contains(snapshotChangedFiles(before, after), "port.go") {
		t.Fatal("deleted contract hidden")
	}
}

func TestShardExecutionPhaseHistory(t *testing.T) {
	attempt := domain.ShardAttempt{}
	now := time.Now()
	phases := []domain.ShardExecutionPhase{domain.ShardPhasePrepared, domain.ShardPhaseREDSetup, domain.ShardPhaseREDVerified, domain.ShardPhaseImplementing, domain.ShardPhaseGREENVerified, domain.ShardPhaseVerified}
	for _, phase := range phases {
		setShardPhase(&attempt, phase, now)
	}
	if attempt.Phase != domain.ShardPhaseVerified || len(attempt.PhaseHistory) != len(phases) {
		t.Fatal("phase evidence lost")
	}
	for i, event := range attempt.PhaseHistory {
		if event.Phase != phases[i] || !event.RecordedAt.Equal(now) {
			t.Fatal("phase history changed")
		}
	}
}

func TestNextShardAttemptIgnoresFinishedOlderBaseline(t *testing.T) {
	value := domain.WorkPackage{ShardID: "persistence", ExecutionBase: domain.ShardExecutionBase{Revision: "new"}}
	older := domain.ShardAttempt{ShardID: value.ShardID, AttemptNumber: 4, BaselineCommit: "old", Status: domain.ShardAttemptBlocked, FinishedAt: timePointer(time.Now())}
	if n, err := nextShardAttemptNumber(value, []domain.ShardAttempt{older}); err != nil || n != 5 {
		t.Fatalf("retry=%d err=%v", n, err)
	}
	older.FinishedAt = nil
	if _, err := nextShardAttemptNumber(value, []domain.ShardAttempt{older}); err == nil {
		t.Fatal("unfinished older baseline allowed")
	}
}

func TestPersistenceSetupIsOptionalForExistingConcreteSeam(t *testing.T) {
	root := t.TempDir()
	wp := domain.WorkPackage{WriteScope: domain.ShardWriteScope{Allow: []string{"internal/infrastructure/persistence/postgres/**"}}}
	write := func(path, source string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/domain/repository_port.go", `package domain; import "context"; type RepositoryPort interface { Find(context.Context,string)([]Item,error) }`)
	if persistenceConcreteSeamExists(root, wp) {
		t.Fatal("absent method seam found")
	}
	write("internal/infrastructure/persistence/postgres/availability.go", setupShell)
	if !persistenceConcreteSeamExists(root, wp) {
		t.Fatal("existing concrete frozen seam missed")
	}
	write("internal/infrastructure/persistence/postgres/availability.go", strings.Replace(setupShell, "id string", "id int", 1))
	if persistenceConcreteSeamExists(root, wp) {
		t.Fatal("wrong concrete signature accepted")
	}
}

func TestWorkerFrozenWorkspaceRejectsChangedHEADAndContracts(t *testing.T) {
	workspace := domain.TaskWorkspace{Path: t.TempDir(), BaseCommit: "frozen"}
	state := domain.WorkspaceState{HeadCommit: "worker-commit", ChangedFiles: []string{"allowed_test.go"}}
	if validateWorkerFrozenHEAD(workspace, state) == nil {
		t.Fatal("dirty worker commit bypassed frozen HEAD")
	}
	state.HeadCommit = "frozen"
	if err := validateWorkerFrozenHEAD(workspace, state); err != nil {
		t.Fatal(err)
	}
	source := []byte("package domain\n")
	baseline := domain.ContractBaseline{Files: []domain.ContractBaselineFile{{Path: "port.go", SHA256: contentHash(source)}}}
	if err := os.WriteFile(filepath.Join(workspace.Path, "port.go"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWorkingFrozenFiles(context.Background(), nil, workspace, baseline); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "port.go"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if verifyWorkingFrozenFiles(context.Background(), nil, workspace, baseline) == nil {
		t.Fatal("uncommitted frozen contract edit accepted")
	}
	if err := os.Remove(filepath.Join(workspace.Path, "port.go")); err != nil {
		t.Fatal(err)
	}
	if verifyWorkingFrozenFiles(context.Background(), nil, workspace, baseline) == nil {
		t.Fatal("missing frozen contract accepted")
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "other.go"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.go", filepath.Join(workspace.Path, "port.go")); err != nil {
		t.Fatal(err)
	}
	if verifyWorkingFrozenFiles(context.Background(), nil, workspace, baseline) == nil {
		t.Fatal("symlink contract accepted")
	}
	baseline.Files[0].Path = "../other.go"
	if verifyWorkingFrozenFiles(context.Background(), nil, workspace, baseline) == nil {
		t.Fatal("escaping frozen path accepted")
	}
}

func TestReplacementBaselineStartsFreshSourcePreflightHistory(t *testing.T) {
	store := &sourcePreflightStoreFake{fanoutExists: true, fanout: domain.ShardFanoutExecution{ContractBaselineID: "old", BaselineCommit: "old-commit", SourcePreflights: []domain.SourcePreflightEvidence{{ReasonCode: "historical-drift"}}}}
	baseline := domain.ContractBaseline{ID: "new", PredecessorBaselineID: "old", ContractBaselineCommit: "new-commit"}
	fresh := []domain.SourcePreflightEvidence{{ReasonCode: "fresh-verified"}}
	history, err := loadSourcePreflightHistory(context.Background(), store, baseline, fresh)
	if err != nil || len(history) != 1 || history[0].ReasonCode != "fresh-verified" {
		t.Fatalf("replacement inherited old history or blocked: %+v %v", history, err)
	}
	baseline.PredecessorBaselineID = "unrelated"
	if _, err := loadSourcePreflightHistory(context.Background(), store, baseline, fresh); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unrelated replacement allowed: %v", err)
	}
	baseline.PredecessorBaselineID = "old"
	baseline.ContractBaselineCommit = "old-commit"
	if _, err := loadSourcePreflightHistory(context.Background(), store, baseline, fresh); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same commit replacement allowed: %v", err)
	}
	if store.fanout.SourcePreflights[0].ReasonCode != "historical-drift" {
		t.Fatal("historical evidence rewritten")
	}
}

func TestHealthyReplacementPreflightPreservesRetiredFanout(t *testing.T) {
	store := &sourcePreflightStoreFake{fanoutExists: true, fanout: domain.ShardFanoutExecution{ContractBaselineID: "old", BaselineCommit: "old-commit", SourcePreflights: []domain.SourcePreflightEvidence{{ReasonCode: "historical-drift"}}}}
	baseline := domain.ContractBaseline{ID: "new", PredecessorBaselineID: "old", ContractBaselineCommit: "new-commit", ExecutionState: domain.ContractBaselineFrozen}
	readiness := domain.FanoutReadinessEvidence{}
	evidence := domain.SourcePreflightEvidence{InspectionStatus: domain.SourcePreflightVerified, ReasonCode: "fresh"}
	if err := applySourcePreflight(context.Background(), store, baseline, &readiness, evidence); err != nil {
		t.Fatal(err)
	}
	if len(store.fanout.SourcePreflights) != 1 || store.fanout.SourcePreflights[0].ReasonCode != "historical-drift" {
		t.Fatal("replacement contaminated retired fanout")
	}
	baseline.PredecessorBaselineID = "unrelated"
	if err := applySourcePreflight(context.Background(), store, baseline, &readiness, evidence); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unrelated baseline appended: %v", err)
	}
	baseline.PredecessorBaselineID = "old"
	evidence.InspectionStatus = domain.SourcePreflightInspectionFailed
	if err := applySourcePreflight(context.Background(), store, baseline, &readiness, evidence); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("blocked replacement appended: %v", err)
	}
}

func TestCandidateREDUsesOrchestratorResultOnly(t *testing.T) {
	candidate := domain.ShardREDEvidence{ExpectedFailure: "semantic RED: availability behavior absent", TestPaths: []string{"availability_test.go"}, Semantic: false, ObservedFailure: ""}
	result := domain.WorkspaceCheckResult{Command: "go test ./...", ExitCode: 1, Output: "--- FAIL: TestAvailability\n semantic RED: availability behavior absent"}
	if err := ValidateSemanticRED(candidate, candidate.TestPaths, result); err != nil {
		t.Fatalf("orchestrator semantic result rejected: %v", err)
	}
	for _, failure := range []string{"TEST_DATABASE_URL is required", "TEST_DATABASE_URL required", "dial tcp: connection refused", "fixture setup failed", "migration failed"} {
		result.Output = "semantic RED: availability behavior absent\n" + failure
		if ValidateSemanticRED(candidate, candidate.TestPaths, result) == nil {
			t.Fatalf("environment failure accepted: %s", failure)
		}
	}
	prompt := redPrompt(domain.WorkPackage{})
	for _, rule := range []string{"Do not run tests", "Docker", "initdb", "semantic=false", "orchestrator"} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("RED candidate prompt lacks %q", rule)
		}
	}
}
