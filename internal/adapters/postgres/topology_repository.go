package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// TopologyRepoPG stores the latest materialized topology catalog. Revision
// metadata is retained, while catalog rows are replaced atomically.
type TopologyRepoPG struct {
	Pool *pgxpool.Pool
}

func (r TopologyRepoPG) Replace(ctx context.Context, catalog domain.TopologyCatalog) (domain.TopologyCatalog, error) {
	if err := validateTopologyCatalog(catalog); err != nil {
		return domain.TopologyCatalog{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TopologyCatalog{}, fmt.Errorf("begin topology transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('course-dev-orchestrator:topology'))`); err != nil {
		return domain.TopologyCatalog{}, fmt.Errorf("lock topology rebuild: %w", err)
	}
	var existingID string
	err = tx.QueryRow(ctx, `
SELECT id FROM topology_revision
ORDER BY built_at DESC, id DESC
LIMIT 1`).Scan(&existingID)
	if err == nil {
		var fingerprint string
		if err := tx.QueryRow(ctx, `SELECT fingerprint FROM topology_revision WHERE id = $1`, existingID).Scan(&fingerprint); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("read current topology fingerprint: %w", err)
		}
		if fingerprint == catalog.Revision.Fingerprint {
			if err := tx.Commit(ctx); err != nil {
				return domain.TopologyCatalog{}, fmt.Errorf("commit reused topology transaction: %w", err)
			}
			return r.Get(ctx)
		}
	} else if err != pgx.ErrNoRows {
		return domain.TopologyCatalog{}, fmt.Errorf("find current topology revision: %w", err)
	}

	revision := catalog.Revision
	err = tx.QueryRow(ctx, `
INSERT INTO topology_revision (
    fingerprint, project_count, service_count, capability_count,
    ownership_count, contract_count, relation_count, drift_count
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, built_at`,
		revision.Fingerprint, revision.ProjectCount, revision.ServiceCount,
		revision.CapabilityCount, revision.OwnershipCount, revision.ContractCount,
		revision.RelationCount, revision.DriftCount,
	).Scan(&revision.ID, &revision.BuiltAt)
	if err != nil {
		return domain.TopologyCatalog{}, fmt.Errorf("insert topology revision: %w", err)
	}

	for _, statement := range []string{
		`DELETE FROM contract_drift`,
		`DELETE FROM service_relation WHERE revision_id IS NOT NULL`,
		`DELETE FROM contract WHERE revision_id IS NOT NULL`,
		`DELETE FROM service_ownership WHERE revision_id IS NOT NULL`,
		`DELETE FROM service_capability WHERE revision_id IS NOT NULL`,
		`DELETE FROM topology_service`,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("clear materialized topology: %w", err)
		}
	}

	for _, service := range catalog.Services {
		if service.Stack == nil {
			service.Stack = []domain.Evidence{}
		}
		stack, marshalErr := json.Marshal(service.Stack)
		if marshalErr != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("marshal topology service stack: %w", marshalErr)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO topology_service (
    revision_id, project_id, snapshot_id, name, repository_role,
    service_kind, purpose, stack
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			revision.ID, service.ProjectID, service.SnapshotID, service.Name,
			service.RepositoryRole, service.ServiceKind, service.Purpose, stack,
		); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert topology service: %w", err)
		}
	}
	for _, capability := range catalog.Capabilities {
		if _, err := tx.Exec(ctx, `
INSERT INTO service_capability (
    revision_id, snapshot_id, project_id, code, name, description, confidence, source
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, revision.ID, capability.SnapshotID,
			capability.ProjectID, capability.Code, capability.Name, capability.Description,
			capability.Confidence, capability.Source); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert topology capability: %w", err)
		}
	}
	for _, ownership := range catalog.Ownership {
		if _, err := tx.Exec(ctx, `
INSERT INTO service_ownership (
    revision_id, snapshot_id, project_id, resource_type, resource_name, confidence, source
) VALUES ($1, $2, $3, $4, $5, $6, $7)`, revision.ID, ownership.SnapshotID,
			ownership.ProjectID, ownership.ResourceType, ownership.ResourceName,
			ownership.Confidence, ownership.Source); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert topology ownership: %w", err)
		}
	}
	for _, contract := range catalog.Contracts {
		if _, err := tx.Exec(ctx, `
INSERT INTO contract (
    revision_id, snapshot_id, project_id, code, type, version, direction,
    definition, source_path, checksum, discovered_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11, now()))`,
			revision.ID, contract.SnapshotID, contract.ProjectID, contract.Code,
			contract.Type, contract.Version, contract.Direction, contract.Definition,
			contract.SourcePath, contract.Checksum, nullableTime(contract.DiscoveredAt)); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert topology contract: %w", err)
		}
	}
	for _, relation := range catalog.Relations {
		if _, err := tx.Exec(ctx, `
INSERT INTO service_relation (
    revision_id, snapshot_id, source_project_id, target_project_id,
    relation_type, contract_code, confidence, source
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, revision.ID, relation.SnapshotID,
			relation.SourceProjectID, relation.TargetProjectID, relation.RelationType,
			relation.ContractCode, relation.Confidence, relation.Source); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert topology relation: %w", err)
		}
	}
	for _, drift := range catalog.Drifts {
		if _, err := tx.Exec(ctx, `
INSERT INTO contract_drift (
    revision_id, producer_project_id, consumer_project_id, contract_code,
    contract_type, producer_version, consumer_version, difference,
    severity, suggested_action
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, revision.ID,
			drift.ProducerProjectID, drift.ConsumerProjectID, drift.ContractCode,
			drift.ContractType, drift.ProducerVersion, drift.ConsumerVersion,
			drift.Difference, drift.Severity, drift.SuggestedAction); err != nil {
			return domain.TopologyCatalog{}, fmt.Errorf("insert contract drift: %w", err)
		}
	}
	if err := insertResourceAuditTx(ctx, tx, "topology_revision", "topology.rebuilt", revision.ID, map[string]any{
		"fingerprint": revision.Fingerprint,
		"projects":    revision.ProjectCount,
		"services":    revision.ServiceCount,
		"contracts":   revision.ContractCount,
		"drifts":      revision.DriftCount,
	}); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TopologyCatalog{}, fmt.Errorf("commit topology transaction: %w", err)
	}
	return r.Get(ctx)
}

func (r TopologyRepoPG) Get(ctx context.Context) (domain.TopologyCatalog, error) {
	var catalog domain.TopologyCatalog
	err := r.Pool.QueryRow(ctx, `
SELECT id, fingerprint, project_count, service_count, capability_count,
       ownership_count, contract_count, relation_count, drift_count, built_at
FROM topology_revision
ORDER BY built_at DESC, id DESC
LIMIT 1`).Scan(
		&catalog.Revision.ID, &catalog.Revision.Fingerprint, &catalog.Revision.ProjectCount,
		&catalog.Revision.ServiceCount, &catalog.Revision.CapabilityCount,
		&catalog.Revision.OwnershipCount, &catalog.Revision.ContractCount,
		&catalog.Revision.RelationCount, &catalog.Revision.DriftCount, &catalog.Revision.BuiltAt,
	)
	if err != nil {
		return domain.TopologyCatalog{}, mapProjectError(err)
	}
	revisionID := catalog.Revision.ID
	if catalog.Services, err = r.readServices(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if catalog.Capabilities, err = r.readCapabilities(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if catalog.Ownership, err = r.readOwnership(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if catalog.Contracts, err = r.readContracts(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if catalog.Relations, err = r.readRelations(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	if catalog.Drifts, err = r.readDrifts(ctx, revisionID); err != nil {
		return domain.TopologyCatalog{}, err
	}
	return catalog, nil
}

func (r TopologyRepoPG) readServices(ctx context.Context, revisionID string) ([]domain.TopologyService, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT revision_id, project_id, snapshot_id, name, repository_role, service_kind, purpose, stack
FROM topology_service WHERE revision_id = $1 ORDER BY name, project_id`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query topology services: %w", err)
	}
	defer rows.Close()
	result := make([]domain.TopologyService, 0)
	for rows.Next() {
		var value domain.TopologyService
		var stack []byte
		if err := rows.Scan(&value.RevisionID, &value.ProjectID, &value.SnapshotID, &value.Name,
			&value.RepositoryRole, &value.ServiceKind, &value.Purpose, &stack); err != nil {
			return nil, fmt.Errorf("scan topology service: %w", err)
		}
		if err := json.Unmarshal(stack, &value.Stack); err != nil {
			return nil, fmt.Errorf("decode topology service stack: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r TopologyRepoPG) readCapabilities(ctx context.Context, revisionID string) ([]domain.ServiceCapability, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id, revision_id, snapshot_id, project_id, code, name, description, confidence, source
FROM service_capability WHERE revision_id = $1 ORDER BY project_id, code, source`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query topology capabilities: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ServiceCapability, 0)
	for rows.Next() {
		var value domain.ServiceCapability
		if err := rows.Scan(&value.ID, &value.RevisionID, &value.SnapshotID, &value.ProjectID,
			&value.Code, &value.Name, &value.Description, &value.Confidence, &value.Source); err != nil {
			return nil, fmt.Errorf("scan topology capability: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r TopologyRepoPG) readOwnership(ctx context.Context, revisionID string) ([]domain.ServiceOwnership, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id, revision_id, snapshot_id, project_id, resource_type, resource_name, confidence, source
FROM service_ownership WHERE revision_id = $1 ORDER BY project_id, resource_type, resource_name, source`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query topology ownership: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ServiceOwnership, 0)
	for rows.Next() {
		var value domain.ServiceOwnership
		if err := rows.Scan(&value.ID, &value.RevisionID, &value.SnapshotID, &value.ProjectID,
			&value.ResourceType, &value.ResourceName, &value.Confidence, &value.Source); err != nil {
			return nil, fmt.Errorf("scan topology ownership: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r TopologyRepoPG) readContracts(ctx context.Context, revisionID string) ([]domain.Contract, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id, revision_id, snapshot_id, project_id, code, type, version, direction,
       definition, source_path, checksum, discovered_at
FROM contract WHERE revision_id = $1 ORDER BY code, project_id, direction`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query topology contracts: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Contract, 0)
	for rows.Next() {
		var value domain.Contract
		if err := rows.Scan(&value.ID, &value.RevisionID, &value.SnapshotID, &value.ProjectID,
			&value.Code, &value.Type, &value.Version, &value.Direction, &value.Definition,
			&value.SourcePath, &value.Checksum, &value.DiscoveredAt); err != nil {
			return nil, fmt.Errorf("scan topology contract: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r TopologyRepoPG) readRelations(ctx context.Context, revisionID string) ([]domain.ServiceRelation, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id, revision_id, snapshot_id, source_project_id, target_project_id,
       relation_type, contract_code, confidence, source
FROM service_relation WHERE revision_id = $1
ORDER BY source_project_id, target_project_id, relation_type, contract_code`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query topology relations: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ServiceRelation, 0)
	for rows.Next() {
		var value domain.ServiceRelation
		if err := rows.Scan(&value.ID, &value.RevisionID, &value.SnapshotID, &value.SourceProjectID,
			&value.TargetProjectID, &value.RelationType, &value.ContractCode,
			&value.Confidence, &value.Source); err != nil {
			return nil, fmt.Errorf("scan topology relation: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r TopologyRepoPG) readDrifts(ctx context.Context, revisionID string) ([]domain.ContractDrift, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id, revision_id, producer_project_id, consumer_project_id, contract_code,
       contract_type, producer_version, consumer_version, difference, severity,
       suggested_action, created_at
FROM contract_drift WHERE revision_id = $1
ORDER BY CASE severity WHEN 'critical' THEN 1 WHEN 'error' THEN 2 WHEN 'warning' THEN 3 ELSE 4 END,
         contract_code, producer_project_id, consumer_project_id`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("query contract drift: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ContractDrift, 0)
	for rows.Next() {
		var value domain.ContractDrift
		if err := rows.Scan(&value.ID, &value.RevisionID, &value.ProducerProjectID,
			&value.ConsumerProjectID, &value.ContractCode, &value.ContractType,
			&value.ProducerVersion, &value.ConsumerVersion, &value.Difference,
			&value.Severity, &value.SuggestedAction, &value.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan contract drift: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func validateTopologyCatalog(catalog domain.TopologyCatalog) error {
	revision := catalog.Revision
	if revision.Fingerprint == "" || revision.ServiceCount != len(catalog.Services) ||
		revision.CapabilityCount != len(catalog.Capabilities) || revision.OwnershipCount != len(catalog.Ownership) ||
		revision.ContractCount != len(catalog.Contracts) || revision.RelationCount != len(catalog.Relations) ||
		revision.DriftCount != len(catalog.Drifts) {
		return fmt.Errorf("inconsistent topology catalog: %w", domain.ErrValidation)
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
