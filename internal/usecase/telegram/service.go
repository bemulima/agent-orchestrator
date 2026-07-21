package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
	planninguc "github.com/bemulima/agent-orchestrator/internal/usecase/planning"
	projectuc "github.com/bemulima/agent-orchestrator/internal/usecase/project"
)

const maxCommandBytes = 4096

var uuidInTextPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}`)
var credentialPattern = regexp.MustCompile(`(?i)\b(token|secret|password|api[_-]?key)\s*[:=]\s*[^\s]+`)
var telegramTokenPattern = regexp.MustCompile(`\b[0-9]{5,20}:[A-Za-z0-9_-]{20,}\b`)
var environmentLinePattern = regexp.MustCompile(`(?m)^[A-Z][A-Z0-9_]{2,}=.*$`)

type connectProject interface {
	Handle(context.Context, projectuc.ConnectInput) (projectuc.ConnectResult, error)
}

type getProject interface {
	Handle(context.Context, string) (domain.Project, error)
}

type listProjects interface {
	Handle(context.Context) ([]domain.Project, error)
}

type scanProject interface {
	Handle(context.Context, string) (projectuc.ScanResult, error)
}

type rebuildTopology interface {
	Handle(context.Context) (domain.TopologyCatalog, error)
}

type createCommand interface {
	Handle(context.Context, planninguc.CreateCommandInput) (domain.Command, error)
}

type createPlan interface {
	Handle(context.Context, string, domain.PlanRequest) (domain.PlanBundle, error)
}

type getPlan interface {
	Handle(context.Context, string) (domain.PlanBundle, error)
}

type decidePlan interface {
	Handle(context.Context, planninguc.DecidePlanInput) (domain.PlanBundle, error)
}

type getRun interface {
	Handle(context.Context, string) (domain.PlanRun, error)
}

type controlRun interface {
	Handle(context.Context, planninguc.ControlRunInput) (domain.PlanRun, error)
}

type getTask interface {
	Handle(context.Context, string) (domain.Task, error)
}

type taskAction interface {
	Handle(context.Context, string) (domain.Task, error)
}

type getOnboarding interface {
	Handle(context.Context, string) (domain.OnboardingRun, error)
}

type decideOnboarding interface {
	Handle(context.Context, onboardinguc.DecideInput) (domain.OnboardingRun, error)
}

type listGitLabLinks interface {
	Handle(context.Context, string) ([]domain.GitLabLink, error)
}

type Operations struct {
	ConnectProject  connectProject
	GetProject      getProject
	ListProjects    listProjects
	ScanProject     scanProject
	RebuildTopology rebuildTopology

	CreateCommand createCommand
	CreatePlan    createPlan
	GetPlan       getPlan
	ApprovePlan   decidePlan
	RejectPlan    decidePlan
	GetRun        getRun
	ControlRun    controlRun
	GetTask       getTask
	RetryTask     taskAction
	CancelTask    taskAction

	GetOnboarding     getOnboarding
	ApproveOnboarding decideOnboarding
	RejectOnboarding  decideOnboarding
	GitLabLinks       listGitLabLinks
}

type Service struct {
	Store   repository.TelegramRepository
	Gateway repository.TelegramGateway
	Ops     Operations
	Users   map[int64]struct{}
	Chats   map[int64]struct{}
	TTL     time.Duration
	Now     func() time.Time
	Random  io.Reader
}

func NewService(
	store repository.TelegramRepository,
	gateway repository.TelegramGateway,
	operations Operations,
	allowedUsers, allowedChats []int64,
	callbackTTL time.Duration,
) *Service {
	users := make(map[int64]struct{}, len(allowedUsers))
	for _, id := range allowedUsers {
		users[id] = struct{}{}
	}
	chats := make(map[int64]struct{}, len(allowedChats))
	for _, id := range allowedChats {
		chats[id] = struct{}{}
	}
	return &Service{
		Store: store, Gateway: gateway, Ops: operations, Users: users, Chats: chats,
		TTL: callbackTTL, Now: time.Now, Random: rand.Reader,
	}
}

func (s *Service) Handle(ctx context.Context, update domain.TelegramUpdate, source string) error {
	userID, chatID, ok := updateIdentity(update)
	if !ok || update.UpdateID < 0 {
		return nil
	}
	callbackID := ""
	if update.CallbackQuery != nil {
		callbackID = update.CallbackQuery.ID
	}
	if !s.authorized(userID, chatID) {
		if callbackID != "" && s.Gateway != nil {
			_ = s.Gateway.AnswerCallback(ctx, callbackID, "Действие недоступно.", true)
		}
		return nil
	}
	if source != "polling" && source != "webhook" {
		return fmt.Errorf("unknown Telegram update source: %w", domain.ErrValidation)
	}
	if s.Store == nil || s.Gateway == nil {
		return fmt.Errorf("Telegram service is not configured")
	}
	callbackAnswered := false
	if callbackID != "" {
		defer func() {
			if !callbackAnswered {
				_ = s.Gateway.AnswerCallback(ctx, callbackID, "Не выполнено.", true)
			}
		}()
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("encode Telegram update: %w", err)
	}
	hash := sha256.Sum256(raw)
	now := s.now()
	claimed, err := s.Store.BeginUpdate(ctx, domain.TelegramUpdateReceipt{
		UpdateID: update.UpdateID, Source: source, Checksum: hex.EncodeToString(hash[:]),
		TelegramUserID: &userID, TelegramChatID: &chatID,
		Status: domain.TelegramUpdateStatusReceived, ReceivedAt: now,
	})
	if err != nil {
		return err
	}
	if !claimed {
		if callbackID != "" {
			if s.Gateway.AnswerCallback(ctx, callbackID, "Уже обработано.", false) == nil {
				callbackAnswered = true
			}
		}
		return nil
	}

	var outgoing domain.TelegramOutgoingMessage
	if update.CallbackQuery != nil {
		outgoing, err = s.processCallback(ctx, *update.CallbackQuery, userID, chatID)
	} else {
		outgoing, err = s.processMessage(ctx, *update.Message, update.UpdateID)
	}
	status := domain.TelegramUpdateStatusProcessed
	callbackAnswer := "Готово."
	callbackAlert := false
	if err != nil {
		status = domain.TelegramUpdateStatusFailed
		outgoing = domain.TelegramOutgoingMessage{ChatID: chatID, Text: safeFailure(err)}
		callbackAnswer = "Не выполнено."
		callbackAlert = true
	}
	if finishErr := s.Store.FinishUpdate(ctx, update.UpdateID, status); finishErr != nil {
		return finishErr
	}
	if strings.TrimSpace(outgoing.Text) != "" {
		outgoing.Text = bounded(sanitize(outgoing.Text), 4000)
		if sendErr := s.Gateway.SendMessage(ctx, outgoing); sendErr != nil {
			_ = s.Store.FinishUpdate(ctx, update.UpdateID, domain.TelegramUpdateStatusFailed)
			if callbackID != "" {
				if s.Gateway.AnswerCallback(ctx, callbackID, "Ответ не доставлен.", true) == nil {
					callbackAnswered = true
				}
			}
			return sendErr
		}
	}
	if callbackID != "" {
		if answerErr := s.Gateway.AnswerCallback(ctx, callbackID, callbackAnswer, callbackAlert); answerErr != nil {
			return answerErr
		}
		callbackAnswered = true
	}
	return nil
}

func (s *Service) processMessage(ctx context.Context, message domain.TelegramMessage, updateID int64) (domain.TelegramOutgoingMessage, error) {
	text := strings.TrimSpace(message.Text)
	if text == "" || len(text) > maxCommandBytes || !utf8.ValidString(text) {
		return domain.TelegramOutgoingMessage{}, fmt.Errorf("invalid Telegram command: %w", domain.ErrValidation)
	}
	if !strings.HasPrefix(text, "/") {
		text = naturalCommand(text)
	}
	command, rest := splitCommand(text)
	switch command {
	case "/start":
		return plain(message.Chat.ID, "Оркестратор готов. Все изменяющие действия требуют одноразовой inline-кнопки.\n\n"+helpText()), nil
	case "/help":
		return plain(message.Chat.ID, helpText()), nil
	case "/projects":
		return s.projects(ctx, message.Chat.ID)
	case "/connect":
		return s.connect(ctx, message.Chat.ID, rest)
	case "/analyze":
		return s.analyze(ctx, message.Chat.ID, rest)
	case "/topology":
		return s.topology(ctx, message.Chat.ID, rest)
	case "/plan":
		return s.plan(ctx, message, rest, updateID)
	case "/status":
		return s.status(ctx, message.Chat.ID, rest)
	case "/approve", "/reject", "/pause", "/resume", "/retry", "/cancel":
		return s.requestAction(ctx, message, strings.TrimPrefix(command, "/"), rest)
	case "/issues":
		return s.issues(ctx, message.Chat.ID, rest)
	default:
		return plain(message.Chat.ID, "Неизвестная команда.\n\n"+helpText()), nil
	}
}

func (s *Service) processCallback(
	ctx context.Context,
	query domain.TelegramCallbackQuery,
	userID, chatID int64,
) (domain.TelegramOutgoingMessage, error) {
	if !strings.HasPrefix(query.Data, "tg:") || len(query.Data) > 64 {
		return domain.TelegramOutgoingMessage{}, fmt.Errorf("invalid callback data: %w", domain.ErrValidation)
	}
	hash := sha256.Sum256([]byte(query.Data))
	grant, err := s.Store.ConsumeCallback(ctx, hex.EncodeToString(hash[:]), userID, chatID, s.now())
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	actor := fmt.Sprintf("telegram:%d", userID)
	switch grant.Action {
	case "approve":
		if grant.ResourceType == "plan" {
			bundle, err := s.Ops.ApprovePlan.Handle(ctx, planninguc.DecidePlanInput{PlanID: grant.ResourceID, Actor: actor})
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, planSummary(bundle)), nil
		}
		if grant.ResourceType == "onboarding_run" {
			run, err := s.Ops.ApproveOnboarding.Handle(ctx, onboardinguc.DecideInput{RunID: grant.ResourceID, Actor: actor})
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, fmt.Sprintf("Onboarding %s: %s.", shortID(run.ID), run.Status)), nil
		}
	case "reject":
		if grant.ResourceType == "plan" {
			bundle, err := s.Ops.RejectPlan.Handle(ctx, planninguc.DecidePlanInput{PlanID: grant.ResourceID, Actor: actor})
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, planSummary(bundle)), nil
		}
		if grant.ResourceType == "onboarding_run" {
			run, err := s.Ops.RejectOnboarding.Handle(ctx, onboardinguc.DecideInput{RunID: grant.ResourceID, Actor: actor})
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, fmt.Sprintf("Onboarding %s: %s.", shortID(run.ID), run.Status)), nil
		}
	case "show_tasks":
		bundle, err := s.Ops.GetPlan.Handle(ctx, grant.ResourceID)
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, tasksSummary(bundle)), nil
	case "change":
		return plain(chatID, "Отправьте уточнённое требование командой /plan <текст>. Исходный план не изменён."), nil
	case "pause", "resume":
		action := domain.RunControlPause
		if grant.Action == "resume" {
			action = domain.RunControlResume
		}
		run, err := s.Ops.ControlRun.Handle(ctx, planninguc.ControlRunInput{RunID: grant.ResourceID, Action: action})
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, fmt.Sprintf("Run %s: запрос %s принят (текущий статус: %s).", shortID(run.ID), grant.Action, run.Status)), nil
	case "retry":
		task, err := s.Ops.RetryTask.Handle(ctx, grant.ResourceID)
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, fmt.Sprintf("Task %s: retry запрошен (статус: %s).", shortID(task.ID), task.Status)), nil
	case "cancel":
		if grant.ResourceType == "run" {
			run, err := s.Ops.ControlRun.Handle(ctx, planninguc.ControlRunInput{RunID: grant.ResourceID, Action: domain.RunControlCancel})
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, fmt.Sprintf("Run %s: отмена запрошена (статус: %s).", shortID(run.ID), run.Status)), nil
		}
		if grant.ResourceType == "task" {
			task, err := s.Ops.CancelTask.Handle(ctx, grant.ResourceID)
			if err != nil {
				return domain.TelegramOutgoingMessage{}, err
			}
			return plain(chatID, fmt.Sprintf("Task %s: отмена запрошена (статус: %s).", shortID(task.ID), task.Status)), nil
		}
	}
	return domain.TelegramOutgoingMessage{}, fmt.Errorf("unsupported callback target: %w", domain.ErrValidation)
}

func (s *Service) projects(ctx context.Context, chatID int64) (domain.TelegramOutgoingMessage, error) {
	projects, err := s.Ops.ListProjects.Handle(ctx)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	if len(projects) == 0 {
		return plain(chatID, "Подключённых проектов пока нет."), nil
	}
	lines := []string{fmt.Sprintf("Проекты (%d):", len(projects))}
	for i, project := range projects {
		if i == 20 {
			lines = append(lines, fmt.Sprintf("…и ещё %d.", len(projects)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("• %s — %s, %s [%s]", bounded(project.Name, 80), project.Status, project.RepositoryRole, shortID(project.ID)))
	}
	return plain(chatID, strings.Join(lines, "\n")), nil
}

func (s *Service) connect(ctx context.Context, chatID int64, rest string) (domain.TelegramOutgoingMessage, error) {
	fields := strings.Fields(rest)
	if len(fields) < 1 || len(fields) > 2 {
		return plain(chatID, "Использование: /connect <absolute-path|git-url> [role]"), nil
	}
	input := projectuc.ConnectInput{RepositoryRole: domain.RepositoryRoleService}
	if len(fields) == 2 {
		input.RepositoryRole = domain.RepositoryRole(fields[1])
	}
	if strings.Contains(fields[0], "://") || strings.HasPrefix(fields[0], "git@") {
		input.GitURL = fields[0]
	} else {
		input.LocalPath = fields[0]
	}
	result, err := s.Ops.ConnectProject.Handle(ctx, input)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	return plain(chatID, fmt.Sprintf("Проект %s подключён и проанализирован: %s, %s [%s].",
		bounded(result.Project.Name, 100), result.Snapshot.ServiceKind, result.Project.Status, shortID(result.Project.ID))), nil
}

func (s *Service) analyze(ctx context.Context, chatID int64, rest string) (domain.TelegramOutgoingMessage, error) {
	if len(strings.Fields(rest)) != 1 {
		return plain(chatID, "Использование: /analyze <project-id-or-name>"), nil
	}
	project, err := s.Ops.GetProject.Handle(ctx, rest)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	result, err := s.Ops.ScanProject.Handle(ctx, project.ID)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	return plain(chatID, fmt.Sprintf("Анализ %s завершён: %s, %s, confidence %.2f.",
		bounded(project.Name, 100), result.Snapshot.ServiceKind, result.Snapshot.Language, result.Snapshot.Confidence)), nil
}

func (s *Service) topology(ctx context.Context, chatID int64, rest string) (domain.TelegramOutgoingMessage, error) {
	if strings.TrimSpace(rest) != "" {
		return plain(chatID, "Использование: /topology"), nil
	}
	catalog, err := s.Ops.RebuildTopology.Handle(ctx)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	return plain(chatID, fmt.Sprintf("Topology %s: проектов %d, сервисов %d, связей %d, контрактов %d, drift %d.",
		shortID(catalog.Revision.ID), catalog.Revision.ProjectCount, len(catalog.Services), len(catalog.Relations),
		len(catalog.Contracts), len(catalog.Drifts))), nil
}

func (s *Service) plan(ctx context.Context, message domain.TelegramMessage, rest string, updateID int64) (domain.TelegramOutgoingMessage, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return plain(message.Chat.ID, "Использование: /plan <описание изменения>"), nil
	}
	user := fmt.Sprintf("%d", message.From.ID)
	command, err := s.Ops.CreateCommand.Handle(ctx, planninguc.CreateCommandInput{
		Source: domain.CommandSourceTelegram, SourceUserID: &user, Text: rest,
		IdempotencyKey: fmt.Sprintf("telegram:update:%d", updateID),
	})
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	bundle, err := s.Ops.CreatePlan.Handle(ctx, command.ID, domain.PlanRequest{})
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	keyboard, err := s.planKeyboard(ctx, bundle.Plan.ID, message.From.ID, message.Chat.ID)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	result := plain(message.Chat.ID, planSummary(bundle))
	result.ReplyMarkup = keyboard
	return result, nil
}

func (s *Service) status(ctx context.Context, chatID int64, rest string) (domain.TelegramOutgoingMessage, error) {
	fields := strings.Fields(rest)
	if len(fields) != 2 || !validUUID(fields[1]) {
		return plain(chatID, "Использование: /status <plan|run|task|onboarding> <uuid>"), nil
	}
	switch fields[0] {
	case "plan":
		bundle, err := s.Ops.GetPlan.Handle(ctx, fields[1])
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, planSummary(bundle)), nil
	case "run":
		run, err := s.Ops.GetRun.Handle(ctx, fields[1])
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, fmt.Sprintf("Run %s: %s; plan %s.", shortID(run.ID), run.Status, shortID(run.PlanID))), nil
	case "task":
		task, err := s.Ops.GetTask.Handle(ctx, fields[1])
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, fmt.Sprintf("Task %s: %s. %s\nPlan: %s; role: %s; risk: %s.",
			shortID(task.ID), task.Status, bounded(task.Title, 180), shortID(task.PlanID), task.Role, task.RiskLevel)), nil
	case "onboarding":
		run, err := s.Ops.GetOnboarding.Handle(ctx, fields[1])
		if err != nil {
			return domain.TelegramOutgoingMessage{}, err
		}
		return plain(chatID, fmt.Sprintf("Onboarding %s: %s; project %s; dry-run: %t.",
			shortID(run.ID), run.Status, shortID(run.ProjectID), run.DryRun)), nil
	default:
		return plain(chatID, "Использование: /status <plan|run|task|onboarding> <uuid>"), nil
	}
}

func (s *Service) requestAction(
	ctx context.Context,
	message domain.TelegramMessage,
	action, rest string,
) (domain.TelegramOutgoingMessage, error) {
	fields := strings.Fields(rest)
	resourceType := ""
	resourceID := ""
	switch action {
	case "approve", "reject":
		if len(fields) == 2 && (fields[0] == "plan" || fields[0] == "onboarding") {
			resourceType, resourceID = fields[0], fields[1]
			if resourceType == "onboarding" {
				resourceType = "onboarding_run"
			}
		}
	case "pause", "resume":
		if len(fields) == 1 {
			resourceType, resourceID = "run", fields[0]
		}
	case "retry":
		if len(fields) == 1 {
			resourceType, resourceID = "task", fields[0]
		}
	case "cancel":
		if len(fields) == 2 && (fields[0] == "run" || fields[0] == "task") {
			resourceType, resourceID = fields[0], fields[1]
		}
	}
	if resourceType == "" || !validUUID(resourceID) {
		return plain(message.Chat.ID, actionUsage(action)), nil
	}
	if err := s.validateActionTarget(ctx, action, resourceType, resourceID); err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	button, err := s.callbackButton(ctx, actionLabel(action), action, resourceType, resourceID, message.From.ID, message.Chat.ID)
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	result := plain(message.Chat.ID, fmt.Sprintf("Текстом действие не выполняется. Подтвердите %s для %s %s одноразовой кнопкой.",
		action, resourceType, shortID(resourceID)))
	result.ReplyMarkup = &domain.TelegramInlineKeyboard{InlineKeyboard: [][]domain.TelegramInlineButton{{button}}}
	return result, nil
}

func (s *Service) validateActionTarget(ctx context.Context, action, resourceType, resourceID string) error {
	switch resourceType {
	case "plan":
		_, err := s.Ops.GetPlan.Handle(ctx, resourceID)
		return err
	case "onboarding_run":
		_, err := s.Ops.GetOnboarding.Handle(ctx, resourceID)
		return err
	case "run":
		_, err := s.Ops.GetRun.Handle(ctx, resourceID)
		return err
	case "task":
		_, err := s.Ops.GetTask.Handle(ctx, resourceID)
		return err
	default:
		return fmt.Errorf("unsupported action %s: %w", action, domain.ErrValidation)
	}
}

func (s *Service) issues(ctx context.Context, chatID int64, rest string) (domain.TelegramOutgoingMessage, error) {
	fields := strings.Fields(rest)
	if len(fields) != 1 || !validUUID(fields[0]) {
		return plain(chatID, "Использование: /issues <plan-uuid>"), nil
	}
	links, err := s.Ops.GitLabLinks.Handle(ctx, fields[0])
	if err != nil {
		return domain.TelegramOutgoingMessage{}, err
	}
	if len(links) == 0 {
		return plain(chatID, "Для плана ещё нет GitLab issue/MR."), nil
	}
	lines := []string{"GitLab:"}
	for i, link := range links {
		if i == 8 {
			lines = append(lines, fmt.Sprintf("…и ещё %d.", len(links)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("• %s %s — %s", link.ResourceType, shortID(link.ResourceID), bounded(link.URL, 500)))
	}
	return plain(chatID, strings.Join(lines, "\n")), nil
}

func (s *Service) planKeyboard(ctx context.Context, planID string, userID, chatID int64) (*domain.TelegramInlineKeyboard, error) {
	definitions := []struct{ label, action string }{
		{"Подтвердить", "approve"}, {"Показать задачи", "show_tasks"},
		{"Изменить", "change"}, {"Отклонить", "reject"},
	}
	buttons := make([]domain.TelegramInlineButton, 0, len(definitions))
	for _, definition := range definitions {
		button, err := s.callbackButton(ctx, definition.label, definition.action, "plan", planID, userID, chatID)
		if err != nil {
			return nil, err
		}
		buttons = append(buttons, button)
	}
	return &domain.TelegramInlineKeyboard{InlineKeyboard: [][]domain.TelegramInlineButton{
		{buttons[0], buttons[1]}, {buttons[2], buttons[3]},
	}}, nil
}

func (s *Service) callbackButton(
	ctx context.Context,
	label, action, resourceType, resourceID string,
	userID, chatID int64,
) (domain.TelegramInlineButton, error) {
	if s.TTL < time.Minute || s.TTL > time.Hour || s.Random == nil {
		return domain.TelegramInlineButton{}, fmt.Errorf("invalid Telegram callback configuration")
	}
	random := make([]byte, 24)
	if _, err := io.ReadFull(s.Random, random); err != nil {
		return domain.TelegramInlineButton{}, fmt.Errorf("create Telegram callback token: %w", err)
	}
	data := "tg:" + base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(data))
	now := s.now()
	err := s.Store.SaveCallback(ctx, domain.TelegramCallbackGrant{
		TokenHash: hex.EncodeToString(hash[:]), Action: action, ResourceType: resourceType,
		ResourceID: resourceID, TelegramUserID: userID, TelegramChatID: chatID,
		Status: domain.TelegramCallbackStatusPending, CreatedAt: now, ExpiresAt: now.Add(s.TTL),
	})
	if err != nil {
		return domain.TelegramInlineButton{}, err
	}
	return domain.TelegramInlineButton{Text: label, CallbackData: data}, nil
}

func (s *Service) authorized(userID, chatID int64) bool {
	_, userAllowed := s.Users[userID]
	_, chatAllowed := s.Chats[chatID]
	return userAllowed && chatAllowed
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

func updateIdentity(update domain.TelegramUpdate) (int64, int64, bool) {
	if update.Message != nil && update.CallbackQuery == nil {
		return update.Message.From.ID, update.Message.Chat.ID,
			update.Message.From.ID > 0 && !update.Message.From.IsBot && update.Message.Chat.ID != 0
	}
	if update.CallbackQuery != nil && update.Message == nil && update.CallbackQuery.Message != nil {
		return update.CallbackQuery.From.ID, update.CallbackQuery.Message.Chat.ID,
			update.CallbackQuery.From.ID > 0 && !update.CallbackQuery.From.IsBot && update.CallbackQuery.Message.Chat.ID != 0
	}
	return 0, 0, false
}

func splitCommand(text string) (string, string) {
	text = strings.TrimSpace(text)
	index := strings.IndexAny(text, " \t\r\n")
	command := text
	rest := ""
	if index >= 0 {
		command, rest = text[:index], strings.TrimSpace(text[index+1:])
	}
	if at := strings.Index(command, "@"); at > 0 {
		command = command[:at]
	}
	return strings.ToLower(command), rest
}

func naturalCommand(text string) string {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"подключи ", "подключить ", "connect "} {
		if strings.HasPrefix(lower, prefix) {
			return "/connect " + strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	for _, prefix := range []string{"проанализируй ", "проанализировать ", "analyze "} {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(trimmed[len(prefix):])
			if len(strings.Fields(rest)) == 1 {
				return "/analyze " + rest
			}
			return "/plan " + trimmed
		}
	}
	if strings.HasPrefix(lower, "почему ") {
		if id := uuidInTextPattern.FindString(trimmed); id != "" {
			return "/status task " + id
		}
	}
	return "/plan " + trimmed
}

func plain(chatID int64, text string) domain.TelegramOutgoingMessage {
	return domain.TelegramOutgoingMessage{ChatID: chatID, Text: bounded(sanitize(text), 4000)}
}

func sanitize(value string) string {
	value = credentialPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = telegramTokenPattern.ReplaceAllString(value, "[TELEGRAM_TOKEN_REDACTED]")
	return environmentLinePattern.ReplaceAllString(value, "[CONFIG REDACTED]")
}

func helpText() string {
	return strings.Join([]string{
		"Команды:",
		"/projects — список проектов",
		"/connect <path|git-url> [role] — подключить и проанализировать",
		"/analyze <project> — обновить анализ",
		"/topology — перестроить topology",
		"/plan <текст> — создать план",
		"/status <plan|run|task|onboarding> <uuid>",
		"/approve <plan|onboarding> <uuid>",
		"/reject <plan|onboarding> <uuid>",
		"/pause <run-uuid> · /resume <run-uuid>",
		"/retry <task-uuid> · /cancel <run|task> <uuid>",
		"/issues <plan-uuid> — ссылки GitLab",
		"/help — эта справка",
	}, "\n")
}

func actionUsage(action string) string {
	switch action {
	case "approve", "reject":
		return fmt.Sprintf("Использование: /%s <plan|onboarding> <uuid>", action)
	case "pause", "resume":
		return fmt.Sprintf("Использование: /%s <run-uuid>", action)
	case "retry":
		return "Использование: /retry <task-uuid>"
	default:
		return "Использование: /cancel <run|task> <uuid>"
	}
}

func actionLabel(action string) string {
	switch action {
	case "reject":
		return "Отклонить"
	case "pause":
		return "Приостановить"
	case "resume":
		return "Возобновить"
	case "retry":
		return "Повторить"
	case "cancel":
		return "Подтвердить отмену"
	default:
		return "Подтвердить"
	}
}

func planSummary(bundle domain.PlanBundle) string {
	run := "не запущен"
	if bundle.Run != nil {
		run = string(bundle.Run.Status)
	}
	return fmt.Sprintf("Plan %s: %s.\n%s\nRisk: %s; задач: %d; run: %s.",
		shortID(bundle.Plan.ID), bundle.Plan.Status, bounded(bundle.Plan.Summary, 500),
		bundle.Plan.RiskLevel, len(bundle.Tasks), run)
}

func tasksSummary(bundle domain.PlanBundle) string {
	lines := []string{fmt.Sprintf("Задачи плана %s (%d):", shortID(bundle.Plan.ID), len(bundle.Tasks))}
	for i, task := range bundle.Tasks {
		if i == 12 {
			lines = append(lines, fmt.Sprintf("…и ещё %d.", len(bundle.Tasks)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("• %s — %s: %s", shortID(task.ID), task.Status, bounded(task.Title, 140)))
	}
	return strings.Join(lines, "\n")
}

func safeFailure(err error) string {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return "Действие запрещено для этого пользователя или чата."
	case errors.Is(err, domain.ErrNotFound):
		return "Ресурс не найден. Проверьте тип и UUID."
	case errors.Is(err, domain.ErrConflict):
		return "Действие уже выполнено или конфликтует с текущим состоянием. Запросите свежий статус."
	case errors.Is(err, domain.ErrInvalidStatus):
		return "Действие устарело или недоступно в текущем состоянии. Запросите свежий статус."
	case errors.Is(err, domain.ErrValidation):
		return "Команда или параметры некорректны. Используйте /help."
	case errors.Is(err, domain.ErrApprovalNeeded):
		return "Сначала требуется подтверждение владельца inline-кнопкой."
	default:
		return "Операция не выполнена из-за внутренней ошибки. Подробности сохранены только в системной диагностике."
	}
}

func bounded(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	for max > 0 && !utf8.RuneStart(value[max]) {
		max--
	}
	return strings.TrimSpace(value[:max]) + "…"
}

func shortID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func validUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
