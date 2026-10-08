package shardexecution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func resumeFixture() (PreparedShard, domain.ShardAttempt) {
	base := strings.Repeat("a", 40)
	wp := domain.WorkPackage{PlanID: "plan", TaskID: "task", ShardID: "shard", ProjectID: "project",
		ExecutionBase: domain.ShardExecutionBase{Kind: domain.ShardBaseContractBaseline, Revision: base, ContractBaselineID: "baseline"},
		WriteScope:    domain.ShardWriteScope{Allow: []string{"internal/http/**"}, MaxFiles: 3},
		Verification:  domain.WorkPackageVerification{CandidateCommands: []string{"go test ./..."}},
	}
	workspace := domain.TaskWorkspace{Path: "/managed/original-verified", BaseCommit: base}
	now := time.Unix(10, 0).UTC()
	attempt := domain.ShardAttempt{ID: "original", PlanID: wp.PlanID, TaskID: wp.TaskID, ShardID: wp.ShardID,
		AttemptNumber: 3, WorkPackage: wp, Workspace: workspace, BaselineCommit: base,
		Status: domain.ShardAttemptVerified, CommitSHA: strings.Repeat("b", 40), WorkerThreadID: "original-thread", FinishedAt: &now,
		ChangedFiles: []string{"internal/http/handler.go", "internal/http/handler_test.go"},
		Red:          &domain.ShardREDEvidence{Semantic: true, TestPaths: []string{"internal/http/handler_test.go"}},
		Green:        &domain.ShardGreenEvidence{Passed: true, Commands: []domain.WorkspaceCheckResult{{Command: "go test ./...", ExitCode: 0}}},
	}
	return PreparedShard{WorkPackage: wp, Workspace: domain.TaskWorkspace{Path: "/managed/new-preparation", BaseCommit: base}}, attempt
}

func TestVerifiedResumeCandidateRetainsLatestOriginalEvidence(t *testing.T) {
	prepared, attempt := resumeFixture()
	older := attempt
	older.AttemptNumber, older.ID, older.Status = 2, "older", domain.ShardAttemptFailed
	history := []domain.ShardAttempt{attempt, older}
	before := compactJSON(history)
	candidate, err := verifiedResumeCandidate(prepared, history)
	if err != nil || candidate == nil || !reflect.DeepEqual(*candidate, attempt) {
		t.Fatalf("candidate = %#v, %v", candidate, err)
	}
	if compactJSON(history) != before {
		t.Fatal("historical attempts were changed")
	}
	latestFailure := older
	latestFailure.AttemptNumber = 4
	candidate, err = (Service{}).reusableVerifiedAttempt(context.Background(), prepared, append(history, latestFailure))
	if err != nil || candidate != nil {
		t.Fatalf("latest unverified attempt reused = %#v, %v", candidate, err)
	}
}

func TestVerifiedResumeCandidateRejectsStalePackageBaseAndIncompleteEvidence(t *testing.T) {
	for _, name := range []string{"package", "base", "prepared workspace", "original workspace", "red", "green", "commit", "finished", "scope", "test"} {
		t.Run(name, func(t *testing.T) {
			prepared, attempt := resumeFixture()
			switch name {
			case "package":
				attempt.WorkPackage.LocalIntent = "changed"
			case "base":
				attempt.BaselineCommit = strings.Repeat("c", 40)
			case "prepared workspace":
				prepared.Workspace.BaseCommit = "stale"
			case "original workspace":
				attempt.Workspace.BaseCommit = "stale"
			case "red":
				attempt.Red.Semantic = false
			case "green":
				attempt.Green.Passed = false
			case "commit":
				attempt.CommitSHA = "missing"
			case "finished":
				attempt.FinishedAt = nil
			case "scope":
				attempt.ChangedFiles = append(attempt.ChangedFiles, "cmd/main.go")
			case "test":
				attempt.ChangedFiles = attempt.ChangedFiles[:1]
			}
			if _, err := (Service{}).reusableVerifiedAttempt(context.Background(), prepared, []domain.ShardAttempt{attempt}); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("stale candidate accepted: %v", err)
			}
		})
	}
}

type resumeWorktrees struct {
	ManagedWorktrees
	state            domain.WorkspaceState
	content          []byte
	exitCode         int
	checks           []string
	commitCalls      int
	mutateAfterCheck bool
}

func (f *resumeWorktrees) Inspect(_ context.Context, _ domain.Project, workspace domain.TaskWorkspace) (domain.WorkspaceState, error) {
	if workspace.BaseCommit != strings.Repeat("b", 40) {
		return domain.WorkspaceState{}, domain.ErrConflict
	}
	return f.state, nil
}
func (f *resumeWorktrees) ReadArtifact(context.Context, domain.TaskWorkspace, string, int64) ([]byte, error) {
	return f.content, nil
}
func (f *resumeWorktrees) VerifyCommit(_ context.Context, _ domain.Project, _ domain.TaskWorkspace, commit, base string, files []string) error {
	f.commitCalls++
	if commit != strings.Repeat("b", 40) || base != strings.Repeat("a", 40) || len(files) != 2 {
		return domain.ErrConflict
	}
	return nil
}
func (f *resumeWorktrees) RunCheck(_ context.Context, _ domain.TaskWorkspace, command string) (domain.WorkspaceCheckResult, error) {
	f.checks = append(f.checks, command)
	if f.mutateAfterCheck {
		f.state.ChangedFiles = []string{"internal/http/handler.go"}
	}
	return domain.WorkspaceCheckResult{Command: command, ExitCode: f.exitCode}, nil
}

func TestVerifiedWorkspaceRevalidatesCommitFrozenBytesAndGreenWithoutMutation(t *testing.T) {
	_, attempt := resumeFixture()
	content := []byte("package domain\n")
	hash := sha256.Sum256(content)
	baseline := domain.ContractBaseline{Files: []domain.ContractBaselineFile{{Path: "internal/domain/port.go", SHA256: hex.EncodeToString(hash[:])}}}
	before := compactJSON(attempt)
	for _, name := range []string{"valid", "head", "dirty", "contract", "green", "check mutation"} {
		t.Run(name, func(t *testing.T) {
			worktrees := &resumeWorktrees{state: domain.WorkspaceState{HeadCommit: attempt.CommitSHA}, content: content}
			switch name {
			case "head":
				worktrees.state.HeadCommit = "wrong"
			case "dirty":
				worktrees.state.ChangedFiles = []string{"internal/http/handler.go"}
			case "contract":
				worktrees.content = []byte("changed")
			case "green":
				worktrees.exitCode = 1
			case "check mutation":
				worktrees.mutateAfterCheck = true
			}
			err := (Service{Worktrees: worktrees}).revalidateVerifiedWorkspace(context.Background(), domain.Project{}, baseline, attempt)
			if name == "valid" {
				if err != nil || worktrees.commitCalls != 1 || len(worktrees.checks) != 1 {
					t.Fatalf("verified revalidation failed: %v, %+v", err, worktrees)
				}
			} else if err == nil {
				t.Fatal("changed verified evidence accepted")
			}
			if compactJSON(attempt) != before {
				t.Fatal("original attempt evidence changed")
			}
		})
	}
}
