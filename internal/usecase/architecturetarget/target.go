// Package architecturetarget builds and evolves explicit TARGET proposals.
//
// The package is deliberately pure application policy: it can only derive a
// proposal from a caller-supplied, verified CURRENT catalog.  It has no file,
// scanner, manifest, or transport dependency, so a TARGET proposal can never
// silently rewrite source architecture evidence.
package architecturetarget

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const proposedIDPrefix = "proposed:"

// Store is the small persistence boundary needed by TARGET use cases.  Its
// implementation belongs to an adapter in a later phase; keeping it here
// avoids making proposal persistence a prerequisite for deterministic policy
// tests.
type Store interface {
	Create(context.Context, domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error)
	Get(context.Context, string) (domain.ArchitectureTargetProposal, error)
	UpdateDraft(context.Context, domain.ArchitectureTargetProposal, int) (domain.ArchitectureTargetProposal, error)
	Transition(context.Context, string, domain.ArchitectureTargetStatus, int, string, string) (domain.ArchitectureTargetProposal, error)
}

// CreateInput contains a complete immutable CURRENT projection.  The caller
// must pass the fingerprint it obtained from the successful S4 verification;
// accepting it separately makes stale UI/API requests detectable rather than
// allowing a proposal to bind whatever catalog happens to be current later.
type CreateInput struct {
	Current            domain.ArchitectureCatalog
	CurrentFingerprint string
	IdempotencyKey     string
	Changes            []domain.ArchitectureTargetChange
}

// Create makes a draft proposal.  It does not alter CURRENT, source files, or
// manifests; its diff and impact are reproducible projections of the supplied
// catalog and changes.
type Create struct{ Store Store }

func (uc Create) Handle(ctx context.Context, input CreateInput) (domain.ArchitectureTargetProposal, error) {
	if uc.Store == nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal store is not configured: %w", domain.ErrValidation)
	}
	if err := validateVerifiedCurrent(input.Current, input.CurrentFingerprint); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	changes := cloneChanges(input.Changes)
	index, err := buildCatalogIndex(input.Current)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if err := validateChanges(index, changes); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	proposal, err := newProposal(input.Current, strings.TrimSpace(input.IdempotencyKey), changes)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	return uc.Store.Create(ctx, proposal)
}

// Get returns one proposal exactly as stored.  It intentionally does not
// consult CURRENT: a proposal remains reviewable against the immutable
// fingerprint to which it was bound.
type Get struct{ Store Store }

func (uc Get) Handle(ctx context.Context, proposalID string) (domain.ArchitectureTargetProposal, error) {
	if uc.Store == nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal store is not configured: %w", domain.ErrValidation)
	}
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal ID is required: %w", domain.ErrValidation)
	}
	return uc.Store.Get(ctx, proposalID)
}

// ReviseInput preserves the proposal's original CURRENT binding.  A caller
// must provide the same verified catalog/fingerprint again, so revisions can
// neither drift to a newer CURRENT nor introduce a guessed target.
type ReviseInput struct {
	ProposalID         string
	ExpectedRevision   int
	Current            domain.ArchitectureCatalog
	CurrentFingerprint string
	Changes            []domain.ArchitectureTargetChange
}

// Revise replaces draft content using optimistic concurrency.  Submitted and
// decided proposals are immutable; the owner must create a new proposal if
// the requested architecture changes after submission.
type Revise struct{ Store Store }

func (uc Revise) Handle(ctx context.Context, input ReviseInput) (domain.ArchitectureTargetProposal, error) {
	if uc.Store == nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal store is not configured: %w", domain.ErrValidation)
	}
	proposalID := strings.TrimSpace(input.ProposalID)
	if proposalID == "" || input.ExpectedRevision < 1 {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal ID and positive expected revision are required: %w", domain.ErrValidation)
	}
	previous, err := uc.Store.Get(ctx, proposalID)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if previous.Status != domain.ArchitectureTargetStatusDraft {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("only a draft target proposal can be revised: %w", domain.ErrInvalidStatus)
	}
	if previous.Revision != input.ExpectedRevision {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal revision is stale: %w", domain.ErrConflict)
	}
	if err := validateVerifiedCurrent(input.Current, input.CurrentFingerprint); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if previous.CurrentFingerprint != input.CurrentFingerprint {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("draft proposal must retain its original CURRENT fingerprint: %w", domain.ErrConflict)
	}
	index, err := buildCatalogIndex(input.Current)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	changes := cloneChanges(input.Changes)
	if err := validateChanges(index, changes); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	next, err := newProposal(input.Current, previous.IdempotencyKey, changes)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	next.ID = previous.ID
	next.Revision = previous.Revision + 1
	next.CreatedAt = previous.CreatedAt
	next.UpdatedAt = previous.UpdatedAt
	next, err = finalize(next)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	return uc.Store.UpdateDraft(ctx, next, input.ExpectedRevision)
}

// SubmitInput is deliberately separate from approval: S5 can only freeze a
// reviewed TARGET request.  S6 later owns authorization and approval.
type SubmitInput struct {
	ProposalID       string
	ExpectedRevision int
	Actor            string
	Comment          string
}

// Submit moves a valid draft to submitted with optimistic concurrency.  It
// revalidates persisted structured content so an adapter cannot submit a
// malformed or unbound record.
type Submit struct{ Store Store }

func (uc Submit) Handle(ctx context.Context, input SubmitInput) (domain.ArchitectureTargetProposal, error) {
	if uc.Store == nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal store is not configured: %w", domain.ErrValidation)
	}
	proposalID, actor := strings.TrimSpace(input.ProposalID), strings.TrimSpace(input.Actor)
	if proposalID == "" || actor == "" || input.ExpectedRevision < 1 {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal ID, actor, and positive expected revision are required: %w", domain.ErrValidation)
	}
	proposal, err := uc.Store.Get(ctx, proposalID)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if proposal.Status != domain.ArchitectureTargetStatusDraft {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("only a draft target proposal can be submitted: %w", domain.ErrInvalidStatus)
	}
	if proposal.Revision != input.ExpectedRevision {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal revision is stale: %w", domain.ErrConflict)
	}
	if err := proposal.Valid(); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	return uc.Store.Transition(ctx, proposalID, domain.ArchitectureTargetStatusSubmitted, input.ExpectedRevision, actor, strings.TrimSpace(input.Comment))
}

type catalogIndex struct {
	services   map[string]struct{}
	operations map[string]map[string]struct{}
}

func validateVerifiedCurrent(catalog domain.ArchitectureCatalog, fingerprint string) error {
	fingerprint = strings.TrimSpace(fingerprint)
	if catalog.Mode != domain.ArchitectureCatalogModeCurrent || catalog.Fingerprint == "" || catalog.Fingerprint != fingerprint {
		return fmt.Errorf("a matching verified CURRENT catalog fingerprint is required: %w", domain.ErrConflict)
	}
	completeness := catalog.Platform.Completeness
	if completeness.UncoveredServiceCount != 0 || completeness.OperationsMissingManifests != 0 || completeness.OperationsBlocked != 0 {
		return fmt.Errorf("CURRENT catalog is incomplete and cannot seed TARGET: %w", domain.ErrConflict)
	}
	if len(catalog.Platform.Services) == 0 {
		return fmt.Errorf("CURRENT catalog has no services: %w", domain.ErrValidation)
	}
	for _, service := range catalog.Platform.Services {
		// SourceCurrent already proves the persisted topology, commit, and
		// checksum match. IsDirty is intentionally presentation evidence: local
		// generated Architecture artifacts and stable owner changes must remain
		// visible without preventing a fingerprint-bound TARGET draft.
		if service.Source.ProjectID == "" || !service.Source.SourceCurrent || !service.Covered || service.Manifest == nil {
			return fmt.Errorf("CURRENT service %q is not verified and covered: %w", service.Source.ProjectID, domain.ErrConflict)
		}
	}
	return nil
}

func buildCatalogIndex(catalog domain.ArchitectureCatalog) (catalogIndex, error) {
	result := catalogIndex{services: make(map[string]struct{}, len(catalog.Platform.Services)), operations: make(map[string]map[string]struct{}, len(catalog.Platform.Services))}
	for _, service := range catalog.Platform.Services {
		serviceID := service.Source.ProjectID
		if _, exists := result.services[serviceID]; exists {
			return catalogIndex{}, fmt.Errorf("CURRENT catalog repeats service %q: %w", serviceID, domain.ErrConflict)
		}
		result.services[serviceID] = struct{}{}
		operationIDs := make(map[string]struct{})
		for _, group := range service.Groups {
			for _, operation := range group.Operations {
				if err := addOperation(operationIDs, operation.Manifest.ID, serviceID); err != nil {
					return catalogIndex{}, err
				}
			}
		}
		for _, operation := range service.Ungrouped {
			if err := addOperation(operationIDs, operation.Manifest.ID, serviceID); err != nil {
				return catalogIndex{}, err
			}
		}
		result.operations[serviceID] = operationIDs
	}
	return result, nil
}

func addOperation(operations map[string]struct{}, operationID, serviceID string) error {
	if strings.TrimSpace(operationID) == "" {
		return fmt.Errorf("CURRENT service %q has an operation without an id: %w", serviceID, domain.ErrConflict)
	}
	if _, exists := operations[operationID]; exists {
		return fmt.Errorf("CURRENT service %q repeats operation %q: %w", serviceID, operationID, domain.ErrConflict)
	}
	operations[operationID] = struct{}{}
	return nil
}

func validateChanges(index catalogIndex, changes []domain.ArchitectureTargetChange) error {
	if len(changes) == 0 {
		return fmt.Errorf("at least one target change is required: %w", domain.ErrValidation)
	}
	for _, change := range changes {
		if err := change.Valid(); err != nil {
			return err
		}
		if err := validateChangeTargets(index, change); err != nil {
			return err
		}
	}
	return nil
}

func validateChangeTargets(index catalogIndex, change domain.ArchitectureTargetChange) error {
	for _, serviceID := range change.AffectedServiceIDs {
		_, known := index.services[serviceID]
		if change.Kind == domain.ArchitectureTargetChangeKindService && change.Action == domain.ArchitectureTargetChangeAdd {
			if !strings.HasPrefix(serviceID, proposedIDPrefix) || known {
				return fmt.Errorf("added service %q must use a new proposed: identifier: %w", serviceID, domain.ErrValidation)
			}
			continue
		}
		if !known {
			return fmt.Errorf("target change %q names service %q absent from CURRENT: %w", change.ID, serviceID, domain.ErrValidation)
		}
	}
	if change.Scope != domain.ArchitectureTargetScopeOperation {
		return nil
	}
	for _, operationID := range change.AffectedOperationIDs {
		if change.Kind == domain.ArchitectureTargetChangeKindOperation && change.Action == domain.ArchitectureTargetChangeAdd {
			if !strings.HasPrefix(operationID, proposedIDPrefix) {
				return fmt.Errorf("added operation %q must use a new proposed: identifier: %w", operationID, domain.ErrValidation)
			}
			continue
		}
		found := false
		for _, serviceID := range change.AffectedServiceIDs {
			if _, exists := index.operations[serviceID][operationID]; exists {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("target change %q names operation %q absent from its CURRENT services: %w", change.ID, operationID, domain.ErrValidation)
		}
	}
	return nil
}

func newProposal(catalog domain.ArchitectureCatalog, idempotencyKey string, changes []domain.ArchitectureTargetChange) (domain.ArchitectureTargetProposal, error) {
	if idempotencyKey == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal idempotency key is required: %w", domain.ErrValidation)
	}
	proposal := domain.ArchitectureTargetProposal{CurrentFingerprint: catalog.Fingerprint, Status: domain.ArchitectureTargetStatusDraft, Revision: 1, IdempotencyKey: idempotencyKey, Changes: changes}
	proposal.Diff = buildDiff(proposal)
	proposal.Impact = buildImpact(catalog, proposal)
	return finalize(proposal)
}

func finalize(proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
	fingerprint, err := proposal.ComputedFingerprint()
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	proposal.Fingerprint = fingerprint
	if err := proposal.Valid(); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	return proposal, nil
}

func buildDiff(proposal domain.ArchitectureTargetProposal) domain.ArchitectureTargetDiff {
	entries := make([]domain.ArchitectureTargetDiffEntry, 0, len(proposal.Changes))
	for _, change := range proposal.Changes {
		entries = append(entries, domain.ArchitectureTargetDiffEntry{ChangeID: change.ID, Scope: change.Scope, Action: change.Action, Kind: change.Kind, AffectedServiceIDs: append([]string(nil), change.AffectedServiceIDs...), AffectedOperationIDs: append([]string(nil), change.AffectedOperationIDs...), Summary: change.Rationale.Summary, Rationale: change.Rationale, DesiredResult: change.DesiredResult, Evidence: append([]domain.ArchitectureEvidence(nil), change.Evidence...), Confidence: change.Confidence, UnresolvedAreas: append([]domain.ArchitectureTargetUnresolved(nil), change.UnresolvedAreas...)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ChangeID < entries[j].ChangeID })
	return domain.ArchitectureTargetDiff{BaseFingerprint: proposal.CurrentFingerprint, Entries: entries}
}

func buildImpact(catalog domain.ArchitectureCatalog, proposal domain.ArchitectureTargetProposal) domain.ArchitectureTargetImpact {
	entries := make([]domain.ArchitectureTargetImpactEntry, 0)
	for _, change := range proposal.Changes {
		entries = append(entries, impactForChange(catalog, change)...)
	}
	sort.Slice(entries, func(i, j int) bool { return impactEntryKey(entries[i]) < impactEntryKey(entries[j]) })
	return domain.ArchitectureTargetImpact{BaseFingerprint: proposal.CurrentFingerprint, Entries: entries}
}

func impactForChange(catalog domain.ArchitectureCatalog, change domain.ArchitectureTargetChange) []domain.ArchitectureTargetImpactEntry {
	entries := make([]domain.ArchitectureTargetImpactEntry, 0)
	seen := make(map[string]struct{})
	add := func(entry domain.ArchitectureTargetImpactEntry) {
		key := impactEntryKey(entry)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			entries = append(entries, entry)
		}
	}
	queue := make([]string, 0, len(change.AffectedServiceIDs))
	visited := make(map[string]struct{})
	for _, serviceID := range change.AffectedServiceIDs {
		if strings.HasPrefix(serviceID, proposedIDPrefix) {
			add(domain.ArchitectureTargetImpactEntry{ChangeID: change.ID, ServiceID: serviceID, Kind: domain.ArchitectureTargetImpactDirect, Explanation: "proposed service has no CURRENT graph relation", Evidence: append([]domain.ArchitectureEvidence(nil), change.Evidence...)})
			continue
		}
		if _, exists := visited[serviceID]; !exists {
			visited[serviceID] = struct{}{}
			queue = append(queue, serviceID)
		}
		add(domain.ArchitectureTargetImpactEntry{ChangeID: change.ID, ServiceID: serviceID, Kind: domain.ArchitectureTargetImpactDirect, Explanation: "service is named directly by the target change", Evidence: append([]domain.ArchitectureEvidence(nil), change.Evidence...)})
	}
	if change.Scope == domain.ArchitectureTargetScopeOperation {
		for _, operationID := range change.AffectedOperationIDs {
			for _, serviceID := range change.AffectedServiceIDs {
				if strings.HasPrefix(serviceID, proposedIDPrefix) {
					continue
				}
				add(domain.ArchitectureTargetImpactEntry{ChangeID: change.ID, ServiceID: serviceID, OperationID: operationID, Kind: domain.ArchitectureTargetImpactDirect, Explanation: "operation is named directly by the target change", Evidence: append([]domain.ArchitectureEvidence(nil), change.Evidence...)})
			}
		}
	}
	for len(queue) > 0 {
		serviceID := queue[0]
		queue = queue[1:]
		for _, relation := range catalog.Platform.Relations {
			if relation.SourceProjectID == serviceID && relation.TargetProjectID != "" {
				if _, known := visited[relation.TargetProjectID]; !known {
					visited[relation.TargetProjectID] = struct{}{}
					queue = append(queue, relation.TargetProjectID)
					add(relationImpact(change.ID, relation.TargetProjectID, domain.ArchitectureTargetImpactDownstream, relation))
				}
			}
			if relation.TargetProjectID == serviceID && relation.SourceProjectID != "" {
				if _, known := visited[relation.SourceProjectID]; !known {
					visited[relation.SourceProjectID] = struct{}{}
					queue = append(queue, relation.SourceProjectID)
					add(relationImpact(change.ID, relation.SourceProjectID, domain.ArchitectureTargetImpactUpstream, relation))
				}
			}
			if relation.SourceProjectID == serviceID && relation.TargetProjectID == "" && relation.ExternalTarget != "" {
				evidence := append([]domain.ArchitectureEvidence(nil), relation.Evidence...)
				if len(evidence) == 0 {
					evidence = append([]domain.ArchitectureEvidence(nil), change.Evidence...)
				}
				add(domain.ArchitectureTargetImpactEntry{ChangeID: change.ID, ServiceID: serviceID, Kind: domain.ArchitectureTargetImpactUnresolved, Explanation: "CURRENT relation has unresolved external target: " + relation.ExternalTarget, Evidence: evidence})
			}
		}
	}
	return entries
}

func relationImpact(changeID, serviceID string, kind domain.ArchitectureTargetImpactKind, relation domain.ArchitectureCatalogRelation) domain.ArchitectureTargetImpactEntry {
	evidence := append([]domain.ArchitectureEvidence(nil), relation.Evidence...)
	return domain.ArchitectureTargetImpactEntry{ChangeID: changeID, ServiceID: serviceID, Kind: kind, Explanation: "CURRENT graph relation " + relation.ID + " connects the affected service", Evidence: evidence}
}

func impactEntryKey(entry domain.ArchitectureTargetImpactEntry) string {
	return strings.Join([]string{entry.ChangeID, entry.ServiceID, entry.OperationID, string(entry.Kind), entry.Explanation}, "\x00")
}

func cloneChanges(values []domain.ArchitectureTargetChange) []domain.ArchitectureTargetChange {
	return append([]domain.ArchitectureTargetChange(nil), values...)
}
