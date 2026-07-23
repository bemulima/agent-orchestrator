package workitem

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestGitHubGatewayPublishesCompleteIssueIdempotently(t *testing.T) {
	var mu sync.Mutex
	issueCreates := 0
	var issuePayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, githubAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		require.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/search/issues":
			mu.Lock()
			created := issueCreates > 0
			mu.Unlock()
			if created {
				writeGitHubFixture(t, w, map[string]any{"items": []map[string]any{{
					"number": 17, "html_url": "https://github.com/acme/service/issues/17", "state": "open",
					"body": "<!-- course-dev-orchestrator:plan:issue:task -->",
				}}})
			} else {
				writeGitHubFixture(t, w, map[string]any{"items": []any{}})
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/labels"):
			writeGitHubFixture(t, w, []map[string]any{{"name": "тип::задача"}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/milestones"):
			writeGitHubFixture(t, w, []map[string]any{{"number": 2, "title": "Релиз"}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/assignees"):
			writeGitHubFixture(t, w, []map[string]any{{"login": "marat"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			mu.Lock()
			defer mu.Unlock()
			issueCreates++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&issuePayload))
			writeGitHubFixture(t, w, map[string]any{
				"number": 17, "html_url": "https://github.com/acme/service/issues/17", "state": "open",
			})
		default:
			t.Fatalf("unexpected GitHub request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	gitURL := "https://github.com/acme/service.git"
	project := domain.Project{GitURL: &gitURL}
	item := completeIssueWorkItem()
	gateway := GitHubGateway{BaseURL: server.URL, Token: "secret-token", HTTPClient: server.Client()}
	publication, err := gateway.PublishIssue(context.Background(), project, item)
	require.NoError(t, err)
	require.EqualValues(t, 17, publication.Number)
	require.Equal(t, 1, issueCreates)
	require.Equal(t, []any{"тип::задача"}, issuePayload["labels"])
	require.Equal(t, []any{"marat"}, issuePayload["assignees"])
	require.EqualValues(t, 2, issuePayload["milestone"])
	require.Contains(t, issuePayload["body"], item.IdempotencyKey)
	reused, err := gateway.PublishIssue(context.Background(), project, item)
	require.NoError(t, err)
	require.Equal(t, publication, reused)
	require.Equal(t, 1, issueCreates)
}

func TestGitHubGatewayPublishesDraftPRWithReviewersAndMetadata(t *testing.T) {
	var pullPayload, issueMetadata, reviewerPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
			writeGitHubFixture(t, w, []any{})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			require.NoError(t, json.NewDecoder(r.Body).Decode(&pullPayload))
			writeGitHubFixture(t, w, map[string]any{
				"number": 23, "html_url": "https://github.com/acme/service/pull/23", "state": "open",
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/labels"):
			writeGitHubFixture(t, w, []map[string]any{{"name": "тип::задача"}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/milestones"):
			writeGitHubFixture(t, w, []map[string]any{{"number": 3, "title": "Релиз"}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/assignees"):
			writeGitHubFixture(t, w, []map[string]any{{"login": "marat"}, {"login": "reviewer"}})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/23"):
			require.NoError(t, json.NewDecoder(r.Body).Decode(&issueMetadata))
			writeGitHubFixture(t, w, map[string]any{
				"number": 23, "html_url": "https://github.com/acme/service/pull/23", "state": "open",
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls/23/requested_reviewers"):
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reviewerPayload))
			writeGitHubFixture(t, w, map[string]any{})
		default:
			t.Fatalf("unexpected GitHub request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	gitURL := "git@github.com:acme/service.git"
	item := completePullRequestWorkItem()
	gateway := GitHubGateway{BaseURL: server.URL, Token: "secret-token", HTTPClient: server.Client()}
	publication, err := gateway.PublishPullRequest(context.Background(), domain.Project{GitURL: &gitURL}, item)
	require.NoError(t, err)
	require.EqualValues(t, 23, publication.Number)
	require.Equal(t, true, pullPayload["draft"])
	require.Equal(t, item.SourceBranch, pullPayload["head"])
	require.Equal(t, item.TargetBranch, pullPayload["base"])
	require.Equal(t, []any{"marat"}, issueMetadata["assignees"])
	require.Equal(t, []any{"reviewer"}, reviewerPayload["reviewers"])
}

func TestGitHubGatewayDryRunPerformsNoExternalWrite(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	gitURL := "https://github.com/acme/service.git"
	gateway := GitHubGateway{BaseURL: server.URL, DryRunMode: true, HTTPClient: server.Client()}
	_, err := gateway.PublishIssue(context.Background(), domain.Project{GitURL: &gitURL}, completeIssueWorkItem())
	require.NoError(t, err)
	require.False(t, called)
}

func completeIssueWorkItem() domain.WorkItem {
	return domain.WorkItem{
		Kind: domain.WorkItemIssue, Status: domain.WorkItemProposed, AgentRole: domain.AgentRunIssueManager,
		Title: "Добавить обработку заказов", Body: "Полное описание задачи на русском языке.",
		Labels: []string{"тип::задача"}, Milestone: "Релиз", Assignees: []string{"marat"},
		IdempotencyKey: "plan:issue:task",
	}
}

func completePullRequestWorkItem() domain.WorkItem {
	return domain.WorkItem{
		Kind: domain.WorkItemPullRequest, Status: domain.WorkItemProposed, AgentRole: domain.AgentRunPullRequestManager,
		Title: "Реализовать обработку заказов", Body: "Полное описание выполненных изменений на русском языке.",
		Labels: []string{"тип::задача"}, Milestone: "Релиз", Assignees: []string{"marat"},
		Reviewers: []string{"reviewer"}, SourceBranch: "ai/task-orders", TargetBranch: "main",
		IdempotencyKey: "plan:pull-request:task",
	}
}

func writeGitHubFixture(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
