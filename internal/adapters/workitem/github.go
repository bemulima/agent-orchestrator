package workitem

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

var taskBranchPattern = regexp.MustCompile(`^ai/task-[a-z0-9._-]+$`)

const (
	githubAPIVersion       = "2026-03-10"
	maxGitHubResponseBytes = 1 << 20
)

type GitHubGateway struct {
	BaseURL    string
	Token      string
	DryRunMode bool
	HTTPClient *http.Client
}

func (g GitHubGateway) Configured() bool {
	return strings.TrimSpace(g.BaseURL) != "" && (g.DryRunMode || strings.TrimSpace(g.Token) != "")
}

func (g GitHubGateway) DryRun() bool { return g.DryRunMode }

func (g GitHubGateway) Metadata(ctx context.Context, project domain.Project) (repository.ProjectIssueMetadata, error) {
	repositoryName, err := githubRepository(project)
	if err != nil {
		return repository.ProjectIssueMetadata{}, err
	}
	var labels []struct {
		Name string `json:"name"`
	}
	if err := g.get(ctx, "/repos/"+repositoryName+"/labels?per_page=100", &labels); err != nil {
		return repository.ProjectIssueMetadata{}, err
	}
	var milestones []struct {
		Title string `json:"title"`
	}
	if err := g.get(ctx, "/repos/"+repositoryName+"/milestones?state=open&per_page=100", &milestones); err != nil {
		return repository.ProjectIssueMetadata{}, err
	}
	var assignees []struct {
		Login string `json:"login"`
	}
	if err := g.get(ctx, "/repos/"+repositoryName+"/assignees?per_page=100", &assignees); err != nil {
		return repository.ProjectIssueMetadata{}, err
	}
	result := repository.ProjectIssueMetadata{}
	for _, value := range labels {
		result.Labels = append(result.Labels, value.Name)
	}
	for _, value := range milestones {
		result.Milestones = append(result.Milestones, value.Title)
	}
	for _, value := range assignees {
		result.Assignees = append(result.Assignees, value.Login)
		result.Reviewers = append(result.Reviewers, value.Login)
	}
	sort.Strings(result.Labels)
	sort.Strings(result.Milestones)
	sort.Strings(result.Assignees)
	sort.Strings(result.Reviewers)
	return result, nil
}

func (g GitHubGateway) GetIssue(ctx context.Context, project domain.Project, number int64) (domain.WorkItemPublication, error) {
	if number < 1 {
		return domain.WorkItemPublication{}, fmt.Errorf("GitHub issue number must be positive: %w", domain.ErrValidation)
	}
	repositoryName, err := githubRepository(project)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	var response githubIssueResponse
	if err := g.get(ctx, "/repos/"+repositoryName+"/issues/"+strconv.FormatInt(number, 10), &response); err != nil {
		return domain.WorkItemPublication{}, err
	}
	if response.PullRequest != nil || response.Number != number || response.HTMLURL == "" {
		return domain.WorkItemPublication{}, fmt.Errorf("GitHub resource is not the requested issue: %w", domain.ErrConflict)
	}
	return domain.WorkItemPublication{Number: response.Number, URL: response.HTMLURL, State: response.State}, nil
}

func (g GitHubGateway) PublishIssue(ctx context.Context, project domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	if err := validateIssueWorkItem(item); err != nil {
		return domain.WorkItemPublication{}, err
	}
	repositoryName, err := githubRepository(project)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	if g.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/" + repositoryName + "/issues/1", State: "preview"}, nil
	}
	marker := "<!-- course-dev-orchestrator:" + item.IdempotencyKey + " -->"
	if existing, found, err := g.findIssue(ctx, repositoryName, marker); err != nil {
		return domain.WorkItemPublication{}, err
	} else if found {
		return existing, nil
	}
	metadata, err := g.Metadata(ctx, project)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	if err := requireAvailable(item.Assignees, metadata.Assignees, "assignee"); err != nil {
		return domain.WorkItemPublication{}, err
	}
	if err := g.ensureLabels(ctx, repositoryName, metadata.Labels, item.Labels); err != nil {
		return domain.WorkItemPublication{}, err
	}
	milestone, err := g.ensureMilestone(ctx, repositoryName, item.Milestone)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	payload := map[string]any{
		"title": item.Title, "body": marker + "\n\n" + item.Body, "labels": item.Labels,
		"milestone": milestone, "assignees": item.Assignees,
	}
	var response githubIssueResponse
	if err := g.send(ctx, http.MethodPost, "/repos/"+repositoryName+"/issues", payload, &response); err != nil {
		return domain.WorkItemPublication{}, err
	}
	return issuePublication(response)
}

func (g GitHubGateway) PublishPullRequest(ctx context.Context, project domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	if err := validatePullRequestWorkItem(item); err != nil {
		return domain.WorkItemPublication{}, err
	}
	repositoryName, err := githubRepository(project)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	if g.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/" + repositoryName + "/pull/1", State: "preview"}, nil
	}
	metadata, err := g.Metadata(ctx, project)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	if err := requireAvailable(item.Assignees, metadata.Assignees, "assignee"); err != nil {
		return domain.WorkItemPublication{}, err
	}
	if err := requireAvailable(item.Reviewers, metadata.Reviewers, "reviewer"); err != nil {
		return domain.WorkItemPublication{}, err
	}
	if err := g.ensureLabels(ctx, repositoryName, metadata.Labels, item.Labels); err != nil {
		return domain.WorkItemPublication{}, err
	}
	milestone, err := g.ensureMilestone(ctx, repositoryName, item.Milestone)
	if err != nil {
		return domain.WorkItemPublication{}, err
	}
	owner := strings.Split(repositoryName, "/")[0]
	var existing []githubIssueResponse
	lookup := "/repos/" + repositoryName + "/pulls?state=all&head=" + url.QueryEscape(owner+":"+item.SourceBranch)
	if err := g.get(ctx, lookup, &existing); err != nil {
		return domain.WorkItemPublication{}, err
	}
	var response githubIssueResponse
	if len(existing) > 0 {
		response = existing[0]
	} else {
		payload := map[string]any{
			"title": item.Title,
			"body":  "<!-- course-dev-orchestrator:" + item.IdempotencyKey + " -->\n\n" + item.Body,
			"head":  item.SourceBranch, "base": item.TargetBranch, "draft": true,
		}
		if err := g.send(ctx, http.MethodPost, "/repos/"+repositoryName+"/pulls", payload, &response); err != nil {
			return domain.WorkItemPublication{}, err
		}
	}
	var updated githubIssueResponse
	if err := g.send(ctx, http.MethodPatch, "/repos/"+repositoryName+"/issues/"+strconv.FormatInt(response.Number, 10), map[string]any{
		"labels": item.Labels, "milestone": milestone, "assignees": item.Assignees,
	}, &updated); err != nil {
		return domain.WorkItemPublication{}, err
	}
	if len(item.Reviewers) > 0 {
		var ignored json.RawMessage
		if err := g.send(ctx, http.MethodPost, "/repos/"+repositoryName+"/pulls/"+strconv.FormatInt(response.Number, 10)+"/requested_reviewers",
			map[string]any{"reviewers": item.Reviewers}, &ignored); err != nil {
			return domain.WorkItemPublication{}, err
		}
	}
	return issuePublication(response)
}

func (g GitHubGateway) PublishBranch(ctx context.Context, project domain.Project, worktreePath, branchName string) error {
	if g.DryRunMode {
		return nil
	}
	if !filepath.IsAbs(worktreePath) || !taskBranchPattern.MatchString(branchName) || strings.TrimSpace(g.Token) == "" {
		return fmt.Errorf("invalid GitHub task branch publication: %w", domain.ErrValidation)
	}
	expectedRepository, err := githubRepository(project)
	if err != nil {
		return err
	}
	remote, err := gitOutput(ctx, worktreePath, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	remoteProject := project
	remoteProject.GitURL = &remote
	actualRepository, err := githubRepository(remoteProject)
	if err != nil || actualRepository != expectedRepository {
		return fmt.Errorf("task worktree remote does not match project: %w", domain.ErrConflict)
	}
	currentBranch, err := gitOutput(ctx, worktreePath, "branch", "--show-current")
	if err != nil || currentBranch != branchName {
		return fmt.Errorf("task worktree is not on the approved branch: %w", domain.ErrConflict)
	}
	askPassDirectory, err := os.MkdirTemp("", "orchestrator-github-askpass-")
	if err != nil {
		return fmt.Errorf("create GitHub credential helper directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(askPassDirectory) }()
	askPassPath := filepath.Join(askPassDirectory, "askpass.sh")
	askPassScript := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' \"$ORCHESTRATOR_GIT_USERNAME\" ;;\n  *Password*) printf '%s\\n' \"$ORCHESTRATOR_GIT_TOKEN\" ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(askPassPath, []byte(askPassScript), 0o700); err != nil {
		return fmt.Errorf("create GitHub credential helper: %w", err)
	}
	command := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"push", "--set-upstream", "origin", branchName)
	command.Dir = worktreePath
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS="+askPassPath,
		"ORCHESTRATOR_GIT_USERNAME=x-access-token", "ORCHESTRATOR_GIT_TOKEN="+g.Token)
	output := boundedCommandOutput{limit: 4000}
	command.Stdout = &output
	command.Stderr = &output
	err = command.Run()
	if err != nil {
		message := strings.TrimSpace(output.String())
		return fmt.Errorf("push GitHub task branch: %s", message)
	}
	return nil
}

type boundedCommandOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedCommandOutput) Write(value []byte) (int, error) {
	written := len(value)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = w.buffer.Write(value)
	}
	return written, nil
}

func (w *boundedCommandOutput) String() string { return w.buffer.String() }

func gitOutput(ctx context.Context, worktreePath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
	command.Dir = worktreePath
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("inspect task worktree Git state: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (g GitHubGateway) findIssue(ctx context.Context, repositoryName, marker string) (domain.WorkItemPublication, bool, error) {
	query := url.Values{"q": {"repo:" + repositoryName + " in:body \"" + marker + "\""}, "per_page": {"10"}}
	var response struct {
		Items []githubIssueResponse `json:"items"`
	}
	if err := g.get(ctx, "/search/issues?"+query.Encode(), &response); err != nil {
		return domain.WorkItemPublication{}, false, err
	}
	for _, item := range response.Items {
		if item.PullRequest == nil && strings.Contains(item.Body, marker) {
			publication, err := issuePublication(item)
			return publication, true, err
		}
	}
	return domain.WorkItemPublication{}, false, nil
}

func (g GitHubGateway) ensureLabels(ctx context.Context, repositoryName string, available, requested []string) error {
	known := make(map[string]struct{}, len(available))
	for _, value := range available {
		known[value] = struct{}{}
	}
	for _, label := range requested {
		if _, ok := known[label]; ok {
			continue
		}
		var response json.RawMessage
		if err := g.send(ctx, http.MethodPost, "/repos/"+repositoryName+"/labels", map[string]any{
			"name": label, "color": "5319e7", "description": "Создано оркестратором после одобрения плана",
		}, &response); err != nil {
			return err
		}
		known[label] = struct{}{}
	}
	return nil
}

func (g GitHubGateway) ensureMilestone(ctx context.Context, repositoryName, requested string) (int64, error) {
	var milestones []struct {
		Number int64  `json:"number"`
		Title  string `json:"title"`
	}
	if err := g.get(ctx, "/repos/"+repositoryName+"/milestones?state=open&per_page=100", &milestones); err != nil {
		return 0, err
	}
	for _, value := range milestones {
		if value.Title == requested {
			return value.Number, nil
		}
	}
	var created struct {
		Number int64 `json:"number"`
	}
	if err := g.send(ctx, http.MethodPost, "/repos/"+repositoryName+"/milestones", map[string]any{
		"title": requested, "description": "Создано оркестратором после одобрения плана",
	}, &created); err != nil {
		return 0, err
	}
	if created.Number < 1 {
		return 0, fmt.Errorf("GitHub returned an invalid milestone: %w", domain.ErrConflict)
	}
	return created.Number, nil
}

func (g GitHubGateway) get(ctx context.Context, endpoint string, target any) error {
	return g.send(ctx, http.MethodGet, endpoint, nil, target)
}

func (g GitHubGateway) send(ctx context.Context, method, endpoint string, payload any, target any) error {
	base, err := url.Parse(strings.TrimRight(g.BaseURL, "/"))
	if err != nil || base.Host == "" || base.User != nil {
		return fmt.Errorf("invalid GitHub base URL: %w", domain.ErrValidation)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	base.Path = path.Join(base.Path, parsed.Path)
	base.RawQuery = parsed.RawQuery
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "course-dev-orchestrator")
	if g.Token != "" {
		request.Header.Set("Authorization", "Bearer "+g.Token)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := g.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return fmt.Errorf("GitHub redirects are disabled: %w", domain.ErrValidation)
			},
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", domain.ErrTransient)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxGitHubResponseBytes {
		return fmt.Errorf("GitHub response is too large: %w", domain.ErrValidation)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch {
		case response.StatusCode == http.StatusNotFound:
			return domain.ErrNotFound
		case response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusUnprocessableEntity:
			return domain.ErrConflict
		case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
			return domain.ErrTransient
		default:
			return fmt.Errorf("GitHub returned HTTP %d: %w", response.StatusCode, domain.ErrValidation)
		}
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

type githubIssueResponse struct {
	Number      int64           `json:"number"`
	HTMLURL     string          `json:"html_url"`
	State       string          `json:"state"`
	Body        string          `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func issuePublication(value githubIssueResponse) (domain.WorkItemPublication, error) {
	if value.Number < 1 || value.HTMLURL == "" {
		return domain.WorkItemPublication{}, fmt.Errorf("GitHub returned incomplete work item: %w", domain.ErrConflict)
	}
	return domain.WorkItemPublication{Number: value.Number, URL: value.HTMLURL, State: value.State}, nil
}

func githubRepository(project domain.Project) (string, error) {
	if project.GitURL == nil {
		return "", fmt.Errorf("project Git URL is required: %w", domain.ErrValidation)
	}
	value := strings.TrimSuffix(strings.TrimSpace(*project.GitURL), ".git")
	if strings.HasPrefix(value, "git@github.com:") {
		value = strings.TrimPrefix(value, "git@github.com:")
	} else {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
			return "", fmt.Errorf("project is not hosted on GitHub: %w", domain.ErrValidation)
		}
		value = strings.TrimPrefix(parsed.Path, "/")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(value, "..") {
		return "", fmt.Errorf("invalid GitHub repository identity: %w", domain.ErrValidation)
	}
	return parts[0] + "/" + parts[1], nil
}

func validateIssueWorkItem(item domain.WorkItem) error {
	if item.Kind != domain.WorkItemIssue || item.AgentRole != domain.AgentRunIssueManager ||
		item.Status != domain.WorkItemProposed || item.Title == "" || item.Body == "" ||
		len(item.Labels) == 0 || item.Milestone == "" || len(item.Assignees) == 0 || item.IdempotencyKey == "" {
		return fmt.Errorf("incomplete issue proposal: %w", domain.ErrValidation)
	}
	return nil
}

func validatePullRequestWorkItem(item domain.WorkItem) error {
	if item.Kind != domain.WorkItemPullRequest || item.AgentRole != domain.AgentRunPullRequestManager ||
		item.Status != domain.WorkItemProposed || item.Title == "" || item.Body == "" ||
		item.SourceBranch == "" || item.TargetBranch == "" || len(item.Labels) == 0 ||
		item.Milestone == "" || len(item.Assignees) == 0 || len(item.Reviewers) == 0 {
		return fmt.Errorf("incomplete pull-request proposal: %w", domain.ErrValidation)
	}
	return nil
}

func requireAvailable(requested, available []string, kind string) error {
	set := make(map[string]struct{}, len(available))
	for _, value := range available {
		set[value] = struct{}{}
	}
	for _, value := range requested {
		if _, ok := set[value]; !ok {
			return fmt.Errorf("GitHub %s %q is unavailable: %w", kind, value, domain.ErrValidation)
		}
	}
	return nil
}

var _ repository.WorkItemGateway = GitHubGateway{}
