package shardplanning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	"github.com/bemulima/agent-orchestrator/internal/planning"
	"github.com/bemulima/agent-orchestrator/internal/usecase/contractfreeze"
)

type RepositoryInspector interface {
	InspectWithAllowedChanges(context.Context, string, []string) (domain.RepositorySource, error)
}

type PlanReader interface {
	GetPlan(context.Context, string) (domain.PlanBundle, error)
}

type ProjectReader interface {
	Get(context.Context, string) (domain.Project, error)
}

type ExecutionPlanStore interface {
	repository.ArchitecturalShardRepository
	repository.ContractBaselineRepository
	repository.FanoutReadinessRepository
}

type ContractFreezer interface {
	Freeze(context.Context, contractfreeze.Input) (domain.ContractBaseline, error)
}

type Service struct {
	Plans        PlanReader
	Projects     ProjectReader
	Execution    ExecutionPlanStore
	Repositories RepositoryInspector
	Catalog      agentcontrol.Catalog
	Materializer contractbaseline.Materializer
	Freezer      ContractFreezer
	Now          func() time.Time
}

type preparedRepository struct {
	project     domain.Project
	profile     agentcontrol.Profile
	profileFP   string
	resolution  domain.ArchitectureProfileResolution
	inspection  domain.RepositorySource
	plannedTask domain.PlannedTask
}

func (s Service) Prepare(ctx context.Context, planID string) (domain.FanoutReadinessEvidence, error) {
	if s.Plans == nil || s.Projects == nil || s.Execution == nil || s.Repositories == nil || s.Materializer == nil {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("shard planning dependencies are incomplete: %w", domain.ErrInvalidStatus)
	}
	bundle, err := s.Plans.GetPlan(ctx, planID)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	if bundle.Plan.Status != domain.PlanStatusApproved {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("plan %q must be approved before shard preparation: %w", planID, domain.ErrInvalidStatus)
	}
	if bundle.Plan.ApprovedFingerprint == nil || strings.TrimSpace(bundle.Plan.Fingerprint) == "" ||
		*bundle.Plan.ApprovedFingerprint != bundle.Plan.Fingerprint {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("approved Plan fingerprint is missing or changed: %w", domain.ErrApprovalNeeded)
	}
	var output domain.PlannerOutput
	if err := json.Unmarshal(bundle.Plan.PlannerOutput, &output); err != nil {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("decode approved planner output: %w", domain.ErrValidation)
	}
	if output.Routing == nil || output.ContractPlan == nil {
		return s.ownerReview(ctx, bundle, "approved plan lacks evidence-backed routing or a contract-plan decision")
	}
	if err := agentcontrol.ValidateCatalog(s.Catalog); err != nil {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("validate current Agent Control Plane catalog: %w", err)
	}
	now := s.now()
	existingBaselines, err := s.Execution.ListContractBaselines(ctx, bundle.Plan.ID)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	existingByTask := make(map[string]domain.ContractBaseline, len(existingBaselines))
	for _, baseline := range existingBaselines {
		existingByTask[baseline.TaskID] = baseline
	}
	plannedTasks := map[string]domain.PlannedTask{}
	for _, plannedTask := range output.Tasks {
		plannedTasks[plannedTask.Key] = plannedTask
	}
	prepared := make(map[string]preparedRepository, len(bundle.Tasks))
	fingerprints := map[string]string{}
	preflightReasons := []string{}
	for _, task := range bundle.Tasks {
		plannedTask, ok := plannedTasks[task.PlannerKey]
		if !ok || task.PlanID != bundle.Plan.ID {
			preflightReasons = append(preflightReasons, "top-level Task does not match approved PlannerOutput")
			continue
		}
		if len(plannedTask.ArchitecturalRoutes) == 0 {
			preflightReasons = append(preflightReasons, "approved Task "+task.ID+" has no selected implementation responsibilities")
		} else {
			for _, routeID := range plannedTask.ArchitecturalRoutes {
				if !routingHasRoute(*output.Routing, task.ProjectID, routeID) {
					preflightReasons = append(preflightReasons, "approved Task "+task.ID+" selected an unrouted responsibility "+routeID)
				}
			}
		}
		project, err := s.Projects.Get(ctx, task.ProjectID)
		if err != nil {
			preflightReasons = append(preflightReasons, "cannot load repository for Task "+task.ID)
			continue
		}
		resolution, found := findResolution(output.Routing.Profiles, project.ID)
		if !found || resolution.Status != domain.ProfileResolutionResolved {
			preflightReasons = append(preflightReasons, "unsupported or unresolved architecture profile for repository "+project.ID)
			continue
		}
		profile, found := s.Catalog.Profiles[resolution.ProfileID]
		if !found {
			preflightReasons = append(preflightReasons, "current catalog lacks profile "+resolution.ProfileID)
			continue
		}
		fingerprint, err := agentcontrol.ProfileFingerprint(s.Catalog, profile.ID)
		if err != nil {
			preflightReasons = append(preflightReasons, "cannot fingerprint profile "+profile.ID)
			continue
		}
		if resolution.ProfileFingerprint == "" || resolution.ProfileFingerprint != fingerprint {
			preflightReasons = append(preflightReasons, "approved profile fingerprint is missing or changed for repository "+project.ID)
			continue
		}
		if project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
			preflightReasons = append(preflightReasons, "repository checkout is unavailable for Task "+task.ID)
			continue
		}
		inspection, err := s.Repositories.InspectWithAllowedChanges(ctx, *project.LocalPath, nil)
		if err != nil || inspection.HeadCommit == "" || inspection.HeadCommit != project.HeadCommit || inspection.IsDirty {
			preflightReasons = append(preflightReasons, "repository base revision changed or checkout is dirty for Task "+task.ID)
			continue
		}
		if err := verifyRequiredShape(*project.LocalPath, profile); err != nil {
			preflightReasons = append(preflightReasons, "architecture profile shape no longer matches repository "+project.ID)
			continue
		}
		prepared[task.ID] = preparedRepository{
			project: project, profile: profile, profileFP: fingerprint,
			resolution: resolution, inspection: inspection, plannedTask: plannedTask,
		}
		fingerprints[project.ID] = fingerprint
	}
	if len(preflightReasons) > 0 {
		for _, task := range bundle.Tasks {
			if err := s.Execution.SaveArchitecturalShards(ctx, bundle.Plan.ID, task.ID, []domain.ArchitecturalShard{}); err != nil {
				return domain.FanoutReadinessEvidence{}, err
			}
		}
		if err := s.invalidateStaleBaselines(ctx, bundle.Plan.ID, preflightReasons); err != nil {
			return domain.FanoutReadinessEvidence{}, err
		}
		return s.ownerReview(ctx, bundle, uniqueReasons(preflightReasons)...)
	}
	if len(bundle.Tasks) == 0 || len(prepared) != len(bundle.Tasks) {
		return s.ownerReview(ctx, bundle, "plan does not contain a repository-level Task that can be sharded")
	}
	contractFingerprint, err := contractbaseline.PlanFingerprint(*output.ContractPlan)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	allBaselines := make([]domain.ContractBaseline, 0, len(bundle.Tasks))
	allContracts := []domain.ContractReference{}
	allContractPaths := map[string][]string{}
	frozenByProject := map[string]domain.ContractBaseline{}
	siblingTasksByProject := map[string][]domain.Task{}
	siblingPlansByProject := map[string][]domain.PlannedTask{}
	for _, sibling := range bundle.Tasks {
		if planned, ok := plannedTasks[sibling.PlannerKey]; ok {
			siblingTasksByProject[sibling.ProjectID] = append(siblingTasksByProject[sibling.ProjectID], sibling)
			siblingPlansByProject[sibling.ProjectID] = append(siblingPlansByProject[sibling.ProjectID], planned)
		}
	}
	for _, task := range bundle.Tasks {
		input := prepared[task.ID]
		ownedBoundaries := ownedBoundariesFor(*output.ContractPlan, task.ProjectID)
		baseline, found := existingByTask[task.ID]
		if !found {
			baseline = newBaseline(bundle, task, input, *output.ContractPlan, contractFingerprint, now)
		} else if baseline.ExecutionState == domain.ContractBaselineInvalidated {
			predecessorID := baseline.ID
			baseline = newBaseline(bundle, task, input, *output.ContractPlan, contractFingerprint, now)
			baseline.PredecessorBaselineID = predecessorID
			baseline.ID = contractbaseline.BaselineID(bundle.Plan.ID, task.ID, task.ProjectID, predecessorID)
		}
		if output.ContractPlan.State == domain.ContractPlanFreezeNeeded && len(ownedBoundaries) > 0 && len(baseline.PlannedContracts) == 0 {
			planned, planErr := contractbaseline.PlannedContractReferences(task.ProjectID, input.profile, ownedBoundaries, output.Routing.EvidenceIndex)
			if planErr != nil {
				baseline.ExecutionState = domain.ContractBaselineBlocked
				baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"cannot derive planned contract scope: " + planErr.Error()}}
				baseline.UpdatedAt = s.now()
				if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
					return domain.FanoutReadinessEvidence{}, err
				}
				allBaselines = append(allBaselines, baseline)
				continue
			}
			baseline.PlannedContracts = planned
		}
		if input.project.HeadCommit != input.inspection.HeadCommit {
			baseline.ExecutionState = domain.ContractBaselineInvalidated
			baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"repository revision differs from planned base"}}
			baseline.UpdatedAt = now
			if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
				return domain.FanoutReadinessEvidence{}, err
			}
			allBaselines = append(allBaselines, baseline)
			continue
		}
		if output.ContractPlan.State == domain.ContractPlanFreezeNeeded && len(ownedBoundaries) == 0 {
			baseline.ExecutionState = domain.ContractBaselineNotRequired
			baseline.Validation = domain.ContractBaselineValidation{Passed: true, Reasons: []string{"contract boundary is owned by another repository Task"}}
			baseline.UpdatedAt = now
			if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
				return domain.FanoutReadinessEvidence{}, err
			}
			allBaselines = append(allBaselines, baseline)
			continue
		}
		if baseline.ExecutionState == domain.ContractBaselineFrozen {
			verified := contractbaseline.VerifyBaseline(ctx, s.Materializer, *input.project.LocalPath, baseline,
				approvedFingerprint(bundle.Plan), *output.ContractPlan, input.profile, input.profileFP, input.inspection.HeadCommit, now)
			if verified.ExecutionState != domain.ContractBaselineFrozen {
				if err := s.Execution.SaveContractBaseline(ctx, verified); err != nil {
					return domain.FanoutReadinessEvidence{}, err
				}
				allBaselines = append(allBaselines, verified)
				continue
			}
			baseline = verified
			if baseline.PlanningContractState == domain.ContractPlanFreezeNeeded && len(ownedBoundaries) > 0 {
				frozenByProject[task.ProjectID] = baseline
			}
		} else if baseline.ExecutionState == domain.ContractBaselineNotRequired && output.ContractPlan.State == domain.ContractPlanNotRequired {
			baseline.UpdatedAt = now
			baseline.Validation = domain.ContractBaselineValidation{Passed: true, Reasons: []string{}}
			if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
				return domain.FanoutReadinessEvidence{}, err
			}
			allBaselines = append(allBaselines, baseline)
			continue
		} else {
			if shared, found := frozenByProject[task.ProjectID]; found && output.ContractPlan.State == domain.ContractPlanFreezeNeeded && len(ownedBoundaries) > 0 {
				baselineID, createdAt, predecessorID := baseline.ID, baseline.CreatedAt, baseline.PredecessorBaselineID
				baseline = shared
				baseline.ID, baseline.CreatedAt, baseline.PredecessorBaselineID = baselineID, createdAt, predecessorID
				baseline.TaskID = task.ID
				baseline.UpdatedAt = s.now()
				if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
					return domain.FanoutReadinessEvidence{}, err
				}
			} else {
				baseline.ExecutionState = domain.ContractBaselinePending
				baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"contract artifacts have not been materialized and verified"}}
				baseline.UpdatedAt = now
				if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
					return domain.FanoutReadinessEvidence{}, err
				}
				if output.ContractPlan.State == domain.ContractPlanPlanned {
					allBaselines = append(allBaselines, baseline)
					continue
				}
				if output.ContractPlan.State != domain.ContractPlanFreezeNeeded {
					baseline.ExecutionState = domain.ContractBaselineNotRequired
					baseline.Validation = domain.ContractBaselineValidation{Passed: true, Reasons: []string{}}
					if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
						return domain.FanoutReadinessEvidence{}, err
					}
					allBaselines = append(allBaselines, baseline)
					continue
				}
				baseline.ExecutionState = domain.ContractBaselineMaterializing
				baseline.UpdatedAt = s.now()
				if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
					return domain.FanoutReadinessEvidence{}, err
				}
				if s.Freezer == nil {
					baseline.ExecutionState = domain.ContractBaselineBlocked
					baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{
						"Contract Agent, diff verifier, independent review, and managed commit pipeline are unavailable",
					}}
					baseline.UpdatedAt = s.now()
					if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
						return domain.FanoutReadinessEvidence{}, err
					}
				} else {
					frozen, freezeErr := s.Freezer.Freeze(ctx, contractfreeze.Input{
						Plan: bundle.Plan, Task: task, PlannedTask: input.plannedTask,
						SiblingTasks: siblingTasksByProject[task.ProjectID], SiblingPlannedTasks: siblingPlansByProject[task.ProjectID],
						Project: input.project, Routing: *output.Routing, ContractPlan: *output.ContractPlan,
						Profile: input.profile, ProfileFingerprint: input.profileFP,
						ContractFingerprint: contractFingerprint, Baseline: baseline,
					})
					if freezeErr != nil {
						baseline.ExecutionState = domain.ContractBaselineBlocked
						baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"Contract Freeze pipeline failed: " + freezeErr.Error()}}
						baseline.UpdatedAt = s.now()
					} else {
						baseline = frozen
					}
					if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
						return domain.FanoutReadinessEvidence{}, err
					}
					if len(ownedBoundaries) > 0 {
						frozenByProject[task.ProjectID] = baseline
					}
				}
			}
		}
		contractReferences := baseline.MaterializedContracts
		if len(contractReferences) == 0 {
			contractReferences = baseline.PlannedContracts
		}
		for _, ref := range contractReferences {
			allContracts = append(allContracts, ref)
		}
		allContractPaths[task.ProjectID] = append(allContractPaths[task.ProjectID], baselinePaths(baseline)...)
		for _, ref := range baseline.PlannedContracts {
			allContractPaths[task.ProjectID] = append(allContractPaths[task.ProjectID], ref.Path)
		}
		allBaselines = append(allBaselines, baseline)
	}
	allContracts = addContractConsumers(allContracts, *output.ContractPlan)

	allShards := []domain.ArchitecturalShard{}
	for _, task := range bundle.Tasks {
		input := prepared[task.ID]
		contracts := contractsForRoutes(allContracts, input.plannedTask.ArchitecturalRoutes)
		executionBase := domain.ShardExecutionBase{
			Kind: domain.ShardBaseSourceRevision, Revision: input.inspection.HeadCommit,
		}
		if output.ContractPlan.State == domain.ContractPlanFreezeNeeded && hasLocalContract(contracts, task.ProjectID) {
			if baseline, found := baselineForProject(allBaselines, task.ProjectID); found {
				executionBase = domain.ShardExecutionBase{
					Kind:               domain.ShardBaseContractBaseline,
					Revision:           baseline.ContractBaselineCommit,
					ContractBaselineID: baseline.ID,
				}
			}
		}
		shards, planErr := planning.PlanArchitecturalShards(planning.ShardPlanningInput{
			PlanID: bundle.Plan.ID, Task: task, Repository: input.project.Name,
			Profile: input.profile, ProfileFingerprint: input.profileFP, ExecutionBase: executionBase,
			Routing: *output.Routing, RouteIDs: input.plannedTask.ArchitecturalRoutes,
			Evidence: output.Routing.EvidenceIndex, Contracts: contracts,
			ContractPaths: allContractPaths[task.ProjectID], Verification: output.Routing.Verification, Now: s.now(),
		})
		if planErr != nil {
			return domain.FanoutReadinessEvidence{}, planErr
		}
		if err := s.Execution.SaveArchitecturalShards(ctx, bundle.Plan.ID, task.ID, shards); err != nil {
			return domain.FanoutReadinessEvidence{}, err
		}
		allShards = append(allShards, shards...)
	}
	// Read persisted rows back so the readiness audit reflects durable records.
	allShards, err = s.Execution.ListArchitecturalShards(ctx, bundle.Plan.ID)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	allBaselines, err = s.Execution.ListContractBaselines(ctx, bundle.Plan.ID)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	readiness := planning.EvaluateFanoutReadiness(planning.FanoutGateInput{
		Plan: bundle.Plan, Tasks: bundle.Tasks, Routing: *output.Routing, ContractPlan: *output.ContractPlan,
		Shards: allShards, Baselines: allBaselines, Fingerprints: fingerprints, Now: s.now(),
	})
	if err := s.Execution.SaveFanoutReadiness(ctx, readiness); err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	return readiness, nil
}

func routingHasRoute(routing domain.RoutingResult, projectID, routeID string) bool {
	for _, route := range routing.Routes {
		if route.ProjectID == projectID && route.RouteID == routeID {
			return true
		}
	}
	return false
}

func (s Service) invalidateStaleBaselines(ctx context.Context, planID string, reasons []string) error {
	baselines, err := s.Execution.ListContractBaselines(ctx, planID)
	if err != nil {
		return err
	}
	var invalidationReasons []string
	for _, reason := range reasons {
		if strings.Contains(reason, "fingerprint") || strings.Contains(reason, "revision") || strings.Contains(reason, "shape") {
			invalidationReasons = append(invalidationReasons, reason)
		}
	}
	if len(invalidationReasons) == 0 {
		return nil
	}
	for _, baseline := range baselines {
		if baseline.ExecutionState != domain.ContractBaselineFrozen {
			continue
		}
		baseline.ExecutionState = domain.ContractBaselineInvalidated
		baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: uniqueReasons(invalidationReasons)}
		baseline.UpdatedAt = s.now()
		if err := s.Execution.SaveContractBaseline(ctx, baseline); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) ownerReview(ctx context.Context, bundle domain.PlanBundle, reasons ...string) (domain.FanoutReadinessEvidence, error) {
	evidence := domain.FanoutReadinessEvidence{
		PlanID: bundle.Plan.ID, State: domain.FanoutReadinessOwnerReview,
		ParallelismScope: domain.FanoutParallelismScopeSameRepository,
		Reasons:          uniqueReasons(reasons), TaskIDs: []string{}, ShardIDs: []string{}, BaselineIDs: []string{},
		ProfileFingerprints: map[string]string{}, RecordedAt: s.now(),
	}
	for _, task := range bundle.Tasks {
		evidence.TaskIDs = append(evidence.TaskIDs, task.ID)
		if err := s.Execution.SaveArchitecturalShards(ctx, bundle.Plan.ID, task.ID, []domain.ArchitecturalShard{}); err != nil {
			return domain.FanoutReadinessEvidence{}, err
		}
	}
	baselines, err := s.Execution.ListContractBaselines(ctx, bundle.Plan.ID)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	for _, baseline := range baselines {
		evidence.BaselineIDs = append(evidence.BaselineIDs, baseline.ID)
	}
	sort.Strings(evidence.BaselineIDs)
	if err := s.Execution.SaveFanoutReadiness(ctx, evidence); err != nil {
		return domain.FanoutReadinessEvidence{}, err
	}
	return evidence, nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func newBaseline(bundle domain.PlanBundle, task domain.Task, input preparedRepository, contractPlan domain.ContractPlan, planFingerprint string, now time.Time) domain.ContractBaseline {
	state := domain.ContractBaselinePending
	validation := domain.ContractBaselineValidation{Passed: false, Reasons: []string{"contract artifacts are awaiting baseline evaluation"}}
	if contractPlan.State == domain.ContractPlanNotRequired {
		state, validation = domain.ContractBaselineNotRequired, domain.ContractBaselineValidation{Passed: true, Reasons: []string{}}
	} else if contractPlan.State == domain.ContractPlanPlanned {
		state = domain.ContractBaselinePending
		validation.Reasons = []string{"contract plan remains unresolved"}
	}
	return domain.ContractBaseline{
		ID:     contractbaseline.BaselineID(bundle.Plan.ID, task.ID, task.ProjectID),
		PlanID: bundle.Plan.ID, TaskID: task.ID, RepositoryProjectID: task.ProjectID,
		Repository: input.project.Name, ProfileID: input.profile.ID,
		PlanningContractState: contractPlan.State, ExecutionState: state,
		ApprovedPlanFingerprint: approvedFingerprint(bundle.Plan),
		MaterializedContracts:   []domain.ContractReference{}, Files: []domain.ContractBaselineFile{},
		ContractPlanFingerprint: planFingerprint, ProfileFingerprint: input.profileFP,
		RepositoryRevision: input.project.HeadCommit, Validation: validation,
		CreatedAt: now, UpdatedAt: now,
	}
}

func approvedFingerprint(plan domain.Plan) string {
	if plan.ApprovedFingerprint == nil {
		return ""
	}
	return *plan.ApprovedFingerprint
}

func findResolution(values []domain.ArchitectureProfileResolution, projectID string) (domain.ArchitectureProfileResolution, bool) {
	for _, value := range values {
		if value.ProjectID == projectID {
			return value, true
		}
	}
	return domain.ArchitectureProfileResolution{}, false
}

func ownedBoundariesFor(plan domain.ContractPlan, projectID string) []domain.PlannedContractBoundary {
	var result []domain.PlannedContractBoundary
	for _, boundary := range plan.Boundaries {
		if boundary.Owner.ProjectID == projectID {
			result = append(result, boundary)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

func addContractConsumers(owned []domain.ContractReference, plan domain.ContractPlan) []domain.ContractReference {
	byKind := map[string]domain.ContractReference{}
	for _, ref := range owned {
		byKind[ref.Kind] = ref
	}
	result := append([]domain.ContractReference(nil), owned...)
	for _, boundary := range plan.Boundaries {
		ref, ok := byKind[boundary.Kind]
		if !ok {
			continue
		}
		for _, consumer := range boundary.Consumers {
			copy := ref
			copy.RouteID, copy.Relation = consumer.RouteID, "consumes"
			for _, implementer := range boundary.Implementers {
				if implementer == consumer {
					copy.Relation = "implements"
				}
			}
			result = append(result, copy)
		}
	}
	return result
}

func contractsForRoutes(values []domain.ContractReference, routeIDs []string) []domain.ContractReference {
	routes := map[string]struct{}{}
	for _, routeID := range routeIDs {
		routes[routeID] = struct{}{}
	}
	var result []domain.ContractReference
	for _, ref := range values {
		if _, ok := routes[ref.RouteID]; ok && ref.RepositoryProjectID != "" && ref.Path != "" {
			result = append(result, ref)
		}
	}
	return result
}

func baselinePaths(baseline domain.ContractBaseline) []string {
	result := make([]string, 0, len(baseline.Files))
	for _, file := range baseline.Files {
		result = append(result, file.Path)
	}
	sort.Strings(result)
	return result
}

func hasLocalContract(contracts []domain.ContractReference, projectID string) bool {
	for _, contract := range contracts {
		if contract.RepositoryProjectID == projectID {
			return true
		}
	}
	return false
}

func baselineForProject(baselines []domain.ContractBaseline, projectID string) (domain.ContractBaseline, bool) {
	for _, baseline := range baselines {
		if baseline.RepositoryProjectID == projectID {
			return baseline, true
		}
	}
	return domain.ContractBaseline{}, false
}

func verifyRequiredShape(root string, profile agentcontrol.Profile) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, required := range profile.RepoShape.RequiredPaths {
		isDirectory := strings.HasSuffix(required, "/")
		required = strings.TrimSuffix(required, "/")
		target := filepath.Join(root, filepath.FromSlash(required))
		current := root
		for _, part := range strings.Split(filepath.FromSlash(required), string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("required profile path is absent or unsafe")
			}
		}
		info, err := os.Lstat(target)
		if err != nil || isDirectory && !info.IsDir() || !isDirectory && !info.Mode().IsRegular() {
			return fmt.Errorf("required profile path has the wrong filesystem type")
		}
	}
	return nil
}

func uniqueReasons(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
