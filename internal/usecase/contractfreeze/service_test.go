package contractfreeze

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/agentpolicy"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestFreezeRunsManagedContractAgentReviewAndLocalCommit(t *testing.T) {
	for _, reviewerStatus := range []string{"PASS", "REJECT"} {
		t.Run(reviewerStatus, func(t *testing.T) {
			project, task, plan, plannedTask, routing, contractPlan, profile, baseline, root, storage := freezeFixture(t)
			validator, err := agent.NewValidator()
			if err != nil {
				t.Fatal(err)
			}
			runner := &freezeRunner{reviewerStatus: reviewerStatus}
			service := Service{
				Worktrees: gitadapter.TaskWorktree{StoragePath: storage, AuthorName: "Freeze Test", AuthorEmail: "freeze@example.test"},
				Runner:    runner, Validator: validator, Materializer: contractbaseline.FileMaterializer{},
				Router:        agentpolicy.Router{CoderModel: "coder-model", DeepModel: "review-model", DeepReasoning: "high"},
				ContractSkill: "Freeze only shared API boundaries.", Now: func() time.Time { return time.Unix(100, 0).UTC() },
			}
			frozen, err := service.Freeze(context.Background(), Input{
				Plan: plan, Task: task, PlannedTask: plannedTask, Project: project,
				Routing: routing, ContractPlan: contractPlan, Profile: profile,
				ProfileFingerprint: "profile-sha", ContractFingerprint: baseline.ContractPlanFingerprint, Baseline: baseline,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.requests) != 2 || runner.requests[0].Role != domain.AgentRunCoder || runner.requests[1].Role != domain.AgentRunReviewer {
				roles := make([]domain.AgentRunRole, 0, len(runner.requests))
				for _, request := range runner.requests {
					roles = append(roles, request.Role)
				}
				t.Fatalf("writer and distinct reviewer did not run in order: roles=%v state=%s reasons=%v", roles, frozen.ExecutionState, frozen.Validation.Reasons)
			}
			if !strings.Contains(runner.requests[0].Prompt, "approved_plan_fingerprint") ||
				!strings.Contains(runner.requests[0].Prompt, "approved_task_intent") ||
				!strings.Contains(runner.requests[0].Prompt, "AvailabilityRange") ||
				!strings.Contains(runner.requests[0].Prompt, "architectural_shards") ||
				!strings.Contains(runner.requests[0].Prompt, "contract_write_scope") ||
				!strings.Contains(runner.requests[0].Prompt, "the orchestrator will execute verification_capability outside your sandbox") ||
				strings.Contains(runner.requests[0].Prompt, "secret-value-fixture") {
				t.Fatalf("Contract Agent context was not bounded as required")
			}
			if reviewerStatus == "PASS" {
				if frozen.ExecutionState != domain.ContractBaselineFrozen || !frozen.Validation.Passed ||
					frozen.ContractBaselineCommit == "" || !frozen.ContractAgentPassed ||
					!frozen.MechanicalReviewPassed || !frozen.ContractVerificationPassed ||
					!frozen.IndependentReviewPassed || frozen.IndependentReviewStatus != "PASS" ||
					frozen.ContractAgentThreadID == frozen.IndependentReviewerID {
					t.Fatalf("complete freeze evidence did not produce FROZEN: %#v", frozen)
				}
				verified := contractbaseline.VerifyBaseline(context.Background(), contractbaseline.FileMaterializer{}, root,
					frozen, plan.Fingerprint, contractPlan, profile, "profile-sha", project.HeadCommit, time.Now().UTC())
				if verified.ExecutionState != domain.ContractBaselineFrozen || !verified.Validation.Passed {
					t.Fatalf("committed baseline failed persisted drift validation: %#v", verified)
				}
			} else if frozen.ExecutionState != domain.ContractBaselineBlocked || frozen.ContractBaselineCommit != "" ||
				frozen.IndependentReviewStatus != "REJECT" || frozen.IndependentReviewPassed {
				t.Fatalf("rejected review produced a frozen commit: %#v", frozen)
			}
			sourceHead := freezeGit(t, root, "rev-parse", "HEAD")
			if strings.TrimSpace(sourceHead) != project.HeadCommit {
				t.Fatalf("managed contract commit changed the connected source checkout: %s", sourceHead)
			}
		})
	}
}

func TestBoundedTaskIntentRejectsUnboundedApprovedInput(t *testing.T) {
	if _, err := boundedTaskIntent(domain.Task{Description: strings.Repeat("x", maxTaskIntentFieldBytes+1)}, domain.PlannedTask{}); err == nil {
		t.Fatal("expected oversized approved task intent to be rejected")
	}
	criteria := make([]string, maxTaskIntentCriteria+1)
	if _, err := boundedTaskIntent(domain.Task{AcceptanceCriteria: criteria}, domain.PlannedTask{}); err == nil {
		t.Fatal("expected excessive acceptance criteria to be rejected")
	}
}

func TestContractFreezeDiffProvenanceSeparatesMaterializerAgentAndBaselineChanges(t *testing.T) {
	const materializedPath = "internal/usecase/application_command_result.go"
	boundary := []domain.PlannedContractBoundary{{Kind: "application-command-result"}}
	source := domain.WorkspaceSnapshot{Files: map[string]string{}}
	materialized := domain.WorkspaceSnapshot{Files: map[string]string{materializedPath: "file:644:skeleton"}}
	materializerChanges := snapshotChanges(source, materialized)
	if !sameStrings(materializerChanges, []string{materializedPath}) {
		t.Fatalf("MaterializerChanges = %v", materializerChanges)
	}

	t.Run("materializer-only file accepted unchanged", func(t *testing.T) {
		agentChanges := snapshotChanges(materialized, materialized)
		baselineChanges := snapshotChanges(source, materialized)
		if len(agentChanges) != 0 || !sameStrings(baselineChanges, []string{materializedPath}) {
			t.Fatalf("AgentChanges=%v BaselineChanges=%v", agentChanges, baselineChanges)
		}
		result := contractAgentResult{ReviewedBoundaries: []string{"application-command-result"},
			AcceptedMaterializedFiles: []string{materializedPath}}
		if err := validateContractAgentClaims(result, materializerChanges, agentChanges, boundary); err != nil {
			t.Fatalf("unchanged accepted materializer output was rejected: %v", err)
		}
	})

	t.Run("agent refinement changes only the agent delta", func(t *testing.T) {
		refined := domain.WorkspaceSnapshot{Files: map[string]string{materializedPath: "file:644:refined"}}
		agentChanges := snapshotChanges(materialized, refined)
		baselineChanges := snapshotChanges(source, refined)
		if !sameStrings(agentChanges, []string{materializedPath}) || !sameStrings(baselineChanges, []string{materializedPath}) {
			t.Fatalf("AgentChanges=%v BaselineChanges=%v", agentChanges, baselineChanges)
		}
		result := contractAgentResult{ReviewedBoundaries: []string{"application-command-result"},
			AgentChangedFiles: []string{materializedPath}, AcceptedMaterializedFiles: []string{materializedPath}}
		if err := validateContractAgentClaims(result, materializerChanges, agentChanges, boundary); err != nil {
			t.Fatalf("accurate refinement claims were rejected: %v", err)
		}
	})

	t.Run("false agent changed-file claim rejected", func(t *testing.T) {
		result := contractAgentResult{ReviewedBoundaries: []string{"application-command-result"},
			AgentChangedFiles: []string{materializedPath}, AcceptedMaterializedFiles: []string{materializedPath}}
		if err := validateContractAgentClaims(result, materializerChanges, nil, boundary); err == nil {
			t.Fatal("agent claim for an unchanged file was accepted")
		}
	})

	t.Run("missing agent changed-file claim rejected", func(t *testing.T) {
		result := contractAgentResult{ReviewedBoundaries: []string{"application-command-result"},
			AcceptedMaterializedFiles: []string{materializedPath}}
		if err := validateContractAgentClaims(result, materializerChanges, []string{materializedPath}, boundary); err == nil {
			t.Fatal("agent delta omitted from its result was accepted")
		}
	})
}

func TestParseContractAgentResultPreservesAnEmptyAgentDeltaAsAnArray(t *testing.T) {
	validator, err := agent.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	materializedPath := "internal/usecase/application_command_result.go"
	raw := []byte(`{"status":"completed","summary":"Accepted deterministic contract declarations.","reviewed_boundaries":["application-command-result"],"agent_changed_files":[],"accepted_materialized_files":["internal/usecase/application_command_result.go"],"checks":[],"artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]}`)
	result, err := parseContractAgentResult(validator, raw)
	if err != nil {
		t.Fatalf("empty AgentChanges result failed structured validation: %v", err)
	}
	if result.AgentResult.FilesChanged == nil || len(result.AgentResult.FilesChanged) != 0 {
		t.Fatalf("empty agent file delta was not preserved as []: %#v", result.AgentResult.FilesChanged)
	}
	if !sameStrings(result.AcceptedMaterializedFiles, []string{materializedPath}) {
		t.Fatalf("accepted materializer paths = %v", result.AcceptedMaterializedFiles)
	}
}

type freezeRunner struct {
	requests       []domain.AgentRunRequest
	reviewerStatus string
}

func (f *freezeRunner) Run(ctx context.Context, request domain.AgentRunRequest, callback repository.AgentThreadCallback) (domain.AgentRunResponse, error) {
	f.requests = append(f.requests, request)
	if request.Role == domain.AgentRunCoder {
		const path = "internal/usecase/application_command_result.go"
		content := "package usecase\n\ntype ApplicationCommandResult interface {\n\tExecute() error\n}\n"
		fullPath := filepath.Join(request.WorkingDirectory, filepath.FromSlash(path))
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return domain.AgentRunResponse{}, err
		}
		threadID := "contract-agent-thread"
		if callback != nil {
			if err := callback(ctx, threadID); err != nil {
				return domain.AgentRunResponse{}, err
			}
		}
		var contextValue agentContext
		contextJSON := strings.SplitN(request.Prompt, "Bounded approved context (JSON):\n", 2)
		if len(contextJSON) != 2 || json.Unmarshal([]byte(contextJSON[1]), &contextValue) != nil {
			return domain.AgentRunResponse{}, domain.ErrValidation
		}
		var reviewed []string
		for _, boundary := range contextValue.ContractPlan.Boundaries {
			reviewed = append(reviewed, boundary.Kind)
		}
		result, _ := json.Marshal(map[string]any{
			"status": "completed", "summary": "Defined the approved application result boundary.",
			"reviewed_boundaries": reviewed, "agent_changed_files": []string{path},
			"accepted_materialized_files": contextValue.MaterializerChanges,
			"checks":                      []domain.AgentCheck{}, "artifacts": []domain.AgentArtifactClaim{},
			"blockers": []string{}, "required_tasks": []domain.RequiredTask{},
			"risks": []string{}, "notes_for_reviewer": []string{},
		})
		return domain.AgentRunResponse{ThreadID: threadID, Result: result}, nil
	}
	if request.Role != domain.AgentRunReviewer {
		return domain.AgentRunResponse{}, domain.ErrValidation
	}
	if !strings.Contains(request.Prompt, "complete_changed_file_contents") ||
		!strings.Contains(request.Prompt, "Execute() error") {
		return domain.AgentRunResponse{}, domain.ErrValidation
	}
	threadID := "contract-review-thread"
	result, _ := json.Marshal(reviewResult{Status: f.reviewerStatus, Summary: "Reviewed the complete contract surface.", Findings: []string{}})
	return domain.AgentRunResponse{ThreadID: threadID, Result: result}, nil
}

func freezeFixture(t *testing.T) (domain.Project, domain.Task, domain.Plan, domain.PlannedTask, domain.RoutingResult,
	domain.ContractPlan, agentcontrol.Profile, domain.ContractBaseline, string, string) {
	t.Helper()
	root := t.TempDir()
	for relative, content := range map[string]string{
		"go.mod":                                    "module example.test/contract-freeze\n\ngo 1.24\n",
		"internal/domain/lesson.go":                 "package domain\ntype Lesson struct{}\n",
		"internal/usecase/complete.go":              "package usecase\ntype Complete struct{}\n",
		"internal/transport/http/handler.go":        "package http\n",
		"internal/infrastructure/postgres/store.go": "package postgres\n",
	} {
		target := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	freezeGit(t, root, "init", "-q")
	freezeGit(t, root, "config", "user.name", "Freeze Test")
	freezeGit(t, root, "config", "user.email", "freeze@example.test")
	freezeGit(t, root, "add", "--all")
	freezeGit(t, root, "commit", "-m", "chore: fixture source base")
	head := strings.TrimSpace(freezeGit(t, root, "rev-parse", "HEAD"))
	localPath := root
	project := domain.Project{ID: "project-1", Name: "freeze-fixture", LocalPath: &localPath, HeadCommit: head}
	task := domain.Task{ID: "task-1", PlanID: "plan-1", ProjectID: project.ID, PlannerKey: "task-key", Title: "availability lookup", Description: "Add an availability lookup contract for an HTTP operation backed by a usecase and repository port.", AcceptanceCriteria: []string{"The shared query includes resource identity and a time range.", "The shared result contains availability intervals."}, RiskLevel: domain.RiskLevelMedium}
	planFingerprint := "approved-plan-fingerprint"
	plan := domain.Plan{ID: task.PlanID, Status: domain.PlanStatusApproved, Fingerprint: planFingerprint, ApprovedFingerprint: &planFingerprint}
	planned := domain.PlannedTask{Key: task.PlannerKey, ProjectID: project.ID, Title: "Define availability lookup contract", Description: task.Description, AcceptanceCriteria: []string{"Use a query with resource ID and AvailabilityRange.", "Return availability intervals without defining runtime behavior."}, ArchitecturalRoutes: []string{"backend.usecase"}}
	profile := agentcontrol.Profile{
		ID: "go.canonical", Stack: "go", ContractLocation: agentcontrol.ContractLocation{
			Language: "go", Extension: ".go", StandardLibraryImports: "allow", ThirdPartyImportPolicy: "project_dependencies",
		},
		Routes: []agentcontrol.ProfileRoute{{ID: "backend.usecase", Purpose: "Application orchestration",
			Targets: []string{"internal/usecase/**"}, ContractSurface: &agentcontrol.ContractSurface{Directory: "internal/usecase", PackageName: "usecase"},
			AllowedDependencies: []string{"backend.domain"}, VerificationKinds: []string{"usecase-unit"}},
			{ID: "backend.domain", Targets: []string{"internal/domain/**"}}},
	}
	routing := domain.RoutingResult{
		Status:   domain.RoutingStatusResolved,
		Profiles: []domain.ArchitectureProfileResolution{{ProjectID: project.ID, Status: domain.ProfileResolutionResolved, ProfileID: profile.ID, ProfileFingerprint: "profile-sha"}},
		Routes: []domain.RoutedTarget{{RouteReference: domain.RouteReference{ProjectID: project.ID, RouteID: "backend.usecase"},
			Paths: []string{"internal/usecase/complete.go"}, EvidenceIDs: []string{"source-usecase"}}},
		EvidenceIDs:   []string{"source-usecase"},
		EvidenceIndex: []domain.RoutingEvidence{{ID: "source-usecase", ProjectID: project.ID, Kind: "source", Path: "internal/usecase/complete.go", Symbol: "Complete", Summary: "Use case boundary"}},
		Verification:  []domain.RouteVerification{{RouteReference: domain.RouteReference{ProjectID: project.ID, RouteID: "backend.usecase"}, Boundary: "usecase-unit", EvidenceState: domain.VerificationEvidenceExisting, Rationale: "fixture"}},
	}
	contractPlan := domain.ContractPlan{
		State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Reason: "shared command result",
		AffectedRoutes: []domain.RouteReference{{ProjectID: project.ID, RouteID: "backend.usecase"}},
		Boundaries: []domain.PlannedContractBoundary{{Kind: "application-command-result", TargetPath: "internal/usecase/application_command_result.go",
			Owner:     domain.RouteReference{ProjectID: project.ID, RouteID: "backend.usecase"},
			Consumers: []domain.RouteReference{{ProjectID: project.ID, RouteID: "backend.transport.http"}}, Rationale: "shared result boundary"}},
	}
	contractFP, err := contractbaseline.PlanFingerprint(contractPlan)
	if err != nil {
		t.Fatal(err)
	}
	baseline := domain.ContractBaseline{
		ID: contractbaseline.BaselineID(plan.ID, task.ID, project.ID), PlanID: plan.ID, TaskID: task.ID,
		RepositoryProjectID: project.ID, Repository: project.Name, ProfileID: profile.ID,
		PlanningContractState: domain.ContractPlanFreezeNeeded, ExecutionState: domain.ContractBaselineMaterializing,
		ApprovedPlanFingerprint: planFingerprint, ContractPlanFingerprint: contractFP,
		ProfileFingerprint: "profile-sha", RepositoryRevision: head,
	}
	return project, task, plan, planned, routing, contractPlan, profile, baseline, root, filepath.Join(t.TempDir(), "managed-worktrees")
}

func freezeGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func TestFreezeRetryUsesFreshManagedIsolation(t *testing.T) {
	project, task, plan, plannedTask, routing, contractPlan, profile, baseline, _, storage := freezeFixture(t)
	validator, err := agent.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	runner := &freezeRunner{reviewerStatus: "REJECT"}
	service := Service{Worktrees: gitadapter.TaskWorktree{StoragePath: storage, AuthorName: "Freeze Test", AuthorEmail: "freeze@example.test"}, Runner: runner, Validator: validator, Materializer: contractbaseline.FileMaterializer{}, Router: agentpolicy.Router{CoderModel: "coder-model", DeepModel: "review-model", DeepReasoning: "high"}, ContractSkill: "Freeze only shared API boundaries."}
	input := Input{Plan: plan, Task: task, PlannedTask: plannedTask, Project: project, Routing: routing, ContractPlan: contractPlan, Profile: profile, ProfileFingerprint: "profile-sha", ContractFingerprint: baseline.ContractPlanFingerprint, Baseline: baseline}
	first, err := service.Freeze(context.Background(), input)
	if err != nil || first.ExecutionState != domain.ContractBaselineBlocked {
		t.Fatalf("first freeze: %v %#v", err, first)
	}
	firstPath := runner.requests[0].WorkingDirectory
	runner.reviewerStatus = "PASS"
	input.Baseline = first
	second, err := service.Freeze(context.Background(), input)
	if err != nil || second.ExecutionState != domain.ContractBaselineFrozen {
		t.Fatalf("retry did not freeze: %v %v", err, second.Validation.Reasons)
	}
	if len(runner.requests) != 4 || runner.requests[2].WorkingDirectory == firstPath {
		t.Fatal("retry reused dirty contract isolation")
	}
	if !strings.Contains(runner.requests[2].Prompt, "A materializer-created path that you edit belongs in BOTH") {
		t.Fatal("edited materializer-path reporting is ambiguous")
	}
	if second.ID != baseline.ID || second.ApprovedPlanFingerprint != plan.Fingerprint {
		t.Fatal("retry changed baseline or approved Plan identity")
	}
}

func TestFreezeInputAcceptsBoundSuccessorAndRejectsMismatchedIdentity(t *testing.T) {
	project, task, plan, planned, routing, contractPlan, profile, baseline, _, storage := freezeFixture(t)
	validator, err := agent.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	service := Service{
		Worktrees: gitadapter.TaskWorktree{StoragePath: storage}, Runner: &freezeRunner{}, Validator: validator,
		Materializer: contractbaseline.FileMaterializer{}, ContractSkill: "Freeze shared boundaries.",
	}
	baseline.PredecessorBaselineID = baseline.ID
	baseline.ID = contractbaseline.BaselineID(plan.ID, task.ID, project.ID, baseline.PredecessorBaselineID)
	input := Input{Plan: plan, Task: task, PlannedTask: planned, Project: project, Routing: routing,
		ContractPlan: contractPlan, Profile: profile, ProfileFingerprint: "profile-sha",
		ContractFingerprint: baseline.ContractPlanFingerprint, Baseline: baseline,
	}
	if err := service.validateInput(input); err != nil {
		t.Fatalf("successor rejected: %v", err)
	}
	input.Baseline.PredecessorBaselineID = "wrong-predecessor"
	if err := service.validateInput(input); err == nil {
		t.Fatal("mismatched predecessor accepted")
	}
	input.Baseline.PredecessorBaselineID = ""
	if err := service.validateInput(input); err == nil {
		t.Fatal("successor without predecessor accepted")
	}
}
