package planning

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Validator struct {
	MaxParallelTasks     int
	MaxRequiredTaskDepth int
	ControlPlane         agentcontrol.Catalog
}

func (v Validator) Validate(ctx context.Context, output domain.PlannerOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(output.Summary) == "" || len(output.Tasks) == 0 {
		return fmt.Errorf("plan summary and tasks are required: %w", domain.ErrValidation)
	}
	if !validRisk(output.RiskLevel) {
		return fmt.Errorf("invalid plan risk level %q: %w", output.RiskLevel, domain.ErrValidation)
	}
	tasks := make(map[string]domain.PlannedTask, len(output.Tasks))
	projects := make(map[string]struct{}, len(output.Tasks))
	for _, task := range output.Tasks {
		if task.Key == "" || task.ProjectID == "" || task.Role == "" || strings.TrimSpace(task.Title) == "" ||
			strings.TrimSpace(task.Description) == "" || len(task.AcceptanceCriteria) == 0 || len(task.WriteScope) == 0 ||
			len(task.VerificationCommands) == 0 || !validRisk(task.RiskLevel) || task.Depth < 0 ||
			task.Depth > v.maxDepth() || !validModelProfile(task.ModelProfile) ||
			!allNonEmpty(task.AcceptanceCriteria) || !validWriteScopes(task.WriteScope) ||
			!allNonEmpty(task.VerificationCommands) {
			return fmt.Errorf("task %q is incomplete: %w", task.Key, domain.ErrValidation)
		}
		if _, exists := tasks[task.Key]; exists {
			return fmt.Errorf("duplicate task key %q: %w", task.Key, domain.ErrConflict)
		}
		if _, exists := projects[task.ProjectID]; exists {
			return fmt.Errorf("project %q is assigned to more than one task: %w", task.ProjectID, domain.ErrValidation)
		}
		tasks[task.Key] = task
		projects[task.ProjectID] = struct{}{}
	}
	indegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))
	edges := make(map[string]struct{}, len(output.Dependencies))
	for key := range tasks {
		indegree[key] = 0
	}
	for _, dependency := range output.Dependencies {
		if dependency.TaskKey == dependency.DependsOnTaskKey || dependency.DependencyType == "" {
			return fmt.Errorf("invalid self or untyped dependency for %q: %w", dependency.TaskKey, domain.ErrValidation)
		}
		if _, exists := tasks[dependency.TaskKey]; !exists {
			return fmt.Errorf("dependency references missing task %q: %w", dependency.TaskKey, domain.ErrValidation)
		}
		if _, exists := tasks[dependency.DependsOnTaskKey]; !exists {
			return fmt.Errorf("dependency references missing prerequisite %q: %w", dependency.DependsOnTaskKey, domain.ErrValidation)
		}
		edge := dependency.TaskKey + "\x00" + dependency.DependsOnTaskKey
		if _, exists := edges[edge]; exists {
			return fmt.Errorf("duplicate dependency for %q: %w", dependency.TaskKey, domain.ErrConflict)
		}
		edges[edge] = struct{}{}
		indegree[dependency.TaskKey]++
		dependents[dependency.DependsOnTaskKey] = append(dependents[dependency.DependsOnTaskKey], dependency.TaskKey)
	}
	ready := make([]string, 0)
	for key, degree := range indegree {
		if degree == 0 {
			ready = append(ready, key)
		}
	}
	processed := 0
	for len(ready) > 0 {
		if len(ready) > v.maxParallel() {
			return fmt.Errorf("plan exposes %d parallel tasks, maximum is %d: %w", len(ready), v.maxParallel(), domain.ErrValidation)
		}
		wave := ready
		ready = nil
		processed += len(wave)
		for _, key := range wave {
			for _, dependent := range dependents[key] {
				indegree[dependent]--
				if indegree[dependent] == 0 {
					ready = append(ready, dependent)
				}
			}
		}
	}
	if processed != len(tasks) {
		return fmt.Errorf("plan task graph contains a cycle: %w", domain.ErrValidation)
	}
	if err := v.validatePlannerMetadata(output, projects); err != nil {
		return err
	}
	return nil
}

func (v Validator) validatePlannerMetadata(output domain.PlannerOutput, projects map[string]struct{}) error {
	if output.PlanningMetadataVersion == 0 {
		if output.Routing != nil || output.ContractPlan != nil {
			return fmt.Errorf("planner metadata requires an explicit version: %w", domain.ErrValidation)
		}
		return nil
	}
	if output.PlanningMetadataVersion != domain.PlannerMetadataVersionV1 ||
		output.Routing == nil || output.ContractPlan == nil {
		return fmt.Errorf("unsupported or incomplete planner metadata version %d: %w", output.PlanningMetadataVersion, domain.ErrValidation)
	}
	if err := agentcontrol.ValidateCatalog(v.ControlPlane); err != nil || strings.TrimSpace(v.ControlPlane.Digest) == "" {
		return fmt.Errorf("canonical routing catalog is unavailable: %w", domain.ErrValidation)
	}
	routing, plan := output.Routing, output.ContractPlan
	if routing.ScopeMode != "analysis_only" || routing.CatalogDigest != v.ControlPlane.Digest ||
		!validRoutingClassification(routing.Classification) || !validRoutingConfidence(routing.Confidence) {
		return fmt.Errorf("routing provenance or scope mode is invalid: %w", domain.ErrValidation)
	}
	if routing.Status != domain.RoutingStatusResolved && routing.Status != domain.RoutingStatusPartial &&
		routing.Status != domain.RoutingStatusUnresolved {
		return fmt.Errorf("unknown routing status %q: %w", routing.Status, domain.ErrValidation)
	}
	reviewRequired := routing.Status != domain.RoutingStatusResolved || routing.Confidence == "low"
	if routing.OwnerReviewRequired != reviewRequired ||
		reviewRequired && !containsString(routing.Avoid, "owner review is required before treating route analysis as implementation scope") {
		return fmt.Errorf("routing uncertainty must require explicit owner review: %w", domain.ErrValidation)
	}
	evidenceByID := make(map[string]domain.RoutingEvidence, len(routing.EvidenceIndex))
	evidenceByPath := make(map[string]domain.RoutingEvidence, len(routing.EvidenceIndex))
	for _, evidence := range routing.EvidenceIndex {
		if evidence.ID == "" || evidence.ProjectID == "" || evidence.Kind == "" || evidence.Summary == "" ||
			!validEvidenceChecksum(evidence.Checksum) || !validEvidencePath(evidence.Path) || !validEvidenceKind(evidence.Kind) {
			return fmt.Errorf("routing evidence is incomplete or unsafe: %w", domain.ErrValidation)
		}
		if _, ok := projects[evidence.ProjectID]; !ok {
			return fmt.Errorf("routing evidence references unselected project %q: %w", evidence.ProjectID, domain.ErrValidation)
		}
		if _, duplicate := evidenceByID[evidence.ID]; duplicate {
			return fmt.Errorf("duplicate routing evidence id %q: %w", evidence.ID, domain.ErrConflict)
		}
		pathKey := evidence.ProjectID + "\x00" + evidence.Path
		if _, duplicate := evidenceByPath[pathKey]; duplicate {
			return fmt.Errorf("duplicate routing evidence path %q: %w", evidence.Path, domain.ErrConflict)
		}
		evidenceByID[evidence.ID] = evidence
		evidenceByPath[pathKey] = evidence
	}
	profileByProject := make(map[string]domain.ArchitectureProfileResolution, len(routing.Profiles))
	for _, resolution := range routing.Profiles {
		if _, ok := projects[resolution.ProjectID]; !ok {
			return fmt.Errorf("profile resolution references unselected project %q: %w", resolution.ProjectID, domain.ErrValidation)
		}
		if _, duplicate := profileByProject[resolution.ProjectID]; duplicate {
			return fmt.Errorf("duplicate profile resolution for project %q: %w", resolution.ProjectID, domain.ErrConflict)
		}
		profileByProject[resolution.ProjectID] = resolution
		switch resolution.Status {
		case domain.ProfileResolutionUnresolved:
			if resolution.ProfileID != "" || resolution.Variant != "" || strings.TrimSpace(resolution.Reason) == "" {
				return fmt.Errorf("unresolved profile for project %q must state a reason and no profile: %w", resolution.ProjectID, domain.ErrValidation)
			}
		case domain.ProfileResolutionResolved:
			profile, ok := v.ControlPlane.Profiles[resolution.ProfileID]
			if !ok {
				return fmt.Errorf("unknown architecture profile %q: %w", resolution.ProfileID, domain.ErrValidation)
			}
			if resolution.ProfileFingerprint != "" {
				fingerprint, err := agentcontrol.ProfileFingerprint(v.ControlPlane, resolution.ProfileID)
				if err != nil || fingerprint != resolution.ProfileFingerprint {
					return fmt.Errorf("architecture profile %q fingerprint does not match canonical profile assets: %w", resolution.ProfileID, domain.ErrConflict)
				}
			}
			if resolution.Variant != "" && !profileHasVariant(profile, resolution.Variant) {
				return fmt.Errorf("unknown profile variant %q for %q: %w", resolution.Variant, resolution.ProfileID, domain.ErrValidation)
			}
			if resolution.ProfileID == "nextjs.common" && resolution.Variant != "" &&
				!variantEvidenceExists(resolution, evidenceByPath) {
				return fmt.Errorf("profile variant %q lacks matching repository path evidence: %w", resolution.Variant, domain.ErrValidation)
			}
			if len(resolution.EvidenceIDs) == 0 || !allEvidenceIDsExist(resolution.EvidenceIDs, evidenceByID, resolution.ProjectID) {
				return fmt.Errorf("resolved profile %q lacks repository evidence: %w", resolution.ProfileID, domain.ErrValidation)
			}
		default:
			return fmt.Errorf("unknown profile resolution status %q: %w", resolution.Status, domain.ErrValidation)
		}
	}
	if len(profileByProject) != len(projects) {
		return fmt.Errorf("routing metadata must resolve every selected project: %w", domain.ErrValidation)
	}
	routes := make(map[string]domain.RoutedTarget, len(routing.Routes))
	routeRefs := make(map[string]domain.RouteReference, len(routing.Routes))
	for _, route := range routing.Routes {
		key := routeKey(route.ProjectID, route.RouteID)
		if route.ProjectID == "" || route.RouteID == "" || len(route.Paths) == 0 ||
			len(route.EvidenceIDs) == 0 {
			return fmt.Errorf("routed target lacks evidence-backed paths: %w", domain.ErrValidation)
		}
		if _, duplicate := routes[key]; duplicate {
			return fmt.Errorf("duplicate routed target %q: %w", key, domain.ErrConflict)
		}
		resolution, ok := profileByProject[route.ProjectID]
		if !ok || resolution.Status != domain.ProfileResolutionResolved {
			return fmt.Errorf("route %q has no resolved architecture profile: %w", key, domain.ErrValidation)
		}
		profile := v.ControlPlane.Profiles[resolution.ProfileID]
		profileRoute := findProfileRoute(profile, route.RouteID)
		if profileRoute == nil {
			return fmt.Errorf("unknown route %q for profile %q: %w", route.RouteID, profile.ID, domain.ErrValidation)
		}
		pathSet := map[string]struct{}{}
		routeEvidenceSet := make(map[string]struct{}, len(route.EvidenceIDs))
		for _, id := range route.EvidenceIDs {
			routeEvidenceSet[id] = struct{}{}
		}
		for _, targetPath := range route.Paths {
			if !validEvidencePath(targetPath) || !evidenceMatchesRoute(targetPath, profileRoute.Targets) {
				return fmt.Errorf("route %q has path incompatible with profile: %w", key, domain.ErrValidation)
			}
			if _, duplicate := pathSet[targetPath]; duplicate {
				return fmt.Errorf("route %q repeats target path %q: %w", key, targetPath, domain.ErrConflict)
			}
			pathSet[targetPath] = struct{}{}
			evidence, exists := evidenceByPath[route.ProjectID+"\x00"+targetPath]
			if !exists {
				return fmt.Errorf("route %q targets a path absent from repository evidence: %w", key, domain.ErrValidation)
			}
			if _, referenced := routeEvidenceSet[evidence.ID]; !referenced {
				return fmt.Errorf("route %q path lacks its own evidence reference: %w", key, domain.ErrValidation)
			}
		}
		if !allEvidenceIDsExist(route.EvidenceIDs, evidenceByID, route.ProjectID) {
			return fmt.Errorf("route %q references missing or foreign evidence: %w", key, domain.ErrValidation)
		}
		for _, evidenceID := range route.EvidenceIDs {
			if _, ok := pathSet[evidenceByID[evidenceID].Path]; !ok {
				return fmt.Errorf("route %q evidence does not support its target paths: %w", key, domain.ErrValidation)
			}
		}
		if !allUniqueNonEmpty(route.Symbols) {
			return fmt.Errorf("route %q contains duplicate or empty symbols: %w", key, domain.ErrValidation)
		}
		knownSymbols := map[string]struct{}{}
		for _, evidenceID := range route.EvidenceIDs {
			for _, symbol := range strings.Split(evidenceByID[evidenceID].Symbol, ", ") {
				if symbol != "" {
					knownSymbols[symbol] = struct{}{}
				}
			}
		}
		for _, symbol := range route.Symbols {
			if _, ok := knownSymbols[symbol]; !ok {
				return fmt.Errorf("route %q cites unsupported symbol %q: %w", key, symbol, domain.ErrValidation)
			}
		}
		routes[key] = route
		routeRefs[key] = domain.RouteReference{ProjectID: route.ProjectID, RouteID: route.RouteID}
	}
	if routing.Status == domain.RoutingStatusResolved {
		if len(routes) == 0 || len(profileByProject) == 0 || anyUnresolvedProfile(profileByProject) {
			return fmt.Errorf("resolved routing status conflicts with profile results: %w", domain.ErrValidation)
		}
		for projectID := range profileByProject {
			if !projectHasRoute(projectID, routes) {
				return fmt.Errorf("resolved routing omitted project %q: %w", projectID, domain.ErrValidation)
			}
		}
	} else if routing.Status == domain.RoutingStatusPartial && len(routes) == 0 {
		return fmt.Errorf("partial routing requires at least one resolved route: %w", domain.ErrValidation)
	} else if routing.Status == domain.RoutingStatusUnresolved &&
		(len(routes) != 0 || strings.TrimSpace(routing.UnresolvedReason) == "") {
		return fmt.Errorf("unresolved routing requires a reason: %w", domain.ErrValidation)
	}
	if routing.Status == domain.RoutingStatusPartial && strings.TrimSpace(routing.UnresolvedReason) == "" {
		return fmt.Errorf("partial routing requires an unresolved reason: %w", domain.ErrValidation)
	}
	if routing.PrimaryRoute != nil {
		if _, ok := routeRefs[routeKey(routing.PrimaryRoute.ProjectID, routing.PrimaryRoute.RouteID)]; !ok {
			return fmt.Errorf("primary route is absent from routing result: %w", domain.ErrValidation)
		}
	} else if len(routes) > 0 {
		return fmt.Errorf("routed plan must identify a primary route: %w", domain.ErrValidation)
	}
	for _, id := range routing.EvidenceIDs {
		if _, ok := evidenceByID[id]; !ok {
			return fmt.Errorf("routing result references missing evidence %q: %w", id, domain.ErrValidation)
		}
	}
	for _, inspection := range routing.ContractsToInspect {
		if strings.TrimSpace(inspection.Kind) == "" || len(inspection.Routes) == 0 ||
			!allEvidenceIDsExist(inspection.EvidenceIDs, evidenceByID, inspection.ProjectID) {
			return fmt.Errorf("contract inspection lacks route or evidence: %w", domain.ErrValidation)
		}
		for _, ref := range inspection.Routes {
			if ref.ProjectID != inspection.ProjectID {
				return fmt.Errorf("contract inspection crosses project ownership: %w", domain.ErrValidation)
			}
			if _, ok := routeRefs[routeKey(ref.ProjectID, ref.RouteID)]; !ok {
				return fmt.Errorf("contract inspection references an unrouted route: %w", domain.ErrValidation)
			}
		}
	}
	verificationByRoute := map[string]domain.RouteVerification{}
	for _, verification := range routing.Verification {
		key := routeKey(verification.ProjectID, verification.RouteID)
		if _, ok := routes[key]; !ok || verification.Boundary != verificationBoundaryForRoute(verification.RouteID) ||
			strings.TrimSpace(verification.Rationale) == "" {
			return fmt.Errorf("verification references an unrouted or incomplete boundary: %w", domain.ErrValidation)
		}
		if _, duplicate := verificationByRoute[key]; duplicate {
			return fmt.Errorf("duplicate verification boundary for %q: %w", key, domain.ErrConflict)
		}
		switch verification.EvidenceState {
		case domain.VerificationEvidenceExisting:
			if len(verification.EvidenceIDs) == 0 {
				return fmt.Errorf("existing verification boundary requires evidence: %w", domain.ErrValidation)
			}
			for _, id := range verification.EvidenceIDs {
				if !allEvidenceIDsExist([]string{id}, evidenceByID, verification.ProjectID) || evidenceByID[id].Kind != "test" {
					return fmt.Errorf("verification boundary references unsupported test evidence: %w", domain.ErrValidation)
				}
			}
		case domain.VerificationEvidenceToAdd:
			if len(verification.EvidenceIDs) != 0 {
				return fmt.Errorf("to_add verification cannot claim existing evidence: %w", domain.ErrValidation)
			}
		default:
			return fmt.Errorf("unknown verification evidence state %q: %w", verification.EvidenceState, domain.ErrValidation)
		}
		verificationByRoute[key] = verification
	}
	if len(verificationByRoute) != len(routes) {
		return fmt.Errorf("every routed target requires one verification boundary: %w", domain.ErrValidation)
	}
	for _, candidate := range routing.SharedBoundaryCandidates {
		if strings.TrimSpace(candidate.Kind) == "" || len(candidate.Routes) < 2 ||
			!allRouteReferencesExist(candidate.Routes, routeRefs) {
			return fmt.Errorf("shared-boundary candidate references invalid routes: %w", domain.ErrValidation)
		}
	}
	if plan.State == domain.ContractPlanNotRequired {
		if plan.Required || plan.FreezeRequired || len(plan.Boundaries) != 0 || len(plan.ChangesRequired) != 0 ||
			len(plan.AffectedRoutes) != 0 || strings.TrimSpace(plan.Reason) == "" ||
			routing.Status != domain.RoutingStatusResolved {
			return fmt.Errorf("NOT_REQUIRED contract plan contains required work: %w", domain.ErrValidation)
		}
	} else if plan.State == domain.ContractPlanFreezeNeeded {
		if !plan.Required || !plan.FreezeRequired || len(routes) < 2 || len(routing.SharedBoundaryCandidates) == 0 ||
			len(plan.Boundaries) == 0 || strings.TrimSpace(plan.Reason) == "" {
			return fmt.Errorf("FREEZE_REQUIRED contract plan is inconsistent: %w", domain.ErrValidation)
		}
	} else if plan.State == domain.ContractPlanPlanned {
		if !plan.Required || plan.FreezeRequired || routing.Status == domain.RoutingStatusResolved ||
			len(routing.SharedBoundaryCandidates) > 0 || len(plan.Boundaries) != 0 ||
			len(plan.ChangesRequired) != 0 || len(plan.IndependentAfterFreeze) != 0 ||
			strings.TrimSpace(plan.Reason) == "" {
			return fmt.Errorf("PLANNED contract state is inconsistent: %w", domain.ErrValidation)
		}
	} else {
		return fmt.Errorf("unknown contract-plan state %q: %w", plan.State, domain.ErrValidation)
	}
	if plan.State == domain.ContractPlanNotRequired && len(routes) >= 2 && len(routing.SharedBoundaryCandidates) > 0 ||
		plan.State == domain.ContractPlanFreezeNeeded && (len(routes) < 2 || len(routing.SharedBoundaryCandidates) == 0) {
		return fmt.Errorf("contract-plan decision does not follow shared-boundary evidence: %w", domain.ErrValidation)
	}
	if len(routing.SharedBoundaryCandidates) > 0 &&
		len(routes) >= 2 && plan.State != domain.ContractPlanFreezeNeeded {
		return fmt.Errorf("shared boundaries require FREEZE_REQUIRED contract planning: %w", domain.ErrValidation)
	}
	if plan.State != domain.ContractPlanNotRequired &&
		!sameRouteReferenceSet(plan.AffectedRoutes, routeRefs) {
		return fmt.Errorf("contract plan affected routes differ from routing result: %w", domain.ErrValidation)
	}
	boundaryKinds := map[string]struct{}{}
	for _, boundary := range plan.Boundaries {
		if strings.TrimSpace(boundary.Kind) == "" || strings.TrimSpace(boundary.Rationale) == "" ||
			strings.TrimSpace(boundary.TargetPath) == "" || len(boundary.Consumers) == 0 ||
			!allRouteReferencesExist(boundary.Consumers, routeRefs) {
			return fmt.Errorf("planned contract boundary has impossible owner or consumers: %w", domain.ErrValidation)
		}
		profileResolution := profileByProject[boundary.Owner.ProjectID]
		profile, ok := v.ControlPlane.Profiles[profileResolution.ProfileID]
		if !ok {
			return fmt.Errorf("contract owner route has no resolved architecture profile: %w", domain.ErrValidation)
		}

		ownerSelected := routeRefExists(boundary.Owner, routeRefs)
		rule, explicit := agentcontrol.BoundaryOwnershipFor(profile, boundary.Kind)
		if explicit && !containsString(rule.OwnerRoutes, boundary.Owner.RouteID) || !explicit && !ownerSelected {
			return fmt.Errorf("contract owner is not permitted by the resolved profile: %w", domain.ErrValidation)
		}
		ownerRoute := findProfileRoute(profile, boundary.Owner.RouteID)
		if ownerRoute == nil {
			return fmt.Errorf("unknown contract owner: %w", domain.ErrValidation)
		}
		if !ownerSelected {
			if !containsContractOwner(plan.ContractOwnerRoutes, boundary.Owner) {
				return fmt.Errorf("contract-only owner was not explicitly recorded: %w", domain.ErrValidation)
			}
			proven := false
			for _, ev := range routing.EvidenceIndex {
				if ev.ProjectID == boundary.Owner.ProjectID && ev.Kind == "source" && evidenceMatchesRoute(ev.Path, ownerRoute.Targets) {
					proven = true
					break
				}
			}
			if !proven {
				return fmt.Errorf("contract-only owner lacks repository evidence: %w", domain.ErrValidation)
			}
		}
		for _, ref := range boundary.Consumers {
			if ref.ProjectID != boundary.Owner.ProjectID || !agentcontrol.ContractDependencyAllowed(profile, ref.RouteID, boundary.Owner.RouteID) {
				return fmt.Errorf("ARCHITECTURE_CONFLICT: contract consumer dependency is forbidden: %w", domain.ErrValidation)
			}
		}
		if !sameReferenceSlice(boundary.Implementers, contractImplementers(profile, boundary.Kind, boundary.Consumers)) {
			return fmt.Errorf("contract implementers differ from profile: %w", domain.ErrValidation)
		}
		targetReferences, targetErr := contractbaseline.PlannedContractReferences(boundary.Owner.ProjectID, profile,
			[]domain.PlannedContractBoundary{boundary}, routing.EvidenceIndex)
		if targetErr != nil || len(targetReferences) != 1 || targetReferences[0].Path != boundary.TargetPath {
			return fmt.Errorf("planned contract target path is outside its profile owner surface: %w", domain.ErrValidation)
		}
		if boundary.Existing {
			if boundary.SourceEvidenceID == "" {
				return fmt.Errorf("existing contract boundary lacks source evidence: %w", domain.ErrValidation)
			}
			evidence, ok := evidenceByID[boundary.SourceEvidenceID]
			if !ok || !containsString(evidence.BoundaryKinds, boundary.Kind) ||
				evidence.Kind != "contract" && evidence.Kind != "source" {
				return fmt.Errorf("existing contract boundary source is not contract/interface evidence: %w", domain.ErrValidation)
			}
			projectInBoundary := evidence.ProjectID == boundary.Owner.ProjectID
			for _, consumer := range boundary.Consumers {
				projectInBoundary = projectInBoundary || evidence.ProjectID == consumer.ProjectID
			}
			if !projectInBoundary {
				return fmt.Errorf("contract evidence belongs to an unrelated project: %w", domain.ErrValidation)
			}
			if evidence.Kind == "contract" && !strings.HasPrefix(evidence.Path, ".ai/contracts/") {
				return fmt.Errorf("contract evidence path is outside local contracts: %w", domain.ErrValidation)
			}
			if evidence.Kind == "source" && !sourceSupportsPlannedBoundary(evidence, boundary, v.ControlPlane, profileByProject) {
				return fmt.Errorf("source evidence is not a compatible interface candidate: %w", domain.ErrValidation)
			}
			if evidence.Path != boundary.TargetPath {
				return fmt.Errorf("existing contract target path differs from its source evidence: %w", domain.ErrValidation)
			}
		} else if boundary.SourceEvidenceID != "" {
			return fmt.Errorf("unverified contract boundary cannot cite a source baseline: %w", domain.ErrValidation)
		}
		if _, duplicate := boundaryKinds[boundary.Kind]; duplicate {
			return fmt.Errorf("contract plan repeats boundary %q: %w", boundary.Kind, domain.ErrConflict)
		}
		boundaryKinds[boundary.Kind] = struct{}{}
		matchedCandidate := false
		for _, candidate := range routing.SharedBoundaryCandidates {
			if candidate.Kind != boundary.Kind {
				continue
			}
			refs := append([]domain.RouteReference(nil), boundary.Consumers...)
			if ownerSelected {
				refs = append(refs, boundary.Owner)
			}
			matchedCandidate = sameReferenceSlice(refs, candidate.Routes)
			break
		}
		if !matchedCandidate {
			return fmt.Errorf("planned contract boundary is not supported by a shared-boundary candidate: %w", domain.ErrValidation)
		}
	}

	if len(plan.ContractOwnerRoutes) > 0 {
		var owners []domain.RouteReference
		for _, boundary := range plan.Boundaries {
			owners = append(owners, boundary.Owner)
		}
		if !sameReferenceSlice(plan.ContractOwnerRoutes, uniqueRouteReferences(owners)) {
			return fmt.Errorf("contract owner routes differ from planned boundary owners: %w", domain.ErrValidation)
		}
	}
	if plan.State == domain.ContractPlanFreezeNeeded && len(boundaryKinds) != len(routing.SharedBoundaryCandidates) {
		return fmt.Errorf("FREEZE_REQUIRED plan must describe each shared-boundary candidate: %w", domain.ErrValidation)
	}
	for _, change := range plan.ChangesRequired {
		if strings.TrimSpace(change.Kind) == "" || strings.TrimSpace(change.Reason) == "" ||
			!routeRefExists(change.Owner, routeRefs) || !routeRefExists(change.Consumer, routeRefs) {
			return fmt.Errorf("contract change references impossible owner or consumer: %w", domain.ErrValidation)
		}
		if _, ok := boundaryKinds[change.Kind]; !ok {
			return fmt.Errorf("contract change references an unplanned boundary: %w", domain.ErrValidation)
		}
	}
	if plan.State != domain.ContractPlanFreezeNeeded && len(plan.IndependentAfterFreeze) != 0 {
		return fmt.Errorf("after-freeze parallelization is invalid without a freeze requirement: %w", domain.ErrValidation)
	}
	for _, group := range plan.IndependentAfterFreeze {
		if len(group) == 0 || !allRouteReferencesExist(group, routeRefs) {
			return fmt.Errorf("parallelization implication references unrouted routes: %w", domain.ErrValidation)
		}
	}
	return nil
}

func validRoutingConfidence(value string) bool {
	return value == "high" || value == "medium" || value == "low"
}

func validRoutingClassification(value string) bool {
	switch value {
	case "persistence", "business-process", "domain-invariant", "http-transport", "external-client",
		"messaging", "frontend-ui", "frontend-api", "frontend-usecase", "maintenance":
		return true
	default:
		return false
	}
}

func validEvidenceKind(value string) bool {
	switch value {
	case "repository_instructions", "service_metadata", "architecture_metadata", "contract",
		"commands", "test_manifest", "stack_manifest", "source", "test":
		return true
	default:
		return false
	}
}

func validEvidenceChecksum(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validEvidencePath(value string) bool {
	return value != "" && !path.IsAbs(value) && path.Clean(value) == value &&
		value != "." && value != ".." && !strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "\\") && !strings.ContainsRune(value, '\x00') &&
		!strings.HasPrefix(path.Base(value), ".env")
}

func profileHasVariant(profile agentcontrol.Profile, variant string) bool {
	for _, candidate := range profile.Variants {
		if candidate.ID == variant {
			return true
		}
	}
	return false
}

func variantEvidenceExists(resolution domain.ArchitectureProfileResolution, paths map[string]domain.RoutingEvidence) bool {
	prefixes := []string{"src/app/" + resolution.Variant + "/"}
	if resolution.Variant == "student" {
		prefixes = append(prefixes, "src/app/(student)/")
	}
	if resolution.Variant == "admin" {
		prefixes = append(prefixes, "src/app/(admin)/")
	}
	for key := range paths {
		_, evidencePath, _ := strings.Cut(key, "\x00")
		for _, prefix := range prefixes {
			if strings.HasPrefix(evidencePath, prefix) {
				return true
			}
		}
	}
	return false
}

func allEvidenceIDsExist(ids []string, evidence map[string]domain.RoutingEvidence, projectID string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		item, ok := evidence[id]
		if !ok || item.ProjectID != projectID {
			return false
		}
	}
	return true
}

func allUniqueNonEmpty(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func routeKey(projectID, routeID string) string {
	return projectID + "\x00" + routeID
}

func anyUnresolvedProfile(profiles map[string]domain.ArchitectureProfileResolution) bool {
	for _, profile := range profiles {
		if profile.Status != domain.ProfileResolutionResolved {
			return true
		}
	}
	return false
}

func projectHasRoute(projectID string, routes map[string]domain.RoutedTarget) bool {
	for _, route := range routes {
		if route.ProjectID == projectID {
			return true
		}
	}
	return false
}

func routeRefExists(ref domain.RouteReference, routes map[string]domain.RouteReference) bool {
	_, ok := routes[routeKey(ref.ProjectID, ref.RouteID)]
	return ok
}

func allRouteReferencesExist(refs []domain.RouteReference, routes map[string]domain.RouteReference) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if !routeRefExists(ref, routes) {
			return false
		}
	}
	return true
}

func sameRouteReferenceSet(refs []domain.RouteReference, routes map[string]domain.RouteReference) bool {
	if len(refs) != len(routes) {
		return false
	}
	seen := map[string]struct{}{}
	for _, ref := range refs {
		key := routeKey(ref.ProjectID, ref.RouteID)
		if _, ok := routes[key]; !ok {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func sameReferenceSlice(left, right []domain.RouteReference) bool {
	return sameRouteReferenceSet(left, referenceMap(right)) && sameRouteReferenceSet(right, referenceMap(left))
}

func sourceSupportsPlannedBoundary(
	evidence domain.RoutingEvidence,
	boundary domain.PlannedContractBoundary,
	catalog agentcontrol.Catalog,
	profiles map[string]domain.ArchitectureProfileResolution,
) bool {
	refs := append([]domain.RouteReference{boundary.Owner}, boundary.Consumers...)
	for _, ref := range refs {
		if ref.ProjectID != evidence.ProjectID {
			continue
		}
		resolution, ok := profiles[ref.ProjectID]
		if !ok || resolution.Status != domain.ProfileResolutionResolved {
			continue
		}
		route := findProfileRoute(catalog.Profiles[resolution.ProfileID], ref.RouteID)
		if route != nil && evidenceMatchesRoute(evidence.Path, route.InterfaceCandidates) &&
			containsString(evidence.BoundaryKinds, boundary.Kind) {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func referenceMap(refs []domain.RouteReference) map[string]domain.RouteReference {
	result := make(map[string]domain.RouteReference, len(refs))
	for _, ref := range refs {
		result[routeKey(ref.ProjectID, ref.RouteID)] = ref
	}
	return result
}

func allNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func validWriteScopes(scopes []string) bool {
	for _, scope := range scopes {
		scope = strings.ReplaceAll(strings.TrimSpace(scope), "\\", "/")
		if scope == "" || path.IsAbs(scope) || scope == ".." || strings.HasPrefix(scope, "../") ||
			strings.Contains(scope, "/../") || strings.HasPrefix(scope, "~/") || strings.ContainsRune(scope, '\x00') ||
			(len(scope) >= 2 && scope[1] == ':') {
			return false
		}
	}
	return true
}

func (v Validator) maxParallel() int {
	if v.MaxParallelTasks < 1 {
		return 1
	}
	if v.MaxParallelTasks > 3 {
		return 3
	}
	return v.MaxParallelTasks
}

func (v Validator) maxDepth() int {
	if v.MaxRequiredTaskDepth < 1 {
		return 1
	}
	return v.MaxRequiredTaskDepth
}

func validRisk(value domain.RiskLevel) bool {
	switch value {
	case domain.RiskLevelLow, domain.RiskLevelMedium, domain.RiskLevelHigh, domain.RiskLevelCritical:
		return true
	default:
		return false
	}
}

func validModelProfile(value string) bool {
	switch value {
	case config.ModelProfileFast, config.ModelProfileStandard, config.ModelProfileDeep, config.ModelProfileReview:
		return true
	default:
		return false
	}
}

var _ repository.PlanValidator = Validator{}

func containsContractOwner(refs []domain.RouteReference, target domain.RouteReference) bool {
	for _, ref := range refs {
		if ref == target {
			return true
		}
	}
	return false
}
