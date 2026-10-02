package architecturecatalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const graphIdentityDomain = "course-dev-orchestrator/architecture-graph.v1"

// StableReferenceID returns an identity for a repository or owner-declared
// service that does not depend on a database project UUID, snapshot UUID, or
// array position. sourceIdentity is the canonical identity already persisted
// by the project source adapter; manifestID is the owner-authored service
// manifest id, or the literal "repository" for an uncovered project.
func StableReferenceID(sourceIdentity, manifestID string) string {
	sourceIdentity = strings.TrimSpace(sourceIdentity)
	manifestID = strings.TrimSpace(manifestID)
	if sourceIdentity == "" {
		return ""
	}
	if manifestID == "" {
		manifestID = "repository"
	}
	return graphID("ref-", sourceIdentity, manifestID)
}

// StableEdgeID returns an identity for one owner-declared relation. The
// relation tuple includes the contract or operation identity so two distinct
// edges between the same references remain distinct without using ordering.
func StableEdgeID(sourceReferenceID, relationType, targetReferenceID, externalTarget, operationID, transport, contract, direction string) string {
	sourceReferenceID = strings.TrimSpace(sourceReferenceID)
	relationType = strings.TrimSpace(relationType)
	if sourceReferenceID == "" || relationType == "" || (strings.TrimSpace(targetReferenceID) == "" && strings.TrimSpace(externalTarget) == "") {
		return ""
	}
	return graphID("edge-", sourceReferenceID, relationType, strings.TrimSpace(targetReferenceID), strings.TrimSpace(externalTarget), strings.TrimSpace(operationID), strings.TrimSpace(transport), strings.TrimSpace(contract), strings.TrimSpace(direction))
}

// ValidateStableGraphIDs checks that every catalog service and relation has
// a unique stable identity and that relation endpoints agree with the
// service references. It deliberately does not infer or validate external
// topology facts; those remain owner-authored catalog inputs.
func ValidateStableGraphIDs(catalog domain.ArchitectureCatalog) error {
	projectReferences := make(map[string]string, len(catalog.Platform.Services))
	referenceIDs := make(map[string]struct{}, len(catalog.Platform.Services))
	for _, service := range catalog.Platform.Services {
		referenceID := service.Source.ReferenceID
		if referenceID == "" {
			return fmt.Errorf("architecture service %q has no stable reference id: %w", service.Source.ProjectName, domain.ErrValidation)
		}
		if _, exists := referenceIDs[referenceID]; exists {
			return fmt.Errorf("duplicate stable architecture reference id %q: %w", referenceID, domain.ErrConflict)
		}
		referenceIDs[referenceID] = struct{}{}
		projectReferences[service.Source.ProjectID] = referenceID
	}

	edgeIDs := make(map[string]struct{}, len(catalog.Platform.Relations))
	for _, relation := range catalog.Platform.Relations {
		if relation.SourceReferenceID == "" || relation.SourceReferenceID != projectReferences[relation.SourceProjectID] {
			return fmt.Errorf("architecture edge %q has an invalid source reference: %w", relation.EdgeID, domain.ErrValidation)
		}
		if relation.TargetProjectID != "" {
			if relation.TargetReferenceID == "" || relation.TargetReferenceID != projectReferences[relation.TargetProjectID] {
				return fmt.Errorf("architecture edge %q has an invalid target reference: %w", relation.EdgeID, domain.ErrValidation)
			}
		} else if relation.TargetReferenceID != "" || strings.TrimSpace(relation.ExternalTarget) == "" {
			return fmt.Errorf("unresolved architecture edge %q lacks its owner-declared target: %w", relation.EdgeID, domain.ErrValidation)
		}
		expected := StableEdgeID(
			relation.SourceReferenceID, string(relation.Type), relation.TargetReferenceID,
			relation.ExternalTarget, relation.OperationID, relation.Transport, relation.Contract, relation.Direction,
		)
		if relation.EdgeID == "" || relation.EdgeID != expected {
			return fmt.Errorf("architecture edge id %q does not match its stable relationship identity: %w", relation.EdgeID, domain.ErrValidation)
		}
		if _, exists := edgeIDs[relation.EdgeID]; exists {
			return fmt.Errorf("duplicate stable architecture edge id %q: %w", relation.EdgeID, domain.ErrConflict)
		}
		edgeIDs[relation.EdgeID] = struct{}{}
	}
	return nil
}

func graphID(prefix string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(graphIdentityDomain))
	_, _ = hash.Write([]byte{0})
	var length [4]byte
	for _, value := range values {
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return prefix + hex.EncodeToString(hash.Sum(nil))
}
