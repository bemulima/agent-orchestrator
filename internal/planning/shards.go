package planning

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ShardPlanningInput struct {
	PlanID             string
	Task               domain.Task
	Repository         string
	Profile            agentcontrol.Profile
	ProfileFingerprint string
	ExecutionBase      domain.ShardExecutionBase
	Routing            domain.RoutingResult
	RouteIDs           []string
	Evidence           []domain.RoutingEvidence
	Verification       []domain.RouteVerification
	Contracts          []domain.ContractReference
	ContractPaths      []string
	Now                time.Time
}

// PlanArchitecturalShards turns one approved project-level Task's already
// persisted routing evidence into narrower internal work units. It does not
// create top-level Tasks or external work items.
func PlanArchitecturalShards(input ShardPlanningInput) ([]domain.ArchitecturalShard, error) {
	if strings.TrimSpace(input.PlanID) == "" || strings.TrimSpace(input.Task.ID) == "" ||
		strings.TrimSpace(input.Task.ProjectID) == "" || strings.TrimSpace(input.Profile.ID) == "" ||
		strings.TrimSpace(input.ProfileFingerprint) == "" {
		return nil, fmt.Errorf("shard planning requires plan, task, repository profile, and profile fingerprint: %w", domain.ErrValidation)
	}
	resolution, found := profileResolutionFor(input.Routing.Profiles, input.Task.ProjectID)
	if !found || resolution.Status != domain.ProfileResolutionResolved || resolution.ProfileID != input.Profile.ID {
		return nil, fmt.Errorf("task %q has no matching resolved profile: %w", input.Task.ID, domain.ErrInvalidStatus)
	}
	evidenceByID := make(map[string]domain.RoutingEvidence, len(input.Evidence))
	for _, evidence := range input.Evidence {
		if evidence.ProjectID == input.Task.ProjectID {
			evidenceByID[evidence.ID] = evidence
		}
	}
	selectedRoutes := make(map[string]struct{}, len(input.RouteIDs))
	for _, routeID := range input.RouteIDs {
		selectedRoutes[routeID] = struct{}{}
	}
	var result []domain.ArchitecturalShard
	for _, target := range input.Routing.Routes {
		if target.ProjectID != input.Task.ProjectID {
			continue
		}
		if _, selected := selectedRoutes[target.RouteID]; !selected {
			continue
		}
		route := profileRoute(input.Profile, target.RouteID)
		if route == nil {
			return nil, fmt.Errorf("routed responsibility %q is absent from profile %q: %w", target.RouteID, input.Profile.ID, domain.ErrValidation)
		}
		if len(target.Paths) == 0 || len(target.EvidenceIDs) == 0 {
			return nil, fmt.Errorf("routed responsibility %q has no evidence-backed targets: %w", target.RouteID, domain.ErrValidation)
		}
		for _, targetPath := range target.Paths {
			if err := agentcontrol.ValidateRelativePath(targetPath); err != nil {
				return nil, fmt.Errorf("shard target path is unsafe: %w", err)
			}
		}
		allow := uniqueSorted(append([]string(nil), target.Paths...))
		var testPaths []string
		var verifyEvidence []string
		for _, id := range target.EvidenceIDs {
			if item, ok := evidenceByID[id]; ok && item.Kind == "test" {
				testPaths = append(testPaths, item.Path)
				verifyEvidence = append(verifyEvidence, id)
			}
		}
		for _, verification := range input.Verification {
			if verification.ProjectID == target.ProjectID && verification.RouteID == target.RouteID {
				verifyEvidence = append(verifyEvidence, verification.EvidenceIDs...)
				for _, id := range verification.EvidenceIDs {
					if item, ok := evidenceByID[id]; ok && item.Kind == "test" {
						testPaths = append(testPaths, item.Path)
					}
				}
			}
		}
		for _, targetPath := range target.Paths {
			if strings.HasSuffix(targetPath, ".go") {
				testPath := strings.TrimSuffix(path.Base(targetPath), ".go") + "_test.go"
				testPaths = append(testPaths, path.Join(path.Dir(targetPath), testPath))
			} else if strings.HasSuffix(targetPath, ".ts") || strings.HasSuffix(targetPath, ".tsx") {
				directory := path.Dir(targetPath)
				stem := strings.TrimSuffix(strings.TrimSuffix(path.Base(targetPath), ".tsx"), ".ts")
				testPaths = append(testPaths,
					path.Join(directory, stem+".test.ts"), path.Join(directory, stem+".test.tsx"),
					path.Join(directory, stem+".spec.ts"), path.Join(directory, stem+".spec.tsx"))
			}
		}
		testPaths = uniqueSorted(testPaths)
		allow = uniqueSorted(append(allow, testPaths...))
		compositionOnly := matchingCompositionSurfaces(input.Profile.CompositionSurfaces, target.Paths)
		composition := isCompositionRoute(target.RouteID) || len(compositionOnly) > 0
		deny := append([]string(nil), input.ContractPaths...)
		readOnlyContracts := append([]string(nil), input.ContractPaths...)
		for _, contract := range input.Contracts {
			deny = append(deny, contract.Path)
			readOnlyContracts = append(readOnlyContracts, contract.Path)
		}
		for _, surface := range input.Profile.CompositionSurfaces {
			deny = append(deny, surface)
		}
		deny = uniqueSorted(deny)
		if composition {
			// Composition is recorded for a later serial phase and cannot join
			// the current worker fan-out.
			compositionOnly = uniqueSorted(append(compositionOnly, target.Paths...))
		}
		verifications := make([]domain.ShardVerification, 0, len(route.VerificationKinds))
		for _, kind := range route.VerificationKinds {
			verifications = append(verifications, domain.ShardVerification{Kind: kind, EvidenceIDs: uniqueSorted(verifyEvidence)})
		}
		status := domain.ShardStatusPlanned
		phase := "workers"
		parallel := true
		if composition {
			status, phase, parallel = domain.ShardStatusComposition, "after_workers", false
		}
		risk := domain.ShardRiskAssessment{Level: input.Task.RiskLevel, Reasons: []string{"risk inherited from the approved repository-level Task"}}
		if route.SharedHotspot {
			risk.Level = domain.RiskLevelHigh
			risk.Reasons = append(risk.Reasons, "profile marks this route as a shared composition hotspot")
		}
		confidence := 0.88
		if strings.EqualFold(input.Routing.Confidence, "medium") {
			confidence = 0.72
		} else if strings.EqualFold(input.Routing.Confidence, "low") {
			confidence = 0.5
		}
		id := stableShardID(input.PlanID, input.Task.ID, input.Task.ProjectID, target.RouteID)
		shard := domain.ArchitecturalShard{
			ID: id, PlanID: input.PlanID, TaskID: input.Task.ID,
			RepositoryProjectID: input.Task.ProjectID, Repository: input.Repository,
			ProfileID: input.Profile.ID, ProfileFingerprint: input.ProfileFingerprint,
			RouteID: target.RouteID, ExecutionBase: input.ExecutionBase,
			LocalIntent: fmt.Sprintf("Implement %s needed by the approved repository Task. Preserve frozen contract boundaries and limit changes to this responsibility's path scope.", route.Purpose),
			Targets:     target, Consumes: contractReferencesFor(input.Contracts, target.RouteID, false),
			Implements: contractReferencesFor(input.Contracts, target.RouteID, true),
			WriteScope: domain.ShardWriteScope{
				Allow: allow, Deny: deny, ReadOnlyContracts: uniqueSorted(readOnlyContracts),
				CompositionOnly: compositionOnly, TestPaths: testPaths, MaxFiles: 12,
			},
			DependsOn:  []string{},
			Acceptance: []string{fmt.Sprintf("The %s responsibility meets its profile contract and passes the listed verification strategy.", route.ID)},
			Risk:       risk, Verification: verifications, Confidence: confidence,
			Status: status, Parallel: parallel, Phase: phase, CreatedAt: input.Now,
		}
		result = append(result, shard)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RouteID < result[j].RouteID })
	return result, nil
}

func stableShardID(planID, taskID, projectID, routeID string) string {
	value := strings.Join([]string{planID, taskID, projectID, routeID}, "\x00")
	hash := sha1.Sum([]byte(value))
	bytes := hash[:16]
	bytes[6] = bytes[6]&0x0f | 0x50
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

// validateShardScopePath accepts a concrete repository path or a directory
// prefix used in a shard deny scope. Artifact paths still use the stricter
// ValidateRelativePath check directly.
func validateShardScopePath(value string) error {
	if strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
	}
	return agentcontrol.ValidateRelativePath(value)
}

func profileResolutionFor(values []domain.ArchitectureProfileResolution, projectID string) (domain.ArchitectureProfileResolution, bool) {
	for _, value := range values {
		if value.ProjectID == projectID {
			return value, true
		}
	}
	return domain.ArchitectureProfileResolution{}, false
}

func profileRoute(profile agentcontrol.Profile, routeID string) *agentcontrol.ProfileRoute {
	for index := range profile.Routes {
		if profile.Routes[index].ID == routeID {
			return &profile.Routes[index]
		}
	}
	return nil
}

func contractReferencesFor(values []domain.ContractReference, routeID string, implementing bool) []domain.ContractReference {
	var result []domain.ContractReference
	for _, ref := range values {
		relation := "consumes"
		if implementing {
			relation = "implements"
		}
		if ref.RouteID == routeID && ref.Relation == relation && ref.Path != "" {
			result = append(result, ref)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

func matchingCompositionSurfaces(surfaces, targets []string) []string {
	var result []string
	for _, surface := range surfaces {
		prefix := strings.TrimSuffix(surface, "**")
		prefix = strings.TrimSuffix(prefix, "/")
		for _, target := range targets {
			if target == prefix || strings.HasPrefix(target, prefix+"/") {
				result = append(result, surface)
			}
		}
	}
	return uniqueSorted(result)
}

func isCompositionRoute(routeID string) bool {
	return routeID == "backend.composition" || routeID == "frontend.app-runtime" || routeID == "frontend.shared.bff"
}
