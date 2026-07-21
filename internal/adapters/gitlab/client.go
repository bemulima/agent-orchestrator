package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	maxGitLabResponseBytes = 1 << 20
	maxGitLabTextBytes     = 128 << 10
)

var taskBranchPattern = regexp.MustCompile(`^ai/task-[a-z0-9._-]+$`)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func (c Client) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Token) != ""
}

func (Client) DryRun() bool { return false }

func (c Client) ResolveProject(ctx context.Context, reference string) (domain.GitLabProject, error) {
	baseURL, err := validatedBaseURL(c.BaseURL, c.Token)
	if err != nil {
		return domain.GitLabProject{}, err
	}
	reference = strings.Trim(strings.TrimSpace(reference), "/")
	if reference == "" || len(reference) > 512 || strings.Contains(reference, "..") {
		return domain.GitLabProject{}, fmt.Errorf("invalid GitLab project reference: %w", domain.ErrValidation)
	}
	endpoint := projectEndpoint(baseURL, reference)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.GitLabProject{}, fmt.Errorf("build GitLab project request: %w", err)
	}
	var response projectResponse
	if err := c.doJSON(request, &response); err != nil {
		return domain.GitLabProject{}, err
	}
	if response.ID <= 0 || !safeGitLabWebURL(baseURL, response.WebURL) {
		return domain.GitLabProject{}, fmt.Errorf("GitLab returned an incomplete project: %w", domain.ErrConflict)
	}
	return domain.GitLabProject{ID: response.ID, Reference: strconv.FormatInt(response.ID, 10), WebURL: response.WebURL}, nil
}

func (c Client) ResolveConnectedProject(ctx context.Context, project domain.Project) (domain.GitLabProject, error) {
	baseURL, err := validatedBaseURL(c.BaseURL, c.Token)
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
	return c.ResolveProject(ctx, reference)
}

func (c Client) EnsureIssue(ctx context.Context, spec domain.GitLabIssueSpec) (domain.GitLabIssue, error) {
	if err := validateIssueSpec(spec); err != nil {
		return domain.GitLabIssue{}, err
	}
	baseURL, _ := validatedBaseURL(c.BaseURL, c.Token)
	endpoint := issuesEndpoint(baseURL, spec.Project.Reference)
	query := endpoint.Query()
	query.Set("state", "all")
	query.Set("search", gitLabSearchTerm(spec.IdempotencyKey))
	query.Set("in", "description")
	query.Set("per_page", "100")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.GitLabIssue{}, fmt.Errorf("build GitLab issue lookup: %w", err)
	}
	var found []issueResponse
	if err := c.doJSON(request, &found); err != nil {
		return domain.GitLabIssue{}, err
	}
	var issue issueResponse
	for _, candidate := range found {
		if strings.Contains(candidate.Description, spec.IdempotencyKey) {
			issue = candidate
			break
		}
	}
	if issue.IID == 0 {
		values := issueValues(spec, spec.Labels)
		request, err = formRequest(ctx, http.MethodPost, issuesEndpoint(baseURL, spec.Project.Reference), values)
		if err != nil {
			return domain.GitLabIssue{}, err
		}
		if err := c.doJSON(request, &issue); err != nil {
			return domain.GitLabIssue{}, err
		}
	}
	values := issueValues(spec, mergeManagedLabels(issue.Labels, spec.Labels))
	if spec.State == "closed" {
		values.Set("state_event", "close")
	} else if issue.State == "closed" {
		values.Set("state_event", "reopen")
	}
	request, err = formRequest(ctx, http.MethodPut, issueEndpoint(baseURL, spec.Project.Reference, issue.IID), values)
	if err != nil {
		return domain.GitLabIssue{}, err
	}
	if err := c.doJSON(request, &issue); err != nil {
		return domain.GitLabIssue{}, err
	}
	return c.domainIssue(baseURL, issue)
}

func (c Client) EnsureComment(ctx context.Context, issue domain.GitLabIssue, body, idempotencyKey string) error {
	if issue.ProjectID <= 0 || issue.IID <= 0 || strings.TrimSpace(body) == "" || len(body) > maxGitLabTextBytes ||
		strings.TrimSpace(idempotencyKey) == "" || !strings.Contains(body, idempotencyKey) {
		return fmt.Errorf("invalid GitLab comment: %w", domain.ErrValidation)
	}
	baseURL, err := validatedBaseURL(c.BaseURL, c.Token)
	if err != nil {
		return err
	}
	projectReference := strconv.FormatInt(issue.ProjectID, 10)
	endpoint := issueNotesEndpoint(baseURL, projectReference, issue.IID)
	query := endpoint.Query()
	query.Set("per_page", "100")
	query.Set("sort", "desc")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build GitLab note lookup: %w", err)
	}
	var notes []noteResponse
	if err := c.doJSON(request, &notes); err != nil {
		return err
	}
	for _, note := range notes {
		if strings.Contains(note.Body, idempotencyKey) {
			return nil
		}
	}
	request, err = formRequest(ctx, http.MethodPost, issueNotesEndpoint(baseURL, projectReference, issue.IID), url.Values{"body": {body}})
	if err != nil {
		return err
	}
	var created noteResponse
	return c.doJSON(request, &created)
}

func (c Client) EnsureIssueLink(ctx context.Context, source, target domain.GitLabIssue) error {
	if source.ProjectID <= 0 || source.IID <= 0 || target.ProjectID <= 0 || target.IID <= 0 {
		return fmt.Errorf("invalid GitLab issue link: %w", domain.ErrValidation)
	}
	baseURL, err := validatedBaseURL(c.BaseURL, c.Token)
	if err != nil {
		return err
	}
	projectReference := strconv.FormatInt(source.ProjectID, 10)
	endpoint := issueLinksEndpoint(baseURL, projectReference, source.IID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build GitLab issue link lookup: %w", err)
	}
	var links []issueResponse
	if err := c.doJSON(request, &links); err != nil {
		return err
	}
	for _, link := range links {
		if link.ProjectID == target.ProjectID && link.IID == target.IID {
			return nil
		}
	}
	values := url.Values{
		"target_project_id": {strconv.FormatInt(target.ProjectID, 10)},
		"target_issue_iid":  {strconv.FormatInt(target.IID, 10)},
		"link_type":         {"relates_to"},
	}
	request, err = formRequest(ctx, http.MethodPost, issueLinksEndpoint(baseURL, projectReference, source.IID), values)
	if err != nil {
		return err
	}
	var created json.RawMessage
	if err := c.doJSON(request, &created); err != nil && !errors.Is(err, domain.ErrConflict) {
		return err
	}
	return nil
}

func (c Client) PushBranch(ctx context.Context, project domain.Project, worktreePath, branchName string) error {
	if !filepath.IsAbs(worktreePath) || !taskBranchPattern.MatchString(branchName) {
		return fmt.Errorf("invalid task branch publication: %w", domain.ErrValidation)
	}
	baseURL, err := validatedBaseURL(c.BaseURL, c.Token)
	if err != nil {
		return err
	}
	_, expectedPath, err := splitGitRemote(stringValue(project.GitURL))
	if err != nil {
		return err
	}
	if err := validateWorktreeRemote(ctx, worktreePath, baseURL.Hostname(), expectedPath); err != nil {
		return err
	}
	if err := pushOnboardingBranchWithToken(ctx, worktreePath, branchName, c.Token); err != nil {
		return fmt.Errorf("push task branch: %w", err)
	}
	return nil
}

func (c Client) EnsureMergeRequest(
	ctx context.Context,
	spec domain.GitLabMergeRequestSpec,
) (domain.GitLabMergeRequest, error) {
	if err := validateMergeRequestSpec(spec); err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	baseURL, _ := validatedBaseURL(c.BaseURL, c.Token)
	endpoint := mergeRequestsEndpoint(baseURL, spec.Project.Reference)
	query := endpoint.Query()
	query.Set("state", "all")
	query.Set("source_branch", spec.SourceBranch)
	query.Set("target_branch", spec.TargetBranch)
	query.Set("per_page", "100")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.GitLabMergeRequest{}, fmt.Errorf("build GitLab merge request lookup: %w", err)
	}
	var found []mergeRequestDetailResponse
	if err := c.doJSON(request, &found); err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	var result mergeRequestDetailResponse
	for _, candidate := range found {
		if candidate.SourceBranch == spec.SourceBranch && candidate.TargetBranch == spec.TargetBranch {
			result = candidate
			break
		}
	}
	labels := mergeManagedLabels(result.Labels, spec.Labels)
	values := url.Values{
		"source_branch":        {spec.SourceBranch},
		"target_branch":        {spec.TargetBranch},
		"title":                {spec.Title},
		"description":          {spec.Description},
		"labels":               {strings.Join(labels, ",")},
		"remove_source_branch": {"false"},
		"squash":               {"false"},
	}
	if result.IID == 0 {
		request, err = formRequest(ctx, http.MethodPost, mergeRequestsEndpoint(baseURL, spec.Project.Reference), values)
	} else {
		values.Del("source_branch")
		values.Del("remove_source_branch")
		values.Del("squash")
		request, err = formRequest(ctx, http.MethodPut, mergeRequestEndpoint(baseURL, spec.Project.Reference, result.IID), values)
	}
	if err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	if err := c.doJSON(request, &result); err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	if result.ProjectID <= 0 || result.IID <= 0 || !safeGitLabWebURL(baseURL, result.WebURL) {
		return domain.GitLabMergeRequest{}, fmt.Errorf("GitLab returned an incomplete merge request: %w", domain.ErrConflict)
	}
	return domain.GitLabMergeRequest{
		ProjectID: result.ProjectID, IID: result.IID, State: result.State,
		SourceBranch: result.SourceBranch, TargetBranch: result.TargetBranch, WebURL: result.WebURL,
	}, nil
}

func (c Client) domainIssue(baseURL *url.URL, issue issueResponse) (domain.GitLabIssue, error) {
	if issue.ProjectID <= 0 || issue.IID <= 0 || !safeGitLabWebURL(baseURL, issue.WebURL) {
		return domain.GitLabIssue{}, fmt.Errorf("GitLab returned an incomplete issue: %w", domain.ErrConflict)
	}
	return domain.GitLabIssue{
		ProjectID: issue.ProjectID, IID: issue.IID, Title: issue.Title,
		State: issue.State, WebURL: issue.WebURL,
	}, nil
}

func (c Client) doJSON(request *http.Request, target any) error {
	request.Header.Set("PRIVATE-TOKEN", c.Token)
	request.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safeClient.Do(request)
	if err != nil {
		return fmt.Errorf("call GitLab API: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGitLabResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read GitLab response: %w", err)
	}
	if len(body) > maxGitLabResponseBytes {
		return fmt.Errorf("GitLab response exceeded size limit: %w", domain.ErrValidation)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GitLab API returned HTTP %d: %w", response.StatusCode, domain.ErrConflict)
	}
	if target == nil || len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode GitLab response: %w", err)
	}
	return nil
}

func validateIssueSpec(spec domain.GitLabIssueSpec) error {
	if spec.Project.ID <= 0 || strings.TrimSpace(spec.Project.Reference) == "" ||
		strings.TrimSpace(spec.Title) == "" || len(spec.Title) > 255 ||
		strings.TrimSpace(spec.Description) == "" || len(spec.Description) > maxGitLabTextBytes ||
		strings.TrimSpace(spec.IdempotencyKey) == "" || !strings.Contains(spec.Description, spec.IdempotencyKey) ||
		(spec.State != "opened" && spec.State != "closed") || !validLabels(spec.Labels) {
		return fmt.Errorf("invalid GitLab issue specification: %w", domain.ErrValidation)
	}
	return nil
}

func validateMergeRequestSpec(spec domain.GitLabMergeRequestSpec) error {
	if spec.Project.ID <= 0 || strings.TrimSpace(spec.Project.Reference) == "" ||
		!taskBranchPattern.MatchString(spec.SourceBranch) || strings.TrimSpace(spec.TargetBranch) == "" ||
		strings.TrimSpace(spec.Title) == "" || len(spec.Title) > 255 ||
		strings.TrimSpace(spec.Description) == "" || len(spec.Description) > maxGitLabTextBytes ||
		strings.TrimSpace(spec.IdempotencyKey) == "" || !strings.Contains(spec.Description, spec.IdempotencyKey) ||
		!validLabels(spec.Labels) {
		return fmt.Errorf("invalid GitLab merge request specification: %w", domain.ErrValidation)
	}
	return nil
}

func validLabels(labels []string) bool {
	if len(labels) == 0 || len(labels) > 16 {
		return false
	}
	for _, label := range labels {
		if strings.TrimSpace(label) == "" || len(label) > 64 || strings.ContainsAny(label, "\r\n,") {
			return false
		}
	}
	return true
}

func issueValues(spec domain.GitLabIssueSpec, labels []string) url.Values {
	return url.Values{
		"title":       {spec.Title},
		"description": {spec.Description},
		"labels":      {strings.Join(labels, ",")},
	}
}

func gitLabSearchTerm(idempotencyKey string) string {
	value := strings.TrimSpace(idempotencyKey)
	value = strings.TrimSuffix(strings.TrimPrefix(value, "<!-- "), " -->")
	if index := strings.LastIndex(value, ":"); index >= 0 && index+1 < len(value) {
		return value[index+1:]
	}
	return value
}

func mergeManagedLabels(existing, desired []string) []string {
	set := make(map[string]struct{}, len(existing)+len(desired))
	result := make([]string, 0, len(existing)+len(desired))
	existingLimit := 16 - len(desired)
	if existingLimit < 0 {
		existingLimit = 0
	}
	for _, label := range existing {
		if len(result) >= existingLimit {
			break
		}
		if strings.TrimSpace(label) == "" || len(label) > 64 || strings.ContainsAny(label, "\r\n,") {
			continue
		}
		if label == "orchestrator" || strings.HasPrefix(label, "orchestrator::") ||
			strings.HasPrefix(label, "status::") || strings.HasPrefix(label, "risk::") {
			continue
		}
		if _, ok := set[label]; !ok {
			set[label] = struct{}{}
			result = append(result, label)
		}
	}
	for _, label := range desired {
		if len(result) >= 16 {
			break
		}
		if _, ok := set[label]; !ok {
			set[label] = struct{}{}
			result = append(result, label)
		}
	}
	return result
}

func formRequest(ctx context.Context, method string, endpoint *url.URL, values url.Values) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build GitLab API request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request, nil
}

func safeGitLabWebURL(baseURL *url.URL, raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.EqualFold(parsed.Hostname(), baseURL.Hostname())
}

func projectEndpoint(baseURL *url.URL, reference string) *url.URL {
	return apiEndpoint(baseURL, "projects", reference)
}

func issuesEndpoint(baseURL *url.URL, reference string) *url.URL {
	return apiEndpoint(baseURL, "projects", reference, "issues")
}

func issueEndpoint(baseURL *url.URL, reference string, iid int64) *url.URL {
	return apiEndpoint(baseURL, "projects", reference, "issues", strconv.FormatInt(iid, 10))
}

func issueNotesEndpoint(baseURL *url.URL, reference string, iid int64) *url.URL {
	return apiEndpoint(baseURL, "projects", reference, "issues", strconv.FormatInt(iid, 10), "notes")
}

func issueLinksEndpoint(baseURL *url.URL, reference string, iid int64) *url.URL {
	return apiEndpoint(baseURL, "projects", reference, "issues", strconv.FormatInt(iid, 10), "links")
}

func mergeRequestEndpoint(baseURL *url.URL, reference string, iid int64) *url.URL {
	return apiEndpoint(baseURL, "projects", reference, "merge_requests", strconv.FormatInt(iid, 10))
}

func apiEndpoint(baseURL *url.URL, parts ...string) *url.URL {
	endpoint := *baseURL
	pathParts := append([]string{"api", "v4"}, parts...)
	basePath := strings.TrimRight(endpoint.Path, "/")
	baseRawPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	for _, part := range pathParts {
		basePath += "/" + part
		baseRawPath += "/" + url.PathEscape(part)
	}
	endpoint.Path = basePath
	endpoint.RawPath = baseRawPath
	return &endpoint
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type projectResponse struct {
	ID     int64  `json:"id"`
	WebURL string `json:"web_url"`
}

type issueResponse struct {
	ProjectID   int64    `json:"project_id"`
	IID         int64    `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
}

type noteResponse struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

type mergeRequestDetailResponse struct {
	ProjectID    int64    `json:"project_id"`
	IID          int64    `json:"iid"`
	State        string   `json:"state"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	WebURL       string   `json:"web_url"`
	Labels       []string `json:"labels"`
}

func stableDryRunID(value string) int64 {
	hash := sha256.Sum256([]byte(value))
	return int64(binary.BigEndian.Uint64(hash[:8])%900000) + 100000
}

var _ repository.GitLabGateway = Client{}
