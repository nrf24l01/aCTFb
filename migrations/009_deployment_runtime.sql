-- P2/P3: integrations, immutable deployment templates/bindings, instance runtime, Pentest report.
-- Additive only. 006 stays unchanged; down removes only objects created here.

-- +goose Up
CREATE TABLE integrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category TEXT NOT NULL CHECK (category IN ('GIT','COMPUTE')),
    type TEXT NOT NULL CHECK (type IN ('GITLAB','GITHUB','GITEA','PROXMOX','CONTAINER','KUBEVIRT','DOCKER','PODMAN','KUBERNETES')),
    kind TEXT CHECK (kind IS NULL OR kind IN ('VM','CONTAINER')),
    driver TEXT CHECK (driver IS NULL OR driver IN ('proxmox','kubevirt','docker','podman','kubernetes')),
    name TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 128),
    base_url TEXT,
    credentials_ciphertext BYTEA,
    credentials_key_version INTEGER,
    settings JSONB NOT NULL DEFAULT '{}',
    publication_policy JSONB,
    status TEXT NOT NULL DEFAULT 'UNKNOWN' CHECK (status IN ('CONNECTED','ERROR','UNKNOWN')),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (category = 'GIT' AND type IN ('GITLAB','GITHUB','GITEA') AND kind IS NULL AND driver IS NULL)
        OR (category = 'COMPUTE' AND kind = 'VM' AND type IN ('PROXMOX','KUBEVIRT') AND driver IN ('proxmox','kubevirt'))
        OR (category = 'COMPUTE' AND kind = 'CONTAINER' AND type IN ('CONTAINER','DOCKER','PODMAN','KUBERNETES') AND driver IN ('docker','podman','kubernetes'))
    )
);

CREATE TABLE deployment_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE deployment_template_drafts (
    template_id UUID PRIMARY KEY REFERENCES deployment_templates(id) ON DELETE CASCADE,
    manifest_yaml TEXT NOT NULL,
    files JSONB NOT NULL DEFAULT '[]',
    updated_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE deployment_template_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES deployment_templates(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version >= 1),
    digest TEXT NOT NULL CHECK (char_length(digest) BETWEEN 1 AND 128),
    manifest JSONB NOT NULL,
    files JSONB NOT NULL DEFAULT '[]',
    published_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, version),
    UNIQUE (template_id, id)
);

CREATE TABLE deployment_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    task_id UUID,
    template_version_id UUID NOT NULL REFERENCES deployment_template_versions(id) ON DELETE RESTRICT,
    parameters JSONB NOT NULL DEFAULT '{}',
    secret_parameters_ciphertext BYTEA,
    secret_key_version INTEGER,
    lifecycle JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    FOREIGN KEY (board_id, module_id, task_id) REFERENCES tasks(board_id, module_id, id) ON DELETE CASCADE,
    UNIQUE (board_id, module_id, id)
);
CREATE UNIQUE INDEX deployment_bindings_scope_uq
    ON deployment_bindings (board_id, module_id, task_id) NULLS NOT DISTINCT;

CREATE TABLE deployment_binding_targets (
    binding_id UUID NOT NULL REFERENCES deployment_bindings(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('VM','CONTAINER')),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    PRIMARY KEY (binding_id, kind)
);

ALTER TABLE tasks
    ADD COLUMN deployment_binding_id UUID REFERENCES deployment_bindings(id) ON DELETE SET NULL;
ALTER TABLE modules
    ADD COLUMN deployment_binding_id UUID REFERENCES deployment_bindings(id) ON DELETE SET NULL;

CREATE TABLE instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('TASK','PENTEST')),
    scope_uuid UUID NOT NULL,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PROVISIONING','RUNNING','RESETTING','DESTROYING','ERROR','EXPIRED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    extension_count INTEGER NOT NULL DEFAULT 0 CHECK (extension_count >= 0),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation >= 1),
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
    provider_labels JSONB NOT NULL DEFAULT '{}',
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    UNIQUE (idempotency_key)
);
CREATE INDEX instances_owner_idx ON instances (board_id, subject_type, subject_id, scope, scope_uuid);
CREATE INDEX instances_status_idx ON instances (status, expires_at);

CREATE TABLE instance_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    key TEXT NOT NULL CHECK (key ~ '^[a-z0-9-]+$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    kind TEXT NOT NULL CHECK (kind IN ('VM','CONTAINER')),
    status TEXT NOT NULL CHECK (status IN ('PROVISIONING','RUNNING','ERROR','DESTROYING','DESTROYED')),
    provider_resource_id TEXT,
    provider_labels JSONB NOT NULL DEFAULT '{}',
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation >= 1),
    UNIQUE (instance_id, key),
    UNIQUE (instance_id, id)
);

CREATE TABLE instance_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    node_key TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('CREATE','RESET','DESTROY')),
    generation BIGINT NOT NULL CHECK (generation >= 1),
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
    status TEXT NOT NULL DEFAULT 'QUEUED' CHECK (status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','CANCELED')),
    error TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (instance_id, node_key, generation, operation)
);

CREATE TABLE instance_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    node_key TEXT NOT NULL,
    key TEXT NOT NULL CHECK (key ~ '^[a-zA-Z0-9_.-]+$'),
    type TEXT NOT NULL,
    host TEXT NOT NULL CHECK (char_length(host) BETWEEN 1 AND 255),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    url TEXT,
    exposure TEXT NOT NULL CHECK (exposure IN ('PUBLIC','PRIVATE')),
    allocated_port INTEGER CHECK (allocated_port IS NULL OR allocated_port BETWEEN 1 AND 65535),
    released_at TIMESTAMPTZ,
    UNIQUE (instance_id, node_key, key)
);
CREATE UNIQUE INDEX instance_endpoints_live_port_uq
    ON instance_endpoints (host, port) WHERE released_at IS NULL;

CREATE TABLE instance_secret_values (
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    node_key TEXT NOT NULL,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    ciphertext BYTEA NOT NULL,
    key_version INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, node_key, key)
);

CREATE TABLE pentest_configs (
    module_id UUID PRIMARY KEY REFERENCES modules(id) ON DELETE CASCADE,
    deployment_binding_id UUID REFERENCES deployment_bindings(id) ON DELETE SET NULL,
    reset_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    max_resets INTEGER NOT NULL DEFAULT 0 CHECK (max_resets BETWEEN 0 AND 100),
    submission_policy JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE pentest_environments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('PROVISIONING','RUNNING','RESETTING','DESTROYING','ERROR','EXPIRED')),
    resets_used INTEGER NOT NULL DEFAULT 0 CHECK (resets_used >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (module_id, subject_type, subject_id)
);

CREATE TABLE pentest_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PENDING','CHECKING','PENDING_REVIEW','ACCEPTED','REJECTED','ERROR')),
    text TEXT,
    score_awarded INTEGER CHECK (score_awarded IS NULL OR score_awarded >= 0),
    message TEXT,
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
    request_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ,
    reviewed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    review_decision TEXT CHECK (review_decision IS NULL OR review_decision IN ('ACCEPT','REJECT')),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    UNIQUE (module_id, subject_type, subject_id, idempotency_key)
);
CREATE UNIQUE INDEX pentest_submissions_one_accepted
    ON pentest_submissions (module_id, subject_type, subject_id) WHERE status = 'ACCEPTED';

CREATE TABLE pentest_submission_files (
    submission_id UUID NOT NULL REFERENCES pentest_submissions(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES file_objects(id) ON DELETE RESTRICT,
    PRIMARY KEY (submission_id, file_id)
);

-- +goose Down
DROP TABLE IF EXISTS pentest_submission_files;
DROP TABLE IF EXISTS pentest_submissions;
DROP TABLE IF EXISTS pentest_environments;
DROP TABLE IF EXISTS pentest_configs;
DROP TABLE IF EXISTS instance_secret_values;
DROP TABLE IF EXISTS instance_endpoints;
DROP TABLE IF EXISTS instance_operations;
DROP TABLE IF EXISTS instance_nodes;
DROP TABLE IF EXISTS instances;
ALTER TABLE modules DROP COLUMN IF EXISTS deployment_binding_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS deployment_binding_id;
DROP TABLE IF EXISTS deployment_binding_targets;
DROP TABLE IF EXISTS deployment_bindings;
DROP TABLE IF EXISTS deployment_template_versions;
DROP TABLE IF EXISTS deployment_template_drafts;
DROP TABLE IF EXISTS deployment_templates;
DROP TABLE IF EXISTS integrations;