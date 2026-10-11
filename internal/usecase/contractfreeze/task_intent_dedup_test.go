package contractfreeze

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func repeatedApprovedIntentFixture() (domain.Task, domain.PlannedTask) {
	criteria := make([]string, 12)
	for i := range criteria {
		size := 676
		if i == 11 {
			size = 684
		}
		criteria[i] = string(rune('A'+i)) + strings.Repeat("x", size-1)
	}
	task := domain.Task{Title: strings.Repeat("t", 127), Description: strings.Repeat("d", 1971), AcceptanceCriteria: criteria}
	planned := domain.PlannedTask{Title: task.Title, Description: task.Description, AcceptanceCriteria: append([]string(nil), criteria...)}
	return task, planned
}

func TestBoundedTaskIntentRetainsOneCompleteExactlyRepeatedApprovedIntent(t *testing.T) {
	task, planned := repeatedApprovedIntentFixture()
	originalTask, originalPlanned := task, planned
	originalTask.AcceptanceCriteria = append([]string(nil), task.AcceptanceCriteria...)
	originalPlanned.AcceptanceCriteria = append([]string(nil), planned.AcceptanceCriteria...)
	intent, err := boundedTaskIntent(task, planned)
	if err != nil {
		t.Fatalf("one complete 10218-byte approved intent repeated in persisted representations was rejected: %v", err)
	}
	if intent.TaskTitle != task.Title || intent.TaskDescription != task.Description || !reflect.DeepEqual(intent.TaskAcceptanceCriteria, task.AcceptanceCriteria) {
		t.Fatal("complete approved task requirements were changed or truncated")
	}
	encoded, err := json.Marshal(intent)
	if err != nil || len(encoded) >= maxTaskIntentBytes {
		t.Fatalf("encoded repeated intent remains oversized: bytes=%d err=%v", len(encoded), err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["planned_task_matches_task"]) != "true" {
		t.Fatal("planned intent lost its explicit exact-match relationship to the retained task")
	}
	for _, key := range []string{"planned_task_title", "planned_task_description", "planned_acceptance_criteria"} {
		if _, present := wire[key]; present {
			t.Fatalf("duplicate approved content remains in %s", key)
		}
	}
	if !reflect.DeepEqual(task, originalTask) || !reflect.DeepEqual(planned, originalPlanned) {
		t.Fatal("context preparation mutated persisted task inputs")
	}
}

func TestBoundedTaskIntentDoesNotDeduplicateDistinctApprovedRequirements(t *testing.T) {
	for _, changed := range []string{"title", "description", "criteria value", "criteria order"} {
		t.Run(changed, func(t *testing.T) {
			task, planned := repeatedApprovedIntentFixture()
			switch changed {
			case "title":
				planned.Title += "!"
			case "description":
				planned.Description += "!"
			case "criteria value":
				planned.AcceptanceCriteria[11] += "!"
			case "criteria order":
				planned.AcceptanceCriteria[0], planned.AcceptanceCriteria[11] = planned.AcceptanceCriteria[11], planned.AcceptanceCriteria[0]
			}
			if _, err := boundedTaskIntent(task, planned); err == nil || !strings.Contains(err.Error(), "exceeds 16384 bytes") {
				t.Fatalf("distinct oversized approved requirements were silently reduced: %v", err)
			}
		})
	}
	task := domain.Task{Title: "Task", Description: "Task constraint", AcceptanceCriteria: []string{"Task criterion"}}
	planned := domain.PlannedTask{Title: "Planned", Description: "Planned constraint", AcceptanceCriteria: []string{"Planned criterion"}}
	intent, err := boundedTaskIntent(task, planned)
	if err != nil || intent.TaskDescription != task.Description || intent.PlannedTaskDescription != planned.Description ||
		!reflect.DeepEqual(intent.TaskAcceptanceCriteria, task.AcceptanceCriteria) || !reflect.DeepEqual(intent.PlannedAcceptanceCriteria, planned.AcceptanceCriteria) {
		t.Fatalf("distinct bounded approved requirements were changed: intent=%#v err=%v", intent, err)
	}
}

func TestBoundedTaskIntentRepeatedInputStillEnforcesAllOriginalLimits(t *testing.T) {
	for _, task := range []domain.Task{
		{Description: strings.Repeat("x", maxTaskIntentFieldBytes+1)},
		{AcceptanceCriteria: make([]string, maxTaskIntentCriteria+1)},
		{AcceptanceCriteria: []string{strings.Repeat("x", maxTaskIntentFieldBytes+1)}},
		{AcceptanceCriteria: []string{strings.Repeat("x", 6000), strings.Repeat("y", 6000), strings.Repeat("z", 6000)}},
	} {
		planned := domain.PlannedTask{Title: task.Title, Description: task.Description, AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...)}
		if _, err := boundedTaskIntent(task, planned); err == nil {
			t.Fatal("duplicate approved input bypassed field, criteria-count or aggregate limit")
		}
	}
}
