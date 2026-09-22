package architecturetarget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// IntegrationClassification records the intentionally small S7 outcome.  An
// approved TARGET affecting one existing repository becomes one issue-backed
// task; a cross-repository TARGET becomes a project plan with a verified DAG.
// Neither outcome authorizes publication of an external issue.
type IntegrationClassification string

const (
	IntegrationClassificationIssue       IntegrationClassification = "issue"
	IntegrationClassificationProjectPlan IntegrationClassification = "project_plan"
)

// IntegrationInput is the immutable approval hand-off from S6 to S7.  The
// caller must bind the request to the exact reviewed fingerprint rather than
// relying on a proposal identifier that could later refer to another version.
type IntegrationInput struct {
	Proposal            domain.ArchitectureTargetProposal
	ExpectedFingerprint string
	Source              domain.CommandSource
	SourceUserID        *string
}

// CommandRequest is deliberately transport- and repository-agnostic.  The
// composition root adapts it to planning.CreateCommand; this package does not
// own planning persistence or execution.
type CommandRequest struct {
	Source         domain.CommandSource
	SourceUserID   *string
	Text           string
	IdempotencyKey string
}

// CommandCreator creates the planning command for an approved TARGET.
type CommandCreator interface {
	CreateCommand(context.Context, CommandRequest) (domain.Command, error)
}

// PlanCreator delegates deterministic/agent planning to the existing planning
// pipeline.  It is intentionally incapable of starting a plan.
type PlanCreator interface {
	CreatePlan(context.Context, string, domain.PlanRequest) (domain.PlanBundle, error)
}

// IssueDraftPreparer prepares local, owner-reviewable work-item proposals. It
// intentionally exposes no publication operation or external gateway.
type IssueDraftPreparer interface {
	PrepareIssueDrafts(context.Context, string) ([]domain.WorkItem, error)
}

// IntegrationResult is suitable for an HTTP adapter to render, while keeping
// the underlying plan and proposed work items independently reviewable.
type IntegrationResult struct {
	Classification     IntegrationClassification
	AffectedProjectIDs []string
	Command            domain.Command
	Plan               domain.PlanBundle
	IssueDrafts        []domain.WorkItem
}

// IntegrateApprovedTarget connects a reviewed, immutable TARGET to the
// existing command -> plan -> issue-draft workflow. It never starts execution
// and never publishes a work item.
type IntegrateApprovedTarget struct {
	Commands CommandCreator
	Plans    PlanCreator
	Issues   IssueDraftPreparer
}

func (uc IntegrateApprovedTarget) Handle(ctx context.Context, input IntegrationInput) (IntegrationResult, error) {
	proposal := input.Proposal
	if err := validApprovedIntegrationProposal(proposal, input.ExpectedFingerprint); err != nil {
		return IntegrationResult{}, err
	}
	if uc.Commands == nil || uc.Plans == nil || uc.Issues == nil {
		return IntegrationResult{}, fmt.Errorf("architecture TARGET integration dependencies are not configured: %w", domain.ErrValidation)
	}

	projects, err := affectedExistingProjects(proposal)
	if err != nil {
		return IntegrationResult{}, err
	}
	classification := IntegrationClassificationProjectPlan
	if len(projects) == 1 {
		classification = IntegrationClassificationIssue
	}

	source := input.Source
	if source == "" {
		source = domain.CommandSourceAPI
	}
	command, err := uc.Commands.CreateCommand(ctx, CommandRequest{
		Source:         source,
		SourceUserID:   input.SourceUserID,
		Text:           integrationCommandText(proposal, projects),
		IdempotencyKey: integrationIdempotencyKey(proposal),
	})
	if err != nil {
		return IntegrationResult{}, fmt.Errorf("create command for approved architecture TARGET: %w", err)
	}
	if strings.TrimSpace(command.ID) == "" {
		return IntegrationResult{}, fmt.Errorf("command creator returned an empty command id: %w", domain.ErrConflict)
	}

	plan, err := uc.Plans.CreatePlan(ctx, command.ID, domain.PlanRequest{RequestedProjectIDs: append([]string(nil), projects...)})
	if err != nil {
		return IntegrationResult{}, fmt.Errorf("create plan for approved architecture TARGET: %w", err)
	}
	if err := validateIntegrationPlan(plan, projects, classification); err != nil {
		return IntegrationResult{}, err
	}

	drafts, err := uc.Issues.PrepareIssueDrafts(ctx, plan.Plan.ID)
	if err != nil {
		return IntegrationResult{}, fmt.Errorf("prepare issue drafts for approved architecture TARGET: %w", err)
	}
	if err := validatePreparedDrafts(drafts, plan.Tasks); err != nil {
		return IntegrationResult{}, err
	}
	return IntegrationResult{
		Classification:     classification,
		AffectedProjectIDs: projects,
		Command:            command,
		Plan:               plan,
		IssueDrafts:        append([]domain.WorkItem(nil), drafts...),
	}, nil
}

func validApprovedIntegrationProposal(proposal domain.ArchitectureTargetProposal, expectedFingerprint string) error {
	expectedFingerprint = strings.TrimSpace(expectedFingerprint)
	if proposal.Status != domain.ArchitectureTargetStatusApproved {
		return fmt.Errorf("only an approved architecture TARGET can be integrated: %w", domain.ErrInvalidStatus)
	}
	if proposal.Fingerprint == "" || expectedFingerprint == "" || proposal.Fingerprint != expectedFingerprint {
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
	for _, change := range proposal.Changes {
		if len(change.UnresolvedAreas) != 0 || containsProposedID(change.AffectedServiceIDs) || containsProposedID(change.AffectedOperationIDs) {
			return fmt.Errorf("approved architecture TARGET contains unresolved or proposed-only implementation scope: %w", domain.ErrConflict)
		}
	}
	for _, entry := range proposal.Diff.Entries {
		if len(entry.UnresolvedAreas) != 0 || containsProposedID(entry.AffectedServiceIDs) || containsProposedID(entry.AffectedOperationIDs) {
			return fmt.Errorf("approved architecture TARGET diff contains unresolved or proposed-only implementation scope: %w", domain.ErrConflict)
		}
	}
	for _, impact := range proposal.Impact.Entries {
		if impact.Kind == domain.ArchitectureTargetImpactUnresolved || strings.HasPrefix(impact.ServiceID, proposedIDPrefix) || strings.HasPrefix(impact.OperationID, proposedIDPrefix) {
			return fmt.Errorf("approved architecture TARGET impact is unresolved or proposed-only: %w", domain.ErrConflict)
		}
	}
	return nil
}

func containsProposedID(values []string) bool {
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), proposedIDPrefix) {
			return true
		}
	}
	return false
}

// affectedExistingProjects is intentionally derived from both immutable S5
// projections. Diff names direct implementation ownership; impact preserves
// upstream/downstream owners that must participate in a cross-service plan.
func affectedExistingProjects(proposal domain.ArchitectureTargetProposal) ([]string, error) {
	projects := make(map[string]struct{})
	for _, entry := range proposal.Diff.Entries {
		for _, projectID := range entry.AffectedServiceIDs {
			projectID = strings.TrimSpace(projectID)
			if projectID == "" || strings.HasPrefix(projectID, proposedIDPrefix) {
				return nil, fmt.Errorf("TARGET diff does not resolve an existing project id: %w", domain.ErrConflict)
			}
			projects[projectID] = struct{}{}
		}
	}
	for _, entry := range proposal.Impact.Entries {
		projectID := strings.TrimSpace(entry.ServiceID)
		if projectID == "" || strings.HasPrefix(projectID, proposedIDPrefix) {
			return nil, fmt.Errorf("TARGET impact does not resolve an existing project id: %w", domain.ErrConflict)
		}
		projects[projectID] = struct{}{}
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("approved architecture TARGET has no existing affected projects: %w", domain.ErrValidation)
	}
	result := make([]string, 0, len(projects))
	for projectID := range projects {
		result = append(result, projectID)
	}
	sort.Strings(result)
	return result, nil
}

func integrationIdempotencyKey(proposal domain.ArchitectureTargetProposal) string {
	digest := sha256.Sum256([]byte(proposal.ID + "\x00" + proposal.Fingerprint))
	return "architecture-target-" + hex.EncodeToString(digest[:])
}

func integrationCommandText(proposal domain.ArchitectureTargetProposal, projects []string) string {
	diff := append([]domain.ArchitectureTargetDiffEntry(nil), proposal.Diff.Entries...)
	sort.Slice(diff, func(left, right int) bool { return diff[left].ChangeID < diff[right].ChangeID })
	impact := append([]domain.ArchitectureTargetImpactEntry(nil), proposal.Impact.Entries...)
	sort.Slice(impact, func(left, right int) bool {
		leftKey := string(impact[left].Kind) + "\x00" + impact[left].ServiceID + "\x00" + impact[left].OperationID + "\x00" + impact[left].ChangeID
		rightKey := string(impact[right].Kind) + "\x00" + impact[right].ServiceID + "\x00" + impact[right].OperationID + "\x00" + impact[right].ChangeID
		return leftKey < rightKey
	})

	var diffLines, operationLines, contractLines, acceptance, evidence []string
	for _, entry := range diff {
		diffLines = append(diffLines, fmt.Sprintf("- %s: %s %s — %s", entry.ChangeID, entry.Action, entry.Kind, entry.Summary))
		if len(entry.AffectedOperationIDs) > 0 {
			operationLines = append(operationLines, fmt.Sprintf("- %s: %s", entry.ChangeID, strings.Join(entry.AffectedOperationIDs, ", ")))
		}
		if entry.Kind == domain.ArchitectureTargetChangeKindContract || entry.Kind == domain.ArchitectureTargetChangeKindEvent || entry.Kind == domain.ArchitectureTargetChangeKindDependency {
			contractLines = append(contractLines, fmt.Sprintf("- %s: %s", entry.ChangeID, entry.DesiredResult.Summary))
		}
		for _, criterion := range entry.DesiredResult.SuccessCriteria {
			acceptance = append(acceptance, "- "+criterion)
		}
		for _, item := range entry.Evidence {
			evidence = append(evidence, "- "+evidenceText(item))
		}
	}
	if len(operationLines) == 0 {
		operationLines = []string{"- No operation identities are directly changed."}
	}
	if len(contractLines) == 0 {
		contractLines = []string{"- No contract or dependency delta is directly requested."}
	}
	if len(acceptance) == 0 {
		acceptance = []string{"- Preserve the approved TARGET result."}
	}
	if len(evidence) == 0 {
		evidence = []string{"- Evidence is retained in the approved TARGET fingerprint."}
	}
	order := make([]string, 0, len(impact))
	for _, entry := range impact {
		identity := entry.ServiceID
		if entry.OperationID != "" {
			identity += "/" + entry.OperationID
		}
		order = append(order, fmt.Sprintf("- %s: %s (%s)", entry.Kind, identity, entry.Explanation))
	}
	return strings.Join([]string{
		"## Objective", "Implement approved architecture TARGET " + proposal.ID + " at fingerprint " + proposal.Fingerprint + ".",
		"## Approved diff", strings.Join(diffLines, "\n"),
		"## Affected services", "- " + strings.Join(projects, "\n- "),
		"## Affected operations", strings.Join(operationLines, "\n"),
		"## Contracts and dependencies", strings.Join(contractLines, "\n"),
		"## Implementation order", strings.Join(order, "\n"),
		"## Acceptance criteria", strings.Join(uniqueSorted(acceptance), "\n"),
		"## Tests", "- Execute the affected repository test suites.\n- Validate changed architecture manifests and generated Mermaid diagrams.",
		"## Evidence", strings.Join(uniqueSorted(evidence), "\n"),
		"## Verification", "- Run the platform-wide architecture completeness audit.\n- Verify CURRENT versus the approved TARGET fingerprint and all affected service, operation, contract, and dependency records.",
	}, "\n\n")
}

func evidenceText(value domain.ArchitectureEvidence) string {
	parts := []string{value.SourcePath}
	if value.Symbol != "" {
		parts = append(parts, value.Symbol)
	}
	if value.StartLine > 0 {
		line := fmt.Sprintf("line %d", value.StartLine)
		if value.EndLine > value.StartLine {
			line += fmt.Sprintf("-%d", value.EndLine)
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, ":")
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateIntegrationPlan(plan domain.PlanBundle, expectedProjects []string, classification IntegrationClassification) error {
	if strings.TrimSpace(plan.Plan.ID) == "" {
		return fmt.Errorf("plan creator returned an empty plan id: %w", domain.ErrConflict)
	}
	if len(plan.Tasks) != len(expectedProjects) {
		return fmt.Errorf("architecture TARGET plan must contain exactly one task per affected project: %w", domain.ErrConflict)
	}
	expected := make(map[string]struct{}, len(expectedProjects))
	for _, projectID := range expectedProjects {
		expected[projectID] = struct{}{}
	}
	tasks := make(map[string]domain.Task, len(plan.Tasks))
	seenProjects := make(map[string]struct{}, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.ProjectID) == "" {
			return fmt.Errorf("architecture TARGET plan has task without immutable task or project identity: %w", domain.ErrConflict)
		}
		if _, allowed := expected[task.ProjectID]; !allowed {
			return fmt.Errorf("architecture TARGET plan includes project %q outside approved impact: %w", task.ProjectID, domain.ErrConflict)
		}
		if _, duplicate := tasks[task.ID]; duplicate {
			return fmt.Errorf("architecture TARGET plan repeats task %q: %w", task.ID, domain.ErrConflict)
		}
		if _, duplicate := seenProjects[task.ProjectID]; duplicate {
			return fmt.Errorf("architecture TARGET plan repeats project %q: %w", task.ProjectID, domain.ErrConflict)
		}
		tasks[task.ID], seenProjects[task.ProjectID] = task, struct{}{}
	}
	if len(seenProjects) != len(expected) {
		return fmt.Errorf("architecture TARGET plan does not cover every approved project: %w", domain.ErrConflict)
	}
	if classification == IntegrationClassificationIssue && len(plan.Dependencies) != 0 {
		return fmt.Errorf("single-project architecture TARGET issue cannot have task dependencies: %w", domain.ErrConflict)
	}
	if err := validateTaskDAG(tasks, plan.Dependencies); err != nil {
		return err
	}
	return nil
}

func validateTaskDAG(tasks map[string]domain.Task, dependencies []domain.TaskDependency) error {
	inDegree := make(map[string]int, len(tasks))
	next := make(map[string][]string, len(tasks))
	for id := range tasks {
		inDegree[id] = 0
	}
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if _, taskExists := tasks[dependency.TaskID]; !taskExists {
			return fmt.Errorf("architecture TARGET plan dependency names an unknown task: %w", domain.ErrConflict)
		}
		if _, prerequisiteExists := tasks[dependency.DependsOnTaskID]; !prerequisiteExists || dependency.TaskID == dependency.DependsOnTaskID {
			return fmt.Errorf("architecture TARGET plan dependency is invalid: %w", domain.ErrConflict)
		}
		key := dependency.TaskID + "\x00" + dependency.DependsOnTaskID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("architecture TARGET plan repeats a task dependency: %w", domain.ErrConflict)
		}
		seen[key] = struct{}{}
		inDegree[dependency.TaskID]++
		next[dependency.DependsOnTaskID] = append(next[dependency.DependsOnTaskID], dependency.TaskID)
	}
	ready := make([]string, 0, len(tasks))
	for id, degree := range inDegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	visited := 0
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		visited++
		for _, dependent := range next[id] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if visited != len(tasks) {
		return fmt.Errorf("architecture TARGET plan task dependencies contain a cycle: %w", domain.ErrConflict)
	}
	return nil
}

func validatePreparedDrafts(drafts []domain.WorkItem, tasks []domain.Task) error {
	if len(drafts) != len(tasks) {
		return fmt.Errorf("architecture TARGET requires exactly one prepared issue draft per task: %w", domain.ErrConflict)
	}
	tasksByID := make(map[string]domain.Task, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	seen := make(map[string]struct{}, len(drafts))
	for _, draft := range drafts {
		if draft.Kind != domain.WorkItemIssue || draft.Status != domain.WorkItemProposed || draft.TaskID == nil {
			return fmt.Errorf("architecture TARGET issue integration permits proposed issue drafts only: %w", domain.ErrConflict)
		}
		task, exists := tasksByID[*draft.TaskID]
		if !exists || task.ProjectID != draft.ProjectID {
			return fmt.Errorf("architecture TARGET issue draft is not bound to its planned task/project: %w", domain.ErrConflict)
		}
		if _, duplicate := seen[*draft.TaskID]; duplicate {
			return fmt.Errorf("architecture TARGET has duplicate issue drafts for one task: %w", domain.ErrConflict)
		}
		seen[*draft.TaskID] = struct{}{}
	}
	return nil
}
