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
	for _, item := range bundle.WorkItems {
		if item.Kind != domain.WorkItemIssue || item.Status == domain.WorkItemPublished || item.Status == domain.WorkItemClosed {
			continue
		}
		if item.Status != domain.WorkItemProposed || item.AgentRole != domain.AgentRunIssueManager ||
			item.PlanFingerprint != bundle.Plan.Fingerprint {
			return nil, fmt.Errorf("issue proposal is stale or invalid: %w", domain.ErrConflict)
		}
		project, err := p.Projects.Get(ctx, item.ProjectID)
		if err != nil {
			return nil, err
		}
		publication, err := p.Gateway.PublishIssue(ctx, project, item)
		if err != nil {
			return nil, err
		}
		if p.Gateway.DryRun() {
			continue
		}
		if _, err := p.Items.MarkWorkItemPublished(ctx, item.ID, publication); err != nil {
			return nil, err
		}
	}
	return p.Items.ListPlanWorkItems(ctx, bundle.Plan.ID)
}
