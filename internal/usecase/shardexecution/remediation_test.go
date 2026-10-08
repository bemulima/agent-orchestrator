package shardexecution

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestRemediationGuardsCurrentRejectedReviewAndFrozenIdentity(t *testing.T) {
	inputs := approvedInputs{plan: domain.Plan{Fingerprint: "approved"}, baseline: domain.ContractBaseline{ID: "baseline", ContractBaselineCommit: "commit", ContractPlanFingerprint: "contracts"}, shards: []domain.ArchitecturalShard{{ID: "shard", RouteID: "backend.usecase", Status: domain.ShardStatusPlanned, Parallel: true}}}
	finished := time.Now()
	previous := domain.ShardFanoutExecution{State: "INTEGRATION_REJECTED", ReviewerVerdict: "REJECT", ReviewerThreadID: "review", ContractBaselineID: "baseline", BaselineCommit: "commit", Attempts: []domain.ShardAttempt{{ShardID: "shard", Status: domain.ShardAttemptVerified, FinishedAt: &finished, BaselineCommit: "commit", CommitSHA: "prior"}}}
	request := RemediationRequest{ID: "owner-decision", OwnerDecision: "owner explicitly authorized selective corrections", PlanFingerprint: "approved", ContractPlanFingerprint: "contracts", BaselineID: "baseline", BaselineCommit: "commit", PriorReviewerThread: "review", Findings: map[string][]string{"backend.usecase": {"public constructor"}}}
	if err := validateRemediation(request, inputs, previous); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RemediationRequest){
		func(r *RemediationRequest) { r.OwnerDecision = "" }, func(r *RemediationRequest) { r.PlanFingerprint = "changed" }, func(r *RemediationRequest) { r.ContractPlanFingerprint = "changed" }, func(r *RemediationRequest) { r.BaselineID = "changed" }, func(r *RemediationRequest) { r.BaselineCommit = "changed" }, func(r *RemediationRequest) { r.PriorReviewerThread = "old-review" }, func(r *RemediationRequest) { r.Findings = map[string][]string{"backend.composition": {"wire"}} },
	} {
		altered := request
		change(&altered)
		if err := validateRemediation(altered, inputs, previous); err == nil {
			t.Fatalf("accepted changed authorization: %+v", altered)
		}
	}
	previous.State = "INTEGRATION_VERIFIED"
	if err := validateRemediation(request, inputs, previous); err == nil {
		t.Fatal("accepted remediation of passed integration")
	}
}

func TestRemediationAllowsVerifiedSupersessionWithoutMutatingHistory(t *testing.T) {
	prepared, prior := resumeFixture()
	prepared.Remediation = &ShardRemediation{RequestID: "decision", PriorCommit: prior.CommitSHA, Findings: []string{"public constructor"}}
	before := prior
	number, err := nextPreparedAttemptNumber(prepared, []domain.ShardAttempt{prior})
	if err != nil || number != prior.AttemptNumber+1 {
		t.Fatalf("number=%d err=%v", number, err)
	}
	if !sameJSON(before, prior) {
		t.Fatal("prior verified evidence mutated")
	}
	prepared.Remediation = nil
	if _, err := nextPreparedAttemptNumber(prepared, []domain.ShardAttempt{prior}); err == nil {
		t.Fatal("ordinary retry superseded verified shard")
	}
	prepared.Remediation = &ShardRemediation{RequestID: "decision", PriorCommit: "different", Findings: []string{"constructor"}}
	if _, err := nextPreparedAttemptNumber(prepared, []domain.ShardAttempt{prior}); err == nil {
		t.Fatal("unrecognized predecessor accepted")
	}
}

type remediationMaterializer struct {
	ManagedWorktrees
	paths []string
	calls int
}

func (m *remediationMaterializer) MaterializeVerifiedSources(_ context.Context, _ domain.Project, source, destination domain.TaskWorkspace, commit, base string, changedFiles, paths []string) (map[string]string, error) {
	m.calls++
	m.paths = append([]string(nil), paths...)
	return map[string]string{paths[0]: "package usecase\nfunc ExistingBehavior() {}\n"}, nil
}

func TestUsecasePreimageCopiesOnlyVerifiedProductionOnSameFrozenBase(t *testing.T) {
	prepared, prior := resumeFixture()
	prepared.WorkPackage.Route = "backend.usecase"
	prepared.WorkPackage.WriteScope.Allow = []string{"internal/usecase/**"}
	prior.WorkPackage = prepared.WorkPackage
	prior.ChangedFiles = []string{"internal/usecase/availability.go", "internal/usecase/availability_test.go"}
	prepared.Remediation = &ShardRemediation{RequestID: "utc-decision", PriorCommit: prior.CommitSHA, Findings: []string{"Location identity rejects zero offset"}}
	m := &remediationMaterializer{}
	before := compactJSON(prior)
	evidence, err := (Service{Worktrees: m}).materializeRemediationPreimage(context.Background(), prepared, domain.Project{}, []domain.ShardAttempt{prior})
	if err != nil || evidence == nil {
		t.Fatalf("preimage=%+v err=%v", evidence, err)
	}
	if m.calls != 1 || len(m.paths) != 1 || m.paths[0] != "internal/usecase/availability.go" {
		t.Fatalf("copied unexpected paths: %v", m.paths)
	}
	if evidence.PriorCommit != prior.CommitSHA || evidence.BaselineCommit != prepared.Workspace.BaseCommit || evidence.RequestID != "utc-decision" || evidence.Hashes[m.paths[0]] != contentHash([]byte(evidence.Sources[m.paths[0]])) {
		t.Fatalf("provenance incomplete: %+v", evidence)
	}
	if compactJSON(prior) != before {
		t.Fatal("prior history mutated")
	}
	prepared.Remediation = nil
	evidence, err = (Service{Worktrees: m}).materializeRemediationPreimage(context.Background(), prepared, domain.Project{}, []domain.ShardAttempt{prior})
	if err != nil || evidence != nil || m.calls != 1 {
		t.Fatal("unaffected shard materialized or changed")
	}
	prepared.Remediation = &ShardRemediation{RequestID: "utc-decision", PriorCommit: prior.CommitSHA, Findings: []string{"utc"}}
	prepared.WorkPackage.Route = "backend.infrastructure.persistence"
	evidence, err = (Service{Worktrees: m}).materializeRemediationPreimage(context.Background(), prepared, domain.Project{}, []domain.ShardAttempt{prior})
	if err != nil || evidence != nil || m.calls != 1 {
		t.Fatal("persistence acquired unapproved reproduction")
	}
}

func TestUsecaseRemediationPromptRequiresLocationBugRedBeforeImplementation(t *testing.T) {
	value := remediationPrompt(&ShardRemediation{RequestID: "utc-decision", PriorCommit: "prior", Findings: []string{"Location identity"}}, "backend.usecase")
	for _, required := range []string{"Location()==time.UTC", "time.FixedZone", "+00:00", "+03:00", "-05:00", "reversed and equal", "Do not change that source", "Only after orchestrator persists actual RED"} {
		if !strings.Contains(value, required) {
			t.Fatalf("missing guard %q", required)
		}
	}
}

func TestHTTPRemediationPromptRequiresActualDelegationREDAndBothTimestampBoundaries(t *testing.T) {
	value := remediationPrompt(&ShardRemediation{RequestID: "http-utc-owner", PriorCommit: "verified-http", Findings: []string{"nonzero offsets delegated"}}, "backend.transport.http")
	for _, required := range []string{
		"exact prior VERIFIED HTTP production source at frozen HEAD",
		"Preserve every production byte and constructor signature during the test-only RED phase",
		"No RED_SETUP or missing-interface test is needed",
		"successful deterministic result for ANY request",
		"spy must not validate timestamps",
		"net/url.Values encoding",
		"UTC Z and +00:00 on BOTH start and end",
		"mixed zero-offset representations",
		"HTTP200, application call count exactly one",
		"+03:00, -05:00 and malformed timestamp independently on EACH of start and end",
		"HTTP400 and application call count exactly zero",
		"all parseable nonzero-offset cases are increasing ranges",
		"assert ZERO spy calls BEFORE checking status",
		"semantic RED: non-zero offset is currently delegated",
		"persist the actual failed regression and untouched preimage evidence BEFORE implementation",
		"Start.Zone() and End.Zone() BEFORE calling the application",
		"Do not change constructors, Usecase, Persistence, Composition, frozen contracts, ContractPlan, routing or approved scope",
	} {
		if !strings.Contains(value, required) {
			t.Errorf("HTTP remediation prompt missing required evidence instruction %q", required)
		}
	}
	if strings.Contains(value, "If ServeHTTP is absent") || strings.Contains(value, "runtime net/http.Handler capability") {
		t.Fatal("HTTP known-bug regression fell back to missing-interface RED")
	}
}
