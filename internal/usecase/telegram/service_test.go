package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
	"github.com/bemulima/agent-orchestrator/internal/usecase/planning"
	"github.com/bemulima/agent-orchestrator/internal/usecase/project"
)

type fakeTelegramStore struct {
	updates   map[int64]string
	callbacks map[string]domain.TelegramCallbackGrant
	offset    int64
	beginErr  error
}

func newFakeTelegramStore() *fakeTelegramStore {
	return &fakeTelegramStore{updates: map[int64]string{}, callbacks: map[string]domain.TelegramCallbackGrant{}}
}

func (f *fakeTelegramStore) BeginUpdate(_ context.Context, receipt domain.TelegramUpdateReceipt) (bool, error) {
	if f.beginErr != nil {
		return false, f.beginErr
	}
	if status, exists := f.updates[receipt.UpdateID]; exists && status != domain.TelegramUpdateStatusFailed {
		return false, nil
	}
	f.updates[receipt.UpdateID] = domain.TelegramUpdateStatusReceived
	return true, nil
}

func (f *fakeTelegramStore) FinishUpdate(_ context.Context, updateID int64, status string) error {
	f.updates[updateID] = status
	return nil
}

func (f *fakeTelegramStore) GetPollOffset(context.Context, string) (int64, error) {
	return f.offset, nil
}
func (f *fakeTelegramStore) SavePollOffset(_ context.Context, _ string, offset int64) error {
	if offset > f.offset {
		f.offset = offset
	}
	return nil
}

func (f *fakeTelegramStore) SaveCallback(_ context.Context, grant domain.TelegramCallbackGrant) error {
	grant.ID = uuid.NewString()
	f.callbacks[grant.TokenHash] = grant
	return nil
}

func (f *fakeTelegramStore) ConsumeCallback(
	_ context.Context,
	tokenHash string,
	userID, chatID int64,
	now time.Time,
) (domain.TelegramCallbackGrant, error) {
	grant, exists := f.callbacks[tokenHash]
	if !exists {
		return domain.TelegramCallbackGrant{}, domain.ErrNotFound
	}
	if grant.TelegramUserID != userID || grant.TelegramChatID != chatID {
		return domain.TelegramCallbackGrant{}, domain.ErrForbidden
	}
	if grant.Status != domain.TelegramCallbackStatusPending {
		return domain.TelegramCallbackGrant{}, domain.ErrConflict
	}
	if !now.Before(grant.ExpiresAt) {
		grant.Status = domain.TelegramCallbackStatusExpired
		f.callbacks[tokenHash] = grant
		return domain.TelegramCallbackGrant{}, domain.ErrInvalidStatus
	}
	grant.Status = domain.TelegramCallbackStatusConsumed
	f.callbacks[tokenHash] = grant
	return grant, nil
}

type fakeTelegramGateway struct {
	messages []domain.TelegramOutgoingMessage
	answers  []string
}

func (f *fakeTelegramGateway) GetUpdates(context.Context, int64, int) ([]domain.TelegramUpdate, error) {
	return nil, nil
}
func (f *fakeTelegramGateway) SendMessage(_ context.Context, message domain.TelegramOutgoingMessage) error {
	f.messages = append(f.messages, message)
	return nil
}
func (f *fakeTelegramGateway) AnswerCallback(_ context.Context, id, text string, alert bool) error {
	f.answers = append(f.answers, fmt.Sprintf("%s:%s:%t", id, text, alert))
	return nil
}
func (f *fakeTelegramGateway) SetWebhook(context.Context, string, string) error { return nil }
func (f *fakeTelegramGateway) DeleteWebhook(context.Context, bool) error        { return nil }

type connectStub struct{ result project.ConnectResult }

func (s connectStub) Handle(context.Context, project.ConnectInput) (project.ConnectResult, error) {
	return s.result, nil
}

type getProjectStub struct{ value domain.Project }

func (s getProjectStub) Handle(context.Context, string) (domain.Project, error) { return s.value, nil }

type listProjectsStub struct {
	values []domain.Project
	err    error
}

func (s listProjectsStub) Handle(context.Context) ([]domain.Project, error) { return s.values, s.err }

type scanStub struct{ value project.ScanResult }

func (s scanStub) Handle(context.Context, string) (project.ScanResult, error) { return s.value, nil }

type topologyStub struct{ value domain.TopologyCatalog }

func (s topologyStub) Handle(context.Context) (domain.TopologyCatalog, error) { return s.value, nil }

type createCommandStub struct{ value domain.Command }

func (s createCommandStub) Handle(context.Context, planning.CreateCommandInput) (domain.Command, error) {
	return s.value, nil
}

type createPlanStub struct{ value domain.PlanBundle }

func (s createPlanStub) Handle(context.Context, string, domain.PlanRequest) (domain.PlanBundle, error) {
	return s.value, nil
}

type getPlanStub struct{ value domain.PlanBundle }

func (s getPlanStub) Handle(context.Context, string) (domain.PlanBundle, error) { return s.value, nil }

type decidePlanStub struct {
	value domain.PlanBundle
	calls *int
}

func (s decidePlanStub) Handle(context.Context, planning.DecidePlanInput) (domain.PlanBundle, error) {
	*s.calls++
	return s.value, nil
}

type getRunStub struct{ value domain.PlanRun }

func (s getRunStub) Handle(context.Context, string) (domain.PlanRun, error) { return s.value, nil }

type controlRunStub struct{ value domain.PlanRun }

func (s controlRunStub) Handle(context.Context, planning.ControlRunInput) (domain.PlanRun, error) {
	return s.value, nil
}

type getTaskStub struct{ value domain.Task }

func (s getTaskStub) Handle(context.Context, string) (domain.Task, error) { return s.value, nil }

type taskActionStub struct{ value domain.Task }

func (s taskActionStub) Handle(context.Context, string) (domain.Task, error) { return s.value, nil }

type getOnboardingStub struct{ value domain.OnboardingRun }

func (s getOnboardingStub) Handle(context.Context, string) (domain.OnboardingRun, error) {
	return s.value, nil
}

type decideOnboardingStub struct{ value domain.OnboardingRun }

func (s decideOnboardingStub) Handle(context.Context, onboarding.DecideInput) (domain.OnboardingRun, error) {
	return s.value, nil
}

type linksStub struct{ values []domain.GitLabLink }

func (s linksStub) Handle(context.Context, string) ([]domain.GitLabLink, error) { return s.values, nil }

type telegramFixture struct {
	service      *Service
	store        *fakeTelegramStore
	gateway      *fakeTelegramGateway
	planID       string
	runID        string
	taskID       string
	onboardingID string
	approveCalls *int
	now          *time.Time
}

func newTelegramFixture() telegramFixture {
	projectID := uuid.NewString()
	planID := uuid.NewString()
	runID := uuid.NewString()
	taskID := uuid.NewString()
	onboardingID := uuid.NewString()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	approveCalls := 0
	projectValue := domain.Project{
		ID: projectID, Name: "demo", Status: domain.ProjectStatusAnalyzed,
		RepositoryRole: domain.RepositoryRoleService,
	}
	task := domain.Task{
		ID: taskID, PlanID: planID, ProjectID: projectID, Status: domain.TaskStatusBlocked,
		Title: "Implement safe change", Role: "coder", RiskLevel: domain.RiskLevelLow,
	}
	run := domain.PlanRun{ID: runID, PlanID: planID, Status: domain.PlanRunStatusRunning}
	bundle := domain.PlanBundle{
		Plan:  domain.Plan{ID: planID, Status: domain.PlanStatusAwaitingApproval, Summary: "Safe plan", RiskLevel: "low"},
		Tasks: []domain.Task{task}, Run: &run,
	}
	onboardingRun := domain.OnboardingRun{
		ID: onboardingID, ProjectID: projectID, Status: domain.OnboardingStatusProposalReady, DryRun: true,
	}
	store := newFakeTelegramStore()
	gateway := &fakeTelegramGateway{}
	randomData := make([]byte, 4096)
	for i := range randomData {
		randomData[i] = byte(i % 251)
	}
	service := NewService(store, gateway, Operations{
		ConnectProject: connectStub{result: project.ConnectResult{
			Project: projectValue, Snapshot: domain.ServiceSnapshot{ServiceKind: domain.ServiceKindBackendService},
		}},
		GetProject: getProjectStub{value: projectValue}, ListProjects: listProjectsStub{values: []domain.Project{projectValue}},
		ScanProject: scanStub{value: project.ScanResult{Project: projectValue, Snapshot: domain.ServiceSnapshot{
			ServiceKind: domain.ServiceKindBackendService, Language: "go", Confidence: 0.95,
		}}},
		RebuildTopology: topologyStub{value: domain.TopologyCatalog{Revision: domain.TopologyRevision{
			ID: uuid.NewString(), ProjectCount: 1, ServiceCount: 1, RelationCount: 0,
		}}},
		CreateCommand: createCommandStub{value: domain.Command{ID: uuid.NewString()}},
		CreatePlan:    createPlanStub{value: bundle}, GetPlan: getPlanStub{value: bundle},
		ApprovePlan: decidePlanStub{value: bundle, calls: &approveCalls},
		RejectPlan:  decidePlanStub{value: bundle, calls: &approveCalls},
		GetRun:      getRunStub{value: run}, ControlRun: controlRunStub{value: run},
		GetTask: getTaskStub{value: task}, RetryTask: taskActionStub{value: task}, CancelTask: taskActionStub{value: task},
		GetOnboarding:     getOnboardingStub{value: onboardingRun},
		ApproveOnboarding: decideOnboardingStub{value: onboardingRun},
		RejectOnboarding:  decideOnboardingStub{value: onboardingRun},
		GitLabLinks: linksStub{values: []domain.GitLabLink{{
			ResourceType: "plan", ResourceID: planID, URL: "https://gitlab.example.test/group/app/-/issues/1",
		}}},
	}, []int64{101, 202}, []int64{-303}, 15*time.Minute)
	service.Now = func() time.Time { return now }
	service.Random = bytes.NewReader(randomData)
	return telegramFixture{
		service: service, store: store, gateway: gateway, planID: planID, runID: runID,
		taskID: taskID, onboardingID: onboardingID, approveCalls: &approveCalls, now: &now,
	}
}

func TestService_AllCommandsUseApplicationOperations(t *testing.T) {
	fixture := newTelegramFixture()
	commands := []string{
		"/start", "/help", "/projects", "/connect /fixtures/demo", "/analyze demo", "/topology",
		"/plan Add safe feature", "/status plan " + fixture.planID,
		"/approve plan " + fixture.planID, "/reject onboarding " + fixture.onboardingID,
		"/pause " + fixture.runID, "/resume " + fixture.runID, "/retry " + fixture.taskID,
		"/cancel task " + fixture.taskID, "/issues " + fixture.planID,
	}
	for index, command := range commands {
		before := len(fixture.gateway.messages)
		if err := fixture.service.Handle(context.Background(), messageUpdate(int64(index+1), 101, -303, command), "polling"); err != nil {
			t.Fatalf("Handle(%q) error = %v", command, err)
		}
		if len(fixture.gateway.messages) != before+1 || strings.TrimSpace(fixture.gateway.messages[before].Text) == "" {
			t.Fatalf("Handle(%q) messages = %#v", command, fixture.gateway.messages[before:])
		}
	}
	if *fixture.approveCalls != 0 {
		t.Fatalf("text-only approval executed a decision: calls = %d", *fixture.approveCalls)
	}
}

func TestService_CallbackIsResourceBoundExpiringAndReplaySafe(t *testing.T) {
	fixture := newTelegramFixture()
	if err := fixture.service.Handle(context.Background(), messageUpdate(10, 101, -303, "/plan change"), "polling"); err != nil {
		t.Fatal(err)
	}
	keyboard := fixture.gateway.messages[len(fixture.gateway.messages)-1].ReplyMarkup
	if keyboard == nil || len(keyboard.InlineKeyboard) != 2 {
		t.Fatalf("plan keyboard = %#v", keyboard)
	}
	approveData := keyboard.InlineKeyboard[0][0].CallbackData

	// A different allowlisted user cannot use a grant bound to the owner.
	if err := fixture.service.Handle(context.Background(), callbackUpdate(11, 202, -303, approveData), "webhook"); err != nil {
		t.Fatal(err)
	}
	if *fixture.approveCalls != 0 || !strings.Contains(lastMessage(fixture.gateway), "запрещено") {
		t.Fatalf("cross-user callback was not rejected: calls=%d message=%q", *fixture.approveCalls, lastMessage(fixture.gateway))
	}

	if err := fixture.service.Handle(context.Background(), callbackUpdate(12, 101, -303, approveData), "webhook"); err != nil {
		t.Fatal(err)
	}
	if *fixture.approveCalls != 1 {
		t.Fatalf("valid callback approval calls = %d, want 1", *fixture.approveCalls)
	}
	if err := fixture.service.Handle(context.Background(), callbackUpdate(13, 101, -303, approveData), "webhook"); err != nil {
		t.Fatal(err)
	}
	if *fixture.approveCalls != 1 || !strings.Contains(lastMessage(fixture.gateway), "уже выполнено") {
		t.Fatalf("replayed callback result: calls=%d message=%q", *fixture.approveCalls, lastMessage(fixture.gateway))
	}

	if err := fixture.service.Handle(context.Background(), messageUpdate(14, 101, -303, "/reject plan "+fixture.planID), "polling"); err != nil {
		t.Fatal(err)
	}
	rejectData := fixture.gateway.messages[len(fixture.gateway.messages)-1].ReplyMarkup.InlineKeyboard[0][0].CallbackData
	*fixture.now = fixture.now.Add(16 * time.Minute)
	if err := fixture.service.Handle(context.Background(), callbackUpdate(15, 101, -303, rejectData), "webhook"); err != nil {
		t.Fatal(err)
	}
	if *fixture.approveCalls != 1 || !strings.Contains(lastMessage(fixture.gateway), "устарело") {
		t.Fatalf("stale callback result: calls=%d message=%q", *fixture.approveCalls, lastMessage(fixture.gateway))
	}
}

func TestService_AllCallbackActionsUseApplicationOperations(t *testing.T) {
	fixture := newTelegramFixture()
	ctx := context.Background()
	updateID := int64(100)
	if err := fixture.service.Handle(ctx, messageUpdate(updateID, 101, -303, "/plan change"), "polling"); err != nil {
		t.Fatal(err)
	}
	planKeyboard := fixture.gateway.messages[len(fixture.gateway.messages)-1].ReplyMarkup
	for _, test := range []struct {
		button domain.TelegramInlineButton
		want   string
	}{
		{planKeyboard.InlineKeyboard[0][0], "Plan "},
		{planKeyboard.InlineKeyboard[0][1], "Задачи плана"},
		{planKeyboard.InlineKeyboard[1][0], "уточнённое требование"},
		{planKeyboard.InlineKeyboard[1][1], "Plan "},
	} {
		updateID++
		if err := fixture.service.Handle(ctx, callbackUpdate(updateID, 101, -303, test.button.CallbackData), "webhook"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(lastMessage(fixture.gateway), test.want) {
			t.Fatalf("callback %q response = %q", test.button.Text, lastMessage(fixture.gateway))
		}
	}

	for _, test := range []struct {
		command string
		want    string
	}{
		{"/approve onboarding " + fixture.onboardingID, "Onboarding"},
		{"/reject onboarding " + fixture.onboardingID, "Onboarding"},
		{"/pause " + fixture.runID, "запрос pause"},
		{"/resume " + fixture.runID, "запрос resume"},
		{"/retry " + fixture.taskID, "retry запрошен"},
		{"/cancel run " + fixture.runID, "отмена запрошена"},
		{"/cancel task " + fixture.taskID, "отмена запрошена"},
	} {
		updateID++
		if err := fixture.service.Handle(ctx, messageUpdate(updateID, 101, -303, test.command), "polling"); err != nil {
			t.Fatal(err)
		}
		keyboard := fixture.gateway.messages[len(fixture.gateway.messages)-1].ReplyMarkup
		if keyboard == nil {
			t.Fatalf("command %q did not issue a callback", test.command)
		}
		updateID++
		if err := fixture.service.Handle(ctx, callbackUpdate(updateID, 101, -303,
			keyboard.InlineKeyboard[0][0].CallbackData), "webhook"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(lastMessage(fixture.gateway), test.want) {
			t.Fatalf("command %q callback response = %q", test.command, lastMessage(fixture.gateway))
		}
	}
}

func TestService_AnswersCallbackWhenStorageFails(t *testing.T) {
	fixture := newTelegramFixture()
	fixture.store.beginErr = errors.New("database unavailable")
	err := fixture.service.Handle(context.Background(), callbackUpdate(200, 101, -303, "tg:opaque"), "webhook")
	if err == nil || len(fixture.gateway.answers) != 1 || !strings.Contains(fixture.gateway.answers[0], "Не выполнено") {
		t.Fatalf("Handle() error = %v, answers = %#v", err, fixture.gateway.answers)
	}
}

func TestService_UnauthorizedUpdatesAreIgnoredAndNaturalTextUsesPlan(t *testing.T) {
	fixture := newTelegramFixture()
	if err := fixture.service.Handle(context.Background(), messageUpdate(20, 999, -303, "/projects"), "polling"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.gateway.messages) != 0 || len(fixture.store.updates) != 0 {
		t.Fatalf("unauthorized update changed state: messages=%d updates=%d", len(fixture.gateway.messages), len(fixture.store.updates))
	}
	if err := fixture.service.Handle(context.Background(), messageUpdate(21, 101, -303,
		"Добавь возможность публиковать только проверенные уроки"), "polling"); err != nil {
		t.Fatal(err)
	}
	if message := lastMessage(fixture.gateway); !strings.Contains(message, "Plan ") {
		t.Fatalf("natural text response = %q", message)
	}
}

func TestService_DoesNotLeakAdapterErrorsOrCredentials(t *testing.T) {
	fixture := newTelegramFixture()
	fixture.service.Ops.ListProjects = listProjectsStub{err: errors.New("token=super-secret PASSWORD=hunter2 full .env")}
	if err := fixture.service.Handle(context.Background(), messageUpdate(30, 101, -303, "/projects"), "polling"); err != nil {
		t.Fatal(err)
	}
	message := lastMessage(fixture.gateway)
	for _, forbidden := range []string{"super-secret", "hunter2", ".env"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("failure leaked %q in %q", forbidden, message)
		}
	}
}

func messageUpdate(updateID, userID, chatID int64, text string) domain.TelegramUpdate {
	return domain.TelegramUpdate{UpdateID: updateID, Message: &domain.TelegramMessage{
		MessageID: updateID, From: domain.TelegramActor{ID: userID}, Chat: domain.TelegramChat{ID: chatID}, Text: text,
	}}
}

func callbackUpdate(updateID, userID, chatID int64, data string) domain.TelegramUpdate {
	return domain.TelegramUpdate{UpdateID: updateID, CallbackQuery: &domain.TelegramCallbackQuery{
		ID: fmt.Sprintf("callback-%d", updateID), From: domain.TelegramActor{ID: userID},
		Message: &domain.TelegramMessage{MessageID: updateID, Chat: domain.TelegramChat{ID: chatID}}, Data: data,
	}}
}

func lastMessage(gateway *fakeTelegramGateway) string {
	if len(gateway.messages) == 0 {
		return ""
	}
	return gateway.messages[len(gateway.messages)-1].Text
}
