package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ArchitectureTargetStatus is the lifecycle of an editable architecture
// proposal. CURRENT is immutable and deliberately not represented by this
// state machine.
type ArchitectureTargetStatus string

const (
	ArchitectureTargetStatusDraft            ArchitectureTargetStatus = "draft"
	ArchitectureTargetStatusSubmitted        ArchitectureTargetStatus = "submitted"
	ArchitectureTargetStatusApproved         ArchitectureTargetStatus = "approved"
	ArchitectureTargetStatusRejected         ArchitectureTargetStatus = "rejected"
	ArchitectureTargetStatusChangesRequested ArchitectureTargetStatus = "changes_requested"
	ArchitectureTargetStatusSuperseded       ArchitectureTargetStatus = "superseded"
)

// ArchitectureTargetScope limits a proposal change to architecture entities
// that are visible in the CURRENT catalog. It intentionally has no generic
// manifest/YAML scope.
type ArchitectureTargetScope string

const (
	ArchitectureTargetScopeService   ArchitectureTargetScope = "service"
	ArchitectureTargetScopeOperation ArchitectureTargetScope = "operation"
)

// ArchitectureTargetChangeAction describes the desired TARGET delta; it does
// not execute code or mutate a CURRENT manifest.
type ArchitectureTargetChangeAction string

const (
	ArchitectureTargetChangeAdd    ArchitectureTargetChangeAction = "add"
	ArchitectureTargetChangeChange ArchitectureTargetChangeAction = "change"
	ArchitectureTargetChangeRemove ArchitectureTargetChangeAction = "remove"
)

// ArchitectureTargetChangeKind is the business-level intent of a delta. The
// action remains useful to render a compact diff, while Kind prevents callers
// from representing unrelated architecture edits as an untyped blob.
type ArchitectureTargetChangeKind string

const (
	ArchitectureTargetChangeKindService         ArchitectureTargetChangeKind = "service"
	ArchitectureTargetChangeKindDependency      ArchitectureTargetChangeKind = "dependency"
	ArchitectureTargetChangeKindOperation       ArchitectureTargetChangeKind = "operation"
	ArchitectureTargetChangeKindContract        ArchitectureTargetChangeKind = "contract"
	ArchitectureTargetChangeKindEvent           ArchitectureTargetChangeKind = "event"
	ArchitectureTargetChangeKindResponsibility  ArchitectureTargetChangeKind = "responsibility"
	ArchitectureTargetChangeKindBusinessProcess ArchitectureTargetChangeKind = "business_process"
)

// ArchitectureTargetProposal is an editable TARGET proposal bound to one
// immutable CURRENT fingerprint. Changes and their deterministic diff/impact
// projections are structured data, never raw YAML or source content.
type ArchitectureTargetProposal struct {
	ID                    string                     `json:"id"`
	CurrentFingerprint    string                     `json:"current_fingerprint"`
	Fingerprint           string                     `json:"fingerprint"`
	Status                ArchitectureTargetStatus   `json:"status"`
	Revision              int                        `json:"revision"`
	IdempotencyKey        string                     `json:"idempotency_key"`
	Changes               []ArchitectureTargetChange `json:"changes"`
	Diff                  ArchitectureTargetDiff     `json:"diff"`
	Impact                ArchitectureTargetImpact   `json:"impact"`
	CreatedAt             time.Time                  `json:"created_at"`
	UpdatedAt             time.Time                  `json:"updated_at"`
	SubmittedAt           *time.Time                 `json:"submitted_at,omitempty"`
	DecidedAt             *time.Time                 `json:"decided_at,omitempty"`
	SupersededAt          *time.Time                 `json:"superseded_at,omitempty"`
	SupersedesProposalID  string                     `json:"supersedes_proposal_id,omitempty"`
	DecidedBy             string                     `json:"decided_by,omitempty"`
	DecisionComment       string                     `json:"decision_comment,omitempty"`
	ChangesRequestedBy    string                     `json:"changes_requested_by,omitempty"`
	ChangesRequestedAt    *time.Time                 `json:"changes_requested_at,omitempty"`
	ChangesRequestComment string                     `json:"changes_request_comment,omitempty"`
}

// ArchitectureTargetChange names exact service/operation identities affected
// by a proposed addition, change, or removal. A service ID and operation ID
// are stable catalog identities rather than paths, unstructured manifests, or
// source snippets.
type ArchitectureTargetChange struct {
	ID                   string                          `json:"id"`
	Scope                ArchitectureTargetScope         `json:"scope"`
	Action               ArchitectureTargetChangeAction  `json:"action"`
	Kind                 ArchitectureTargetChangeKind    `json:"kind"`
	AffectedServiceIDs   []string                        `json:"affected_service_ids"`
	AffectedOperationIDs []string                        `json:"affected_operation_ids,omitempty"`
	Rationale            ArchitectureTargetRationale     `json:"rationale"`
	DesiredResult        ArchitectureTargetDesiredResult `json:"desired_result"`
	Evidence             []ArchitectureEvidence          `json:"evidence"`
	Confidence           float64                         `json:"confidence"`
	UnresolvedAreas      []ArchitectureTargetUnresolved  `json:"unresolved_areas,omitempty"`
}

// ArchitectureTargetUnresolved makes a material unknown explicit instead of
// turning a proposal assumption into a fake source fact.
type ArchitectureTargetUnresolved struct {
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

// ArchitectureTargetRationale and ArchitectureTargetDesiredResult keep human
// intent typed and bounded. They intentionally cannot carry arbitrary YAML,
// source files, or credentials.
type ArchitectureTargetRationale struct {
	Summary string `json:"summary"`
	Details string `json:"details,omitempty"`
}

type ArchitectureTargetDesiredResult struct {
	Summary         string   `json:"summary"`
	SuccessCriteria []string `json:"success_criteria"`
}

// ArchitectureTargetDiff is a deterministic, evidence-preserving view of the
// requested changes. BaseFingerprint must equal the proposal's immutable
// CURRENT fingerprint.
type ArchitectureTargetDiff struct {
	BaseFingerprint string                        `json:"base_fingerprint"`
	Entries         []ArchitectureTargetDiffEntry `json:"entries"`
}

type ArchitectureTargetDiffEntry struct {
	ChangeID             string                          `json:"change_id"`
	Scope                ArchitectureTargetScope         `json:"scope"`
	Action               ArchitectureTargetChangeAction  `json:"action"`
	Kind                 ArchitectureTargetChangeKind    `json:"kind"`
	AffectedServiceIDs   []string                        `json:"affected_service_ids"`
	AffectedOperationIDs []string                        `json:"affected_operation_ids,omitempty"`
	Summary              string                          `json:"summary"`
	Rationale            ArchitectureTargetRationale     `json:"rationale"`
	DesiredResult        ArchitectureTargetDesiredResult `json:"desired_result"`
	Evidence             []ArchitectureEvidence          `json:"evidence"`
	Confidence           float64                         `json:"confidence"`
	UnresolvedAreas      []ArchitectureTargetUnresolved  `json:"unresolved_areas,omitempty"`
}

// ArchitectureTargetImpact is a deterministic, queryable impact projection.
// Unknown relationships are represented with Kind=unresolved rather than
// guessed as service edges.
type ArchitectureTargetImpact struct {
	BaseFingerprint string                          `json:"base_fingerprint"`
	Entries         []ArchitectureTargetImpactEntry `json:"entries"`
}

type ArchitectureTargetImpactKind string

const (
	ArchitectureTargetImpactDirect     ArchitectureTargetImpactKind = "direct"
	ArchitectureTargetImpactUpstream   ArchitectureTargetImpactKind = "upstream"
	ArchitectureTargetImpactDownstream ArchitectureTargetImpactKind = "downstream"
	ArchitectureTargetImpactUnresolved ArchitectureTargetImpactKind = "unresolved"
)

type ArchitectureTargetImpactEntry struct {
	ChangeID    string                       `json:"change_id"`
	ServiceID   string                       `json:"service_id"`
	OperationID string                       `json:"operation_id,omitempty"`
	Kind        ArchitectureTargetImpactKind `json:"kind"`
	Explanation string                       `json:"explanation"`
	Evidence    []ArchitectureEvidence       `json:"evidence"`
}

// Valid validates data safe for persistence. It is intentionally strict about
// the proposal's immutable CURRENT binding and optimistic revision, but keeps
// semantic impact calculation in the application layer.
func (p ArchitectureTargetProposal) Valid() error {
	if !validArchitectureFingerprint(p.CurrentFingerprint) {
		return fmt.Errorf("current fingerprint must be a SHA-256 hex value: %w", ErrValidation)
	}
	if !validArchitectureTargetStatus(p.Status) || p.Status == "" {
		return fmt.Errorf("target proposal status is invalid: %w", ErrValidation)
	}
	if p.Revision < 1 {
		return fmt.Errorf("target proposal revision must be positive: %w", ErrValidation)
	}
	if strings.TrimSpace(p.IdempotencyKey) == "" || len(p.IdempotencyKey) > 255 {
		return fmt.Errorf("target proposal idempotency key is required and bounded: %w", ErrValidation)
	}
	if len(p.Changes) == 0 {
		return fmt.Errorf("target proposal requires at least one change: %w", ErrValidation)
	}
	changesByID := make(map[string]ArchitectureTargetChange, len(p.Changes))
	for _, change := range p.Changes {
		if err := change.Valid(); err != nil {
			return err
		}
		if _, exists := changesByID[change.ID]; exists {
			return fmt.Errorf("duplicate target change id %q: %w", change.ID, ErrValidation)
		}
		changesByID[change.ID] = change
	}
	if err := p.Diff.Valid(p.CurrentFingerprint, changesByID); err != nil {
		return err
	}
	changeIDs := make(map[string]struct{}, len(changesByID))
	for id := range changesByID {
		changeIDs[id] = struct{}{}
	}
	if err := p.Impact.Valid(p.CurrentFingerprint, changeIDs); err != nil {
		return err
	}
	computed, err := p.ComputedFingerprint()
	if err != nil {
		return err
	}
	if p.Fingerprint != "" && p.Fingerprint != computed {
		return fmt.Errorf("target proposal fingerprint does not match its structured content: %w", ErrConflict)
	}
	if err := validateTargetHumanText(p.DecisionComment, 4000, true); err != nil {
		return fmt.Errorf("target proposal decision comment: %w", err)
	}
	if err := validateTargetHumanText(p.ChangesRequestComment, 4000, true); err != nil {
		return fmt.Errorf("target proposal requested changes comment: %w", err)
	}
	return nil
}

// Validate is the conventional spelling for callers outside the domain. Valid
// remains for consistency with older domain value objects.
func (p ArchitectureTargetProposal) Validate() error { return p.Valid() }

func (c ArchitectureTargetChange) Valid() error {
	if !validArchitectureTargetID(c.ID) {
		return fmt.Errorf("target change id is invalid: %w", ErrValidation)
	}
	if c.Scope != ArchitectureTargetScopeService && c.Scope != ArchitectureTargetScopeOperation {
		return fmt.Errorf("target change %q has invalid scope: %w", c.ID, ErrValidation)
	}
	if c.Action != ArchitectureTargetChangeAdd && c.Action != ArchitectureTargetChangeChange && c.Action != ArchitectureTargetChangeRemove {
		return fmt.Errorf("target change %q has invalid action: %w", c.ID, ErrValidation)
	}
	if !validArchitectureTargetChangeKind(c.Kind) {
		return fmt.Errorf("target change %q has invalid kind: %w", c.ID, ErrValidation)
	}
	if !validTargetKindAction(c.Kind, c.Action) {
		return fmt.Errorf("target change %q has incompatible kind/action: %w", c.ID, ErrValidation)
	}
	if !validTargetScopeForKind(c.Kind, c.Scope) {
		return fmt.Errorf("target change %q has incompatible kind/scope: %w", c.ID, ErrValidation)
	}
	if len(c.AffectedServiceIDs) == 0 {
		return fmt.Errorf("target change %q requires exact affected service ids: %w", c.ID, ErrValidation)
	}
	if err := validArchitectureTargetIDs(c.AffectedServiceIDs); err != nil {
		return fmt.Errorf("target change %q affected services: %w", c.ID, err)
	}
	if c.Scope == ArchitectureTargetScopeService && len(c.AffectedOperationIDs) != 0 {
		return fmt.Errorf("service target change %q cannot name operation ids: %w", c.ID, ErrValidation)
	}
	if c.Scope == ArchitectureTargetScopeOperation && len(c.AffectedOperationIDs) == 0 {
		return fmt.Errorf("operation target change %q requires exact operation ids: %w", c.ID, ErrValidation)
	}
	if len(c.AffectedOperationIDs) > 0 {
		if err := validArchitectureTargetIDs(c.AffectedOperationIDs); err != nil {
			return fmt.Errorf("target change %q affected operations: %w", c.ID, err)
		}
	}
	if err := validProposedTargetIDs(c.Kind, c.Action, c.AffectedServiceIDs, c.AffectedOperationIDs); err != nil {
		return fmt.Errorf("target change %q proposed ids: %w", c.ID, err)
	}
	if err := c.Rationale.Valid(); err != nil {
		return fmt.Errorf("target change %q rationale: %w", c.ID, err)
	}
	if err := c.DesiredResult.Valid(); err != nil {
		return fmt.Errorf("target change %q desired result: %w", c.ID, err)
	}
	if len(c.Evidence) == 0 {
		return fmt.Errorf("target change %q requires evidence: %w", c.ID, ErrValidation)
	}
	if err := validArchitectureTargetEvidence(c.Evidence); err != nil {
		return fmt.Errorf("target change %q evidence: %w", c.ID, err)
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return fmt.Errorf("target change %q confidence must be between zero and one: %w", c.ID, ErrValidation)
	}
	if err := validArchitectureTargetUnresolved(c.UnresolvedAreas); err != nil {
		return fmt.Errorf("target change %q unresolved areas: %w", c.ID, err)
	}
	return nil
}

func (r ArchitectureTargetRationale) Valid() error {
	if err := validateTargetHumanText(r.Summary, 1000, false); err != nil {
		return err
	}
	return validateTargetHumanText(r.Details, 8000, true)
}

func (r ArchitectureTargetDesiredResult) Valid() error {
	if err := validateTargetHumanText(r.Summary, 1000, false); err != nil {
		return err
	}
	if len(r.SuccessCriteria) == 0 || len(r.SuccessCriteria) > 32 {
		return fmt.Errorf("at least one and at most 32 success criteria are required: %w", ErrValidation)
	}
	for _, criterion := range r.SuccessCriteria {
		if err := validateTargetHumanText(criterion, 1000, false); err != nil {
			return fmt.Errorf("success criterion: %w", err)
		}
	}
	return nil
}

func (d ArchitectureTargetDiff) Valid(currentFingerprint string, changes map[string]ArchitectureTargetChange) error {
	if d.BaseFingerprint != currentFingerprint {
		return fmt.Errorf("target diff must bind the proposal current fingerprint: %w", ErrConflict)
	}
	if len(d.Entries) == 0 {
		return fmt.Errorf("target diff requires entries: %w", ErrValidation)
	}
	seen := make(map[string]struct{}, len(d.Entries))
	for _, entry := range d.Entries {
		change, exists := changes[entry.ChangeID]
		if !exists {
			return fmt.Errorf("target diff names unknown change %q: %w", entry.ChangeID, ErrValidation)
		}
		if _, duplicate := seen[entry.ChangeID]; duplicate {
			return fmt.Errorf("target diff repeats change %q: %w", entry.ChangeID, ErrValidation)
		}
		seen[entry.ChangeID] = struct{}{}
		if entry.Scope != ArchitectureTargetScopeService && entry.Scope != ArchitectureTargetScopeOperation {
			return fmt.Errorf("target diff %q has invalid scope: %w", entry.ChangeID, ErrValidation)
		}
		if entry.Action != ArchitectureTargetChangeAdd && entry.Action != ArchitectureTargetChangeChange && entry.Action != ArchitectureTargetChangeRemove {
			return fmt.Errorf("target diff %q has invalid action: %w", entry.ChangeID, ErrValidation)
		}
		if !validArchitectureTargetChangeKind(entry.Kind) || !validTargetKindAction(entry.Kind, entry.Action) {
			return fmt.Errorf("target diff %q has invalid kind/action: %w", entry.ChangeID, ErrValidation)
		}
		if !validTargetScopeForKind(entry.Kind, entry.Scope) {
			return fmt.Errorf("target diff %q has invalid kind/scope: %w", entry.ChangeID, ErrValidation)
		}
		if err := validArchitectureTargetIDs(entry.AffectedServiceIDs); err != nil {
			return fmt.Errorf("target diff %q affected services: %w", entry.ChangeID, err)
		}
		if len(entry.AffectedOperationIDs) > 0 {
			if err := validArchitectureTargetIDs(entry.AffectedOperationIDs); err != nil {
				return fmt.Errorf("target diff %q affected operations: %w", entry.ChangeID, err)
			}
		}
		if err := validProposedTargetIDs(entry.Kind, entry.Action, entry.AffectedServiceIDs, entry.AffectedOperationIDs); err != nil {
			return fmt.Errorf("target diff %q proposed ids: %w", entry.ChangeID, err)
		}
		if err := validateTargetHumanText(entry.Summary, 1000, false); err != nil {
			return fmt.Errorf("target diff %q summary: %w", entry.ChangeID, err)
		}
		if err := entry.Rationale.Valid(); err != nil {
			return fmt.Errorf("target diff %q rationale: %w", entry.ChangeID, err)
		}
		if err := entry.DesiredResult.Valid(); err != nil {
			return fmt.Errorf("target diff %q desired result: %w", entry.ChangeID, err)
		}
		if len(entry.Evidence) == 0 {
			return fmt.Errorf("target diff %q requires evidence: %w", entry.ChangeID, ErrValidation)
		}
		if err := validArchitectureTargetEvidence(entry.Evidence); err != nil {
			return fmt.Errorf("target diff %q evidence: %w", entry.ChangeID, err)
		}
		if entry.Confidence < 0 || entry.Confidence > 1 {
			return fmt.Errorf("target diff %q confidence must be between zero and one: %w", entry.ChangeID, ErrValidation)
		}
		if err := validArchitectureTargetUnresolved(entry.UnresolvedAreas); err != nil {
			return fmt.Errorf("target diff %q unresolved areas: %w", entry.ChangeID, err)
		}
		if !targetDiffMatchesChange(entry, change) {
			return fmt.Errorf("target diff %q does not preserve its structured change: %w", entry.ChangeID, ErrConflict)
		}
	}
	if len(seen) != len(changes) {
		return fmt.Errorf("target diff must contain exactly one entry for each change: %w", ErrValidation)
	}
	return nil
}

func targetDiffMatchesChange(entry ArchitectureTargetDiffEntry, change ArchitectureTargetChange) bool {
	entryCanonical := ArchitectureTargetDiffEntry{
		ChangeID:             change.ID,
		Scope:                change.Scope,
		Action:               change.Action,
		Kind:                 change.Kind,
		AffectedServiceIDs:   append([]string(nil), change.AffectedServiceIDs...),
		AffectedOperationIDs: append([]string(nil), change.AffectedOperationIDs...),
		Rationale:            change.Rationale,
		DesiredResult:        ArchitectureTargetDesiredResult{Summary: change.DesiredResult.Summary, SuccessCriteria: append([]string(nil), change.DesiredResult.SuccessCriteria...)},
		Evidence:             append([]ArchitectureEvidence(nil), change.Evidence...),
		Confidence:           change.Confidence,
		UnresolvedAreas:      append([]ArchitectureTargetUnresolved(nil), change.UnresolvedAreas...),
	}
	actual := cloneArchitectureTargetDiffEntry(entry)
	sort.Strings(entryCanonical.AffectedServiceIDs)
	sort.Strings(entryCanonical.AffectedOperationIDs)
	sort.Strings(entryCanonical.DesiredResult.SuccessCriteria)
	sort.Slice(entryCanonical.Evidence, func(left, right int) bool {
		return targetEvidenceKey(entryCanonical.Evidence[left]) < targetEvidenceKey(entryCanonical.Evidence[right])
	})
	sort.Slice(entryCanonical.UnresolvedAreas, func(left, right int) bool {
		return targetUnresolvedKey(entryCanonical.UnresolvedAreas[left]) < targetUnresolvedKey(entryCanonical.UnresolvedAreas[right])
	})
	sort.Strings(actual.AffectedServiceIDs)
	sort.Strings(actual.AffectedOperationIDs)
	sort.Strings(actual.DesiredResult.SuccessCriteria)
	sort.Slice(actual.Evidence, func(left, right int) bool {
		return targetEvidenceKey(actual.Evidence[left]) < targetEvidenceKey(actual.Evidence[right])
	})
	sort.Slice(actual.UnresolvedAreas, func(left, right int) bool {
		return targetUnresolvedKey(actual.UnresolvedAreas[left]) < targetUnresolvedKey(actual.UnresolvedAreas[right])
	})
	actual.Summary = ""
	entryCanonical.Summary = ""
	return reflect.DeepEqual(actual, entryCanonical)
}

func (i ArchitectureTargetImpact) Valid(currentFingerprint string, changeIDs map[string]struct{}) error {
	if i.BaseFingerprint != currentFingerprint {
		return fmt.Errorf("target impact must bind the proposal current fingerprint: %w", ErrConflict)
	}
	if len(i.Entries) == 0 {
		return fmt.Errorf("target impact requires entries: %w", ErrValidation)
	}
	for _, entry := range i.Entries {
		if _, exists := changeIDs[entry.ChangeID]; !exists {
			return fmt.Errorf("target impact names unknown change %q: %w", entry.ChangeID, ErrValidation)
		}
		if !validArchitectureTargetID(entry.ServiceID) {
			return fmt.Errorf("target impact has invalid service id: %w", ErrValidation)
		}
		if entry.OperationID != "" && !validArchitectureTargetID(entry.OperationID) {
			return fmt.Errorf("target impact has invalid operation id: %w", ErrValidation)
		}
		switch entry.Kind {
		case ArchitectureTargetImpactDirect, ArchitectureTargetImpactUpstream, ArchitectureTargetImpactDownstream, ArchitectureTargetImpactUnresolved:
		default:
			return fmt.Errorf("target impact has invalid kind: %w", ErrValidation)
		}
		if err := validateTargetHumanText(entry.Explanation, 2000, false); err != nil {
			return fmt.Errorf("target impact explanation: %w", err)
		}
		if err := validArchitectureTargetEvidence(entry.Evidence); err != nil {
			return fmt.Errorf("target impact evidence: %w", err)
		}
	}
	return nil
}

// ComputedFingerprint returns a stable checksum for the full reviewed TARGET
// content. Callers persist it as Fingerprint and use it for approval binding.
func (p ArchitectureTargetProposal) ComputedFingerprint() (string, error) {
	canonical := struct {
		CurrentFingerprint string                     `json:"current_fingerprint"`
		Changes            []ArchitectureTargetChange `json:"changes"`
		Diff               ArchitectureTargetDiff     `json:"diff"`
		Impact             ArchitectureTargetImpact   `json:"impact"`
	}{
		CurrentFingerprint: p.CurrentFingerprint,
		Changes:            canonicalTargetChanges(p.Changes),
		Diff:               canonicalTargetDiff(p.Diff),
		Impact:             canonicalTargetImpact(p.Impact),
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal target fingerprint input: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// CanTransitionArchitectureTarget is deliberately small and does not infer an
// approval decision. S6 owns authorization; this domain rule keeps terminal
// TARGET versions immutable.
func CanTransitionArchitectureTarget(from, to ArchitectureTargetStatus) bool {
	switch from {
	case ArchitectureTargetStatusDraft:
		return to == ArchitectureTargetStatusSubmitted
	case ArchitectureTargetStatusSubmitted:
		return to == ArchitectureTargetStatusApproved || to == ArchitectureTargetStatusRejected || to == ArchitectureTargetStatusChangesRequested || to == ArchitectureTargetStatusSuperseded
	case ArchitectureTargetStatusChangesRequested:
		return to == ArchitectureTargetStatusSuperseded
	default:
		return false
	}
}

func validArchitectureTargetStatus(value ArchitectureTargetStatus) bool {
	switch value {
	case ArchitectureTargetStatusDraft, ArchitectureTargetStatusSubmitted, ArchitectureTargetStatusApproved, ArchitectureTargetStatusRejected, ArchitectureTargetStatusChangesRequested, ArchitectureTargetStatusSuperseded:
		return true
	default:
		return false
	}
}

func validArchitectureTargetChangeKind(value ArchitectureTargetChangeKind) bool {
	switch value {
	case ArchitectureTargetChangeKindService, ArchitectureTargetChangeKindDependency, ArchitectureTargetChangeKindOperation, ArchitectureTargetChangeKindContract, ArchitectureTargetChangeKindEvent, ArchitectureTargetChangeKindResponsibility, ArchitectureTargetChangeKindBusinessProcess:
		return true
	default:
		return false
	}
}

func validTargetKindAction(kind ArchitectureTargetChangeKind, action ArchitectureTargetChangeAction) bool {
	switch kind {
	case ArchitectureTargetChangeKindService, ArchitectureTargetChangeKindDependency, ArchitectureTargetChangeKindOperation:
		return action == ArchitectureTargetChangeAdd || action == ArchitectureTargetChangeChange || action == ArchitectureTargetChangeRemove
	case ArchitectureTargetChangeKindContract, ArchitectureTargetChangeKindEvent, ArchitectureTargetChangeKindResponsibility, ArchitectureTargetChangeKindBusinessProcess:
		return action == ArchitectureTargetChangeAdd || action == ArchitectureTargetChangeChange
	default:
		return false
	}
}

func validTargetScopeForKind(kind ArchitectureTargetChangeKind, scope ArchitectureTargetScope) bool {
	switch kind {
	case ArchitectureTargetChangeKindService, ArchitectureTargetChangeKindDependency, ArchitectureTargetChangeKindResponsibility:
		return scope == ArchitectureTargetScopeService
	case ArchitectureTargetChangeKindOperation, ArchitectureTargetChangeKindBusinessProcess:
		return scope == ArchitectureTargetScopeOperation
	case ArchitectureTargetChangeKindContract, ArchitectureTargetChangeKindEvent:
		return scope == ArchitectureTargetScopeService || scope == ArchitectureTargetScopeOperation
	default:
		return false
	}
}

var architectureTargetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
var architectureFingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var targetSecretPattern = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|authorization)\s*[:=]\s*[^\s]+`)
var targetYAMLPattern = regexp.MustCompile(`(?m)^\s*(---|\.\.\.|schema|apiVersion|kind|metadata|spec)\s*(:|$)`)

func validArchitectureTargetID(value string) bool {
	return architectureTargetIDPattern.MatchString(value)
}

func validArchitectureTargetIDs(values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("at least one exact id is required: %w", ErrValidation)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validArchitectureTargetID(value) {
			return fmt.Errorf("invalid architecture id %q: %w", value, ErrValidation)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate architecture id %q: %w", value, ErrValidation)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validProposedTargetIDs(kind ArchitectureTargetChangeKind, action ArchitectureTargetChangeAction, services, operations []string) error {
	for _, id := range append(append([]string(nil), services...), operations...) {
		if strings.HasPrefix(id, "proposed:") && action != ArchitectureTargetChangeAdd {
			return fmt.Errorf("proposed identity %q is allowed only for add: %w", id, ErrValidation)
		}
	}
	if action != ArchitectureTargetChangeAdd {
		return nil
	}
	if kind == ArchitectureTargetChangeKindService {
		for _, id := range services {
			if !strings.HasPrefix(id, "proposed:") {
				return fmt.Errorf("a service addition must name a proposed: service identity: %w", ErrValidation)
			}
		}
	}
	if kind == ArchitectureTargetChangeKindOperation {
		for _, id := range operations {
			if !strings.HasPrefix(id, "proposed:") {
				return fmt.Errorf("an operation addition must name a proposed: operation identity: %w", ErrValidation)
			}
		}
	}
	return nil
}

func validArchitectureFingerprint(value string) bool {
	return architectureFingerprintPattern.MatchString(value)
}

func validateTargetHumanText(value string, maximum int, optional bool) error {
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return nil
	}
	if value == "" || len(value) > maximum {
		return fmt.Errorf("bounded human text is required: %w", ErrValidation)
	}
	if strings.Contains(value, "```") || targetYAMLPattern.MatchString(value) {
		return fmt.Errorf("raw YAML or code blocks are not permitted: %w", ErrValidation)
	}
	if targetSecretPattern.MatchString(value) {
		return fmt.Errorf("secret-bearing text is not permitted: %w", ErrValidation)
	}
	return nil
}

func validArchitectureTargetEvidence(values []ArchitectureEvidence) error {
	seen := make(map[string]struct{}, len(values))
	for _, evidence := range values {
		evidence.SourcePath = strings.TrimSpace(evidence.SourcePath)
		if evidence.SourcePath == "" || strings.HasPrefix(evidence.SourcePath, "/") || strings.Contains(evidence.SourcePath, "..") || len(evidence.SourcePath) > 2048 {
			return fmt.Errorf("evidence source path is invalid: %w", ErrValidation)
		}
		for _, segment := range strings.Split(evidence.SourcePath, "/") {
			if segment == ".env" || strings.HasPrefix(segment, ".env.") {
				return fmt.Errorf("evidence must not reference environment secret files: %w", ErrValidation)
			}
		}
		if evidence.StartLine < 0 || evidence.EndLine < 0 || (evidence.EndLine != 0 && evidence.EndLine < evidence.StartLine) {
			return fmt.Errorf("evidence line span is invalid: %w", ErrValidation)
		}
		key := strings.Join([]string{evidence.SourcePath, evidence.Symbol, fmt.Sprint(evidence.StartLine), fmt.Sprint(evidence.EndLine), evidence.Checksum}, "\x00")
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate evidence: %w", ErrValidation)
		}
		seen[key] = struct{}{}
	}
	if len(values) == 0 {
		return fmt.Errorf("at least one evidence item is required: %w", ErrValidation)
	}
	return nil
}

func validArchitectureTargetUnresolved(values []ArchitectureTargetUnresolved) error {
	if len(values) > 32 {
		return fmt.Errorf("at most 32 unresolved areas are allowed: %w", ErrValidation)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateTargetHumanText(value.Area, 255, false); err != nil {
			return fmt.Errorf("unresolved area: %w", err)
		}
		if err := validateTargetHumanText(value.Reason, 2000, false); err != nil {
			return fmt.Errorf("unresolved reason: %w", err)
		}
		key := value.Area + "\x00" + value.Reason
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate unresolved area: %w", ErrValidation)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func canonicalTargetChanges(values []ArchitectureTargetChange) []ArchitectureTargetChange {
	result := make([]ArchitectureTargetChange, len(values))
	for index := range values {
		result[index] = cloneArchitectureTargetChange(values[index])
	}
	for index := range result {
		sort.Strings(result[index].AffectedServiceIDs)
		sort.Strings(result[index].AffectedOperationIDs)
		sort.Strings(result[index].DesiredResult.SuccessCriteria)
		sort.Slice(result[index].Evidence, func(left, right int) bool {
			return targetEvidenceKey(result[index].Evidence[left]) < targetEvidenceKey(result[index].Evidence[right])
		})
		sort.Slice(result[index].UnresolvedAreas, func(left, right int) bool {
			return targetUnresolvedKey(result[index].UnresolvedAreas[left]) < targetUnresolvedKey(result[index].UnresolvedAreas[right])
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func canonicalTargetDiff(value ArchitectureTargetDiff) ArchitectureTargetDiff {
	entries := make([]ArchitectureTargetDiffEntry, len(value.Entries))
	for index := range value.Entries {
		entries[index] = cloneArchitectureTargetDiffEntry(value.Entries[index])
	}
	value.Entries = entries
	for index := range value.Entries {
		sort.Strings(value.Entries[index].AffectedServiceIDs)
		sort.Strings(value.Entries[index].AffectedOperationIDs)
		sort.Strings(value.Entries[index].DesiredResult.SuccessCriteria)
		sort.Slice(value.Entries[index].Evidence, func(left, right int) bool {
			return targetEvidenceKey(value.Entries[index].Evidence[left]) < targetEvidenceKey(value.Entries[index].Evidence[right])
		})
		sort.Slice(value.Entries[index].UnresolvedAreas, func(left, right int) bool {
			return targetUnresolvedKey(value.Entries[index].UnresolvedAreas[left]) < targetUnresolvedKey(value.Entries[index].UnresolvedAreas[right])
		})
	}
	sort.Slice(value.Entries, func(left, right int) bool { return value.Entries[left].ChangeID < value.Entries[right].ChangeID })
	return value
}

func canonicalTargetImpact(value ArchitectureTargetImpact) ArchitectureTargetImpact {
	entries := make([]ArchitectureTargetImpactEntry, len(value.Entries))
	for index := range value.Entries {
		entries[index] = value.Entries[index]
		entries[index].Evidence = append([]ArchitectureEvidence(nil), value.Entries[index].Evidence...)
	}
	value.Entries = entries
	for index := range value.Entries {
		sort.Slice(value.Entries[index].Evidence, func(left, right int) bool {
			return targetEvidenceKey(value.Entries[index].Evidence[left]) < targetEvidenceKey(value.Entries[index].Evidence[right])
		})
	}
	sort.Slice(value.Entries, func(left, right int) bool {
		leftEntry, rightEntry := value.Entries[left], value.Entries[right]
		return strings.Join([]string{leftEntry.ChangeID, leftEntry.ServiceID, leftEntry.OperationID, string(leftEntry.Kind)}, "\x00") < strings.Join([]string{rightEntry.ChangeID, rightEntry.ServiceID, rightEntry.OperationID, string(rightEntry.Kind)}, "\x00")
	})
	return value
}

func targetEvidenceKey(value ArchitectureEvidence) string {
	return strings.Join([]string{value.SourcePath, value.Symbol, fmt.Sprint(value.StartLine), fmt.Sprint(value.EndLine), value.Checksum}, "\x00")
}

func targetUnresolvedKey(value ArchitectureTargetUnresolved) string {
	return value.Area + "\x00" + value.Reason
}

func cloneArchitectureTargetChange(value ArchitectureTargetChange) ArchitectureTargetChange {
	value.AffectedServiceIDs = append([]string(nil), value.AffectedServiceIDs...)
	value.AffectedOperationIDs = append([]string(nil), value.AffectedOperationIDs...)
	value.DesiredResult.SuccessCriteria = append([]string(nil), value.DesiredResult.SuccessCriteria...)
	value.Evidence = append([]ArchitectureEvidence(nil), value.Evidence...)
	value.UnresolvedAreas = append([]ArchitectureTargetUnresolved(nil), value.UnresolvedAreas...)
	return value
}

func cloneArchitectureTargetDiffEntry(value ArchitectureTargetDiffEntry) ArchitectureTargetDiffEntry {
	value.AffectedServiceIDs = append([]string(nil), value.AffectedServiceIDs...)
	value.AffectedOperationIDs = append([]string(nil), value.AffectedOperationIDs...)
	value.DesiredResult.SuccessCriteria = append([]string(nil), value.DesiredResult.SuccessCriteria...)
	value.Evidence = append([]ArchitectureEvidence(nil), value.Evidence...)
	value.UnresolvedAreas = append([]ArchitectureTargetUnresolved(nil), value.UnresolvedAreas...)
	return value
}
