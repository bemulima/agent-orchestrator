package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type DistributionRepository interface {
	Inspect(context.Context, Catalog) (RepositorySnapshot, error)
	Apply(context.Context, RepositorySnapshot, Proposal, string) (ApplyResult, error)
}

type DistributionService struct {
	Repository DistributionRepository
}

func (service DistributionService) Plan(ctx context.Context, catalog Catalog) (Proposal, error) {
	if service.Repository == nil {
		return Proposal{}, errors.New("distribution repository is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Proposal{}, err
	}
	snapshot, err := service.Repository.Inspect(ctx, catalog)
	if err != nil {
		return Proposal{}, err
	}
	return BuildDistributionProposal(catalog, snapshot)
}

// Apply binds owner approval to the exact proposal fingerprint. Repository
// adapters must revalidate the base revision and target files immediately
// before writing.
func (service DistributionService) Apply(ctx context.Context, catalog Catalog, approvedFingerprint string) (ApplyResult, error) {
	if service.Repository == nil {
		return ApplyResult{}, errors.New("distribution repository is not configured")
	}
	if strings.TrimSpace(approvedFingerprint) == "" {
		return ApplyResult{}, ErrApprovalRequired
	}
	proposal, err := service.Plan(ctx, catalog)
	if err != nil {
		return ApplyResult{}, err
	}
	if proposal.Fingerprint != approvedFingerprint {
		return ApplyResult{}, fmt.Errorf("proposal changed since approval: %w", ErrApprovalRequired)
	}
	for _, change := range proposal.Changes {
		if change.State == AssetStateConflicting || change.State == AssetStateLocallyModified {
			return ApplyResult{}, fmt.Errorf("%w: %s is %s", ErrApplyConflict, change.AssetID, change.State)
		}
	}
	snapshot, err := service.Repository.Inspect(ctx, catalog)
	if err != nil {
		return ApplyResult{}, err
	}
	current, err := BuildDistributionProposal(catalog, snapshot)
	if err != nil {
		return ApplyResult{}, err
	}
	if current.Fingerprint != approvedFingerprint {
		return ApplyResult{}, fmt.Errorf("proposal changed since approval: %w", ErrApprovalRequired)
	}
	return service.Repository.Apply(ctx, snapshot, current, approvedFingerprint)
}
