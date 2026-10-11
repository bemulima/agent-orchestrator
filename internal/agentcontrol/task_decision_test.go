package agentcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowCapabilityValidation(t *testing.T) {
	valid := WorkflowCapability{
		ID: "skill/task-route", Status: CapabilityReadyVerified, Scope: "read-only routing for evidenced operations",
		Profiles: []string{"go.canonical", "nextjs.common"}, Evidence: []string{"agent-system/skills/task-route/SKILL.md"},
		ExecutionRequirements: []string{"Applicable repository evidence and permitted execution method"},
	}
	if err := ValidateWorkflowCapabilities(nil); err != nil {
		t.Fatalf("legacy manifest without an inventory: %v", err)
	}
	for _, status := range []CapabilityStatus{CapabilityReadyVerified, CapabilityCanaryVerified, CapabilityPartiallyImplemented, CapabilityNotStarted, CapabilityBlocked, CapabilityUnknown} {
		value := valid
		value.Status = status
		if err := ValidateWorkflowCapabilities([]WorkflowCapability{value}); err != nil {
			t.Fatalf("owner status %s rejected: %v", status, err)
		}
	}
	tests := []struct {
		name   string
		mutate func(*WorkflowCapability)
	}{
		{"empty id", func(c *WorkflowCapability) { c.ID = "" }},
		{"absolute id", func(c *WorkflowCapability) { c.ID = "/task-route" }},
		{"traversal id", func(c *WorkflowCapability) { c.ID = "skill/../task-route" }},
		{"backslash id", func(c *WorkflowCapability) { c.ID = "skill\\task-route" }},
		{"whitespace id", func(c *WorkflowCapability) { c.ID = "task route" }},
		{"unknown status", func(c *WorkflowCapability) { c.Status = "PASS" }},
		{"empty scope", func(c *WorkflowCapability) { c.Scope = " \n" }},
		{"missing evidence", func(c *WorkflowCapability) { c.Evidence = nil }},
		{"empty evidence", func(c *WorkflowCapability) { c.Evidence = []string{""} }},
		{"absolute evidence", func(c *WorkflowCapability) { c.Evidence = []string{"/tmp/evidence"} }},
		{"traversal evidence", func(c *WorkflowCapability) { c.Evidence = []string{"docs/../../evidence"} }},
		{"unclean evidence", func(c *WorkflowCapability) { c.Evidence = []string{"docs/./evidence.md"} }},
		{"backslash evidence", func(c *WorkflowCapability) { c.Evidence = []string{"docs\\evidence.md"} }},
		{"control evidence", func(c *WorkflowCapability) { c.Evidence = []string{"docs/evidence\n.md"} }},
		{"missing requirements", func(c *WorkflowCapability) { c.ExecutionRequirements = nil }},
		{"empty requirement", func(c *WorkflowCapability) { c.ExecutionRequirements = []string{" "} }},
		{"invalid profile", func(c *WorkflowCapability) { c.Profiles = []string{"../go.canonical"} }},
		{"duplicate profile", func(c *WorkflowCapability) { c.Profiles = []string{"go.canonical", "go.canonical"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := ValidateWorkflowCapabilities([]WorkflowCapability{value}); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	if err := ValidateWorkflowCapabilities([]WorkflowCapability{valid, valid}); err == nil {
		t.Fatal("duplicate capability accepted")
	}
}

func TestFutureTaskReadinessRequiresAllCurrentFacts(t *testing.T) {
	for _, status := range []CapabilityStatus{CapabilityReadyVerified, CapabilityCanaryVerified, CapabilityPartiallyImplemented, CapabilityNotStarted, CapabilityBlocked, CapabilityUnknown} {
		t.Run(string(status), func(t *testing.T) {
			request := futureTaskFixture(status)
			decision := EvaluateFutureTask(request, nil)
			ready := status == CapabilityReadyVerified
			if decision.MainTaskAllowed != ready || decision.AgentSetupAllowed || decision.OwnerChoiceRequired == ready {
				t.Fatalf("incorrect readiness: %+v", decision)
			}
			if ready && decision.Action != FutureTaskApplyExisting || !ready && decision.Action != FutureTaskWaitOwner {
				t.Fatalf("incorrect action: %+v", decision)
			}
		})
	}
	tests := []struct {
		name   string
		mutate func(*FutureTaskRequest)
	}{
		{"repository missing", func(r *FutureTaskRequest) { r.Repository = "" }},
		{"task missing", func(r *FutureTaskRequest) { r.TaskFingerprint = "" }},
		{"requirements missing", func(r *FutureTaskRequest) { r.Requirements = nil }},
		{"unsafe required id", func(r *FutureTaskRequest) { r.Requirements[0].ID = "../worker" }},
		{"status missing", func(r *FutureTaskRequest) { r.Requirements[0].Status = "" }},
		{"evidence unknown", func(r *FutureTaskRequest) { r.Requirements[0].EvidenceConfirmed = false }},
		{"different repository", func(r *FutureTaskRequest) { r.Requirements[0].RepositoryMatched = false }},
		{"different operation or stack", func(r *FutureTaskRequest) { r.Requirements[0].OperationMatched = false }},
		{"execution denied", func(r *FutureTaskRequest) { r.Requirements[0].ExecutionPermitted = false }},
		{"duplicate requirements", func(r *FutureTaskRequest) { r.Requirements = append(r.Requirements, r.Requirements[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := futureTaskFixture(CapabilityReadyVerified)
			test.mutate(&request)
			decision := EvaluateFutureTask(request, nil)
			if decision.Action != FutureTaskWaitOwner || !decision.OwnerChoiceRequired || decision.MainTaskAllowed || decision.AgentSetupAllowed || len(decision.Blockers) == 0 {
				t.Fatalf("unestablished readiness was authorized: %+v", decision)
			}
		})
	}
}

func TestFutureTaskOwnerChoicesAreExplicitAndTaskBound(t *testing.T) {
	request := futureTaskFixture(CapabilityPartiallyImplemented)
	for _, test := range []struct {
		choice OwnerChoice
		action FutureTaskAction
		main   bool
		setup  bool
	}{
		{OwnerFinishRequiredCapability, FutureTaskFinishRequiredCapability, false, true},
		{OwnerUsePermittedLocalDevelopment, FutureTaskUsePermittedLocalDevelopment, true, false},
		{OwnerStop, FutureTaskStop, false, false},
	} {
		t.Run(string(test.choice), func(t *testing.T) {
			choice := futureTaskChoice(request, test.choice)
			decision := EvaluateFutureTask(request, &choice)
			if decision.Action != test.action || decision.OwnerChoiceRequired || decision.MainTaskAllowed != test.main || decision.AgentSetupAllowed != test.setup {
				t.Fatalf("incorrect owner selection: %+v", decision)
			}
			if repeated := EvaluateFutureTask(request, &choice); repeated.OwnerChoiceRequired {
				t.Fatal("unchanged task redundantly requests owner choice")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*OwnerChoiceRecord)
	}{
		{"repository", func(c *OwnerChoiceRecord) { c.Repository += "/other" }},
		{"task", func(c *OwnerChoiceRecord) { c.TaskFingerprint = "other-task" }},
		{"requirements", func(c *OwnerChoiceRecord) { c.RequirementsFingerprint = "stale" }},
		{"unknown choice", func(c *OwnerChoiceRecord) { c.Choice = "automatic_setup" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			choice := futureTaskChoice(request, OwnerUsePermittedLocalDevelopment)
			test.mutate(&choice)
			decision := EvaluateFutureTask(request, &choice)
			if decision.Action != FutureTaskWaitOwner || !decision.OwnerChoiceRequired || decision.MainTaskAllowed || decision.AgentSetupAllowed {
				t.Fatalf("mismatched owner choice accepted: %+v", decision)
			}
		})
	}
	request.LocalDevelopmentPermitted = false
	choice := futureTaskChoice(request, OwnerUsePermittedLocalDevelopment)
	if decision := EvaluateFutureTask(request, &choice); decision.Action != FutureTaskBlocked || decision.MainTaskAllowed || decision.AgentSetupAllowed {
		t.Fatalf("unpermitted local route accepted: %+v", decision)
	}
}

func TestFutureTaskRequirementFingerprintBindsFactsAndPermissions(t *testing.T) {
	request := futureTaskFixture(CapabilityPartiallyImplemented)
	second := request.Requirements[0]
	second.ID, second.Status = "contracts", CapabilityReadyVerified
	request.Requirements = append(request.Requirements, second)
	first := EvaluateFutureTask(request, nil).RequirementsFingerprint
	request.Requirements[0], request.Requirements[1] = request.Requirements[1], request.Requirements[0]
	if reordered := EvaluateFutureTask(request, nil).RequirementsFingerprint; first != reordered {
		t.Fatal("requirements order changes decision identity")
	}
	for _, mutate := range []func(*FutureTaskRequest){
		func(r *FutureTaskRequest) { r.Requirements[0].Status = CapabilityCanaryVerified },
		func(r *FutureTaskRequest) { r.Requirements[0].EvidenceConfirmed = false },
		func(r *FutureTaskRequest) { r.Requirements[0].RepositoryMatched = false },
		func(r *FutureTaskRequest) { r.Requirements[0].OperationMatched = false },
		func(r *FutureTaskRequest) { r.Requirements[0].ExecutionPermitted = false },
		func(r *FutureTaskRequest) { r.ProductionRequired = true },
		func(r *FutureTaskRequest) { r.ProductionAdmissionAllowed = true },
		func(r *FutureTaskRequest) { r.LocalDevelopmentPermitted = false },
	} {
		changed := request
		changed.Requirements = append([]FutureTaskRequirement(nil), request.Requirements...)
		choice := futureTaskChoice(request, OwnerUsePermittedLocalDevelopment)
		mutate(&changed)
		decision := EvaluateFutureTask(changed, &choice)
		if decision.RequirementsFingerprint == first || decision.MainTaskAllowed || decision.AgentSetupAllowed {
			t.Fatalf("changed conditions retained an executable decision: %+v", decision)
		}
	}
}

func TestFutureTaskAbsentChoiceNeverInvokesWritingCallback(t *testing.T) {
	for _, status := range []CapabilityStatus{CapabilityCanaryVerified, CapabilityPartiallyImplemented, CapabilityNotStarted, CapabilityBlocked, CapabilityUnknown} {
		t.Run(string(status), func(t *testing.T) {
			request := futureTaskFixture(status)
			assertFutureTaskNoWrites(t, request, nil)
		})
	}
	request := futureTaskFixture(CapabilityReadyVerified)
	request.Requirements[0].EvidenceConfirmed = false
	choice := futureTaskChoice(request, OwnerUsePermittedLocalDevelopment)
	assertFutureTaskNoWrites(t, request, &choice)
}

func TestFutureTaskMissingEvidenceFingerprintBlocksMainAction(t *testing.T) {
	request := futureTaskFixture(CapabilityReadyVerified)
	request.Requirements[0].EvidenceFingerprint = ""
	decision := EvaluateFutureTask(request, nil)
	if decision.MainTaskAllowed || decision.AgentSetupAllowed || !decision.OwnerChoiceRequired {
		t.Fatalf("semantic RED: READY task without exact evidence identity is executable: %+v", decision)
	}
	assertFutureTaskNoWrites(t, request, nil)
}

func TestFutureTaskMalformedEvidenceFingerprintNeverInvokesWritingCallback(t *testing.T) {
	for _, fingerprint := range []string{"", "sha256:short", strings.Repeat("a", 64), "sha256:" + strings.Repeat("g", 64), " sha256:" + strings.Repeat("a", 64)} {
		t.Run(fingerprint, func(t *testing.T) {
			for _, status := range []CapabilityStatus{CapabilityReadyVerified, CapabilityPartiallyImplemented, CapabilityNotStarted, CapabilityUnknown} {
				request := futureTaskFixture(status)
				request.Requirements[0].EvidenceFingerprint = fingerprint
				assertFutureTaskNoWrites(t, request, nil)
				for _, selection := range []OwnerChoice{OwnerFinishRequiredCapability, OwnerUsePermittedLocalDevelopment, OwnerStop} {
					choice := futureTaskChoice(request, selection)
					assertFutureTaskNoWrites(t, request, &choice)
				}
			}
		})
	}
}

func TestFutureTaskChangedExactEvidenceInvalidatesOwnerChoiceWithoutChangedBooleans(t *testing.T) {
	request := futureTaskFixture(CapabilityPartiallyImplemented)
	for _, selection := range []OwnerChoice{OwnerFinishRequiredCapability, OwnerUsePermittedLocalDevelopment} {
		t.Run(string(selection), func(t *testing.T) {
			choice := futureTaskChoice(request, selection)
			changed := request
			changed.Requirements = append([]FutureTaskRequirement(nil), request.Requirements...)
			changed.Requirements[0].EvidenceFingerprint = "sha256:" + strings.Repeat("b", 64)
			decision := EvaluateFutureTask(changed, &choice)
			if decision.RequirementsFingerprint == choice.RequirementsFingerprint || decision.Action != FutureTaskWaitOwner || !decision.OwnerChoiceRequired || decision.MainTaskAllowed || decision.AgentSetupAllowed {
				t.Fatalf("changed exact evidence reused owner consent despite unchanged summary facts: %+v", decision)
			}
			assertFutureTaskNoWrites(t, changed, &choice)
		})
	}
}

func TestFutureTaskProductionDenialNeverInvokesWritingCallback(t *testing.T) {
	request := futureTaskFixture(CapabilityReadyVerified)
	request.ProductionRequired = true
	for _, selection := range []OwnerChoice{"", OwnerFinishRequiredCapability, OwnerUsePermittedLocalDevelopment, OwnerStop} {
		t.Run(string(selection), func(t *testing.T) {
			var choice *OwnerChoiceRecord
			if selection != "" {
				value := futureTaskChoice(request, selection)
				choice = &value
			}
			decision := EvaluateFutureTask(request, choice)
			if decision.Action != FutureTaskBlocked || decision.MainTaskAllowed || decision.AgentSetupAllowed {
				t.Fatalf("production denial bypassed: %+v", decision)
			}
			assertFutureTaskNoWrites(t, request, choice)
		})
	}
}

func TestFutureTaskGuardAllowsOnlySelectedActionAndRequiredSetupID(t *testing.T) {
	tests := []struct {
		name       string
		status     CapabilityStatus
		choice     OwnerChoice
		allowed    FutureTaskActionKind
		capability string
	}{
		{"ready main only", CapabilityReadyVerified, "", FutureTaskMainAgentWorkflow, ""},
		{"partial local only", CapabilityPartiallyImplemented, OwnerUsePermittedLocalDevelopment, FutureTaskMainLocalDevelopment, ""},
		{"not started local only", CapabilityNotStarted, OwnerUsePermittedLocalDevelopment, FutureTaskMainLocalDevelopment, ""},
		{"known uncertainty local only", CapabilityUnknown, OwnerUsePermittedLocalDevelopment, FutureTaskMainLocalDevelopment, ""},
		{"partial narrow setup", CapabilityPartiallyImplemented, OwnerFinishRequiredCapability, FutureTaskSetupRequiredCapability, "workers"},
		{"not started narrow setup", CapabilityNotStarted, OwnerFinishRequiredCapability, FutureTaskSetupRequiredCapability, "workers"},
		{"blocked narrow setup", CapabilityBlocked, OwnerFinishRequiredCapability, FutureTaskSetupRequiredCapability, "workers"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := futureTaskFixture(test.status)
			var choice *OwnerChoiceRecord
			if test.choice != "" {
				value := futureTaskChoice(request, test.choice)
				choice = &value
			}
			root := t.TempDir()
			fixture := filepath.Join(root, "selected-action.txt")
			calls := 0
			callback := func(context.Context) error {
				calls++
				return os.WriteFile(fixture, []byte("authorized fixture mutation"), 0600)
			}
			for _, kind := range []FutureTaskActionKind{FutureTaskMainAgentWorkflow, FutureTaskMainLocalDevelopment, FutureTaskSetupRequiredCapability} {
				id := ""
				if kind == FutureTaskSetupRequiredCapability {
					id = "workers"
				}
				if kind == test.allowed {
					continue
				}
				if err := RunFutureTaskAction(context.Background(), request, choice, kind, id, callback); !errors.Is(err, ErrFutureTaskActionDenied) {
					t.Fatalf("wrong action did not fail closed: %s: %v", kind, err)
				}
			}
			if err := RunFutureTaskAction(context.Background(), request, choice, FutureTaskSetupRequiredCapability, "unrequested-capability", callback); !errors.Is(err, ErrFutureTaskActionDenied) {
				t.Fatalf("unrequested setup was authorized: %v", err)
			}
			if calls != 0 {
				t.Fatalf("denied callbacks ran %d times", calls)
			}
			if err := RunFutureTaskAction(context.Background(), request, choice, test.allowed, test.capability, callback); err != nil {
				t.Fatalf("selected action denied: %v", err)
			}
			content, err := os.ReadFile(fixture)
			if err != nil || string(content) != "authorized fixture mutation" || calls != 1 {
				t.Fatalf("selected callback not observed once: calls=%d content=%q err=%v", calls, content, err)
			}
		})
	}
}

func TestFutureTaskSetupCannotPromoteCanaryUnknownOrReadyRequirements(t *testing.T) {
	for _, status := range []CapabilityStatus{CapabilityReadyVerified, CapabilityCanaryVerified, CapabilityUnknown} {
		t.Run(string(status), func(t *testing.T) {
			request := futureTaskFixture(CapabilityPartiallyImplemented)
			other := request.Requirements[0]
			other.ID, other.Status = "other", status
			request.Requirements = append(request.Requirements, other)
			choice := futureTaskChoice(request, OwnerFinishRequiredCapability)
			calls := 0
			err := RunFutureTaskAction(context.Background(), request, &choice, FutureTaskSetupRequiredCapability, "other", func(context.Context) error { calls++; return nil })
			if !errors.Is(err, ErrFutureTaskActionDenied) || calls != 0 {
				t.Fatalf("nonmissing or unknown capability promoted to setup: %v calls=%d", err, calls)
			}
		})
	}
	request := futureTaskFixture(CapabilityBlocked)
	request.Requirements[0].ExecutionPermitted = false
	choice := futureTaskChoice(request, OwnerFinishRequiredCapability)
	assertFutureTaskNoWrites(t, request, &choice)
}

func TestFutureTaskReverificationInvalidatesOldChoiceAndUsesExistingRules(t *testing.T) {
	request := futureTaskFixture(CapabilityPartiallyImplemented)
	choice := futureTaskChoice(request, OwnerFinishRequiredCapability)
	request.Requirements[0].Status = CapabilityReadyVerified
	if decision := EvaluateFutureTask(request, &choice); decision.Action != FutureTaskWaitOwner || decision.MainTaskAllowed || decision.AgentSetupAllowed {
		t.Fatalf("old requirement digest accepted after reverification: %+v", decision)
	}
	if decision := EvaluateFutureTask(request, nil); decision.Action != FutureTaskApplyExisting || !decision.MainTaskAllowed || decision.AgentSetupAllowed || decision.OwnerChoiceRequired {
		t.Fatalf("reverified operation did not apply existing rules: %+v", decision)
	}
}

func TestFutureTaskGuardContextAndCallbackErrors(t *testing.T) {
	request := futureTaskFixture(CapabilityReadyVerified)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	callback := func(context.Context) error { calls++; return errors.New("fixture failure") }
	if err := RunFutureTaskAction(ctx, request, nil, FutureTaskMainAgentWorkflow, "", callback); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled action invoked: %v calls=%d", err, calls)
	}
	if err := RunFutureTaskAction(context.Background(), request, nil, FutureTaskMainAgentWorkflow, "", nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := RunFutureTaskAction(context.Background(), request, nil, FutureTaskMainAgentWorkflow, "", callback); err == nil || err.Error() != "fixture failure" || calls != 1 {
		t.Fatalf("callback failure not propagated: %v calls=%d", err, calls)
	}
}

func futureTaskFixture(status CapabilityStatus) FutureTaskRequest {
	return FutureTaskRequest{
		Repository: "/approved-fixture-repository", TaskFingerprint: "task-fixture-fingerprint", LocalDevelopmentPermitted: true,
		Requirements: []FutureTaskRequirement{{ID: "workers", Status: status, EvidenceConfirmed: true, EvidenceFingerprint: "sha256:" + strings.Repeat("a", 64), RepositoryMatched: true, OperationMatched: true, ExecutionPermitted: true}},
	}
}

func futureTaskChoice(request FutureTaskRequest, selection OwnerChoice) OwnerChoiceRecord {
	return OwnerChoiceRecord{Repository: request.Repository, TaskFingerprint: request.TaskFingerprint,
		RequirementsFingerprint: EvaluateFutureTask(request, nil).RequirementsFingerprint, Choice: selection}
}

func assertFutureTaskNoWrites(t *testing.T, request FutureTaskRequest, choice *OwnerChoiceRecord) {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "protected-fixture.txt")
	const original = "original owner data"
	if err := os.WriteFile(fixture, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	callback := func(context.Context) error {
		calls++
		return os.WriteFile(fixture, []byte("unauthorized mutation"), 0600)
	}
	for _, kind := range []FutureTaskActionKind{FutureTaskMainAgentWorkflow, FutureTaskMainLocalDevelopment, FutureTaskSetupRequiredCapability} {
		id := ""
		if kind == FutureTaskSetupRequiredCapability {
			id = "workers"
		}
		if err := RunFutureTaskAction(context.Background(), request, choice, kind, id, callback); !errors.Is(err, ErrFutureTaskActionDenied) {
			t.Fatalf("denied action did not return authorization error: %s: %v", kind, err)
		}
	}
	content, err := os.ReadFile(fixture)
	if err != nil || strings.TrimSpace(string(content)) != original || calls != 0 {
		t.Fatalf("owner barrier failed: calls=%d content=%q err=%v", calls, content, err)
	}
}
