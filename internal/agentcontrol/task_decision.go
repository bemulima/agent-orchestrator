package agentcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// CapabilityStatus describes implementation evidence, not production admission.
// In particular, a canary does not establish readiness for another operation,
// repository, stack, or execution environment.
type CapabilityStatus string

const (
	CapabilityReadyVerified        CapabilityStatus = "READY_VERIFIED"
	CapabilityCanaryVerified       CapabilityStatus = "CANARY_VERIFIED"
	CapabilityPartiallyImplemented CapabilityStatus = "PARTIALLY_IMPLEMENTED"
	CapabilityNotStarted           CapabilityStatus = "NOT_STARTED"
	CapabilityBlocked              CapabilityStatus = "BLOCKED"
	CapabilityUnknown              CapabilityStatus = "UNKNOWN"
)

// WorkflowCapability belongs to the existing agent-control manifest. Scope and
// execution requirements must be satisfied separately for the requested task.
type WorkflowCapability struct {
	ID                    string           `yaml:"id" json:"id"`
	Status                CapabilityStatus `yaml:"status" json:"status"`
	Scope                 string           `yaml:"scope" json:"scope"`
	Profiles              []string         `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Evidence              []string         `yaml:"evidence" json:"evidence"`
	ExecutionRequirements []string         `yaml:"execution_requirements" json:"execution_requirements"`
}

func ValidateWorkflowCapabilities(capabilities []WorkflowCapability) error {
	seen := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		if !validCapabilityID(capability.ID) || seen[capability.ID] {
			return fmt.Errorf("workflow capability has unsafe or duplicate id %q", capability.ID)
		}
		seen[capability.ID] = true
		if !validCapabilityStatus(capability.Status) {
			return fmt.Errorf("workflow capability %q has unknown status %q", capability.ID, capability.Status)
		}
		if strings.TrimSpace(capability.Scope) == "" || len(capability.Evidence) == 0 || len(capability.ExecutionRequirements) == 0 {
			return fmt.Errorf("workflow capability %q requires scope, evidence, and execution requirements", capability.ID)
		}
		for _, evidence := range capability.Evidence {
			if evidence != strings.TrimSpace(evidence) || containsControl(evidence) || strings.Contains(evidence, "\\") || validateRelativePath(evidence) != nil {
				return fmt.Errorf("workflow capability %q has unsafe relative evidence path %q", capability.ID, evidence)
			}
		}
		for _, requirement := range capability.ExecutionRequirements {
			if strings.TrimSpace(requirement) == "" {
				return fmt.Errorf("workflow capability %q has an empty execution requirement", capability.ID)
			}
		}
		profiles := make(map[string]bool, len(capability.Profiles))
		for _, profile := range capability.Profiles {
			if !validCapabilityID(profile) || profiles[profile] {
				return fmt.Errorf("workflow capability %q has unsafe or duplicate profile %q", capability.ID, profile)
			}
			profiles[profile] = true
		}
	}
	return nil
}

// FutureTaskRequirement carries task-specific classification, never an automatic
// promotion of a central canary. A caller must establish and explicitly classify
// READY_VERIFIED for the exact operation before EvaluateFutureTask can use it.
type FutureTaskRequirement struct {
	ID                string           `json:"id" yaml:"id"`
	Status            CapabilityStatus `json:"status" yaml:"status"`
	EvidenceConfirmed bool             `json:"evidence_confirmed" yaml:"evidence_confirmed"`
	// EvidenceFingerprint is the trusted caller's canonical sha256:<hex> digest
	// of exact capability metadata (scope, profiles, evidence references and
	// execution requirements), repository/revision, task operation and current
	// verification/permission evidence. Recompute it whenever any bound content
	// changes. Summary booleans alone cannot identify that evidence. This pure
	// gate validates and binds the digest; it does not inspect or hash sources.
	EvidenceFingerprint string `json:"evidence_fingerprint" yaml:"evidence_fingerprint"`
	RepositoryMatched   bool   `json:"repository_matched" yaml:"repository_matched"`
	OperationMatched    bool   `json:"operation_matched" yaml:"operation_matched"`
	ExecutionPermitted  bool   `json:"execution_permitted" yaml:"execution_permitted"`
}

type FutureTaskRequest struct {
	Repository                 string                  `json:"repository" yaml:"repository"`
	TaskFingerprint            string                  `json:"task_fingerprint" yaml:"task_fingerprint"`
	Requirements               []FutureTaskRequirement `json:"requirements" yaml:"requirements"`
	ProductionRequired         bool                    `json:"production_required" yaml:"production_required"`
	ProductionAdmissionAllowed bool                    `json:"production_admission_allowed" yaml:"production_admission_allowed"`
	LocalDevelopmentPermitted  bool                    `json:"local_development_permitted" yaml:"local_development_permitted"`
}

type OwnerChoice string

const (
	OwnerFinishRequiredCapability     OwnerChoice = "finish_required_capability"
	OwnerUsePermittedLocalDevelopment OwnerChoice = "use_permitted_local_development"
	OwnerStop                         OwnerChoice = "stop"
)

type OwnerChoiceRecord struct {
	Repository              string      `json:"repository" yaml:"repository"`
	TaskFingerprint         string      `json:"task_fingerprint" yaml:"task_fingerprint"`
	RequirementsFingerprint string      `json:"requirements_fingerprint" yaml:"requirements_fingerprint"`
	Choice                  OwnerChoice `json:"choice" yaml:"choice"`
}

type FutureTaskAction string

const (
	FutureTaskApplyExisting                FutureTaskAction = "apply_existing_rules"
	FutureTaskWaitOwner                    FutureTaskAction = "wait_owner_decision"
	FutureTaskFinishRequiredCapability     FutureTaskAction = "finish_required_capability"
	FutureTaskUsePermittedLocalDevelopment FutureTaskAction = "use_permitted_local_development"
	FutureTaskStop                         FutureTaskAction = "stop"
	FutureTaskBlocked                      FutureTaskAction = "blocked"
)

type FutureTaskDecision struct {
	Action                  FutureTaskAction `json:"action" yaml:"action"`
	RequirementsFingerprint string           `json:"requirements_fingerprint" yaml:"requirements_fingerprint"`
	OwnerChoiceRequired     bool             `json:"owner_choice_required" yaml:"owner_choice_required"`
	MainTaskAllowed         bool             `json:"main_task_allowed" yaml:"main_task_allowed"`
	AgentSetupAllowed       bool             `json:"agent_setup_allowed" yaml:"agent_setup_allowed"`
	Blockers                []string         `json:"blockers" yaml:"blockers"`
}

// EvaluateFutureTask is a pure, task-scoped policy decision. Callers establish
// evidence by bounded read-only discovery; this function performs no discovery,
// setup, execution, or automatic production certification.
func EvaluateFutureTask(request FutureTaskRequest, choice *OwnerChoiceRecord) FutureTaskDecision {
	decision := FutureTaskDecision{
		Action: FutureTaskWaitOwner, RequirementsFingerprint: requirementsFingerprint(request),
		OwnerChoiceRequired: true, Blockers: []string{},
	}
	if request.ProductionRequired && !request.ProductionAdmissionAllowed {
		decision.Action = FutureTaskBlocked
		decision.OwnerChoiceRequired = false
		decision.Blockers = append(decision.Blockers, "production admission is denied; a task choice cannot authorize a fallback")
		return decision
	}
	if !validTaskIdentity(request.Repository) || !validTaskIdentity(request.TaskFingerprint) || len(request.Requirements) == 0 {
		decision.Blockers = append(decision.Blockers, "UNKNOWN: repository, task identity, or required capabilities are missing")
		return decision
	}
	seen := make(map[string]bool, len(request.Requirements))
	allReady := true
	for _, requirement := range request.Requirements {
		if !validCapabilityID(requirement.ID) || seen[requirement.ID] || !validCapabilityStatus(requirement.Status) || !isSHA256(requirement.EvidenceFingerprint) ||
			!requirement.EvidenceConfirmed || !requirement.RepositoryMatched || !requirement.OperationMatched {
			decision.Blockers = append(decision.Blockers, "UNKNOWN: required capability has invalid, unconfirmed, or mismatched facts: "+requirement.ID)
			return decision
		}
		seen[requirement.ID] = true
		if requirement.Status != CapabilityReadyVerified || !requirement.ExecutionPermitted {
			allReady = false
			decision.Blockers = append(decision.Blockers, "required capability is not ready for permitted execution: "+requirement.ID+" ("+string(requirement.Status)+")")
		}
	}
	if choice != nil {
		if choice.Repository != request.Repository || choice.TaskFingerprint != request.TaskFingerprint ||
			choice.RequirementsFingerprint != decision.RequirementsFingerprint {
			decision.Blockers = append(decision.Blockers, "owner choice is stale or belongs to a different repository, task, or requirements")
			return decision
		}
		switch choice.Choice {
		case OwnerStop:
			decision.Action, decision.OwnerChoiceRequired = FutureTaskStop, false
			return decision
		case OwnerUsePermittedLocalDevelopment:
			decision.OwnerChoiceRequired = false
			if !request.LocalDevelopmentPermitted {
				decision.Action = FutureTaskBlocked
				decision.Blockers = append(decision.Blockers, "no existing permitted local development path is established")
				return decision
			}
			decision.Action, decision.MainTaskAllowed = FutureTaskUsePermittedLocalDevelopment, true
			return decision
		case OwnerFinishRequiredCapability:
			if allReady {
				decision.Action, decision.OwnerChoiceRequired, decision.MainTaskAllowed = FutureTaskApplyExisting, false, true
				return decision
			}
			decision.Action, decision.OwnerChoiceRequired = FutureTaskFinishRequiredCapability, false
			for _, requirement := range request.Requirements {
				if missingSetupPermitted(requirement) {
					decision.AgentSetupAllowed = true
				}
			}
			if !decision.AgentSetupAllowed {
				decision.Action = FutureTaskBlocked
				decision.Blockers = append(decision.Blockers, "no known missing required capability has a permitted setup path")
			}
			return decision
		default:
			decision.Blockers = append(decision.Blockers, "owner choice is unknown")
			return decision
		}
	}
	if allReady {
		decision.Action, decision.OwnerChoiceRequired, decision.MainTaskAllowed = FutureTaskApplyExisting, false, true
	}
	return decision
}

type FutureTaskActionKind string

const (
	FutureTaskMainAgentWorkflow       FutureTaskActionKind = "main_agent_workflow"
	FutureTaskMainLocalDevelopment    FutureTaskActionKind = "main_local_development"
	FutureTaskSetupRequiredCapability FutureTaskActionKind = "setup_required_capability"
)

var ErrFutureTaskActionDenied = errors.New("future task action is not authorized by the applicable capability and owner decision")

// RunFutureTaskAction checks the decision before invoking a caller-supplied
// action. Setup is confined to one explicitly requested, known missing ID; it
// does not grant runtime, filesystem, or production permission beyond the input
// facts. This helper must be called by a dispatcher to guard that dispatcher.
func RunFutureTaskAction(ctx context.Context, request FutureTaskRequest, choice *OwnerChoiceRecord, kind FutureTaskActionKind, capabilityID string, callback func(context.Context) error) error {
	if ctx == nil {
		return errors.New("future task action requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	decision := EvaluateFutureTask(request, choice)
	allowed := false
	switch kind {
	case FutureTaskMainAgentWorkflow:
		allowed = capabilityID == "" && decision.MainTaskAllowed && decision.Action == FutureTaskApplyExisting
	case FutureTaskMainLocalDevelopment:
		allowed = capabilityID == "" && decision.MainTaskAllowed && decision.Action == FutureTaskUsePermittedLocalDevelopment
	case FutureTaskSetupRequiredCapability:
		if decision.AgentSetupAllowed && decision.Action == FutureTaskFinishRequiredCapability {
			for _, requirement := range request.Requirements {
				if requirement.ID == capabilityID && missingSetupPermitted(requirement) {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return fmt.Errorf("%w: %s (%s)", ErrFutureTaskActionDenied, kind, strings.Join(decision.Blockers, "; "))
	}
	if callback == nil {
		return errors.New("future task action requires a callback")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return callback(ctx)
}

func requirementsFingerprint(request FutureTaskRequest) string {
	requirements := append([]FutureTaskRequirement(nil), request.Requirements...)
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].ID != requirements[j].ID {
			return requirements[i].ID < requirements[j].ID
		}
		first, _ := json.Marshal(requirements[i])
		second, _ := json.Marshal(requirements[j])
		return string(first) < string(second)
	})
	material, _ := json.Marshal(struct {
		Requirements               []FutureTaskRequirement `json:"requirements"`
		ProductionRequired         bool                    `json:"production_required"`
		ProductionAdmissionAllowed bool                    `json:"production_admission_allowed"`
		LocalDevelopmentPermitted  bool                    `json:"local_development_permitted"`
	}{requirements, request.ProductionRequired, request.ProductionAdmissionAllowed, request.LocalDevelopmentPermitted})
	digest := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func missingSetupPermitted(requirement FutureTaskRequirement) bool {
	return requirement.ExecutionPermitted && requirement.EvidenceConfirmed && isSHA256(requirement.EvidenceFingerprint) && requirement.RepositoryMatched && requirement.OperationMatched &&
		(requirement.Status == CapabilityPartiallyImplemented || requirement.Status == CapabilityNotStarted || requirement.Status == CapabilityBlocked)
}

func validCapabilityStatus(status CapabilityStatus) bool {
	switch status {
	case CapabilityReadyVerified, CapabilityCanaryVerified, CapabilityPartiallyImplemented, CapabilityNotStarted, CapabilityBlocked, CapabilityUnknown:
		return true
	default:
		return false
	}
}

func validCapabilityID(value string) bool {
	if validateRelativePath(value) != nil {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' ||
			character == '.' || character == '-' || character == '_' || character == '/' {
			continue
		}
		return false
	}
	return true
}

func validTaskIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !containsControl(value)
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}
