DELETE FROM audit_event WHERE resource_type = 'topology_revision';
DROP TABLE IF EXISTS contract_drift;
DROP TABLE IF EXISTS topology_service;

DELETE FROM service_relation WHERE revision_id IS NOT NULL;
DELETE FROM contract WHERE revision_id IS NOT NULL;
DELETE FROM service_ownership WHERE revision_id IS NOT NULL;
DELETE FROM service_capability WHERE revision_id IS NOT NULL;

DROP INDEX IF EXISTS service_relation_revision_idx;
DROP INDEX IF EXISTS contract_revision_idx;
DROP INDEX IF EXISTS service_ownership_revision_idx;
DROP INDEX IF EXISTS service_capability_revision_idx;

ALTER TABLE service_relation DROP COLUMN IF EXISTS snapshot_id, DROP COLUMN IF EXISTS revision_id;
ALTER TABLE contract DROP COLUMN IF EXISTS snapshot_id, DROP COLUMN IF EXISTS revision_id;
ALTER TABLE service_ownership DROP COLUMN IF EXISTS snapshot_id, DROP COLUMN IF EXISTS revision_id;
ALTER TABLE service_capability DROP COLUMN IF EXISTS snapshot_id, DROP COLUMN IF EXISTS revision_id;

DROP TABLE IF EXISTS topology_revision;
