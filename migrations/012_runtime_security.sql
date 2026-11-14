-- Runtime/security invariants before P2/P4: template allowlists, network leases,
-- immutable desired snapshots, AD repository bindings/results/awards, PENTEST files.
-- Additive only; 007-011 stay unchanged.

-- +goose Up
-- Exact immutable-version allowlists. Only global admin mutates these rows.
CREATE TABLE deployment_template_allowlist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_version_id UUID NOT NULL REFERENCES deployment_template_versions(id) ON DELETE CASCADE,
    board_id UUID REFERENCES boards(id) ON DELETE CASCADE,
    module_id UUID,
    parameters_policy JSONB,
    approved_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK (board_id IS NOT NULL OR module_id IS NULL),
    CHECK (module_id IS NULL OR board_id IS NOT NULL),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX deployment_template_allowlist_active_uq
    ON deployment_template_allowlist (template_version_id, board_id, module_id)
    NULLS NOT DISTINCT WHERE revoked_at IS NULL;
CREATE INDEX deployment_template_allowlist_scope_idx
    ON deployment_template_allowlist (board_id, module_id, template_version_id)
    WHERE revoked_at IS NULL;

CREATE TABLE deployment_template_allowlist_targets (
    allowlist_id UUID NOT NULL REFERENCES deployment_template_allowlist(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('VM','CONTAINER')),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    PRIMARY KEY (allowlist_id, kind, integration_id)
);
CREATE INDEX deployment_template_allowlist_targets_integration_idx
    ON deployment_template_allowlist_targets (integration_id, allowlist_id);

-- Trusted core allocation only; never copied from a client manifest.
CREATE TABLE instance_network_leases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('VM','CONTAINER')),
    allocation_key TEXT NOT NULL CHECK (char_length(allocation_key) BETWEEN 1 AND 255),
    claim JSONB NOT NULL DEFAULT '{}',
    leased_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    released_at TIMESTAMPTZ,
    CHECK (released_at IS NULL OR released_at >= leased_at)
);
CREATE UNIQUE INDEX instance_network_leases_live_uq
    ON instance_network_leases (integration_id, allocation_key) WHERE released_at IS NULL;
CREATE INDEX instance_network_leases_instance_idx
    ON instance_network_leases (instance_id, released_at);

ALTER TABLE instances
    ADD COLUMN binding_id UUID REFERENCES deployment_bindings(id) ON DELETE SET NULL,
    ADD COLUMN template_version_id UUID REFERENCES deployment_template_versions(id) ON DELETE RESTRICT,
    ADD COLUMN desired_snapshot JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN request_actor_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN removed_at TIMESTAMPTZ;

-- Provider inventory must survive board/module deletion until cleanup.
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_board_id_module_id_fkey;
ALTER TABLE instances ADD CONSTRAINT instances_board_id_module_id_fkey
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX instances_live_owner_scope_uq
    ON instances (board_id, module_id, scope, scope_uuid, subject_type, subject_id)
    WHERE removed_at IS NULL;
CREATE INDEX instances_removed_idx ON instances (removed_at) WHERE removed_at IS NOT NULL;

ALTER TABLE instance_operations
    ADD COLUMN requested_by UUID REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN authorized_context JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN desired_snapshot JSONB NOT NULL DEFAULT '{}';

-- PENTEST report bytes are protected by the same file lifecycle.
ALTER TABLE file_objects
    ADD COLUMN attached_pentest_submission_id UUID REFERENCES pentest_submissions(id) ON DELETE SET NULL;
ALTER TABLE file_objects
    ADD CONSTRAINT file_objects_one_attachment_check
        CHECK (attached_submission_id IS NULL OR attached_pentest_submission_id IS NULL);
CREATE INDEX file_objects_pentest_submission_idx
    ON file_objects (attached_pentest_submission_id) WHERE attached_pentest_submission_id IS NOT NULL;

-- Published template versions are immutable at the database layer.
-- +goose StatementBegin
CREATE FUNCTION deployment_template_versions_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'published deployment template version is immutable';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER deployment_template_versions_immutable
    BEFORE UPDATE OR DELETE ON deployment_template_versions
    FOR EACH ROW EXECUTE FUNCTION deployment_template_versions_immutable();

-- AD repository credentials live once per integration/repository/branch.
CREATE TABLE repository_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    repository_ref TEXT NOT NULL CHECK (char_length(repository_ref) BETWEEN 1 AND 512),
    branch TEXT NOT NULL CHECK (char_length(branch) BETWEEN 1 AND 255),
    webhook_secret_ciphertext BYTEA,
    webhook_secret_key_version INTEGER,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (integration_id, repository_ref, branch)
);

ALTER TABLE ad_repository_assignments
    ADD COLUMN source_binding_id UUID REFERENCES repository_bindings(id) ON DELETE RESTRICT,
    ADD COLUMN exploit_binding_id UUID REFERENCES repository_bindings(id) ON DELETE RESTRICT;

-- 011 was additive and empty in the accepted tree. Keep old rows untouched: abort
-- with an explicit message instead of deleting or guessing a backfill.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM git_webhook_deliveries) THEN
        RAISE EXCEPTION '012: git_webhook_deliveries has rows; apply a manual nullable binding backfill before replacing its FK';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE git_webhook_deliveries DROP CONSTRAINT IF EXISTS git_webhook_deliveries_repository_binding_id_fkey;
ALTER TABLE git_webhook_deliveries ADD CONSTRAINT git_webhook_deliveries_repository_binding_id_fkey
    FOREIGN KEY (repository_binding_id) REFERENCES repository_bindings(id) ON DELETE CASCADE;

CREATE TABLE ad_run_results (
    run_id UUID NOT NULL REFERENCES ad_runs(id) ON DELETE CASCADE,
    vulnerability_id UUID NOT NULL REFERENCES ad_vulnerabilities(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('PASSED','FAILED','ERROR','SKIPPED')),
    safe_verdict JSONB NOT NULL DEFAULT '{}',
    score_awarded INTEGER CHECK (score_awarded IS NULL OR score_awarded >= 0),
    PRIMARY KEY (run_id, vulnerability_id)
);

CREATE TABLE ad_awards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    vulnerability_id UUID NOT NULL REFERENCES ad_vulnerabilities(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('EXPLOIT','PATCH')),
    run_id UUID NOT NULL REFERENCES ad_runs(id) ON DELETE RESTRICT,
    points INTEGER NOT NULL CHECK (points >= 0),
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (subject_type, subject_id, vulnerability_id, kind)
);
CREATE INDEX ad_awards_module_idx ON ad_awards (module_id, awarded_at);

-- +goose Down
DROP TABLE IF EXISTS ad_awards;
DROP TABLE IF EXISTS ad_run_results;
ALTER TABLE git_webhook_deliveries DROP CONSTRAINT IF EXISTS git_webhook_deliveries_repository_binding_id_fkey;
ALTER TABLE git_webhook_deliveries ADD CONSTRAINT git_webhook_deliveries_repository_binding_id_fkey
    FOREIGN KEY (repository_binding_id) REFERENCES ad_repository_assignments(id) ON DELETE CASCADE;
ALTER TABLE ad_repository_assignments
    DROP COLUMN IF EXISTS exploit_binding_id,
    DROP COLUMN IF EXISTS source_binding_id;
DROP TABLE IF EXISTS repository_bindings;

DROP TRIGGER IF EXISTS deployment_template_versions_immutable ON deployment_template_versions;
DROP FUNCTION IF EXISTS deployment_template_versions_immutable();

DROP INDEX IF EXISTS file_objects_pentest_submission_idx;
ALTER TABLE file_objects
    DROP CONSTRAINT IF EXISTS file_objects_one_attachment_check,
    DROP COLUMN IF EXISTS attached_pentest_submission_id;

ALTER TABLE instance_operations
    DROP COLUMN IF EXISTS desired_snapshot,
    DROP COLUMN IF EXISTS authorized_context,
    DROP COLUMN IF EXISTS requested_by;

DROP INDEX IF EXISTS instances_removed_idx;
DROP INDEX IF EXISTS instances_live_owner_scope_uq;
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_board_id_module_id_fkey;
ALTER TABLE instances ADD CONSTRAINT instances_board_id_module_id_fkey
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE;
ALTER TABLE instances
    DROP COLUMN IF EXISTS removed_at,
    DROP COLUMN IF EXISTS request_actor_id,
    DROP COLUMN IF EXISTS desired_snapshot,
    DROP COLUMN IF EXISTS template_version_id,
    DROP COLUMN IF EXISTS binding_id;

DROP TABLE IF EXISTS instance_network_leases;
DROP TABLE IF EXISTS deployment_template_allowlist_targets;
DROP TABLE IF EXISTS deployment_template_allowlist;