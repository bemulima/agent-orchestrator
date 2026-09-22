package architecturetarget

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// VerificationStatus is the outcome of comparing a newly built CURRENT
// catalog with one approved, immutable TARGET proposal.
//
// The values are intentionally uppercase because they are an operator-facing
// lifecycle result rather than the editable TARGET proposal status.
type VerificationStatus string

const (
	VerificationStatusMatched              VerificationStatus = "MATCHED"
	VerificationStatusPartiallyImplemented VerificationStatus = "PARTIALLY_IMPLEMENTED"
	VerificationStatusDrift                VerificationStatus = "DRIFT"
	VerificationStatusNotImplemented       VerificationStatus = "NOT_IMPLEMENTED"
	VerificationStatusPending              VerificationStatus = "VERIFICATION_PENDING"
)

// VerificationInput is deliberately composed of immutable inputs. Proposal
// is the persisted, approved record selected by the caller; Current is a new
// manifest-backed CURRENT catalog. Issue or project-plan state is not an input
// because it is not evidence that an architecture change was implemented.
type VerificationInput struct {
	Proposal            domain.ArchitectureTargetProposal
	ExpectedFingerprint string
	Current             domain.ArchitectureCatalog
}

// VerificationReport is a deterministic, evidence-preserving S8 result. A
// gap means a structural expectation was not observed. An unknown means that
// the TARGET expresses a semantic intention which cannot be proved from the
// typed CURRENT manifest material.
type VerificationReport struct {
	Status                VerificationStatus            `json:"status"`
	ProposalID            string                        `json:"proposal_id"`
	ProposalFingerprint   string                        `json:"proposal_fingerprint"`
	TargetBaseFingerprint string                        `json:"target_base_fingerprint"`
	CurrentFingerprint    string                        `json:"current_fingerprint"`
	Changes               []ChangeVerificationResult    `json:"changes"`
	Gaps                  []VerificationGap             `json:"gaps"`
	Unknowns              []VerificationUnknown         `json:"unknowns"`
	Evidence              []domain.ArchitectureEvidence `json:"evidence"`
	Confidence            float64                       `json:"confidence"`
}

// ChangeVerificationResult keeps the evidence and uncertainty localized to
// one approved change instead of allowing a platform-level result to hide a
// partially implemented target.
type ChangeVerificationResult struct {
	ChangeID   string                        `json:"change_id"`
	Status     VerificationStatus            `json:"status"`
	Gaps       []VerificationGap             `json:"gaps,omitempty"`
	Unknowns   []VerificationUnknown         `json:"unknowns,omitempty"`
	Evidence   []domain.ArchitectureEvidence `json:"evidence,omitempty"`
	Confidence float64                       `json:"confidence"`
}

type VerificationGap struct {
	ChangeID    string `json:"change_id"`
	Expectation string `json:"expectation"`
	Reason      string `json:"reason"`
}

type VerificationUnknown struct {
	ChangeID string                        `json:"change_id,omitempty"`
	Area     string                        `json:"area"`
	Reason   string                        `json:"reason"`
	Evidence []domain.ArchitectureEvidence `json:"evidence,omitempty"`
}

// VerifyApprovedTarget performs S8's CURRENT-versus-TARGET comparison. It is
// pure application policy: it neither scans files nor changes CURRENT,
// proposals, issues, or plans.
type VerifyApprovedTarget struct{}

func (VerifyApprovedTarget) Handle(input VerificationInput) (VerificationReport, error) {
	proposal := input.Proposal
	if err := validApprovedVerificationProposal(proposal, input.ExpectedFingerprint); err != nil {
		return VerificationReport{}, err
	}

	report := VerificationReport{
		ProposalID:            proposal.ID,
		ProposalFingerprint:   proposal.Fingerprint,
		TargetBaseFingerprint: proposal.CurrentFingerprint,
		CurrentFingerprint:    input.Current.Fingerprint,
	}
	if err := validateVerifiedCurrent(input.Current, input.Current.Fingerprint); err != nil {
		report.Status = VerificationStatusPending
		report.Unknowns = []VerificationUnknown{{Area: "CURRENT catalog", Reason: "CURRENT is incomplete or unverified: " + err.Error()}}
		return finalizeVerificationReport(report), nil
	}
	if input.Current.Fingerprint == proposal.CurrentFingerprint {
		report.Status = VerificationStatusNotImplemented
		for _, change := range sortedTargetChanges(proposal.Changes) {
			report.Changes = append(report.Changes, ChangeVerificationResult{
				ChangeID: change.ID,
				Status:   VerificationStatusNotImplemented,
				Gaps: []VerificationGap{{
					ChangeID:    change.ID,
					Expectation: "CURRENT differs from the approved TARGET base",
					Reason:      "the newly verified CURRENT fingerprint equals the TARGET base fingerprint",
				}},
			})
		}
		return finalizeVerificationReport(report), nil
	}

	index := buildVerificationCurrentIndex(input.Current)
	matched, partiallyImplemented := 0, 0
	for _, change := range sortedTargetChanges(proposal.Changes) {
		result := index.verify(change)
		report.Changes = append(report.Changes, result)
		report.Gaps = append(report.Gaps, result.Gaps...)
		report.Unknowns = append(report.Unknowns, result.Unknowns...)
		report.Evidence = append(report.Evidence, result.Evidence...)
		if result.Status == VerificationStatusMatched {
			matched++
		}
		if result.Status == VerificationStatusPartiallyImplemented {
			partiallyImplemented++
		}
	}
	switch {
	case matched == len(report.Changes) && partiallyImplemented == 0:
		report.Status = VerificationStatusMatched
	case matched > 0 || partiallyImplemented > 0:
		report.Status = VerificationStatusPartiallyImplemented
	default:
		report.Status = VerificationStatusDrift
	}
	return finalizeVerificationReport(report), nil
}

func validApprovedVerificationProposal(proposal domain.ArchitectureTargetProposal, expectedFingerprint string) error {
	if proposal.Status != domain.ArchitectureTargetStatusApproved {
		return fmt.Errorf("only an approved architecture TARGET can be verified: %w", domain.ErrInvalidStatus)
	}
	expectedFingerprint = strings.TrimSpace(expectedFingerprint)
	if expectedFingerprint == "" || proposal.Fingerprint == "" || proposal.Fingerprint != expectedFingerprint {
		return fmt.Errorf("architecture TARGET fingerprint is stale or missing: %w", domain.ErrConflict)
	}
	if err := proposal.Valid(); err != nil {
		return err
	}
	computed, err := proposal.ComputedFingerprint()
	if err != nil {
		return err
	}
	if computed != expectedFingerprint {
		return fmt.Errorf("approved architecture TARGET content no longer matches its fingerprint: %w", domain.ErrConflict)
	}
	return nil
}

type verificationCurrentIndex struct {
	services   map[string]domain.ArchitectureCatalogService
	operations map[string]map[string]domain.ArchitectureOperationManifest
	relations  []domain.ArchitectureCatalogRelation
}

func buildVerificationCurrentIndex(catalog domain.ArchitectureCatalog) verificationCurrentIndex {
	index := verificationCurrentIndex{
		services:   make(map[string]domain.ArchitectureCatalogService, len(catalog.Platform.Services)),
		operations: make(map[string]map[string]domain.ArchitectureOperationManifest, len(catalog.Platform.Services)),
		relations:  append([]domain.ArchitectureCatalogRelation(nil), catalog.Platform.Relations...),
	}
	for _, service := range catalog.Platform.Services {
		index.services[service.Source.ProjectID] = service
		operations := make(map[string]domain.ArchitectureOperationManifest)
		for _, group := range service.Groups {
			for _, operation := range group.Operations {
				operations[operation.Manifest.ID] = operation.Manifest
			}
		}
		for _, operation := range service.Ungrouped {
			operations[operation.Manifest.ID] = operation.Manifest
		}
		index.operations[service.Source.ProjectID] = operations
	}
	sort.Slice(index.relations, func(i, j int) bool { return index.relations[i].ID < index.relations[j].ID })
	return index
}

func (index verificationCurrentIndex) verify(change domain.ArchitectureTargetChange) ChangeVerificationResult {
	result := ChangeVerificationResult{ChangeID: change.ID, Status: VerificationStatusDrift}
	for _, unresolved := range change.UnresolvedAreas {
		result.Unknowns = append(result.Unknowns, VerificationUnknown{
			ChangeID: change.ID,
			Area:     unresolved.Area,
			Reason:   "approved TARGET explicitly leaves this area unresolved: " + unresolved.Reason,
		})
	}
	switch change.Action {
	case domain.ArchitectureTargetChangeAdd:
		if index.addedIdentityExists(change) {
			if len(result.Unknowns) == 0 {
				result.Status = VerificationStatusMatched
			} else {
				result.Status = VerificationStatusPartiallyImplemented
			}
			result.Evidence = append(result.Evidence, change.Evidence...)
			return finalizeChangeResult(result)
		}
		result.Gaps = append(result.Gaps, VerificationGap{ChangeID: change.ID, Expectation: "approved added identity exists in CURRENT", Reason: "the exact added service or operation identity is absent from CURRENT"})
	case domain.ArchitectureTargetChangeRemove:
		if index.removedIdentityAbsent(change) {
			if len(result.Unknowns) == 0 {
				result.Status = VerificationStatusMatched
			} else {
				result.Status = VerificationStatusPartiallyImplemented
			}
			result.Evidence = append(result.Evidence, change.Evidence...)
			return finalizeChangeResult(result)
		}
		result.Gaps = append(result.Gaps, VerificationGap{ChangeID: change.ID, Expectation: "approved removed identity is absent from CURRENT", Reason: "the exact service or operation identity remains in CURRENT"})
	default:
		index.observeKnownMaterial(change, &result)
		result.Unknowns = append(result.Unknowns, VerificationUnknown{
			ChangeID: change.ID,
			Area:     string(change.Kind),
			Reason:   "TARGET records a business intent but not a typed desired manifest value; CURRENT cannot prove that the semantic change was implemented",
		})
	}
	return finalizeChangeResult(result)
}

func (index verificationCurrentIndex) addedIdentityExists(change domain.ArchitectureTargetChange) bool {
	if change.Scope == domain.ArchitectureTargetScopeService {
		for _, serviceID := range change.AffectedServiceIDs {
			if _, exists := index.services[serviceID]; !exists {
				return false
			}
		}
		return true
	}
	for _, serviceID := range change.AffectedServiceIDs {
		for _, operationID := range change.AffectedOperationIDs {
			if _, exists := index.operations[serviceID][operationID]; !exists {
				return false
			}
		}
	}
	return true
}

func (index verificationCurrentIndex) removedIdentityAbsent(change domain.ArchitectureTargetChange) bool {
	if change.Scope == domain.ArchitectureTargetScopeService {
		for _, serviceID := range change.AffectedServiceIDs {
			if _, exists := index.services[serviceID]; exists {
				return false
			}
		}
		return true
	}
	for _, serviceID := range change.AffectedServiceIDs {
		for _, operationID := range change.AffectedOperationIDs {
			if _, exists := index.operations[serviceID][operationID]; exists {
				return false
			}
		}
	}
	return true
}

// observeKnownMaterial adds only literal manifest material already available
// in CURRENT. It intentionally never upgrades a semantic change to MATCHED:
// a proposal does not contain a typed replacement value to compare against.
func (index verificationCurrentIndex) observeKnownMaterial(change domain.ArchitectureTargetChange, result *ChangeVerificationResult) {
	switch change.Kind {
	case domain.ArchitectureTargetChangeKindResponsibility:
		for _, serviceID := range change.AffectedServiceIDs {
			if service, exists := index.services[serviceID]; exists && service.Manifest != nil {
				for _, item := range service.Manifest.Responsibilities {
					result.Evidence = append(result.Evidence, item.Evidence...)
				}
			}
		}
	case domain.ArchitectureTargetChangeKindBusinessProcess:
		for _, serviceID := range change.AffectedServiceIDs {
			for _, operationID := range change.AffectedOperationIDs {
				if operation, exists := index.operations[serviceID][operationID]; exists {
					result.Evidence = append(result.Evidence, operation.Evidence...)
					for _, step := range operation.BusinessProcess {
						result.Evidence = append(result.Evidence, step.Description.Evidence...)
					}
				}
			}
		}
	case domain.ArchitectureTargetChangeKindDependency, domain.ArchitectureTargetChangeKindContract, domain.ArchitectureTargetChangeKindEvent:
		for _, relation := range index.relationsFor(change) {
			result.Evidence = append(result.Evidence, relation.Evidence...)
		}
	default:
		for _, serviceID := range change.AffectedServiceIDs {
			if service, exists := index.services[serviceID]; exists && service.Manifest != nil {
				result.Evidence = append(result.Evidence, service.Manifest.Evidence...)
			}
		}
	}
}

func (index verificationCurrentIndex) relationsFor(change domain.ArchitectureTargetChange) []domain.ArchitectureCatalogRelation {
	wanted := map[domain.ArchitectureTargetChangeKind]domain.ArchitectureCatalogRelationType{
		domain.ArchitectureTargetChangeKindDependency: domain.ArchitectureCatalogRelationOutboundDependency,
		domain.ArchitectureTargetChangeKindContract:   domain.ArchitectureCatalogRelationContract,
		domain.ArchitectureTargetChangeKindEvent:      domain.ArchitectureCatalogRelationEvent,
	}[change.Kind]
	if wanted == "" {
		return nil
	}
	affected := make(map[string]struct{}, len(change.AffectedServiceIDs))
	for _, serviceID := range change.AffectedServiceIDs {
		affected[serviceID] = struct{}{}
	}
	var result []domain.ArchitectureCatalogRelation
	for _, relation := range index.relations {
		if relation.Type != wanted {
			continue
		}
		_, sourceAffected := affected[relation.SourceProjectID]
		_, targetAffected := affected[relation.TargetProjectID]
		if sourceAffected || targetAffected {
			result = append(result, relation)
		}
	}
	return result
}

func finalizeChangeResult(result ChangeVerificationResult) ChangeVerificationResult {
	result.Gaps = sortedVerificationGaps(result.Gaps)
	result.Unknowns = sortedVerificationUnknowns(result.Unknowns)
	result.Evidence = uniqueSortedVerificationEvidence(result.Evidence)
	if result.Status == VerificationStatusMatched {
		result.Confidence = 1
	}
	return result
}

func finalizeVerificationReport(report VerificationReport) VerificationReport {
	report.Changes = append([]ChangeVerificationResult(nil), report.Changes...)
	sort.Slice(report.Changes, func(i, j int) bool { return report.Changes[i].ChangeID < report.Changes[j].ChangeID })
	report.Gaps = sortedVerificationGaps(report.Gaps)
	report.Unknowns = sortedVerificationUnknowns(report.Unknowns)
	report.Evidence = uniqueSortedVerificationEvidence(report.Evidence)
	if len(report.Changes) > 0 {
		matched, partiallyImplemented := 0, 0
		for _, change := range report.Changes {
			if change.Status == VerificationStatusMatched {
				matched++
			}
			if change.Status == VerificationStatusPartiallyImplemented {
				partiallyImplemented++
			}
		}
		report.Confidence = (float64(matched) + float64(partiallyImplemented)*.5) / float64(len(report.Changes))
	}
	return report
}

func sortedTargetChanges(values []domain.ArchitectureTargetChange) []domain.ArchitectureTargetChange {
	result := append([]domain.ArchitectureTargetChange(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sortedVerificationGaps(values []VerificationGap) []VerificationGap {
	result := append([]VerificationGap(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].ChangeID+"\x00"+result[i].Expectation+"\x00"+result[i].Reason < result[j].ChangeID+"\x00"+result[j].Expectation+"\x00"+result[j].Reason
	})
	return result
}

func sortedVerificationUnknowns(values []VerificationUnknown) []VerificationUnknown {
	result := append([]VerificationUnknown(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].ChangeID+"\x00"+result[i].Area+"\x00"+result[i].Reason < result[j].ChangeID+"\x00"+result[j].Area+"\x00"+result[j].Reason
	})
	return result
}

func uniqueSortedVerificationEvidence(values []domain.ArchitectureEvidence) []domain.ArchitectureEvidence {
	seen := make(map[string]domain.ArchitectureEvidence, len(values))
	for _, value := range values {
		key := strings.Join([]string{value.SourcePath, value.Symbol, fmt.Sprint(value.StartLine), fmt.Sprint(value.EndLine), value.Checksum}, "\x00")
		seen[key] = value
	}
	result := make([]domain.ArchitectureEvidence, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.Join([]string{result[i].SourcePath, result[i].Symbol, fmt.Sprint(result[i].StartLine), fmt.Sprint(result[i].EndLine), result[i].Checksum}, "\x00") < strings.Join([]string{result[j].SourcePath, result[j].Symbol, fmt.Sprint(result[j].StartLine), fmt.Sprint(result[j].EndLine), result[j].Checksum}, "\x00")
	})
	return result
}
