package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestClientEnsuresIssuesNotesLinksAndMergeRequestsIdempotently(t *testing.T) {
	var server *httptest.Server
	issueExists := false
	mergeRequestExists := false
	noteExists := false
	linkExists := false
	issuePosts := 0
	mergeRequestPosts := 0
	notePosts := 0
	linkPosts := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "fixture-token" {
			t.Fatalf("PRIVATE-TOKEN = %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		path := r.URL.EscapedPath()
		issue := issueResponse{
			ProjectID: 42, IID: 7, Title: "Task", Description: "<!-- task:key -->",
			State: "opened", WebURL: server.URL + "/group/project/-/issues/7",
			Labels: []string{"team::backend", "status::old"},
		}
		mergeRequest := mergeRequestDetailResponse{
			ProjectID: 42, IID: 9, State: "opened", SourceBranch: "ai/task-fixture",
			TargetBranch: "main", WebURL: server.URL + "/group/project/-/merge_requests/9",
			Labels: []string{"team::backend", "status::old"},
		}
		switch {
		case r.Method == http.MethodGet && path == "/api/v4/projects/42":
			_ = json.NewEncoder(w).Encode(projectResponse{ID: 42, WebURL: server.URL + "/group/project"})
		case path == "/api/v4/projects/42/issues" && r.Method == http.MethodGet:
			if r.URL.Query().Get("in") != "description" || r.URL.Query().Get("search") != "key" {
				t.Fatalf("issue lookup query = %s", r.URL.RawQuery)
			}
			if issueExists {
				_ = json.NewEncoder(w).Encode([]issueResponse{issue})
			} else {
				_ = json.NewEncoder(w).Encode([]issueResponse{})
			}
		case path == "/api/v4/projects/42/issues" && r.Method == http.MethodPost:
			issuePosts++
			issueExists = true
			_ = json.NewEncoder(w).Encode(issue)
		case path == "/api/v4/projects/42/issues/7" && r.Method == http.MethodPut:
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if labels := r.Form.Get("labels"); !strings.Contains(labels, "team::backend") || strings.Contains(labels, "status::old") {
				t.Fatalf("updated issue labels = %q", labels)
			}
			_ = json.NewEncoder(w).Encode(issue)
		case path == "/api/v4/projects/42/issues/7/notes" && r.Method == http.MethodGet:
			if noteExists {
				_ = json.NewEncoder(w).Encode([]noteResponse{{ID: 1, Body: "<!-- note:key -->\nstatus"}})
			} else {
				_ = json.NewEncoder(w).Encode([]noteResponse{})
			}
		case path == "/api/v4/projects/42/issues/7/notes" && r.Method == http.MethodPost:
			notePosts++
			noteExists = true
			_ = json.NewEncoder(w).Encode(noteResponse{ID: 1, Body: "<!-- note:key -->\nstatus"})
		case path == "/api/v4/projects/42/issues/7/links" && r.Method == http.MethodGet:
			if linkExists {
				_ = json.NewEncoder(w).Encode([]issueResponse{{ProjectID: 43, IID: 8}})
			} else {
				_ = json.NewEncoder(w).Encode([]issueResponse{})
			}
		case path == "/api/v4/projects/42/issues/7/links" && r.Method == http.MethodPost:
			linkPosts++
			linkExists = true
			_ = json.NewEncoder(w).Encode(map[string]any{"link_type": "relates_to"})
		case path == "/api/v4/projects/42/merge_requests" && r.Method == http.MethodGet:
			if mergeRequestExists {
				_ = json.NewEncoder(w).Encode([]mergeRequestDetailResponse{mergeRequest})
			} else {
				_ = json.NewEncoder(w).Encode([]mergeRequestDetailResponse{})
			}
		case path == "/api/v4/projects/42/merge_requests" && r.Method == http.MethodPost:
			mergeRequestPosts++
			mergeRequestExists = true
			_ = json.NewEncoder(w).Encode(mergeRequest)
		case path == "/api/v4/projects/42/merge_requests/9" && r.Method == http.MethodPut:
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if labels := r.Form.Get("labels"); !strings.Contains(labels, "team::backend") || strings.Contains(labels, "status::old") {
				t.Fatalf("updated MR labels = %q", labels)
			}
			_ = json.NewEncoder(w).Encode(mergeRequest)
		default:
			t.Fatalf("unexpected GitLab request %s %s", r.Method, path)
		}
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "fixture-token", HTTPClient: server.Client()}
	project, err := client.ResolveProject(context.Background(), "42")
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	issueSpec := domain.GitLabIssueSpec{
		Project: project, Title: "Task", Description: "<!-- task:key -->\nTask",
		Labels: []string{"orchestrator", "orchestrator::task"}, IdempotencyKey: "<!-- task:key -->", State: "opened",
	}
	firstIssue, err := client.EnsureIssue(context.Background(), issueSpec)
	if err != nil {
		t.Fatalf("EnsureIssue() error = %v", err)
	}
	secondIssue, err := client.EnsureIssue(context.Background(), issueSpec)
	if err != nil || secondIssue.IID != firstIssue.IID || issuePosts != 1 {
		t.Fatalf("second EnsureIssue() = %#v, %v, posts=%d", secondIssue, err, issuePosts)
	}
	for range 2 {
		if err := client.EnsureComment(context.Background(), firstIssue, "<!-- note:key -->\nstatus", "<!-- note:key -->"); err != nil {
			t.Fatalf("EnsureComment() error = %v", err)
		}
		if err := client.EnsureIssueLink(context.Background(), firstIssue, domain.GitLabIssue{ProjectID: 43, IID: 8}); err != nil {
			t.Fatalf("EnsureIssueLink() error = %v", err)
		}
	}
	if notePosts != 1 || linkPosts != 1 {
		t.Fatalf("note posts=%d link posts=%d", notePosts, linkPosts)
	}
	mrSpec := domain.GitLabMergeRequestSpec{
		Project: project, SourceBranch: "ai/task-fixture", TargetBranch: "main", Title: "Task",
		Description: "<!-- mr:key -->\nTask", Labels: []string{"orchestrator"}, IdempotencyKey: "<!-- mr:key -->",
	}
	firstMR, err := client.EnsureMergeRequest(context.Background(), mrSpec)
	if err != nil {
		t.Fatalf("EnsureMergeRequest() error = %v", err)
	}
	secondMR, err := client.EnsureMergeRequest(context.Background(), mrSpec)
	if err != nil || secondMR.IID != firstMR.IID || mergeRequestPosts != 1 {
		t.Fatalf("second EnsureMergeRequest() = %#v, %v, posts=%d", secondMR, err, mergeRequestPosts)
	}
}

func TestClientUsesEncodedSelfHostedBasePath(t *testing.T) {
	var observed string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.URL.EscapedPath()
		base := (&url.URL{Scheme: "http", Host: r.Host, Path: "/gitlab"}).String()
		_ = json.NewEncoder(w).Encode(projectResponse{ID: 12, WebURL: base + "/group/project"})
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL + "/gitlab", Token: "fixture-token", HTTPClient: server.Client()}
	if _, err := client.ResolveProject(context.Background(), "group/project"); err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	if observed != "/gitlab/api/v4/projects/group%2Fproject" {
		t.Fatalf("escaped path = %q", observed)
	}
}

func TestDryRunAdapterIsDeterministic(t *testing.T) {
	adapter := DryRunAdapter{BaseURL: "https://gitlab.example.test", Token: "fixture-token"}
	project, err := adapter.ResolveProject(context.Background(), "group/control")
	if err != nil {
		t.Fatal(err)
	}
	spec := domain.GitLabIssueSpec{
		Project: project, Title: "Plan", Description: "<!-- plan:key -->",
		Labels: []string{"orchestrator"}, IdempotencyKey: "<!-- plan:key -->", State: "opened",
	}
	first, _ := adapter.EnsureIssue(context.Background(), spec)
	second, _ := adapter.EnsureIssue(context.Background(), spec)
	if !first.DryRun || first.IID != second.IID || !strings.Contains(first.WebURL, strconv.FormatInt(first.IID, 10)) {
		t.Fatalf("dry-run issues = %#v / %#v", first, second)
	}
}
