package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type DryRunAdapter struct {
	BaseURL string
	Token   string
}

func (d DryRunAdapter) Configured() bool {
	return strings.TrimSpace(d.BaseURL) != "" && strings.TrimSpace(d.Token) != ""
}

func (DryRunAdapter) DryRun() bool { return true }

func (d DryRunAdapter) ResolveProject(_ context.Context, reference string) (domain.GitLabProject, error) {
	baseURL, err := validatedBaseURL(d.BaseURL, d.Token)
	if err != nil {
		return domain.GitLabProject{}, err
	}
	reference = strings.Trim(strings.TrimSpace(reference), "/")
	if reference == "" {
		return domain.GitLabProject{}, fmt.Errorf("GitLab project reference is required: %w", domain.ErrValidation)
	}
	id, parseErr := strconv.ParseInt(reference, 10, 64)
	if parseErr != nil || id <= 0 {
		id = stableDryRunID(reference)
	}
	return domain.GitLabProject{ID: id, Reference: strconv.FormatInt(id, 10), WebURL: dryRunURL(baseURL, reference)}, nil
}

func (d DryRunAdapter) ResolveConnectedProject(ctx context.Context, project domain.Project) (domain.GitLabProject, error) {
	baseURL, err := validatedBaseURL(d.BaseURL, d.Token)
	if err != nil {
		return domain.GitLabProject{}, err
	}
	reference, matches, err := gitLabProjectReference(project, baseURL)
	if err != nil {
		return domain.GitLabProject{}, err
	}
	if !matches {
		return domain.GitLabProject{}, fmt.Errorf("project %q is not hosted by configured GitLab: %w", project.Name, domain.ErrValidation)
	}
	return d.ResolveProject(ctx, reference)
}

func (d DryRunAdapter) EnsureIssue(_ context.Context, spec domain.GitLabIssueSpec) (domain.GitLabIssue, error) {
	if err := validateIssueSpec(spec); err != nil {
		return domain.GitLabIssue{}, err
	}
	baseURL, _ := validatedBaseURL(d.BaseURL, d.Token)
	iid := stableDryRunID(spec.IdempotencyKey)
	return domain.GitLabIssue{
		ProjectID: spec.Project.ID, IID: iid, Title: spec.Title, State: spec.State,
		WebURL: dryRunURL(baseURL, strconv.FormatInt(spec.Project.ID, 10)+"/-/issues/"+strconv.FormatInt(iid, 10)), DryRun: true,
	}, nil
}

func (DryRunAdapter) EnsureComment(context.Context, domain.GitLabIssue, string, string) error {
	return nil
}

func (DryRunAdapter) EnsureIssueLink(context.Context, domain.GitLabIssue, domain.GitLabIssue) error {
	return nil
}

func (DryRunAdapter) PushBranch(context.Context, domain.Project, string, string) error { return nil }

func (d DryRunAdapter) EnsureMergeRequest(
	_ context.Context,
	spec domain.GitLabMergeRequestSpec,
) (domain.GitLabMergeRequest, error) {
	if err := validateMergeRequestSpec(spec); err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	baseURL, _ := validatedBaseURL(d.BaseURL, d.Token)
	iid := stableDryRunID(spec.IdempotencyKey)
	return domain.GitLabMergeRequest{
		ProjectID: spec.Project.ID, IID: iid, State: "opened",
		SourceBranch: spec.SourceBranch, TargetBranch: spec.TargetBranch,
		WebURL: dryRunURL(baseURL, strconv.FormatInt(spec.Project.ID, 10)+"/-/merge_requests/"+strconv.FormatInt(iid, 10)), DryRun: true,
	}, nil
}

func dryRunURL(baseURL *url.URL, path string) string {
	copyURL := *baseURL
	copyURL.Path = strings.TrimRight(copyURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	copyURL.RawPath = ""
	return copyURL.String()
}

var _ repository.GitLabGateway = DryRunAdapter{}
