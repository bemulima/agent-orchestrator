package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ProjectRepoPG is the PostgreSQL implementation of project persistence.
type ProjectRepoPG struct {
	Pool *pgxpool.Pool
}

func (r ProjectRepoPG) Upsert(ctx context.Context, project domain.Project) (domain.Project, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Project{}, fmt.Errorf("begin project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx, `
INSERT INTO project (
    name, status, repository_role, source_identity, local_path, git_url,
    default_branch, current_branch, head_commit, is_dirty
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (source_identity) DO UPDATE SET
    local_path = COALESCE(project.local_path, EXCLUDED.local_path),
    git_url = COALESCE(project.git_url, EXCLUDED.git_url),
    repository_role = CASE
        WHEN project.repository_role = 'unknown' THEN EXCLUDED.repository_role
        ELSE project.repository_role
    END,
    default_branch = EXCLUDED.default_branch,
    current_branch = EXCLUDED.current_branch,
    head_commit = EXCLUDED.head_commit,
    is_dirty = EXCLUDED.is_dirty,
    updated_at = now()
RETURNING `+projectColumns,
		project.Name,
		project.Status,
		project.RepositoryRole,
		project.SourceIdentity,
		project.LocalPath,
		project.GitURL,
		project.DefaultBranch,
		project.CurrentBranch,
		project.HeadCommit,
		project.IsDirty,
	)
	result, err := scanProject(row)
	if err != nil {
		return domain.Project{}, mapProjectError(err)
	}
	if err := insertAuditTx(ctx, tx, "project.connected", result.ID, map[string]any{
		"source_identity": result.SourceIdentity,
		"repository_role": result.RepositoryRole,
	}); err != nil {
		return domain.Project{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Project{}, fmt.Errorf("commit project transaction: %w", err)
	}
	return result, nil
}

func (r ProjectRepoPG) Get(ctx context.Context, id string) (domain.Project, error) {
	project, err := scanProject(r.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM project WHERE id = $1`, id))
	return project, mapProjectError(err)
}

func (r ProjectRepoPG) GetByName(ctx context.Context, name string) (domain.Project, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+projectColumns+` FROM project WHERE name = $1 ORDER BY created_at LIMIT 2`, name)
	if err != nil {
		return domain.Project{}, fmt.Errorf("query project by name: %w", err)
	}
	defer rows.Close()
	projects := make([]domain.Project, 0, 2)
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return domain.Project{}, fmt.Errorf("scan project by name: %w", scanErr)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return domain.Project{}, fmt.Errorf("iterate projects by name: %w", err)
	}
	if len(projects) == 0 {
		return domain.Project{}, domain.ErrNotFound
	}
	if len(projects) > 1 {
		return domain.Project{}, fmt.Errorf("multiple projects named %q: %w", name, domain.ErrConflict)
	}
	return projects[0], nil
}

func (r ProjectRepoPG) List(ctx context.Context) ([]domain.Project, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+projectColumns+` FROM project ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	projects := make([]domain.Project, 0)
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan project: %w", scanErr)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (r ProjectRepoPG) UpdateSourceState(
	ctx context.Context,
	id string,
	status domain.ProjectStatus,
	source domain.RepositorySource,
) (domain.Project, error) {
	project, err := scanProject(r.Pool.QueryRow(ctx, `
UPDATE project SET
    status = $2,
    git_url = COALESCE(NULLIF($3, ''), git_url),
    default_branch = $4,
    current_branch = $5,
    head_commit = $6,
    is_dirty = $7,
    updated_at = now()
WHERE id = $1
RETURNING `+projectColumns,
		id, status, source.GitURL, source.DefaultBranch, source.CurrentBranch, source.HeadCommit, source.IsDirty,
	))
	return project, mapProjectError(err)
}

func (r ProjectRepoPG) UpdateStatus(ctx context.Context, id string, status domain.ProjectStatus) error {
	command, err := r.Pool.Exec(ctx, `UPDATE project SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("update project status: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r ProjectRepoPG) SaveDiscovery(
	ctx context.Context,
	project domain.Project,
	snapshot domain.ServiceSnapshot,
	report domain.DiscoveryReport,
) (domain.ServiceSnapshot, error) {
	rawReport, err := json.Marshal(report)
	if err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("marshal discovery report: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("begin discovery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, project.ID); err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("lock project discovery: %w", err)
	}
	existing, existingErr := scanServiceSnapshot(tx.QueryRow(ctx, `
SELECT id, project_id, version, commit_sha, branch, is_dirty, content_checksum,
       service_kind, language, framework, purpose, confidence, discovered_at,
       raw_report, status
FROM service_snapshot
WHERE project_id = $1 AND commit_sha = $2 AND branch = $3 AND is_dirty = $4
  AND content_checksum = $5
ORDER BY version DESC
LIMIT 1`, project.ID, snapshot.CommitSHA, snapshot.Branch, snapshot.IsDirty, snapshot.ContentChecksum))
	if existingErr == nil {
		if _, err := tx.Exec(ctx, `
UPDATE project SET status = 'analyzed', current_branch = $2, head_commit = $3,
    is_dirty = $4, updated_at = now()
WHERE id = $1`, project.ID, snapshot.Branch, snapshot.CommitSHA, snapshot.IsDirty); err != nil {
			return domain.ServiceSnapshot{}, fmt.Errorf("mark reused project scan analyzed: %w", err)
		}
		if err := insertAuditTx(ctx, tx, "project.scan_reused", project.ID, map[string]any{
			"snapshot_id": existing.ID,
			"version":     existing.Version,
		}); err != nil {
			return domain.ServiceSnapshot{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ServiceSnapshot{}, fmt.Errorf("commit reused discovery transaction: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return domain.ServiceSnapshot{}, fmt.Errorf("find matching discovery snapshot: %w", existingErr)
	}
	row := tx.QueryRow(ctx, `
INSERT INTO service_snapshot (
    project_id, version, commit_sha, branch, is_dirty, content_checksum,
    service_kind, language, framework, purpose, confidence, raw_report, status
) VALUES (
    $1,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM service_snapshot WHERE project_id = $1),
    $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING id, project_id, version, commit_sha, branch, is_dirty, content_checksum,
          service_kind, language, framework, purpose, confidence, discovered_at,
          raw_report, status`,
		project.ID,
		snapshot.CommitSHA,
		snapshot.Branch,
		snapshot.IsDirty,
		snapshot.ContentChecksum,
		snapshot.ServiceKind,
		snapshot.Language,
		snapshot.Framework,
		snapshot.Purpose,
		snapshot.Confidence,
		rawReport,
		snapshot.Status,
	)
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.ProjectID,
		&snapshot.Version,
		&snapshot.CommitSHA,
		&snapshot.Branch,
		&snapshot.IsDirty,
		&snapshot.ContentChecksum,
		&snapshot.ServiceKind,
		&snapshot.Language,
		&snapshot.Framework,
		&snapshot.Purpose,
		&snapshot.Confidence,
		&snapshot.DiscoveredAt,
		&snapshot.RawReport,
		&snapshot.Status,
	); err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("insert discovery snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE project SET status = 'analyzed', current_branch = $2, head_commit = $3,
    is_dirty = $4, updated_at = now()
WHERE id = $1`, project.ID, snapshot.Branch, snapshot.CommitSHA, snapshot.IsDirty); err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("mark project analyzed: %w", err)
	}
	if err := insertAuditTx(ctx, tx, "project.scanned", project.ID, map[string]any{
		"snapshot_id": snapshot.ID,
		"version":     snapshot.Version,
		"commit_sha":  snapshot.CommitSHA,
		"is_dirty":    snapshot.IsDirty,
	}); err != nil {
		return domain.ServiceSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ServiceSnapshot{}, fmt.Errorf("commit discovery transaction: %w", err)
	}
	return snapshot, nil
}

func (r ProjectRepoPG) GetLatestDiscovery(
	ctx context.Context,
	projectID string,
) (domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	var snapshot domain.ServiceSnapshot
	err := r.Pool.QueryRow(ctx, `
SELECT id, project_id, version, commit_sha, branch, is_dirty, content_checksum,
       service_kind, language, framework, purpose, confidence, discovered_at,
       raw_report, status
FROM service_snapshot
WHERE project_id = $1
ORDER BY version DESC
LIMIT 1`, projectID).Scan(
		&snapshot.ID,
		&snapshot.ProjectID,
		&snapshot.Version,
		&snapshot.CommitSHA,
		&snapshot.Branch,
		&snapshot.IsDirty,
		&snapshot.ContentChecksum,
		&snapshot.ServiceKind,
		&snapshot.Language,
		&snapshot.Framework,
		&snapshot.Purpose,
		&snapshot.Confidence,
		&snapshot.DiscoveredAt,
		&snapshot.RawReport,
		&snapshot.Status,
	)
	if err != nil {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, mapProjectError(err)
	}
	var report domain.DiscoveryReport
	if err := json.Unmarshal(snapshot.RawReport, &report); err != nil {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, fmt.Errorf("decode discovery report: %w", err)
	}
	return snapshot, report, nil
}

const projectColumns = `id, name, status, repository_role, source_identity,
local_path, git_url, default_branch, current_branch, head_commit, is_dirty,
gitlab_project_id, created_at, updated_at`

type rowScanner interface {
	Scan(...any) error
}

func scanProject(row rowScanner) (domain.Project, error) {
	var project domain.Project
	err := row.Scan(
		&project.ID,
		&project.Name,
		&project.Status,
		&project.RepositoryRole,
		&project.SourceIdentity,
		&project.LocalPath,
		&project.GitURL,
		&project.DefaultBranch,
		&project.CurrentBranch,
		&project.HeadCommit,
		&project.IsDirty,
		&project.GitLabProjectID,
		&project.CreatedAt,
		&project.UpdatedAt,
	)
	return project, err
}

func scanServiceSnapshot(row rowScanner) (domain.ServiceSnapshot, error) {
	var snapshot domain.ServiceSnapshot
	err := row.Scan(
		&snapshot.ID,
		&snapshot.ProjectID,
		&snapshot.Version,
		&snapshot.CommitSHA,
		&snapshot.Branch,
		&snapshot.IsDirty,
		&snapshot.ContentChecksum,
		&snapshot.ServiceKind,
		&snapshot.Language,
		&snapshot.Framework,
		&snapshot.Purpose,
		&snapshot.Confidence,
		&snapshot.DiscoveredAt,
		&snapshot.RawReport,
		&snapshot.Status,
	)
	return snapshot, err
}

func mapProjectError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("project already exists: %w", domain.ErrConflict)
	}
	return err
}

func insertAuditTx(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	resourceID string,
	payload map[string]any,
) error {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO audit_event (actor_type, action, resource_type, resource_id, payload)
VALUES ('system', $1, 'project', $2, $3)`, action, resourceID, rawPayload)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}
