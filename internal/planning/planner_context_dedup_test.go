package planning

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPlannerContextRetainsOneCompleteRequest(t *testing.T) {
	request := strings.Repeat("Preserve all exact owner contracts and acceptance constraints.\n", 2048)
	baseline := domain.PlannerOutput{}
	var projects []domain.Project
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		path := "/checkout/" + id
		projects = append(projects, domain.Project{ID: id, LocalPath: &path})
		baseline.Tasks = append(baseline.Tasks, domain.PlannedTask{ProjectID: id, Description: request, WriteScope: []string{"internal/**"}, VerificationCommands: []string{"go test ./..."}})
	}
	value, _, err := buildPlannerAgentContext(request, baseline, domain.TopologyCatalog{}, projects, domain.RoutingResult{}, domain.ContractPlan{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.Request != request {
		t.Fatal("complete request was truncated or changed")
	}
	for index, task := range value.Baseline.Tasks {
		if task.Description == request || !strings.Contains(task.Description, "CONTEXT.request") || len(task.WriteScope) != 1 || len(task.VerificationCommands) != 1 {
			t.Fatalf("prompt task %s lost request reference or approved scope", task.ProjectID)
		}
		if baseline.Tasks[index].Description != request {
			t.Fatal("prompt preparation mutated the source baseline")
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) >= 1<<20 {
		t.Fatalf("repeated baseline text exceeds the retained runner cap: bytes=%d err=%v", len(encoded), err)
	}
}

func TestPlannerContextPreservesDifferentOwnerDescriptions(t *testing.T) {
	path := "/checkout/a"
	baseline := domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "a", Description: "Unique owner restriction and acceptance evidence."}}}
	value, _, err := buildPlannerAgentContext("Shared request", baseline, domain.TopologyCatalog{}, []domain.Project{{ID: "a", LocalPath: &path}}, domain.RoutingResult{}, domain.ContractPlan{}, nil, nil)
	if err != nil || value.Baseline.Tasks[0].Description != baseline.Tasks[0].Description {
		t.Fatalf("distinct owner description was changed: %#v err=%v", value.Baseline.Tasks, err)
	}
}
