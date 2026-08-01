package workitem

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type IssuePublisher struct {
	Plans    planGetter
	Projects repository.ProjectRepository
	Items    repository.WorkItemRepository
	Gateway  repository.WorkItemGateway
}

func (p IssuePublisher) Publish(ctx context.Context, planID string) ([]domain.WorkItem, error) {
	if p.Plans == nil || p.Projects == nil || p.Items == nil || p.Gateway == nil || !p.Gateway.Configured() {
		return nil, fmt.Errorf("issue publisher is not configured: %w", domain.ErrInvalidStatus)
	}
	bundle, err := p.Plans.GetPlan(ctx, strings.TrimSpace(planID))
	if err != nil {
		return nil, err
	}
	if bundle.Plan.Status != domain.PlanStatusApproved || bundle.Approval == nil ||
		bundle.Approval.Status != string(domain.ApprovalStatusApproved) ||
		bundle.Plan.ApprovedFingerprint == nil || *bundle.Plan.ApprovedFingerprint != bundle.Plan.Fingerprint {
		return nil, fmt.Errorf("the exact plan version must be approved before issue publication: %w", domain.ErrApprovalNeeded)
	}
	if err := validateIssuePublicationBundle(bundle); err != nil {
		return nil, err
	}
	published := 0
	for _, item := range bundle.WorkItems {
		if item.Kind == domain.WorkItemIssue && (item.Status == domain.WorkItemPublished || item.Status == domain.WorkItemClosed) {
			published++
		}
	}
	for _, item := range bundle.WorkItems {
		if item.Kind != domain.WorkItemIssue || item.Status == domain.WorkItemPublished || item.Status == domain.WorkItemClosed {
			continue
		}
		project, err := p.Projects.Get(ctx, item.ProjectID)
		if err != nil {
			return nil, err
		}
		publication, err := p.Gateway.PublishIssue(ctx, project, item)
		if err != nil {
			return nil, fmt.Errorf("publish issue after %d of %d remaining proposals: %w", published, len(bundle.Tasks), err)
		}
		if p.Gateway.DryRun() {
			continue
		}
		if _, err := p.Items.MarkWorkItemPublished(ctx, item.ID, publication); err != nil {
			return nil, fmt.Errorf("save published issue after %d of %d proposals: %w", published, len(bundle.Tasks), err)
		}
		published++
	}
	return p.Items.ListPlanWorkItems(ctx, bundle.Plan.ID)
}

func validateIssuePublicationBundle(bundle domain.PlanBundle) error {
	proposals := make(map[string]domain.WorkItem, len(bundle.Tasks))
	for _, item := range bundle.WorkItems {
		if item.Kind != domain.WorkItemIssue || item.Status == domain.WorkItemCancelled {
			continue
		}
		if item.TaskID == nil || strings.TrimSpace(*item.TaskID) == "" {
			return fmt.Errorf("every issue proposal must belong to one plan task: %w", domain.ErrConflict)
		}
		if _, exists := proposals[*item.TaskID]; exists {
			return fmt.Errorf("plan task %s has more than one active issue proposal: %w", *item.TaskID, domain.ErrConflict)
		}
		proposals[*item.TaskID] = item
	}
	if len(bundle.Tasks) == 0 || len(proposals) != len(bundle.Tasks) {
		return fmt.Errorf("every plan task needs one approved issue proposal before publication: %w", domain.ErrApprovalNeeded)
	}
	for _, task := range bundle.Tasks {
		item, exists := proposals[task.ID]
		if !exists {
			return fmt.Errorf("task %s has no approved issue proposal: %w", task.ID, domain.ErrApprovalNeeded)
		}
		if item.Status == domain.WorkItemProposed && (item.AgentRole != domain.AgentRunIssueManager ||
			item.PlanFingerprint != bundle.Plan.Fingerprint) {
			return fmt.Errorf("issue proposal is stale or invalid: %w", domain.ErrConflict)
		}
	}
	return nil
}
