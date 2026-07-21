package agent

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const maxStructuredResultBytes = 512 << 10

//go:embed schema/*.json
var schemas embed.FS

type Validator struct {
	agentSchemaMap    map[string]any
	reviewerSchemaMap map[string]any
	agentSchema       *jsonschema.Schema
	reviewerSchema    *jsonschema.Schema
}

func NewValidator() (*Validator, error) {
	agentSchemaMap, agentSchema, err := compileSchema("schema/coder-result.schema.json", "coder-result.json")
	if err != nil {
		return nil, err
	}
	reviewerSchemaMap, reviewerSchema, err := compileSchema("schema/reviewer-result.schema.json", "reviewer-result.json")
	if err != nil {
		return nil, err
	}
	return &Validator{
		agentSchemaMap: agentSchemaMap, reviewerSchemaMap: reviewerSchemaMap,
		agentSchema: agentSchema, reviewerSchema: reviewerSchema,
	}, nil
}

func compileSchema(path, resource string) (map[string]any, *jsonschema.Schema, error) {
	content, err := schemas.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read embedded schema %s: %w", path, err)
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(content, &schemaMap); err != nil {
		return nil, nil, fmt.Errorf("decode embedded schema %s: %w", path, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
	if err != nil {
		return nil, nil, fmt.Errorf("parse embedded schema %s: %w", path, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resource, value); err != nil {
		return nil, nil, fmt.Errorf("add embedded schema %s: %w", path, err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, nil, fmt.Errorf("compile embedded schema %s: %w", path, err)
	}
	return schemaMap, compiled, nil
}

func (v *Validator) AgentSchema() map[string]any {
	return cloneSchema(v.agentSchemaMap)
}

func (v *Validator) ReviewerSchema() map[string]any {
	return cloneSchema(v.reviewerSchemaMap)
}

func (v *Validator) ValidateAgentResult(content []byte) (domain.AgentResult, error) {
	var result domain.AgentResult
	if err := validateJSON(content, v.agentSchema, &result); err != nil {
		return domain.AgentResult{}, fmt.Errorf("validate coder result: %w", err)
	}
	if result.Status == domain.AgentResultCompleted && len(result.RequiredTasks) != 0 {
		return domain.AgentResult{}, fmt.Errorf("completed result cannot request dependent tasks: %w", domain.ErrValidation)
	}
	if result.Status == domain.AgentResultBlocked && len(result.Blockers) == 0 && len(result.RequiredTasks) == 0 {
		return domain.AgentResult{}, fmt.Errorf("blocked result must describe a blocker: %w", domain.ErrValidation)
	}
	for _, path := range result.FilesChanged {
		if !safeRelativePath(path) {
			return domain.AgentResult{}, fmt.Errorf("unsafe claimed changed path %q: %w", path, domain.ErrWriteScope)
		}
	}
	for _, artifact := range result.Artifacts {
		if !safeRelativePath(artifact.Path) {
			return domain.AgentResult{}, fmt.Errorf("unsafe artifact path %q: %w", artifact.Path, domain.ErrWriteScope)
		}
	}
	return result, nil
}

func (v *Validator) ValidateReviewerResult(content []byte) (domain.ReviewerResult, error) {
	var result domain.ReviewerResult
	if err := validateJSON(content, v.reviewerSchema, &result); err != nil {
		return domain.ReviewerResult{}, fmt.Errorf("validate reviewer result: %w", err)
	}
	if result.Status == domain.ReviewApproved && len(result.BlockingIssues) != 0 {
		return domain.ReviewerResult{}, fmt.Errorf("approved review contains blocking issues: %w", domain.ErrValidation)
	}
	if result.Status == domain.ReviewChangesRequested && len(result.BlockingIssues) == 0 {
		return domain.ReviewerResult{}, fmt.Errorf("changes-requested review has no blocking issues: %w", domain.ErrValidation)
	}
	for _, issue := range append(append([]domain.ReviewIssue(nil), result.BlockingIssues...), result.NonBlockingIssues...) {
		if issue.Path != "" && !safeRelativePath(issue.Path) {
			return domain.ReviewerResult{}, fmt.Errorf("unsafe review path %q: %w", issue.Path, domain.ErrWriteScope)
		}
	}
	return result, nil
}

func validateJSON(content []byte, schema *jsonschema.Schema, target any) error {
	if len(content) == 0 || len(content) > maxStructuredResultBytes || !json.Valid(content) {
		return fmt.Errorf("structured result is not bounded valid JSON: %w", domain.ErrValidation)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode structured result: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("JSON Schema mismatch: %w: %w", err, domain.ErrValidation)
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("decode typed structured result: %w", err)
	}
	return nil
}

func safeRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	cleaned := filepath.Clean(filepath.FromSlash(value))
	return value != "" && !filepath.IsAbs(cleaned) && cleaned != "." && cleaned != ".." &&
		!strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) && !strings.ContainsRune(value, '\x00')
}

func cloneSchema(value map[string]any) map[string]any {
	content, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(content, &cloned)
	return cloned
}

var _ repository.AgentResultValidator = (*Validator)(nil)
