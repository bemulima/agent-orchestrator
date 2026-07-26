package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestAgentSchemasRequireEveryDeclaredObjectProperty(t *testing.T) {
	validator, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	for name, schema := range map[string]map[string]any{
		"coder": validator.AgentSchema(), "reviewer": validator.ReviewerSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			assertStrictAgentObjectRequirements(t, schema, "$")
		})
	}
}

func assertStrictAgentObjectRequirements(t *testing.T, value any, path string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			required := make(map[string]struct{})
			if values, ok := typed["required"].([]any); ok {
				for _, value := range values {
					if name, ok := value.(string); ok {
						required[name] = struct{}{}
					}
				}
			} else if values, ok := typed["required"].([]string); ok {
				for _, name := range values {
					required[name] = struct{}{}
				}
			}
			for name := range properties {
				if _, ok := required[name]; !ok {
					t.Fatalf("%s property %q is not required", path, name)
				}
			}
		}
		for key, child := range typed {
			assertStrictAgentObjectRequirements(t, child, path+"."+key)
		}
	case []any:
		for index, child := range typed {
			assertStrictAgentObjectRequirements(t, child, fmt.Sprintf("%s[%d]", path, index))
		}
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			t.Fatal(err)
		}
		assertStrictAgentObjectRequirements(t, decoded, path)
	}
}

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
