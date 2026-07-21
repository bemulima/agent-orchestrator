package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const maxGitLabResponseBytes = 1 << 20

var onboardingBranchPattern = regexp.MustCompile(`^ai/onboard-[a-z0-9._-]+$`)

type OnboardingPublisher struct {
	BaseURL    string
	Token      string
	DryRun     bool
	HTTPClient *http.Client
	PushBranch func(context.Context, string, string) error
}

func (p OnboardingPublisher) Publish(
	ctx context.Context,
	project domain.Project,
	_ domain.OnboardingRun,
	result domain.OnboardingApplyResult,
) (domain.OnboardingPublication, error) {
	if strings.TrimSpace(p.BaseURL) == "" && strings.TrimSpace(p.Token) == "" {
		return domain.OnboardingPublication{Details: "GitLab is not configured"}, nil
	}
	baseURL, err := validatedBaseURL(p.BaseURL, p.Token)
	if err != nil {
		return domain.OnboardingPublication{}, err
	}
	projectReference, matchesGitLab, err := gitLabProjectReference(project, baseURL)
	if err != nil {
		return domain.OnboardingPublication{}, err
	}
	if !matchesGitLab {
		return domain.OnboardingPublication{Details: "project has no remote on the configured GitLab host"}, nil
	}
	if p.DryRun {
		return domain.OnboardingPublication{Details: "GitLab dry-run is enabled; branch and merge request were not published"}, nil
	}
	if !filepath.IsAbs(result.WorktreePath) || !onboardingBranchPattern.MatchString(result.BranchName) || result.CommitSHA == "" {
		return domain.OnboardingPublication{}, fmt.Errorf("incomplete onboarding commit for GitLab publication: %w", domain.ErrValidation)
	}
	push := p.PushBranch
	if push == nil {
		_, expectedRepositoryPath, splitErr := splitGitRemote(*project.GitURL)
		if splitErr != nil {
			return domain.OnboardingPublication{}, splitErr
		}
		if err := validateWorktreeRemote(ctx, result.WorktreePath, baseURL.Hostname(), expectedRepositoryPath); err != nil {
			return domain.OnboardingPublication{}, err
		}
		push = func(pushContext context.Context, worktreePath, branchName string) error {
			return pushOnboardingBranchWithToken(pushContext, worktreePath, branchName, p.Token)
		}
	}
	if err := push(ctx, result.WorktreePath, result.BranchName); err != nil {
		return domain.OnboardingPublication{}, fmt.Errorf("push onboarding branch: %w", err)
	}
	targetBranch := strings.TrimSpace(project.DefaultBranch)
	if targetBranch == "" {
		targetBranch = "main"
	}
	mergeRequest, found, err := p.findMergeRequest(ctx, baseURL, projectReference, result.BranchName, targetBranch)
	if err != nil {
		return domain.OnboardingPublication{}, err
	}
	if !found {
		mergeRequest, err = p.createMergeRequest(ctx, baseURL, projectReference, project.Name, result.BranchName, targetBranch)
		if err != nil {
			return domain.OnboardingPublication{}, err
		}
	}
	if mergeRequest.ProjectID <= 0 || mergeRequest.IID <= 0 || strings.TrimSpace(mergeRequest.WebURL) == "" {
		return domain.OnboardingPublication{}, fmt.Errorf("GitLab returned an incomplete merge request: %w", domain.ErrConflict)
	}
	mergeRequestURL, err := url.Parse(mergeRequest.WebURL)
	if err != nil || mergeRequestURL.Hostname() == "" || !strings.EqualFold(mergeRequestURL.Hostname(), baseURL.Hostname()) ||
		mergeRequestURL.Scheme != "https" && mergeRequestURL.Scheme != "http" {
		return domain.OnboardingPublication{}, fmt.Errorf("GitLab returned an unsafe merge request URL: %w", domain.ErrConflict)
	}
	return domain.OnboardingPublication{
		Published: true, GitLabProjectID: mergeRequest.ProjectID,
		MergeRequestIID: mergeRequest.IID, MergeRequestURL: mergeRequest.WebURL,
		Details: "GitLab branch and merge request are published",
	}, nil
}

type mergeRequestResponse struct {
	ProjectID int64  `json:"project_id"`
	IID       int64  `json:"iid"`
	WebURL    string `json:"web_url"`
}

func (p OnboardingPublisher) findMergeRequest(
	ctx context.Context,
	baseURL *url.URL,
	projectReference, sourceBranch, targetBranch string,
) (mergeRequestResponse, bool, error) {
	endpoint := mergeRequestsEndpoint(baseURL, projectReference)
	query := endpoint.Query()
	query.Set("state", "opened")
	query.Set("source_branch", sourceBranch)
	query.Set("target_branch", targetBranch)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return mergeRequestResponse{}, false, fmt.Errorf("build GitLab merge request lookup: %w", err)
	}
	var mergeRequests []mergeRequestResponse
	if err := p.doJSON(request, &mergeRequests); err != nil {
		return mergeRequestResponse{}, false, err
	}
	if len(mergeRequests) == 0 {
		return mergeRequestResponse{}, false, nil
	}
	return mergeRequests[0], true, nil
}

func (p OnboardingPublisher) createMergeRequest(
	ctx context.Context,
	baseURL *url.URL,
	projectReference, projectName, sourceBranch, targetBranch string,
) (mergeRequestResponse, error) {
	values := url.Values{
		"source_branch":        {sourceBranch},
		"target_branch":        {targetBranch},
		"title":                {"chore(ai): onboard " + projectName},
		"remove_source_branch": {"false"},
		"squash":               {"false"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, mergeRequestsEndpoint(baseURL, projectReference).String(), strings.NewReader(values.Encode()))
	if err != nil {
		return mergeRequestResponse{}, fmt.Errorf("build GitLab merge request creation: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var mergeRequest mergeRequestResponse
	if err := p.doJSON(request, &mergeRequest); err != nil {
		return mergeRequestResponse{}, err
	}
	return mergeRequest, nil
}

func (p OnboardingPublisher) doJSON(request *http.Request, target any) error {
	request.Header.Set("PRIVATE-TOKEN", p.Token)
	request.Header.Set("Accept", "application/json")
	client := p.HTTPClient
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
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode GitLab response: %w", err)
	}
	return nil
}

func validatedBaseURL(rawBaseURL, token string) (*url.URL, error) {
	if strings.TrimSpace(rawBaseURL) == "" || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("GitLab base URL and token must be configured together: %w", domain.ErrValidation)
	}
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawBaseURL), "/"))
	if err != nil || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" ||
		baseURL.Scheme != "https" && baseURL.Scheme != "http" {
		return nil, fmt.Errorf("invalid GitLab base URL: %w", domain.ErrValidation)
	}
	return baseURL, nil
}

func gitLabProjectReference(project domain.Project, baseURL *url.URL) (string, bool, error) {
	if project.GitURL == nil || strings.TrimSpace(*project.GitURL) == "" {
		return "", false, nil
	}
	host, repositoryPath, err := splitGitRemote(*project.GitURL)
	if err != nil {
		return "", false, err
	}
	if !strings.EqualFold(host, baseURL.Hostname()) {
		return "", false, nil
	}
	if project.GitLabProjectID != nil && *project.GitLabProjectID > 0 {
		return strconv.FormatInt(*project.GitLabProjectID, 10), true, nil
	}
	return repositoryPath, true, nil
}

func splitGitRemote(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		at := strings.LastIndex(raw, "@")
		colon := strings.Index(raw[at+1:], ":")
		if at >= 0 && colon >= 0 {
			colon += at + 1
			return cleanRemoteParts(raw[at+1:colon], raw[colon+1:])
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", "", fmt.Errorf("invalid project Git remote: %w", domain.ErrValidation)
	}
	return cleanRemoteParts(parsed.Hostname(), parsed.Path)
}

func cleanRemoteParts(host, repositoryPath string) (string, string, error) {
	host = strings.TrimSpace(host)
	repositoryPath = strings.Trim(strings.TrimSpace(repositoryPath), "/")
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	if host == "" || repositoryPath == "" || repositoryPath == "." || strings.HasPrefix(repositoryPath, "../") ||
		strings.Contains(repositoryPath, "/../") {
		return "", "", fmt.Errorf("invalid project Git remote path: %w", domain.ErrValidation)
	}
	return host, repositoryPath, nil
}

func mergeRequestsEndpoint(baseURL *url.URL, projectReference string) *url.URL {
	endpoint := *baseURL
	basePath := strings.TrimRight(endpoint.Path, "/")
	baseRawPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	endpoint.Path = basePath + "/api/v4/projects/" + projectReference + "/merge_requests"
	endpoint.RawPath = baseRawPath + "/api/v4/projects/" + url.PathEscape(projectReference) + "/merge_requests"
	return &endpoint
}

func pushOnboardingBranch(ctx context.Context, worktreePath, branchName string) error {
	return pushOnboardingBranchWithToken(ctx, worktreePath, branchName, "")
}

func pushOnboardingBranchWithToken(ctx context.Context, worktreePath, branchName, token string) error {
	command := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"push", "--set-upstream", "origin", branchName)
	command.Dir = worktreePath
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "SSH_ASKPASS=/bin/false")
	if strings.TrimSpace(token) != "" {
		askPassDirectory, err := os.MkdirTemp("", "orchestrator-git-askpass-")
		if err != nil {
			return fmt.Errorf("create Git credential helper directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(askPassDirectory) }()
		askPassPath := filepath.Join(askPassDirectory, "askpass.sh")
		askPassScript := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' \"$ORCHESTRATOR_GIT_USERNAME\" ;;\n  *Password*) printf '%s\\n' \"$ORCHESTRATOR_GIT_TOKEN\" ;;\n  *) exit 1 ;;\nesac\n"
		if err := os.WriteFile(askPassPath, []byte(askPassScript), 0o700); err != nil {
			return fmt.Errorf("create Git credential helper: %w", err)
		}
		command.Env = append(command.Env, "GIT_ASKPASS="+askPassPath,
			"ORCHESTRATOR_GIT_USERNAME=oauth2", "ORCHESTRATOR_GIT_TOKEN="+token)
	} else {
		command.Env = append(command.Env, "GIT_ASKPASS=/bin/false")
	}
	var stdout limitedWriter
	var stderr limitedWriter
	stdout.limit = maxGitLabResponseBytes
	stderr.limit = maxGitLabResponseBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func validateWorktreeRemote(ctx context.Context, worktreePath, expectedHost, expectedRepositoryPath string) error {
	command := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "remote", "get-url", "origin")
	command.Dir = worktreePath
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout limitedWriter
	var stderr limitedWriter
	stdout.limit = maxGitLabResponseBytes
	stderr.limit = maxGitLabResponseBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("resolve onboarding origin: %w", err)
	}
	host, repositoryPath, err := splitGitRemote(strings.TrimSpace(stdout.String()))
	if err != nil {
		return err
	}
	if !strings.EqualFold(host, expectedHost) || repositoryPath != expectedRepositoryPath {
		return fmt.Errorf("onboarding worktree origin differs from approved GitLab project: %w", domain.ErrForbidden)
	}
	return nil
}

type limitedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(content []byte) (int, error) {
	originalLength := len(content)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(content) > remaining {
			content = content[:remaining]
		}
		_, _ = w.buffer.Write(content)
	}
	return originalLength, nil
}

func (w *limitedWriter) String() string { return w.buffer.String() }

var _ repository.OnboardingPublisher = OnboardingPublisher{}
