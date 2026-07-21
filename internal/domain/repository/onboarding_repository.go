package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type OnboardingRepository interface {
	CreateOrGet(context.Context, domain.OnboardingRun) (domain.OnboardingRun, error)
	Get(context.Context, string) (domain.OnboardingRun, error)
	Approve(context.Context, string, string, string) (domain.OnboardingRun, error)
	Reject(context.Context, string, string, string) (domain.OnboardingRun, error)
	BeginApply(context.Context, string) (domain.OnboardingRun, error)
	RecordPublication(context.Context, string, domain.OnboardingPublication) (domain.OnboardingRun, error)
	CompleteApply(context.Context, string, domain.OnboardingApplyResult) (domain.OnboardingRun, error)
	FailApply(context.Context, string, string) error
}

type OnboardingGenerator interface {
	Generate(context.Context, domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) (domain.OnboardingProposal, string, error)
}

type OnboardingWorktree interface {
	DryRun(context.Context, domain.Project, domain.OnboardingRun) (domain.OnboardingApplyResult, error)
	Apply(context.Context, domain.Project, domain.OnboardingRun) (domain.OnboardingApplyResult, error)
}

type OnboardingPublisher interface {
	Publish(context.Context, domain.Project, domain.OnboardingRun, domain.OnboardingApplyResult) (domain.OnboardingPublication, error)
}
