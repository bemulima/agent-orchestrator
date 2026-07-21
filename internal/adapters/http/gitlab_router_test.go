package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	gitlabuc "github.com/bemulima/agent-orchestrator/internal/usecase/gitlab"
)

func TestRouterGitLabSyncLinksAndSignedWebhook(t *testing.T) {
	syncFake := gitLabSyncFake{result: domain.GitLabSyncResult{PlanID: "plan-id"}}
	linksFake := gitLabLinksFake{links: []domain.GitLabLink{{ResourceType: domain.GitLabResourcePlan, ResourceID: "plan-id"}}}
	webhookFake := &gitLabWebhookFake{result: domain.GitLabWebhookResult{EventUUID: "event-uuid-1234", Status: "processed"}}
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{},
		GitLabHandler: &handlers.GitLabHandler{Sync: syncFake, Links: linksFake, Webhook: webhookFake},
	})
	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodPost, path: "/api/v1/plans/plan-id/gitlab/sync", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/plans/plan-id/gitlab", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/integrations/gitlab/webhook", body: `{"object_kind":"issue"}`, status: http.StatusAccepted},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		if test.path == "/api/v1/integrations/gitlab/webhook" {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Gitlab-Token", "fixture-secret")
			request.Header.Set("X-Gitlab-Event", "Issue Hook")
			request.Header.Set("X-Gitlab-Event-UUID", "event-uuid-1234")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if webhookFake.input.Token != "fixture-secret" || webhookFake.input.EventUUID != "event-uuid-1234" ||
		webhookFake.input.EventType != "Issue Hook" {
		t.Fatalf("webhook input = %#v", webhookFake.input)
	}
}

func TestRouterGitLabWebhookRejectsWrongContentTypeAndLargeBody(t *testing.T) {
	webhookFake := &gitLabWebhookFake{}
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{},
		GitLabHandler: &handlers.GitLabHandler{Webhook: webhookFake},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gitlab/webhook", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d", response.Code)
	}
	large := bytes.Repeat([]byte("x"), (1<<20)+1)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gitlab/webhook", bytes.NewReader(large))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("large body status = %d", response.Code)
	}
}

type gitLabSyncFake struct {
	result domain.GitLabSyncResult
	err    error
}

func (f gitLabSyncFake) Handle(context.Context, string) (domain.GitLabSyncResult, error) {
	return f.result, f.err
}

type gitLabLinksFake struct {
	links []domain.GitLabLink
	err   error
}

func (f gitLabLinksFake) Handle(context.Context, string) ([]domain.GitLabLink, error) {
	return f.links, f.err
}

type gitLabWebhookFake struct {
	input  gitlabuc.WebhookInput
	result domain.GitLabWebhookResult
	err    error
}

func (f *gitLabWebhookFake) Handle(_ context.Context, input gitlabuc.WebhookInput) (domain.GitLabWebhookResult, error) {
	f.input = input
	return f.result, f.err
}
