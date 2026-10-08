package planning

import (
	"encoding/hex"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type FanoutGateInput struct {
	Plan         domain.Plan
	Tasks        []domain.Task
	Routing      domain.RoutingResult
	ContractPlan domain.ContractPlan
	Shards       []domain.ArchitecturalShard
	Baselines    []domain.ContractBaseline
	Fingerprints map[string]string
	Now          time.Time
}

func EvaluateFanoutReadiness(input FanoutGateInput) domain.FanoutReadinessEvidence {
	evidence := domain.FanoutReadinessEvidence{
		PlanID: input.Plan.ID, State: domain.FanoutReadinessReady,
		ParallelismScope: domain.FanoutParallelismScopeSameRepository, Reasons: []string{},
		TaskIDs: []string{}, ShardIDs: []string{}, BaselineIDs: []string{},
		ProfileFingerprints: map[string]string{}, RecordedAt: input.Now,
	}
	for _, task := range input.Tasks {
		evidence.TaskIDs = append(evidence.TaskIDs, task.ID)
	}
	for _, shard := range input.Shards {
		evidence.ShardIDs = append(evidence.ShardIDs, shard.ID)
		if current := input.Fingerprints[shard.RepositoryProjectID]; current != "" {
			evidence.ProfileFingerprints[shard.RepositoryProjectID] = current
		}
	}
	for _, baseline := range input.Baselines {
		evidence.BaselineIDs = append(evidence.BaselineIDs, baseline.ID)
	}
	sort.Strings(evidence.TaskIDs)
	sort.Strings(evidence.ShardIDs)
	sort.Strings(evidence.BaselineIDs)

	if input.Plan.Status != domain.PlanStatusApproved {
		gateReason(&evidence, "plan is not approved", false)
	}
	approvedFingerprint := ""
	if input.Plan.ApprovedFingerprint != nil {
		approvedFingerprint = *input.Plan.ApprovedFingerprint
	}
	if approvedFingerprint == "" || input.Plan.Fingerprint == "" || approvedFingerprint != input.Plan.Fingerprint {
		gateReason(&evidence, "approved Plan fingerprint is missing or changed", false)
	}
	if input.Routing.OwnerReviewRequired || input.Routing.Status != domain.RoutingStatusResolved {
		gateReason(&evidence, "routing or architecture profile requires owner review", true)
	}
	for _, profile := range input.Routing.Profiles {
		if profile.Status != domain.ProfileResolutionResolved || profile.ProfileID == "" {
			gateReason(&evidence, "unresolved architecture profile for repository "+profile.ProjectID, true)
		}
	}
	if input.ContractPlan.State == domain.ContractPlanPlanned {
		gateReason(&evidence, "contract-plan decision is unresolved", true)
	}
	if input.ContractPlan.State != domain.ContractPlanNotRequired && input.ContractPlan.State != domain.ContractPlanFreezeNeeded && input.ContractPlan.State != domain.ContractPlanPlanned {
		gateReason(&evidence, "unknown planning contract state", false)
	}
	contractFingerprint := ""
	if input.ContractPlan.State == domain.ContractPlanFreezeNeeded {
		var err error
		contractFingerprint, err = contractbaseline.PlanFingerprint(input.ContractPlan)
		if err != nil {
			gateReason(&evidence, "current ContractPlan fingerprint could not be computed", false)
		}
	}

	baselineByProject := make(map[string]domain.ContractBaseline, len(input.Baselines))
	contractCommitByProject := map[string]string{}
	for _, baseline := range input.Baselines {
		if _, found := baselineByProject[baseline.RepositoryProjectID]; !found {
			baselineByProject[baseline.RepositoryProjectID] = baseline
		}
		if baseline.ExecutionState == domain.ContractBaselineMaterializing || baseline.ExecutionState == domain.ContractBaselinePending ||
			baseline.ExecutionState == domain.ContractBaselineBlocked || baseline.ExecutionState == domain.ContractBaselineInvalidated {
			gateReason(&evidence, "contract baseline is not frozen for repository "+baseline.RepositoryProjectID, false)
		}
		if baseline.ExecutionState == domain.ContractBaselineFrozen && !baseline.Validation.Passed {
			gateReason(&evidence, "frozen contract baseline failed validation for repository "+baseline.RepositoryProjectID, false)
		}
		if baseline.ExecutionState == domain.ContractBaselineFrozen {
			if existing, found := contractCommitByProject[baseline.RepositoryProjectID]; found && existing != baseline.ContractBaselineCommit {
				gateReason(&evidence, "sibling baselines for repository "+baseline.RepositoryProjectID+" have different contract commits", false)
			}
			contractCommitByProject[baseline.RepositoryProjectID] = baseline.ContractBaselineCommit
			if baseline.ApprovedPlanFingerprint != approvedFingerprint {
				gateReason(&evidence, "contract baseline is bound to a different approved Plan fingerprint", false)
			}
			filesValid := len(baseline.Files) > 0
			for _, file := range baseline.Files {
				if !validHexDigest(file.SHA256, 64) {
					filesValid = false
					break
				}
			}
			if !validGitSHA(baseline.ContractBaselineCommit) || !validHexDigest(baseline.AggregateSHA256, 64) || !filesValid ||
				contractbaseline.AggregateFilesSHA256(baseline.Files) != baseline.AggregateSHA256 ||
				baseline.ContractPlanFingerprint != contractFingerprint ||
				!baseline.ContractAgentPassed || baseline.ContractAgentThreadID == "" || baseline.ContractAgentResultSHA256 == "" ||
				!baseline.MechanicalReviewPassed || baseline.MechanicalReviewSHA256 == "" ||
				!baseline.ContractVerificationPassed || baseline.ContractVerification == "" ||
				!baseline.IndependentReviewPassed || baseline.IndependentReviewStatus != "PASS" ||
				strings.TrimSpace(baseline.IndependentReviewerID) == "" || baseline.IndependentReviewerID == baseline.ContractAgentThreadID ||
				baseline.IndependentReviewSHA256 == "" || !validHexDigest(baseline.ContractAgentResultSHA256, 64) ||
				!validHexDigest(baseline.MechanicalReviewSHA256, 64) || !validHexDigest(baseline.IndependentReviewSHA256, 64) {
				gateReason(&evidence, "contract baseline lacks complete commit, agent, verification, or independent review evidence", false)
			}
		}
	}
	if len(input.Shards) == 0 {
		gateReason(&evidence, "no architectural shards were persisted", false)
	}
	tasksByID := make(map[string]domain.Task, len(input.Tasks))
	for _, task := range input.Tasks {
		tasksByID[task.ID] = task
	}
	if input.ContractPlan.State == domain.ContractPlanFreezeNeeded {
		for _, boundary := range input.ContractPlan.Boundaries {
			baseline, ok := baselineByProject[boundary.Owner.ProjectID]
			if !ok || baseline.ExecutionState != domain.ContractBaselineFrozen || !baseline.Validation.Passed ||
				baseline.ContractBaselineCommit == "" || baseline.AggregateSHA256 == "" ||
				!baseline.ContractAgentPassed || !baseline.IndependentReviewPassed ||
				strings.TrimSpace(baseline.IndependentReviewerID) == "" || baseline.ApprovedPlanFingerprint != approvedFingerprint {
				gateReason(&evidence, "contract boundary "+boundary.Kind+" has no verified frozen baseline", false)
			}
			found := false
			for _, ref := range baseline.MaterializedContracts {
				if ref.Kind == boundary.Kind && ref.RepositoryProjectID == boundary.Owner.ProjectID {
					found = true
				}
			}
			if !found {
				gateReason(&evidence, "contract boundary "+boundary.Kind+" is incomplete", false)
			}
		}
	}
	for _, task := range input.Tasks {
		if input.ContractPlan.State == domain.ContractPlanNotRequired {
			baseline, ok := baselineByProject[task.ProjectID]
			if !ok || baseline.ExecutionState != domain.ContractBaselineNotRequired {
				gateReason(&evidence, "missing not-required baseline for repository "+task.ProjectID, false)
			}
		}
	}
	if !validShardDependencies(input.Shards) {
		gateReason(&evidence, "architectural shard dependencies are invalid", false)
	}
	if conflict := conflictingShardScopes(input.Shards); conflict != "" {
		gateReason(&evidence, "conflicting write scopes: "+conflict, false)
	}
	for _, shard := range input.Shards {
		if strings.TrimSpace(shard.ID) == "" || strings.TrimSpace(shard.TaskID) == "" ||
			strings.TrimSpace(shard.LocalIntent) == "" || len(shard.Targets.Paths) == 0 ||
			len(shard.Targets.EvidenceIDs) == 0 || len(shard.WriteScope.Allow) == 0 ||
			shard.WriteScope.MaxFiles < 1 || len(shard.Verification) == 0 || len(shard.Acceptance) == 0 {
			gateReason(&evidence, "architectural shard is missing required planning evidence: "+shard.RouteID, false)
		}
		if task, found := tasksByID[shard.TaskID]; !found || task.ProjectID != shard.RepositoryProjectID || task.PlanID != input.Plan.ID {
			gateReason(&evidence, "architectural shard is detached from its approved repository Task: "+shard.RouteID, false)
		}
		routed := false
		for _, target := range input.Routing.Routes {
			if target.ProjectID == shard.RepositoryProjectID && target.RouteID == shard.RouteID &&
				sameStringSet(target.Paths, shard.Targets.Paths) && sameStringSet(target.EvidenceIDs, shard.Targets.EvidenceIDs) {
				routed = true
				break
			}
		}
		if !routed {
			gateReason(&evidence, "architectural shard target is not in the approved routing evidence: "+shard.RouteID, false)
		}
		if shard.Status == domain.ShardStatusOwnerReview {
			gateReason(&evidence, "shard requires owner review: "+shard.RouteID, true)
		}
		if shard.Parallel && shard.Phase != "workers" {
			gateReason(&evidence, "non-worker-phase shard cannot join current fan-out", false)
		}
		if shard.ProfileFingerprint == "" || input.Fingerprints[shard.RepositoryProjectID] != shard.ProfileFingerprint {
			gateReason(&evidence, "architecture profile fingerprint changed for repository "+shard.RepositoryProjectID, false)
		}
		if shard.ExecutionBase.Kind == domain.ShardBaseSourceRevision && strings.TrimSpace(shard.ExecutionBase.Revision) == "" {
			gateReason(&evidence, "shard has no approved source base revision: "+shard.RouteID, false)
		}
		if shard.ExecutionBase.Kind == domain.ShardBaseContractBaseline &&
			(shard.ExecutionBase.ContractBaselineID == "" || shard.ExecutionBase.Revision == "") {
			gateReason(&evidence, "shard has no frozen contract baseline base: "+shard.RouteID, false)
		}
		if shard.ExecutionBase.Kind == domain.ShardBaseContractBaseline {
			baseline, found := baselineByProject[shard.RepositoryProjectID]
			if !found || baseline.ExecutionState != domain.ContractBaselineFrozen ||
				shard.ExecutionBase.Revision != baseline.ContractBaselineCommit ||
				shard.ExecutionBase.ContractBaselineID != baseline.ID {
				gateReason(&evidence, "contract-dependent shard does not use the repository's verified frozen commit: "+shard.RouteID, false)
			}
		}
		if input.ContractPlan.State == domain.ContractPlanFreezeNeeded &&
			(len(shard.Consumes) > 0 || len(shard.Implements) > 0) {
			baseline, found := baselineByProject[shard.RepositoryProjectID]
			if !found || baseline.ExecutionState != domain.ContractBaselineFrozen ||
				shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline ||
				shard.ExecutionBase.Revision != baseline.ContractBaselineCommit {
				gateReason(&evidence, "contract-dependent sibling shard has a different or missing common contract base: "+shard.RouteID, false)
			}
		}
		if shard.ExecutionBase.Kind != domain.ShardBaseSourceRevision && shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline {
			gateReason(&evidence, "shard execution base is missing or unknown: "+shard.RouteID, false)
		}
		for _, ownedPath := range append(append([]string(nil), shard.WriteScope.Allow...), shard.WriteScope.Deny...) {
			if err := validateShardScopePath(ownedPath); err != nil {
				gateReason(&evidence, "architectural shard has an unsafe write-scope path: "+shard.RouteID, false)
				break
			}
		}
	}
	return evidence
}

func sameStringSet(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	counts := make(map[string]int, len(first))
	for _, value := range first {
		counts[value]++
	}
	for _, value := range second {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func validGitSHA(value string) bool {
	return validHexDigest(value, 40) || validHexDigest(value, 64)
}

func validHexDigest(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func gateReason(evidence *domain.FanoutReadinessEvidence, reason string, ownerReview bool) {
	evidence.Reasons = append(evidence.Reasons, reason)
	if ownerReview && evidence.State == domain.FanoutReadinessReady {
		evidence.State = domain.FanoutReadinessOwnerReview
	} else if !ownerReview {
		evidence.State = domain.FanoutReadinessBlocked
	}
}

func validShardDependencies(shards []domain.ArchitecturalShard) bool {
	byID := make(map[string]domain.ArchitecturalShard, len(shards))
	for _, shard := range shards {
		byID[shard.ID] = shard
	}
	for _, shard := range shards {
		for _, dependency := range shard.DependsOn {
			if dependency == shard.ID {
				return false
			}
			if _, exists := byID[dependency]; !exists {
				return false
			}
		}
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		for _, dependency := range byID[id].DependsOn {
			if !visit(dependency) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range byID {
		if !visit(id) {
			return false
		}
	}
	return true
}

func conflictingShardScopes(shards []domain.ArchitecturalShard) string {
	for left := 0; left < len(shards); left++ {
		if !shards[left].Parallel || shards[left].Phase != "workers" {
			continue
		}
		for right := left + 1; right < len(shards); right++ {
			if !shards[right].Parallel || shards[right].Phase != "workers" ||
				shards[left].RepositoryProjectID != shards[right].RepositoryProjectID {
				continue
			}
			for _, first := range shards[left].WriteScope.Allow {
				for _, second := range shards[right].WriteScope.Allow {
					if scopePathsOverlap(first, second) {
						return shards[left].RouteID + " <> " + shards[right].RouteID + " at " + first + " / " + second
					}
				}
			}
		}
	}
	return ""
}

func scopePathsOverlap(first, second string) bool {
	if first == second {
		return true
	}
	if matched, _ := path.Match(first, second); matched {
		return true
	}
	if matched, _ := path.Match(second, first); matched {
		return true
	}
	first = strings.TrimSuffix(first, "/**")
	second = strings.TrimSuffix(second, "/**")
	return strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}
