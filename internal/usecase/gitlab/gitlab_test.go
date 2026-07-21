package gitlab

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gitlabadapter "github.com/bemulima/agent-orchestrator/internal/adapters/gitlab"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestSyncIsIdempotentAcrossIssuesCommentsBranchAndMergeRequest(t *testing.T) {
	projectID := int64(42)
	gitURL := "https://gitlab.example.test/group/service.git"
	task := domain.Task{
		ID: "task-id", PlanID: "plan-id", ProjectID: "project-id", Title: "Implement change",
		Description: "A bounded fixture task", Status: domain.TaskStatusCompleted,
		AcceptanceCriteria: []string{"tests pass"}, WriteScope: []string{"internal/**"},
		RiskLevel: domain.RiskLevelLow,
	}
	bundle := approvedBundle(task)
	commit := "0123456789abcdef0123456789abcdef01234567"
	attempts := attemptListFake{values: map[string][]domain.TaskAttempt{
		task.ID: {{
			TaskID: task.ID, Status: domain.TaskAttemptStatusCompleted, CommitSHA: &commit,
			WorktreePath: "/tmp/worktree", BranchName: "ai/task-fixture",
		}},
	}}
	gateway := gitlabadapter.NewFakeAdapter()
	links := newLinkRepoFake()
	useCase := Sync{
		Plans: planGetterFake{bundle: bundle},
		Projects: projectGetterFake{values: map[string]domain.Project{
			"project-id": {ID: "project-id", Name: "service", GitURL: &gitURL, GitLabProjectID: &projectID, DefaultBranch: "main"},
		}},
		TaskExecutions: attempts, Links: links, Gateway: gateway, ControlProject: "99",
	}

	first, err := useCase.Handle(context.Background(), bundle.Plan.ID)
	if err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	second, err := useCase.Handle(context.Background(), bundle.Plan.ID)
	if err != nil {
		t.Fatalf("second Handle() error = %v", err)
	}
	if first.PlanIssue.IID != second.PlanIssue.IID || first.Items[0].MergeRequest == nil ||
		second.Items[0].MergeRequest == nil || first.Items[0].MergeRequest.IID != second.Items[0].MergeRequest.IID {
		t.Fatalf("sync results are not stable: %#v / %#v", first, second)
	}
	if gateway.IssueCreates != 2 || gateway.MRCreates != 1 || gateway.BranchCreates != 1 {
		t.Fatalf("external creates: issues=%d mrs=%d branches=%d", gateway.IssueCreates, gateway.MRCreates, gateway.BranchCreates)
	}
	if gateway.CommentCreates != 3 || gateway.LinkCreates != 1 {
		t.Fatalf("idempotent metadata creates: comments=%d links=%d", gateway.CommentCreates, gateway.LinkCreates)
	}
	if links.saveCalls != 6 {
		t.Fatalf("SaveGitLabLink calls = %d, want 6 idempotent upserts", links.saveCalls)
	}
}

func TestSyncDryRunHasNoPersistenceAndDoesNotRequireApproval(t *testing.T) {
	gitURL := "https://gitlab.example.test/group/service.git"
	task := domain.Task{
		ID: "task-id", PlanID: "plan-id", ProjectID: "project-id", Title: "Preview change",
		Description: "fixture", Status: domain.TaskStatusPlanned,
		AcceptanceCriteria: []string{"preview exists"}, WriteScope: []string{"docs/**"}, RiskLevel: domain.RiskLevelLow,
	}
	bundle := approvedBundle(task)
	bundle.Approval = nil
	links := newLinkRepoFake()
	result, err := (Sync{
		Plans: planGetterFake{bundle: bundle},
		Projects: projectGetterFake{values: map[string]domain.Project{
			"project-id": {ID: "project-id", Name: "service", GitURL: &gitURL},
		}},
		Links: links,
		Gateway: gitlabadapter.DryRunAdapter{
			BaseURL: "https://gitlab.example.test", Token: "fixture-token",
		},
		ControlProject: "group/control",
	}).Handle(context.Background(), bundle.Plan.ID)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !result.DryRun || !result.PlanIssue.DryRun || links.saveCalls != 0 {
		t.Fatalf("dry-run result = %#v, saves=%d", result, links.saveCalls)
	}
}

func TestSyncRequiresApprovalBeforeExternalWrites(t *testing.T) {
	bundle := approvedBundle()
	bundle.Approval = nil
	_, err := (Sync{
		Plans: planGetterFake{bundle: bundle}, Links: newLinkRepoFake(),
		Gateway: gitlabadapter.NewFakeAdapter(), ControlProject: "control",
	}).Handle(context.Background(), bundle.Plan.ID)
	if !errors.Is(err, domain.ErrApprovalNeeded) {
		t.Fatalf("Handle() error = %v, want approval required", err)
	}
}

func TestBoundedGitLabTextPreservesUTF8(t *testing.T) {
	value := "Изменение " + strings.Repeat("я", 200)
	title := boundedTitle(value)
	if len(title) > 255 || !utf8.ValidString(title) {
		t.Fatalf("bounded title has %d bytes and valid=%v", len(title), utf8.ValidString(title))
	}
}

func TestProcessWebhookValidatesTokenAndNormalizesMergeRequest(t *testing.T) {
	repository := newLinkRepoFake()
	useCase := ProcessWebhook{Secret: "0123456789abcdef", Links: repository}
	body := []byte(`{
  "object_kind":"merge_request",
  "project":{"id":42,"path_with_namespace":"group/service"},
  "object_attributes":{"iid":7,"state":"merged","source_branch":"ai/task-fixture","unrelated":true},
  "user":{"name":"Fixture"}
}`)
	input := WebhookInput{
		Token: "0123456789abcdef", EventUUID: "event-uuid-1234",
		EventType: "Merge Request Hook", Body: body,
	}
	result, err := useCase.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if result.Status != "processed" || repository.lastEvent.ObjectKind != "merge_request" ||
		repository.lastEvent.ExternalState != "merged" || repository.lastEvent.ObjectIID != 7 ||
		len(repository.lastEvent.PayloadChecksum) != 64 {
		t.Fatalf("normalized webhook = %#v, result=%#v", repository.lastEvent, result)
	}
	input.Token = "wrong"
	if _, err := useCase.Handle(context.Background(), input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("invalid token error = %v", err)
	}
}

func TestProcessWebhookValidatesHMACAndRejectsStaleOrTamperedDelivery(t *testing.T) {
	repository := newLinkRepoFake()
	now := time.Unix(1_800_000_000, 0).UTC()
	key := []byte("0123456789abcdef0123456789abcdef")
	token := "whsec_" + base64.StdEncoding.EncodeToString(key)
	useCase := ProcessWebhook{SigningToken: token, Links: repository, Now: func() time.Time { return now }}
	body := []byte(`{"object_kind":"issue","project":{"id":42},"object_attributes":{"iid":7,"state":"opened"}}`)
	input := WebhookInput{
		MessageID: "message-id-1234", EventUUID: "message-id-1234",
		Timestamp: strconv.FormatInt(now.Unix(), 10), EventType: "Issue Hook", Body: body,
	}
	input.Signature = webhookSignature(key, input.MessageID, input.Timestamp, input.Body)
	if _, err := useCase.Handle(context.Background(), input); err != nil {
		t.Fatalf("signed Handle() error = %v", err)
	}
	tampered := input
	tampered.Body = append(append([]byte(nil), body...), ' ')
	if _, err := useCase.Handle(context.Background(), tampered); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("tampered webhook error = %v", err)
	}
	stale := input
	stale.Timestamp = strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
	stale.Signature = webhookSignature(key, stale.MessageID, stale.Timestamp, stale.Body)
	if _, err := useCase.Handle(context.Background(), stale); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stale webhook error = %v", err)
	}
}

func webhookSignature(key []byte, messageID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(messageID + "." + timestamp + "." + string(body)))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func approvedBundle(tasks ...domain.Task) domain.PlanBundle {
	return domain.PlanBundle{
		Plan: domain.Plan{
			ID: "plan-id", Status: domain.PlanStatusApproved, Summary: "Fixture plan",
			RiskLevel: "low",
		},
		Tasks:    tasks,
		Approval: &domain.Approval{Status: "approved"},
	}
}

type planGetterFake struct {
	bundle domain.PlanBundle
	err    error
}

func (f planGetterFake) GetPlan(context.Context, string) (domain.PlanBundle, error) {
	return f.bundle, f.err
}

type projectGetterFake struct {
	values map[string]domain.Project
}

func (f projectGetterFake) Get(_ context.Context, id string) (domain.Project, error) {
	project, ok := f.values[id]
	if !ok {
		return domain.Project{}, domain.ErrNotFound
	}
	return project, nil
}

type attemptListFake struct {
	values map[string][]domain.TaskAttempt
}

func (f attemptListFake) ListAttempts(_ context.Context, id string) ([]domain.TaskAttempt, error) {
	return append([]domain.TaskAttempt(nil), f.values[id]...), nil
}

type linkRepoFake struct {
	values    map[string]domain.GitLabLink
	saveCalls int
	lastEvent domain.GitLabWebhookEvent
}

func newLinkRepoFake() *linkRepoFake {
	return &linkRepoFake{values: make(map[string]domain.GitLabLink)}
}

func (f *linkRepoFake) GetGitLabLink(_ context.Context, resourceType, resourceID string, projectID int64) (domain.GitLabLink, error) {
	link, ok := f.values[resourceType+":"+resourceID]
	if !ok || link.GitLabProjectID != projectID {
		return domain.GitLabLink{}, domain.ErrNotFound
	}
	return link, nil
}

func (f *linkRepoFake) SaveGitLabLink(_ context.Context, link domain.GitLabLink) (domain.GitLabLink, error) {
	f.saveCalls++
	key := link.ResourceType + ":" + link.ResourceID
	if existing, ok := f.values[key]; ok {
		if link.IssueIID == nil {
			link.IssueIID = existing.IssueIID
		}
		if link.MergeRequestIID == nil {
			link.MergeRequestIID = existing.MergeRequestIID
		}
	}
	link.ID = key
	f.values[key] = link
	return link, nil
}

func (f *linkRepoFake) ListGitLabLinksForPlan(context.Context, string) ([]domain.GitLabLink, error) {
	result := make([]domain.GitLabLink, 0, len(f.values))
	for _, link := range f.values {
		result = append(result, link)
	}
	return result, nil
}

func (f *linkRepoFake) ApplyGitLabWebhook(_ context.Context, event domain.GitLabWebhookEvent) (domain.GitLabWebhookResult, error) {
	f.lastEvent = event
	return domain.GitLabWebhookResult{EventUUID: event.EventUUID, Status: "processed"}, nil
}
