-- Confirmed CURRENT processes are version-controlled YAML manifests. These
-- tables are a query/index snapshot only; no row is authoritative over the
-- source manifest. Candidate processes are stored separately and can never be
-- selected by a CURRENT-process query without an explicit verification flow.
CREATE TABLE architecture_business_process_snapshot (
    process_id varchar(128) NOT NULL CHECK (process_id ~ '^[a-z][a-z0-9._-]{0,127}$'),
    manifest_checksum varchar(64) NOT NULL CHECK (manifest_checksum ~ '^[a-f0-9]{64}$'),
    manifest_revision integer NOT NULL CHECK (manifest_revision > 0),
    source_path text NOT NULL CHECK (source_path ~ '^\.ai/architecture/processes/[A-Za-z0-9._-]+\.ya?ml$'),
    provenance varchar(32) NOT NULL CHECK (provenance IN ('owner-authored', 'evidence-backed')),
    confidence numeric(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    current_fingerprint varchar(64) NOT NULL CHECK (current_fingerprint ~ '^[a-f0-9]{64}$'),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    captured_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (process_id, manifest_checksum)
);

CREATE TABLE architecture_business_process_index (
    process_id varchar(128) PRIMARY KEY,
    manifest_checksum varchar(64) NOT NULL,
    current_fingerprint varchar(64) NOT NULL CHECK (current_fingerprint ~ '^[a-f0-9]{64}$'),
    source_path text NOT NULL CHECK (source_path ~ '^\.ai/architecture/processes/[A-Za-z0-9._-]+\.ya?ml$'),
    provenance varchar(32) NOT NULL CHECK (provenance IN ('owner-authored', 'evidence-backed')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (process_id, manifest_checksum)
        REFERENCES architecture_business_process_snapshot (process_id, manifest_checksum)
        ON DELETE RESTRICT
);

CREATE INDEX architecture_business_process_index_fingerprint_idx
    ON architecture_business_process_index (current_fingerprint, updated_at DESC, process_id);

CREATE TABLE architecture_business_process_candidate (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    candidate_key varchar(255) NOT NULL,
    current_fingerprint varchar(64) NOT NULL CHECK (current_fingerprint ~ '^[a-f0-9]{64}$'),
    status varchar(32) NOT NULL DEFAULT 'candidate_unverified'
        CHECK (status = 'candidate_unverified'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence) = 'array'),
    confidence numeric(4,3) NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (candidate_key, current_fingerprint)
);

CREATE INDEX architecture_business_process_candidate_status_idx
    ON architecture_business_process_candidate (status, current_fingerprint, updated_at DESC);
