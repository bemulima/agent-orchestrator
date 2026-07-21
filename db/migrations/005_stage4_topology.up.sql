CREATE TABLE topology_revision (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint varchar(64) NOT NULL,
    project_count integer NOT NULL CHECK (project_count >= 0),
    service_count integer NOT NULL CHECK (service_count >= 0),
    capability_count integer NOT NULL CHECK (capability_count >= 0),
    ownership_count integer NOT NULL CHECK (ownership_count >= 0),
    contract_count integer NOT NULL CHECK (contract_count >= 0),
    relation_count integer NOT NULL CHECK (relation_count >= 0),
    drift_count integer NOT NULL CHECK (drift_count >= 0),
    built_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX topology_revision_built_idx ON topology_revision (built_at DESC, id DESC);
CREATE INDEX topology_revision_fingerprint_idx ON topology_revision (fingerprint);

CREATE TABLE topology_service (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision_id uuid NOT NULL REFERENCES topology_revision(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    snapshot_id uuid NOT NULL REFERENCES service_snapshot(id) ON DELETE CASCADE,
    name varchar(255) NOT NULL,
    repository_role varchar(32) NOT NULL,
    service_kind varchar(64) NOT NULL,
    purpose text NOT NULL DEFAULT '',
    stack jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(stack) = 'array'),
    UNIQUE (revision_id, project_id)
);

CREATE INDEX topology_service_revision_name_idx ON topology_service (revision_id, name, project_id);

ALTER TABLE service_capability
    ADD COLUMN revision_id uuid REFERENCES topology_revision(id) ON DELETE CASCADE,
    ADD COLUMN snapshot_id uuid REFERENCES service_snapshot(id) ON DELETE CASCADE;

ALTER TABLE service_ownership
    ADD COLUMN revision_id uuid REFERENCES topology_revision(id) ON DELETE CASCADE,
    ADD COLUMN snapshot_id uuid REFERENCES service_snapshot(id) ON DELETE CASCADE;

ALTER TABLE contract
    ADD COLUMN revision_id uuid REFERENCES topology_revision(id) ON DELETE CASCADE,
    ADD COLUMN snapshot_id uuid REFERENCES service_snapshot(id) ON DELETE CASCADE;

ALTER TABLE service_relation
    ADD COLUMN revision_id uuid REFERENCES topology_revision(id) ON DELETE CASCADE,
    ADD COLUMN snapshot_id uuid REFERENCES service_snapshot(id) ON DELETE CASCADE;

CREATE INDEX service_capability_revision_idx ON service_capability (revision_id, project_id, code);
CREATE INDEX service_ownership_revision_idx ON service_ownership (revision_id, project_id, resource_type, resource_name);
CREATE INDEX contract_revision_idx ON contract (revision_id, project_id, code, direction);
CREATE INDEX service_relation_revision_idx ON service_relation (revision_id, source_project_id, target_project_id);

CREATE TABLE contract_drift (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision_id uuid NOT NULL REFERENCES topology_revision(id) ON DELETE CASCADE,
    producer_project_id uuid REFERENCES project(id) ON DELETE CASCADE,
    consumer_project_id uuid REFERENCES project(id) ON DELETE CASCADE,
    contract_code varchar(512) NOT NULL,
    contract_type varchar(32) NOT NULL CHECK (contract_type IN (
        'http', 'event', 'database', 'graphql', 'grpc', 'file', 'environment'
    )),
    producer_version varchar(128) NOT NULL DEFAULT '',
    consumer_version varchar(128) NOT NULL DEFAULT '',
    difference jsonb NOT NULL CHECK (jsonb_typeof(difference) = 'object'),
    severity varchar(32) NOT NULL CHECK (severity IN ('info', 'warning', 'error', 'critical')),
    suggested_action text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX contract_drift_identity_unique
    ON contract_drift (
        revision_id,
        COALESCE(producer_project_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(consumer_project_id, '00000000-0000-0000-0000-000000000000'::uuid),
        contract_code
    );
CREATE INDEX contract_drift_revision_severity_idx ON contract_drift (revision_id, severity, contract_code);
