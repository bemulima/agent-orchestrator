package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type GitLabRepoPG struct {
	Pool *pgxpool.Pool
}

func (r GitLabRepoPG) GetGitLabLink(
	ctx context.Context,
	resourceType, resourceID string,
	projectID int64,
) (domain.GitLabLink, error) {
	link, err := scanGitLabLink(r.Pool.QueryRow(ctx, `
SELECT `+gitLabLinkColumns+`
FROM gitlab_link
WHERE resource_type = $1 AND resource_id = $2 AND gitlab_project_id = $3`,
		resourceType, resourceID, projectID))
	return link, mapGitLabError(err)
}

func (r GitLabRepoPG) SaveGitLabLink(ctx context.Context, link domain.GitLabLink) (domain.GitLabLink, error) {
	if !validGitLabResource(link.ResourceType) || strings.TrimSpace(link.ResourceID) == "" || link.GitLabProjectID <= 0 ||
		(link.IssueIID == nil && link.MergeRequestIID == nil) || strings.TrimSpace(link.URL) == "" {
		return domain.GitLabLink{}, fmt.Errorf("incomplete GitLab link: %w", domain.ErrValidation)
	}
	if link.IssueIID != nil && *link.IssueIID <= 0 || link.MergeRequestIID != nil && *link.MergeRequestIID <= 0 {
		return domain.GitLabLink{}, fmt.Errorf("invalid GitLab resource IID: %w", domain.ErrValidation)
	}
	parsedURL, err := url.Parse(strings.TrimSpace(link.URL))
	if err != nil || parsedURL.Hostname() == "" || parsedURL.User != nil || len(link.URL) > 4096 ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return domain.GitLabLink{}, fmt.Errorf("invalid GitLab link URL: %w", domain.ErrValidation)
	}
	if link.PipelineStatus != "" && !validPipelineStatus(link.PipelineStatus) {
		return domain.GitLabLink{}, fmt.Errorf("invalid GitLab pipeline status: %w", domain.ErrValidation)
	}
	if link.ExternalState == "" {
		link.ExternalState = "unknown"
	}
	if !validExternalState(link.ExternalState) {
		return domain.GitLabLink{}, fmt.Errorf("unsupported GitLab state %q: %w", link.ExternalState, domain.ErrValidation)
	}
	issueState := link.IssueState
	if issueState == "" {
		issueState = "unknown"
		if link.IssueIID != nil && link.MergeRequestIID == nil {
			issueState = link.ExternalState
		}
	}
	mergeRequestState := link.MergeRequestState
	if mergeRequestState == "" {
		mergeRequestState = "unknown"
		if link.MergeRequestIID != nil {
			mergeRequestState = link.ExternalState
		}
	}
	if (issueState != "unknown" && issueState != "opened" && issueState != "closed") ||
		!validExternalState(mergeRequestState) {
		return domain.GitLabLink{}, fmt.Errorf("unsupported GitLab resource state: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.GitLabLink{}, fmt.Errorf("begin GitLab link transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		"gitlab-link:"+link.ResourceType+":"+link.ResourceID); err != nil {
		return domain.GitLabLink{}, fmt.Errorf("lock GitLab link: %w", err)
	}
	stored, err := scanGitLabLink(tx.QueryRow(ctx, `
INSERT INTO gitlab_link (
    resource_type, resource_id, gitlab_project_id, issue_iid, merge_request_iid,
    url, external_state, issue_state, merge_request_state, pipeline_status, last_synced_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (resource_type, resource_id, gitlab_project_id)
DO UPDATE SET
    issue_iid = COALESCE(EXCLUDED.issue_iid, gitlab_link.issue_iid),
    merge_request_iid = COALESCE(EXCLUDED.merge_request_iid, gitlab_link.merge_request_iid),
    url = EXCLUDED.url,
    external_state = EXCLUDED.external_state,
    issue_state = CASE WHEN EXCLUDED.issue_state = 'unknown' THEN gitlab_link.issue_state ELSE EXCLUDED.issue_state END,
    merge_request_state = CASE WHEN EXCLUDED.merge_request_state = 'unknown' THEN gitlab_link.merge_request_state ELSE EXCLUDED.merge_request_state END,
    pipeline_status = CASE WHEN EXCLUDED.pipeline_status = '' THEN gitlab_link.pipeline_status ELSE EXCLUDED.pipeline_status END,
    last_synced_at = now(),
    updated_at = now()
RETURNING `+gitLabLinkColumns,
		link.ResourceType, link.ResourceID, link.GitLabProjectID, link.IssueIID,
		link.MergeRequestIID, link.URL, link.ExternalState, issueState, mergeRequestState, link.PipelineStatus))
	if err != nil {
		return domain.GitLabLink{}, mapGitLabError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, link.ResourceType, "gitlab.link_synced", link.ResourceID, map[string]any{
		"gitlab_project_id": stored.GitLabProjectID, "issue_iid": stored.IssueIID,
		"merge_request_iid": stored.MergeRequestIID, "external_state": stored.ExternalState,
	}); err != nil {
		return domain.GitLabLink{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.GitLabLink{}, fmt.Errorf("commit GitLab link: %w", err)
	}
	return stored, nil
}

func (r GitLabRepoPG) ListGitLabLinksForPlan(ctx context.Context, planID string) ([]domain.GitLabLink, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT `+gitLabLinkQualifiedColumns+`
FROM gitlab_link link
WHERE (link.resource_type = 'plan' AND link.resource_id = $1)
   OR (link.resource_type = 'task' AND link.resource_id IN (SELECT id FROM task WHERE plan_id = $1))
ORDER BY CASE link.resource_type WHEN 'plan' THEN 0 ELSE 1 END, link.resource_id`, planID)
	if err != nil {
		return nil, fmt.Errorf("list GitLab plan links: %w", err)
	}
	defer rows.Close()
	links := make([]domain.GitLabLink, 0)
	for rows.Next() {
		link, scanErr := scanGitLabLink(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan GitLab plan link: %w", scanErr)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate GitLab plan links: %w", err)
	}
	return links, nil
}

func (r GitLabRepoPG) ApplyGitLabWebhook(
	ctx context.Context,
	event domain.GitLabWebhookEvent,
) (domain.GitLabWebhookResult, error) {
	if err := validateGitLabWebhookEvent(event); err != nil {
		return domain.GitLabWebhookResult{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.GitLabWebhookResult{}, fmt.Errorf("begin GitLab webhook transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "gitlab-webhook:"+event.EventUUID); err != nil {
		return domain.GitLabWebhookResult{}, fmt.Errorf("lock GitLab webhook: %w", err)
	}
	var existingStatus string
	var existingLinkID *string
	err = tx.QueryRow(ctx, `
SELECT status, gitlab_link_id FROM gitlab_webhook_event WHERE event_uuid = $1`, event.EventUUID).
		Scan(&existingStatus, &existingLinkID)
	if err == nil {
		result := domain.GitLabWebhookResult{EventUUID: event.EventUUID, Status: existingStatus, Duplicate: true}
		if existingLinkID != nil {
			link, scanErr := scanGitLabLink(tx.QueryRow(ctx, `SELECT `+gitLabLinkColumns+` FROM gitlab_link WHERE id = $1`, *existingLinkID))
			if scanErr != nil {
				return domain.GitLabWebhookResult{}, fmt.Errorf("load duplicate GitLab webhook link: %w", scanErr)
			}
			result.Link = &link
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.GitLabWebhookResult{}, fmt.Errorf("commit duplicate GitLab webhook: %w", err)
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.GitLabWebhookResult{}, fmt.Errorf("find GitLab webhook event: %w", err)
	}

	lookupColumn := "issue_iid"
	if event.ObjectKind != "issue" {
		lookupColumn = "merge_request_iid"
	}
	link, err := scanGitLabLink(tx.QueryRow(ctx, `
SELECT `+gitLabLinkColumns+` FROM gitlab_link
WHERE gitlab_project_id = $1 AND `+lookupColumn+` = $2
FOR UPDATE`, event.GitLabProjectID, event.ObjectIID))
	status := "processed"
	var linkID *string
	if errors.Is(err, pgx.ErrNoRows) {
		status = "ignored"
	} else if err != nil {
		return domain.GitLabWebhookResult{}, fmt.Errorf("find GitLab webhook link: %w", err)
	} else {
		currentState := link.IssueState
		if event.ObjectKind == "merge_request" {
			currentState = link.MergeRequestState
		}
		if event.ObjectKind != "pipeline" && !validExternalTransition(currentState, event.ExternalState, event.ObjectKind) {
			return domain.GitLabWebhookResult{}, fmt.Errorf("GitLab %s state cannot transition from %s to %s: %w",
				event.ObjectKind, currentState, event.ExternalState, domain.ErrInvalidStatus)
		}
		if event.ObjectKind == "pipeline" {
			link.PipelineStatus = event.PipelineStatus
		} else {
			link.ExternalState = event.ExternalState
			if event.ObjectKind == "issue" {
				link.IssueState = event.ExternalState
			} else {
				link.MergeRequestState = event.ExternalState
			}
		}
		link, err = scanGitLabLink(tx.QueryRow(ctx, `
UPDATE gitlab_link SET
    external_state = $2,
    issue_state = $3,
    merge_request_state = $4,
    pipeline_status = CASE WHEN $5 = '' THEN pipeline_status ELSE $5 END,
    last_event_uuid = $6,
    last_synced_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING `+gitLabLinkColumns, link.ID, link.ExternalState, link.IssueState,
			link.MergeRequestState, event.PipelineStatus, event.EventUUID))
		if err != nil {
			return domain.GitLabWebhookResult{}, fmt.Errorf("update GitLab webhook link: %w", err)
		}
		linkID = &link.ID
		if err := insertResourceAuditTx(ctx, tx, link.ResourceType, "gitlab.webhook."+event.ObjectKind, link.ResourceID, map[string]any{
			"event_uuid": event.EventUUID, "external_state": link.ExternalState,
			"pipeline_status": link.PipelineStatus, "gitlab_project_id": event.GitLabProjectID,
		}); err != nil {
			return domain.GitLabWebhookResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO gitlab_webhook_event (
    event_uuid, event_type, object_kind, gitlab_project_id, object_iid,
    payload_checksum, status, gitlab_link_id, received_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.EventUUID, event.EventType, event.ObjectKind, event.GitLabProjectID,
		event.ObjectIID, event.PayloadChecksum, status, linkID, event.ReceivedAt); err != nil {
		return domain.GitLabWebhookResult{}, mapGitLabError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.GitLabWebhookResult{}, fmt.Errorf("commit GitLab webhook: %w", err)
	}
	result := domain.GitLabWebhookResult{EventUUID: event.EventUUID, Status: status}
	if linkID != nil {
		result.Link = &link
	}
	return result, nil
}

func validateGitLabWebhookEvent(event domain.GitLabWebhookEvent) error {
	if len(strings.TrimSpace(event.EventUUID)) < 8 || len(event.EventUUID) > 128 ||
		strings.TrimSpace(event.EventType) == "" || len(event.EventType) > 128 ||
		event.GitLabProjectID <= 0 || event.ObjectIID <= 0 || len(event.PayloadChecksum) != 64 || event.ReceivedAt.IsZero() {
		return fmt.Errorf("incomplete GitLab webhook event: %w", domain.ErrValidation)
	}
	if _, err := hex.DecodeString(event.PayloadChecksum); err != nil {
		return fmt.Errorf("invalid GitLab webhook checksum: %w", domain.ErrValidation)
	}
	switch event.ObjectKind {
	case "issue":
		if event.ExternalState != "opened" && event.ExternalState != "closed" {
			return fmt.Errorf("unsupported GitLab issue state %q: %w", event.ExternalState, domain.ErrValidation)
		}
	case "merge_request":
		if !validExternalState(event.ExternalState) || event.ExternalState == "unknown" {
			return fmt.Errorf("unsupported GitLab webhook state %q: %w", event.ExternalState, domain.ErrValidation)
		}
	case "pipeline":
		if !validPipelineStatus(event.PipelineStatus) {
			return fmt.Errorf("unsupported GitLab pipeline status %q: %w", event.PipelineStatus, domain.ErrValidation)
		}
	default:
		return fmt.Errorf("unsupported GitLab webhook kind %q: %w", event.ObjectKind, domain.ErrValidation)
	}
	return nil
}

func validExternalState(value string) bool {
	switch value {
	case "unknown", "opened", "closed", "merged", "locked":
		return true
	default:
		return false
	}
}

func validPipelineStatus(value string) bool {
	switch value {
	case "created", "waiting_for_resource", "preparing", "pending", "running", "success", "failed", "canceled", "skipped", "manual", "scheduled":
		return true
	default:
		return false
	}
}

func validExternalTransition(from, to, kind string) bool {
	if from == to || from == "unknown" {
		return true
	}
	if kind == "issue" {
		return (from == "opened" && to == "closed") || (from == "closed" && to == "opened")
	}
	if from == "merged" {
		return false
	}
	if to == "merged" {
		return from == "opened" || from == "locked"
	}
	return (from == "opened" || from == "closed" || from == "locked") &&
		(to == "opened" || to == "closed" || to == "locked")
}

func validGitLabResource(value string) bool {
	return value == domain.GitLabResourcePlan || value == domain.GitLabResourceTask || value == domain.GitLabResourceOnboardingRun
}

func mapGitLabError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

const gitLabLinkColumns = `id, resource_type, resource_id, gitlab_project_id,
issue_iid, merge_request_iid, url, external_state, issue_state, merge_request_state, pipeline_status,
last_event_uuid, last_synced_at, created_at, updated_at`

const gitLabLinkQualifiedColumns = `link.id, link.resource_type, link.resource_id, link.gitlab_project_id,
link.issue_iid, link.merge_request_iid, link.url, link.external_state, link.issue_state, link.merge_request_state, link.pipeline_status,
link.last_event_uuid, link.last_synced_at, link.created_at, link.updated_at`

func scanGitLabLink(row rowScanner) (domain.GitLabLink, error) {
	var link domain.GitLabLink
	err := row.Scan(
		&link.ID, &link.ResourceType, &link.ResourceID, &link.GitLabProjectID,
		&link.IssueIID, &link.MergeRequestIID, &link.URL, &link.ExternalState,
		&link.IssueState, &link.MergeRequestState, &link.PipelineStatus,
		&link.LastEventUUID, &link.LastSyncedAt,
		&link.CreatedAt, &link.UpdatedAt,
	)
	return link, err
}
