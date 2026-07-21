package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestOnboardingPublisherPushesAndCreatesIdempotentMergeRequest(t *testing.T) {
	postCalls := 0
	mergeRequestExists := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "fixture-token" {
			t.Fatalf("PRIVATE-TOKEN = %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		if !strings.Contains(r.URL.EscapedPath(), "group%2Fproject") {
			t.Fatalf("project path is not URL encoded: %q", r.URL.EscapedPath())
		}
		response := mergeRequestResponse{ProjectID: 42, IID: 7, WebURL: serverURL(r) + "/group/project/-/merge_requests/7"}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("source_branch") != "ai/onboard-project-run" || r.URL.Query().Get("target_branch") != "main" {
				t.Fatalf("unexpected lookup query: %s", r.URL.RawQuery)
			}
			if mergeRequestExists {
				_ = json.NewEncoder(w).Encode([]mergeRequestResponse{response})
			} else {
				_ = json.NewEncoder(w).Encode([]mergeRequestResponse{})
			}
		case http.MethodPost:
			postCalls++
			mergeRequestExists = true
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("source_branch") != "ai/onboard-project-run" || r.Form.Get("target_branch") != "main" {
				t.Fatalf("unexpected creation form: %#v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	gitURL := server.URL + "/group/project.git"
	project := domain.Project{Name: "project", GitURL: &gitURL, DefaultBranch: "main"}
	pushCalls := 0
	publisher := OnboardingPublisher{
		BaseURL: server.URL, Token: "fixture-token", HTTPClient: server.Client(),
		PushBranch: func(_ context.Context, path, branch string) error {
			pushCalls++
			if path != "/tmp/worktree" || branch != "ai/onboard-project-run" {
				t.Fatalf("unexpected push %q %q", path, branch)
			}
			return nil
		},
	}
	result := domain.OnboardingApplyResult{
		WorktreePath: "/tmp/worktree", BranchName: "ai/onboard-project-run", CommitSHA: "abc123",
	}

	first, err := publisher.Publish(context.Background(), project, domain.OnboardingRun{}, result)
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	second, err := publisher.Publish(context.Background(), project, domain.OnboardingRun{}, result)
	if err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
	if !first.Published || first.MergeRequestIID != 7 || second.MergeRequestURL != first.MergeRequestURL {
		t.Fatalf("publications = %#v / %#v", first, second)
	}
	if postCalls != 1 || pushCalls != 2 {
		t.Fatalf("post calls = %d, push calls = %d", postCalls, pushCalls)
	}
}

func TestOnboardingPublisherDryRunHasNoExternalWrites(t *testing.T) {
	serverCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { serverCalls++ }))
	defer server.Close()
	gitURL := server.URL + "/group/project.git"
	pushCalls := 0
	publisher := OnboardingPublisher{
		BaseURL: server.URL, Token: "fixture-token", DryRun: true, HTTPClient: server.Client(),
		PushBranch: func(context.Context, string, string) error { pushCalls++; return nil },
	}
	publication, err := publisher.Publish(context.Background(), domain.Project{GitURL: &gitURL}, domain.OnboardingRun{}, domain.OnboardingApplyResult{})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if publication.Published || pushCalls != 0 || serverCalls != 0 || !strings.Contains(publication.Details, "dry-run") {
		t.Fatalf("dry-run publication = %#v, pushes=%d requests=%d", publication, pushCalls, serverCalls)
	}
}

func TestOnboardingPublisherDisabledWithoutConfiguration(t *testing.T) {
	publication, err := (OnboardingPublisher{}).Publish(context.Background(), domain.Project{}, domain.OnboardingRun{}, domain.OnboardingApplyResult{})
	if err != nil || publication.Published || !strings.Contains(publication.Details, "not configured") {
		t.Fatalf("Publish() = %#v, %v", publication, err)
	}
}

func TestOnboardingPublisherSkipsProjectOnAnotherHost(t *testing.T) {
	gitURL := "https://github.example.test/group/project.git"
	publication, err := (OnboardingPublisher{
		BaseURL: "https://gitlab.example.test", Token: "fixture-token",
	}).Publish(context.Background(), domain.Project{GitURL: &gitURL}, domain.OnboardingRun{}, domain.OnboardingApplyResult{})
	if err != nil || publication.Published || !strings.Contains(publication.Details, "no remote") {
		t.Fatalf("Publish() = %#v, %v", publication, err)
	}
}

func TestOnboardingPublisherDoesNotFollowAPIRedirects(t *testing.T) {
	redirectTargetCalls := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargetCalls++ }))
	defer redirectTarget.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer server.Close()
	gitURL := server.URL + "/group/project.git"
	publisher := OnboardingPublisher{
		BaseURL: server.URL, Token: "fixture-token", HTTPClient: server.Client(),
		PushBranch: func(context.Context, string, string) error { return nil },
	}
	_, err := publisher.Publish(context.Background(), domain.Project{Name: "project", GitURL: &gitURL}, domain.OnboardingRun{}, domain.OnboardingApplyResult{
		WorktreePath: "/tmp/worktree", BranchName: "ai/onboard-project-run", CommitSHA: "abc",
	})
	if err == nil || redirectTargetCalls != 0 {
		t.Fatalf("Publish() error = %v, redirect target calls = %d", err, redirectTargetCalls)
	}
}

func TestPushOnboardingBranchPublishesCurrentBranch(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	worktree := filepath.Join(root, "worktree")
	runGitLabTestGit(t, root, "init", "--bare", remote)
	if err := os.Mkdir(worktree, 0o750); err != nil {
		t.Fatal(err)
	}
	runGitLabTestGit(t, worktree, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitLabTestGit(t, worktree, "add", "AGENTS.md")
	runGitLabTestGit(t, worktree, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "onboard")
	runGitLabTestGit(t, worktree, "checkout", "-b", "ai/onboard-project-run")
	runGitLabTestGit(t, worktree, "remote", "add", "origin", remote)

	if err := pushOnboardingBranch(context.Background(), worktree, "ai/onboard-project-run"); err != nil {
		t.Fatalf("pushOnboardingBranch() error = %v", err)
	}
	want := runGitLabTestGit(t, worktree, "rev-parse", "HEAD")
	got := runGitLabTestGit(t, root, "--git-dir", remote, "rev-parse", "refs/heads/ai/onboard-project-run")
	if got != want {
		t.Fatalf("remote commit = %s, want %s", got, want)
	}
}

func serverURL(r *http.Request) string {
	return (&url.URL{Scheme: "http", Host: r.Host}).String()
}

func runGitLabTestGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
