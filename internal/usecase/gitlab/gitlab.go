package gitlab

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const maxWebhookBytes = 1 << 20

var eventUUIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

type Sync struct {
	Plans          planGetter
	Projects       projectGetter
	TaskExecutions attemptLister
	Links          repository.GitLabLinkRepository
	Gateway        repository.GitLabGateway
	ControlProject string
}

type planGetter interface {
	GetPlan(context.Context, string) (domain.PlanBundle, error)
}

type projectGetter interface {
	Get(context.Context, string) (domain.Project, error)
}

type attemptLister interface {
	ListAttempts(context.Context, string) ([]domain.TaskAttempt, error)
}

func (u Sync) Handle(ctx context.Context, planID string) (domain.GitLabSyncResult, error) {
	if u.Gateway == nil || !u.Gateway.Configured() {
		return domain.GitLabSyncResult{}, fmt.Errorf("GitLab integration is not configured: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(u.ControlProject) == "" {
		return domain.GitLabSyncResult{}, fmt.Errorf("GitLab control project is not configured: %w", domain.ErrValidation)
	}
	bundle, err := u.Plans.GetPlan(ctx, strings.TrimSpace(planID))
	if err != nil {
		return domain.GitLabSyncResult{}, err
	}
	if !u.Gateway.DryRun() && (bundle.Approval == nil || bundle.Approval.Status != "approved") {
		return domain.GitLabSyncResult{}, fmt.Errorf("plan approval is required before GitLab writes: %w", domain.ErrApprovalNeeded)
	}
	controlProject, err := u.Gateway.ResolveProject(ctx, u.ControlProject)
	if err != nil {
		return domain.GitLabSyncResult{}, err
	}
	type resolvedTaskProject struct {
		project domain.Project
		gitLab  domain.GitLabProject
	}
	resolvedProjects := make(map[string]resolvedTaskProject, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		project, getErr := u.Projects.Get(ctx, task.ProjectID)
		if getErr != nil {
			return domain.GitLabSyncResult{}, getErr
		}
		gitLabProject, resolveErr := u.Gateway.ResolveConnectedProject(ctx, project)
		if resolveErr != nil {
			return domain.GitLabSyncResult{}, resolveErr
		}
		resolvedProjects[task.ID] = resolvedTaskProject{project: project, gitLab: gitLabProject}
	}
	planMarker := marker("plan", bundle.Plan.ID)
	planIssue, err := u.Gateway.EnsureIssue(ctx, domain.GitLabIssueSpec{
		Project: controlProject, Title: boundedTitle("[Plan] " + bundle.Plan.Summary),
		Description: planDescription(bundle, nil, planMarker), Labels: planLabels(bundle.Plan),
		IdempotencyKey: planMarker, State: issueStateForPlan(bundle.Plan.Status),
	})
	if err != nil {
		return domain.GitLabSyncResult{}, err
	}
	if !u.Gateway.DryRun() {
		if _, err := u.Links.SaveGitLabLink(ctx, issueLink(domain.GitLabResourcePlan, bundle.Plan.ID, planIssue)); err != nil {
			return domain.GitLabSyncResult{}, err
		}
	}

	items := make([]domain.GitLabSyncItem, 0, len(bundle.Tasks))
	childIssues := make(map[string]domain.GitLabIssue, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		resolved := resolvedProjects[task.ID]
		project := resolved.project
		gitLabProject := resolved.gitLab
		taskMarker := marker("task", task.ID)
		issue, issueErr := u.Gateway.EnsureIssue(ctx, domain.GitLabIssueSpec{
			Project: gitLabProject, Title: boundedTitle("[Task] " + task.Title),
			Description: taskDescription(task, planIssue, taskMarker), Labels: taskLabels(task),
			IdempotencyKey: taskMarker, State: issueStateForTask(task.Status),
		})
		if issueErr != nil {
			return domain.GitLabSyncResult{}, issueErr
		}
		childIssues[task.ID] = issue
		if !u.Gateway.DryRun() {
			if _, saveErr := u.Links.SaveGitLabLink(ctx, issueLink(domain.GitLabResourceTask, task.ID, issue)); saveErr != nil {
				return domain.GitLabSyncResult{}, saveErr
			}
		}
		if linkErr := u.Gateway.EnsureIssueLink(ctx, planIssue, issue); linkErr != nil {
			return domain.GitLabSyncResult{}, linkErr
		}
		statusMarker := marker("task-status", task.ID+":"+string(task.Status))
		if commentErr := u.Gateway.EnsureComment(ctx, issue,
			statusMarker+"\nOrchestrator status: `"+string(task.Status)+"`.", statusMarker); commentErr != nil {
			return domain.GitLabSyncResult{}, commentErr
		}
		item := domain.GitLabSyncItem{
			ResourceType: domain.GitLabResourceTask, ResourceID: task.ID, Issue: issue, Action: "issue_synced",
		}
		mergeRequest, mrErr := u.syncTaskMergeRequest(ctx, task, project, gitLabProject, issue)
		if mrErr != nil {
			return domain.GitLabSyncResult{}, mrErr
		}
		if mergeRequest != nil {
			item.MergeRequest = mergeRequest
			item.Action = "merge_request_synced"
			if !u.Gateway.DryRun() {
				link := issueLink(domain.GitLabResourceTask, task.ID, issue)
				link.MergeRequestIID = &mergeRequest.IID
				link.URL = mergeRequest.WebURL
				link.ExternalState = mergeRequest.State
				link.IssueState = issue.State
				link.MergeRequestState = mergeRequest.State
				if _, saveErr := u.Links.SaveGitLabLink(ctx, link); saveErr != nil {
					return domain.GitLabSyncResult{}, saveErr
				}
			}
		}
		items = append(items, item)
	}

	planIssue, err = u.Gateway.EnsureIssue(ctx, domain.GitLabIssueSpec{
		Project: controlProject, Title: boundedTitle("[Plan] " + bundle.Plan.Summary),
		Description: planDescription(bundle, childIssues, planMarker), Labels: planLabels(bundle.Plan),
		IdempotencyKey: planMarker, State: issueStateForPlan(bundle.Plan.Status),
	})
	if err != nil {
		return domain.GitLabSyncResult{}, err
	}
	planStatusMarker := marker("plan-status", bundle.Plan.ID+":"+string(bundle.Plan.Status))
	if err := u.Gateway.EnsureComment(ctx, planIssue,
		planStatusMarker+"\nOrchestrator plan status: `"+string(bundle.Plan.Status)+"`.", planStatusMarker); err != nil {
		return domain.GitLabSyncResult{}, err
	}
	return domain.GitLabSyncResult{
		PlanID: bundle.Plan.ID, DryRun: u.Gateway.DryRun(), PlanIssue: planIssue,
		Items: items, SyncedAt: time.Now().UTC(),
	}, nil
}

func (u Sync) syncTaskMergeRequest(
	ctx context.Context,
	task domain.Task,
	project domain.Project,
	gitLabProject domain.GitLabProject,
	issue domain.GitLabIssue,
) (*domain.GitLabMergeRequest, error) {
	if task.Status != domain.TaskStatusCompleted || u.TaskExecutions == nil {
		return nil, nil
	}
	attempts, err := u.TaskExecutions.ListAttempts(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	var completed *domain.TaskAttempt
	for i := range attempts {
		attempt := &attempts[i]
		if attempt.Status == domain.TaskAttemptStatusCompleted && attempt.CommitSHA != nil &&
			strings.TrimSpace(*attempt.CommitSHA) != "" && attempt.WorktreePath != "" && attempt.BranchName != "" {
			completed = attempt
			break
		}
	}
	if completed == nil {
		return nil, nil
	}
	if err := u.Gateway.PushBranch(ctx, project, completed.WorktreePath, completed.BranchName); err != nil {
		return nil, err
	}
	targetBranch := strings.TrimSpace(project.DefaultBranch)
	if targetBranch == "" {
		targetBranch = "main"
	}
	mrMarker := marker("merge-request", task.ID)
	description := strings.Join([]string{
		mrMarker,
		"Implements orchestrator task [#" + strconv.FormatInt(issue.IID, 10) + "](" + issue.WebURL + ").",
		"",
		"Source commit: `" + *completed.CommitSHA + "`",
		"",
		"This merge request requires owner review. The orchestrator never merges or deploys it.",
	}, "\n")
	mergeRequest, err := u.Gateway.EnsureMergeRequest(ctx, domain.GitLabMergeRequestSpec{
		Project: gitLabProject, SourceBranch: completed.BranchName, TargetBranch: targetBranch,
		Title: boundedTitle(task.Title), Description: description,
		Labels: []string{"orchestrator", "orchestrator::task", "status::review"}, IdempotencyKey: mrMarker,
	})
	if err != nil {
		return nil, err
	}
	commentMarker := marker("task-merge-request", task.ID+":"+strconv.FormatInt(mergeRequest.IID, 10))
	if err := u.Gateway.EnsureComment(ctx, issue,
		commentMarker+"\nMerge request: "+mergeRequest.WebURL, commentMarker); err != nil {
		return nil, err
	}
	return &mergeRequest, nil
}

type Links struct {
	Links repository.GitLabLinkRepository
	Plans planGetter
}

func (u Links) Handle(ctx context.Context, planID string) ([]domain.GitLabLink, error) {
	if strings.TrimSpace(planID) == "" {
		return nil, fmt.Errorf("plan ID is required: %w", domain.ErrValidation)
	}
	if _, err := u.Plans.GetPlan(ctx, planID); err != nil {
		return nil, err
	}
	return u.Links.ListGitLabLinksForPlan(ctx, planID)
}

type WebhookInput struct {
	Token     string
	MessageID string
	Timestamp string
	Signature string
	EventUUID string
	EventType string
	Body      []byte
}

type ProcessWebhook struct {
	Secret       string
	SigningToken string
	Links        repository.GitLabLinkRepository
	Now          func() time.Time
}

func (u ProcessWebhook) Handle(ctx context.Context, input WebhookInput) (domain.GitLabWebhookResult, error) {
	if err := u.authenticate(input); err != nil {
		return domain.GitLabWebhookResult{}, err
	}
	if !eventUUIDPattern.MatchString(strings.TrimSpace(input.EventUUID)) {
		return domain.GitLabWebhookResult{}, fmt.Errorf("invalid GitLab event UUID: %w", domain.ErrValidation)
	}
	if len(input.Body) == 0 || len(input.Body) > maxWebhookBytes {
		return domain.GitLabWebhookResult{}, fmt.Errorf("invalid GitLab webhook body size: %w", domain.ErrValidation)
	}
	var payload webhookPayload
	decoder := json.NewDecoder(strings.NewReader(string(input.Body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		// GitLab payloads evolve. Decode known fields after proving the body is
		// valid JSON; unknown fields are intentionally ignored.
		if !json.Valid(input.Body) || json.Unmarshal(input.Body, &payload) != nil {
			return domain.GitLabWebhookResult{}, fmt.Errorf("decode GitLab webhook: %w", domain.ErrValidation)
		}
	}
	event, supported, err := normalizedWebhook(input, payload)
	if err != nil {
		return domain.GitLabWebhookResult{}, err
	}
	if !supported {
		return domain.GitLabWebhookResult{EventUUID: input.EventUUID, Status: "ignored"}, nil
	}
	return u.Links.ApplyGitLabWebhook(ctx, event)
}

func (u ProcessWebhook) authenticate(input WebhookInput) error {
	if strings.TrimSpace(input.Signature) != "" {
		token := strings.TrimSpace(u.SigningToken)
		if !strings.HasPrefix(token, "whsec_") || !eventUUIDPattern.MatchString(strings.TrimSpace(input.MessageID)) {
			return fmt.Errorf("GitLab webhook signing is not configured: %w", domain.ErrForbidden)
		}
		if strings.TrimSpace(input.EventUUID) != strings.TrimSpace(input.MessageID) {
			return fmt.Errorf("GitLab webhook message ID mismatch: %w", domain.ErrForbidden)
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(token, "whsec_"))
		if err != nil || len(key) < 16 {
			return fmt.Errorf("invalid GitLab webhook signing configuration: %w", domain.ErrForbidden)
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(input.Timestamp), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid GitLab webhook timestamp: %w", domain.ErrForbidden)
		}
		now := time.Now().UTC()
		if u.Now != nil {
			now = u.Now().UTC()
		}
		delta := now.Sub(time.Unix(seconds, 0).UTC())
		if delta < 0 {
			delta = -delta
		}
		if delta > 5*time.Minute {
			return fmt.Errorf("stale GitLab webhook timestamp: %w", domain.ErrForbidden)
		}
		message := input.MessageID + "." + input.Timestamp + "." + string(input.Body)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(message))
		expected := []byte("v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		for _, candidate := range strings.Fields(input.Signature) {
			if hmac.Equal(expected, []byte(candidate)) {
				return nil
			}
		}
		return fmt.Errorf("invalid GitLab webhook signature: %w", domain.ErrForbidden)
	}
	secret := strings.TrimSpace(u.Secret)
	if len(secret) < 16 || subtle.ConstantTimeCompare([]byte(input.Token), []byte(secret)) != 1 {
		return fmt.Errorf("invalid GitLab webhook token: %w", domain.ErrForbidden)
	}
	return nil
}

func normalizedWebhook(input WebhookInput, payload webhookPayload) (domain.GitLabWebhookEvent, bool, error) {
	kind := strings.TrimSpace(payload.ObjectKind)
	expectedEventType := ""
	objectIID := payload.ObjectAttributes.IID
	state := strings.ToLower(strings.TrimSpace(payload.ObjectAttributes.State))
	pipelineStatus := ""
	switch kind {
	case "issue":
		expectedEventType = "Issue Hook"
	case "merge_request":
		expectedEventType = "Merge Request Hook"
	case "pipeline":
		expectedEventType = "Pipeline Hook"
		pipelineStatus = strings.ToLower(strings.TrimSpace(payload.ObjectAttributes.Status))
		if len(payload.MergeRequests) == 0 || payload.MergeRequests[0].IID <= 0 {
			return domain.GitLabWebhookEvent{}, false, nil
		}
		objectIID = payload.MergeRequests[0].IID
		state = "unknown"
	default:
		return domain.GitLabWebhookEvent{}, false, nil
	}
	if strings.TrimSpace(input.EventType) != expectedEventType {
		return domain.GitLabWebhookEvent{}, false, fmt.Errorf("GitLab event header does not match payload: %w", domain.ErrValidation)
	}
	checksum := sha256.Sum256(input.Body)
	event := domain.GitLabWebhookEvent{
		EventUUID: strings.TrimSpace(input.EventUUID), EventType: expectedEventType, ObjectKind: kind,
		GitLabProjectID: payload.Project.ID, ObjectIID: objectIID, ExternalState: state,
		PipelineStatus: pipelineStatus, SourceBranch: strings.TrimSpace(payload.ObjectAttributes.SourceBranch),
		PayloadChecksum: hex.EncodeToString(checksum[:]), ReceivedAt: time.Now().UTC(),
	}
	return event, true, nil
}

type webhookPayload struct {
	ObjectKind string `json:"object_kind"`
	Project    struct {
		ID int64 `json:"id"`
	} `json:"project"`
	ObjectAttributes struct {
		IID          int64  `json:"iid"`
		State        string `json:"state"`
		Status       string `json:"status"`
		SourceBranch string `json:"source_branch"`
	} `json:"object_attributes"`
	MergeRequests []struct {
		IID int64 `json:"iid"`
	} `json:"merge_requests"`
}

func planDescription(bundle domain.PlanBundle, children map[string]domain.GitLabIssue, idempotencyKey string) string {
	lines := []string{idempotencyKey, bundle.Plan.Summary, "", "## Tasks"}
	for _, task := range bundle.Tasks {
		checked := " "
		if task.Status == domain.TaskStatusCompleted || task.Status == domain.TaskStatusCancelled {
			checked = "x"
		}
		title := task.Title
		if issue, ok := children[task.ID]; ok {
			title = "[" + title + "](" + issue.WebURL + ")"
		}
		lines = append(lines, "- ["+checked+"] "+title+" — `"+string(task.Status)+"`")
	}
	lines = append(lines, "", "Risk: `"+bundle.Plan.RiskLevel+"`", "", "Owner approval remains authoritative in the orchestrator.")
	return boundedText(strings.Join(lines, "\n"))
}

func taskDescription(task domain.Task, planIssue domain.GitLabIssue, idempotencyKey string) string {
	lines := []string{
		idempotencyKey,
		"Parent plan: [#" + strconv.FormatInt(planIssue.IID, 10) + "](" + planIssue.WebURL + ")",
		"", task.Description, "", "## Acceptance criteria",
	}
	checked := " "
	if task.Status == domain.TaskStatusCompleted {
		checked = "x"
	}
	for _, criterion := range task.AcceptanceCriteria {
		lines = append(lines, "- ["+checked+"] "+criterion)
	}
	lines = append(lines, "", "Write scope: `"+strings.Join(task.WriteScope, "`, `")+"`")
	return boundedText(strings.Join(lines, "\n"))
}

func planLabels(plan domain.Plan) []string {
	return stableLabels("orchestrator", "orchestrator::plan", "status::"+string(plan.Status), "risk::"+plan.RiskLevel)
}

func taskLabels(task domain.Task) []string {
	return stableLabels("orchestrator", "orchestrator::task", "status::"+string(task.Status), "risk::"+string(task.RiskLevel))
}

func stableLabels(values ...string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func issueLink(resourceType, resourceID string, issue domain.GitLabIssue) domain.GitLabLink {
	iid := issue.IID
	return domain.GitLabLink{
		ResourceType: resourceType, ResourceID: resourceID, GitLabProjectID: issue.ProjectID,
		IssueIID: &iid, URL: issue.WebURL, ExternalState: issue.State, IssueState: issue.State,
	}
}

func marker(kind, id string) string {
	return "<!-- course-dev-orchestrator:" + kind + ":" + id + " -->"
}

func issueStateForPlan(status domain.PlanStatus) string {
	switch status {
	case domain.PlanStatusCompleted, domain.PlanStatusCancelled:
		return "closed"
	default:
		return "opened"
	}
}

func issueStateForTask(status domain.TaskStatus) string {
	switch status {
	case domain.TaskStatusCompleted, domain.TaskStatusCancelled:
		return "closed"
	default:
		return "opened"
	}
}

func boundedTitle(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if len(value) > 255 {
		return truncateUTF8(value, 255, "...")
	}
	return value
}

func boundedText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128<<10 {
		return truncateUTF8(value, 128<<10, "\n\n[truncated]")
	}
	return value
}

func truncateUTF8(value string, maxBytes int, suffix string) string {
	limit := maxBytes - len(suffix)
	if limit < 0 {
		return ""
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit] + suffix
}
