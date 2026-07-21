package agent

import (
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestValidatorAcceptsStructuredCoderAndReviewerResults(t *testing.T) {
	validator, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	coder := []byte(`{
        "status":"completed","summary":"implemented","files_changed":["internal/orders.go"],
        "checks":[{"name":"go test ./...","status":"passed","details":"ok"}],
        "artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]
    }`)
	if result, err := validator.ValidateAgentResult(coder); err != nil || result.Status != domain.AgentResultCompleted {
		t.Fatalf("ValidateAgentResult() = %#v, %v", result, err)
	}
	reviewer := []byte(`{
        "status":"approved","summary":"looks good","blocking_issues":[],
        "non_blocking_issues":[],"risks":[],"suggested_checks":[]
    }`)
	if result, err := validator.ValidateReviewerResult(reviewer); err != nil || result.Status != domain.ReviewApproved {
		t.Fatalf("ValidateReviewerResult() = %#v, %v", result, err)
	}
}

func TestValidatorRejectsSchemaAndSemanticViolations(t *testing.T) {
	validator, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]byte{
		[]byte(`{"status":"completed"}`),
		[]byte(`{"status":"completed","summary":"x","files_changed":["../escape"],"checks":[],"artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]}`),
		[]byte(`{"status":"blocked","summary":"x","files_changed":[],"checks":[],"artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]}`),
	}
	for _, content := range tests {
		if _, err := validator.ValidateAgentResult(content); !errors.Is(err, domain.ErrValidation) && !errors.Is(err, domain.ErrWriteScope) {
			t.Fatalf("ValidateAgentResult(%s) error = %v", content, err)
		}
	}
}
