package shardexecution

import (
	"context"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func httpFormatRecoveryFixture() (PreparedShard, domain.ShardAttempt) {
	prepared, attempt := resumeFixture()
	prepared.WorkPackage.Route = "backend.transport.http"
	prepared.WorkPackage.Verification.TestPaths = []string{"internal/http/handler_test.go"}
	prepared.WorkPackage.Contracts.ReadOnlyPaths = []string{"internal/domain/repository_port.go"}
	prepared.Remediation = &ShardRemediation{RequestID: "owner-http", PriorCommit: "previous", Findings: []string{"reject offset before delegation"}}
	attempt.WorkPackage = prepared.WorkPackage
	attempt.Workspace = prepared.Workspace
	attempt.ID = "same-attempt"
	attempt.AttemptNumber = 2
	attempt.Status = domain.ShardAttemptFailed
	attempt.Red = nil
	attempt.Green = nil
	attempt.CommitSHA = ""
	attempt.ChangedFiles = nil
	attempt.Phase = domain.ShardPhasePrepared
	attempt.PhaseHistory = []domain.ShardPhaseEvent{{Phase: domain.ShardPhasePrepared}}
	attempt.Model = "actual-model"
	attempt.ReasoningEffort = "low"
	attempt.StartedAt = time.Unix(10, 0)
	attempt.WorkerStartedAt = attempt.StartedAt
	attempt.Blockers = []string{`decode semantic RED test-only WorkerResult: strict JSON decode: parsing time "2026-10-04" as "2006-01-02T15:04:05Z07:00": cannot parse "" as "T"`}
	source := "package http\n// audited original behavior\n"
	attempt.RemediationPreimage = &domain.ShardRemediationPreimage{RequestID: prepared.Remediation.RequestID, PriorCommit: prepared.Remediation.PriorCommit, BaselineCommit: prepared.WorkPackage.ExecutionBase.Revision, Sources: map[string]string{"internal/http/handler.go": source}, Hashes: map[string]string{"internal/http/handler.go": contentHash([]byte(source))}}
	return prepared, attempt
}

func TestHTTPFormatRecoveryRetainsSameIdentityAndNeverClaimsRed(t *testing.T) {
	prepared, attempt := httpFormatRecoveryFixture()
	before := compactJSON(attempt)
	candidate, err := httpFormatRecoveryCandidate(prepared, []domain.ShardAttempt{attempt})
	if err != nil || candidate == nil {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	if candidate.ID != attempt.ID || candidate.WorkerThreadID != attempt.WorkerThreadID || candidate.StartedAt != attempt.StartedAt || candidate.Model != attempt.Model || candidate.AttemptNumber != attempt.AttemptNumber || candidate.Red != nil {
		t.Fatal("recovery invented a new attempt or semanticRED")
	}
	if compactJSON(attempt) != before {
		t.Fatal("classification mutated audit")
	}
	newer := attempt
	newer.AttemptNumber++
	newer.Blockers = []string{"fixture failure"}
	if _, err := httpFormatRecoveryCandidate(prepared, []domain.ShardAttempt{attempt, newer}); err == nil {
		t.Fatal("old eligible format failure bypassed latest failure")
	}
}

func TestHTTPFormatRecoveryRejectsOtherFailuresImplementationAndChangedOwnerScope(t *testing.T) {
	for _, mutate := range []func(*PreparedShard, *domain.ShardAttempt){
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Blockers = []string{"fixture failed"} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Blockers = []string{"CONTRACT_CHANGE_REQUIRED"} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Phase = domain.ShardPhaseImplementing },
		func(p *PreparedShard, a *domain.ShardAttempt) {
			a.PhaseHistory = append(a.PhaseHistory, domain.ShardPhaseEvent{Phase: domain.ShardPhaseImplementing})
		},
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Red = &domain.ShardREDEvidence{} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Green = &domain.ShardGreenEvidence{} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.Implementation = &domain.WorkerResult{} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.REDSetup = &domain.ShardREDSetupEvidence{} },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.CommitSHA = "commit" },
		func(p *PreparedShard, a *domain.ShardAttempt) {
			a.FormatRecoveryHistory = []domain.ShardAttemptFormatRecovery{{Reason: "already recovered"}}
		},
		func(p *PreparedShard, a *domain.ShardAttempt) { a.RemediationPreimage.RequestID = "changed-owner" },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.RemediationPreimage.BaselineCommit = "changed-base" },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.RemediationPreimage.PriorCommit = "changed-prior" },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.WorkerThreadID = "" },
		func(p *PreparedShard, a *domain.ShardAttempt) { a.WorkPackage.WriteScope.Allow = []string{"other/**"} },
	} {
		prepared, attempt := httpFormatRecoveryFixture()
		mutate(&prepared, &attempt)
		if httpFormatRecoveryEligible(prepared, attempt) {
			t.Fatalf("unsafe recovery allowed %+v", attempt)
		}
	}
}

type httpRecoveryWorktrees struct {
	ManagedWorktrees
	state domain.WorkspaceState
	files map[string][]byte
}

func (w *httpRecoveryWorktrees) Inspect(context.Context, domain.Project, domain.TaskWorkspace) (domain.WorkspaceState, error) {
	return w.state, nil
}
func (w *httpRecoveryWorktrees) ReadArtifact(_ context.Context, _ domain.TaskWorkspace, path string, _ int64) ([]byte, error) {
	return w.files[path], nil
}

func TestHTTPFormatRecoveryRequiresFrozenHeadUnchangedPreimageAndTestOnlyCandidate(t *testing.T) {
	for _, kind := range []string{"valid", "head", "production", "sibling", "contract"} {
		t.Run(kind, func(t *testing.T) {
			prepared, attempt := httpFormatRecoveryFixture()
			w := &httpRecoveryWorktrees{state: domain.WorkspaceState{HeadCommit: prepared.WorkPackage.ExecutionBase.Revision, ChangedFiles: []string{"internal/http/handler.go", "internal/http/handler_test.go"}}, files: map[string][]byte{"internal/http/handler.go": []byte(attempt.RemediationPreimage.Sources["internal/http/handler.go"]), "internal/http/handler_test.go": []byte("package http\n// actual candidate\n")}}
			switch kind {
			case "head":
				w.state.HeadCommit = "changed"
			case "production":
				w.files["internal/http/handler.go"] = []byte("behavior change")
			case "sibling":
				w.state.ChangedFiles = append(w.state.ChangedFiles, "internal/http/other.go")
			case "contract":
				w.state.ChangedFiles = append(w.state.ChangedFiles, "internal/domain/repository_port.go")
			}
			paths, err := (Service{Worktrees: w}).validateHTTPFormatRecoveryWorkspace(context.Background(), prepared, attempt, domain.Project{})
			if kind == "valid" {
				if err != nil || len(paths) != 1 || paths[0] != "internal/http/handler_test.go" {
					t.Fatalf("paths=%v err=%v", paths, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s drift", kind)
			}
		})
	}
}
