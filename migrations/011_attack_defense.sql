-- P4: Attack+Defense Git assignments, webhook dedup, durable isolated runs and logs.
-- Additive only. 006 stays unchanged; down removes only objects created here.

-- +goose Up
CREATE TABLE ad_configs (
    module_id UUID PRIMARY KEY REFERENCES modules(id) ON DELETE CASCADE,
    default_source_branch TEXT NOT NULL CHECK (char_length(default_source_branch) BETWEEN 1 AND 255),
    default_exploit_branch TEXT NOT NULL CHECK (char_length(default_exploit_branch) BETWEEN 1 AND 255),
    runner_image TEXT NOT NULL CHECK (char_length(runner_image) BETWEEN 1 AND 512),
    run_timeout_seconds INTEGER NOT NULL CHECK (run_timeout_seconds BETWEEN 1 AND 3600),
    max_concurrent_runs_per_subject INTEGER NOT NULL CHECK (max_concurrent_runs_per_subject BETWEEN 1 AND 10),
    manual_rerun_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ad_vulnerabilities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    key TEXT NOT NULL CHECK (key ~ '^[A-Za-z0-9_.-]+$'),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    exploit_points INTEGER NOT NULL CHECK (exploit_points BETWEEN 0 AND 1000000000),
    patch_points INTEGER NOT NULL CHECK (patch_points BETWEEN 0 AND 1000000000),
    exploit_test_selector TEXT NOT NULL CHECK (char_length(exploit_test_selector) BETWEEN 1 AND 255),
    patch_test_selector TEXT NOT NULL CHECK (char_length(patch_test_selector) BETWEEN 1 AND 255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (module_id, key)
);

CREATE TABLE ad_repository_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    source_integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    source_repository TEXT NOT NULL CHECK (char_length(source_repository) BETWEEN 1 AND 512),
    source_branch TEXT NOT NULL CHECK (char_length(source_branch) BETWEEN 1 AND 255),
    exploit_integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE RESTRICT,
    exploit_repository TEXT NOT NULL CHECK (char_length(exploit_repository) BETWEEN 1 AND 512),
    exploit_branch TEXT NOT NULL CHECK (char_length(exploit_branch) BETWEEN 1 AND 255),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (module_id, subject_type, subject_id)
);
CREATE UNIQUE INDEX ad_repository_assignments_source_uq
    ON ad_repository_assignments (module_id, source_integration_id, source_repository, source_branch);
CREATE UNIQUE INDEX ad_repository_assignments_exploit_uq
    ON ad_repository_assignments (module_id, exploit_integration_id, exploit_repository, exploit_branch);

CREATE TABLE ad_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    repository_assignment_id UUID NOT NULL REFERENCES ad_repository_assignments(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('EXPLOIT','PATCH')),
    commit_sha TEXT NOT NULL CHECK (commit_sha ~ '^[A-Fa-f0-9]{40}$|^[A-Fa-f0-9]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('QUEUED','RUNNING','PASSED','FAILED','ERROR','CANCELED')),
    provider_delivery_id TEXT,
    requested_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    error TEXT
);
CREATE UNIQUE INDEX ad_runs_logical_uq
    ON ad_runs (module_id, subject_type, subject_id, kind, lower(commit_sha));
CREATE INDEX ad_runs_status_idx ON ad_runs (status, created_at);

CREATE TABLE ad_run_logs (
    run_id UUID NOT NULL REFERENCES ad_runs(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    line TEXT NOT NULL CHECK (char_length(line) <= 4000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE git_webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_binding_id UUID NOT NULL REFERENCES ad_repository_assignments(id) ON DELETE CASCADE,
    provider_delivery_id TEXT NOT NULL CHECK (char_length(provider_delivery_id) BETWEEN 1 AND 255),
    commit_sha TEXT NOT NULL CHECK (commit_sha ~ '^[A-Fa-f0-9]{40}$|^[A-Fa-f0-9]{64}$'),
    status TEXT NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED','PROCESSED','DUPLICATE','REJECTED','ERROR')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    error TEXT,
    UNIQUE (repository_binding_id, provider_delivery_id)
);
CREATE INDEX git_webhook_deliveries_commit_idx
    ON git_webhook_deliveries (repository_binding_id, lower(commit_sha));

-- +goose Down
DROP TABLE IF EXISTS git_webhook_deliveries;
DROP TABLE IF EXISTS ad_run_logs;
DROP TABLE IF EXISTS ad_runs;
DROP TABLE IF EXISTS ad_repository_assignments;
DROP TABLE IF EXISTS ad_vulnerabilities;
DROP TABLE IF EXISTS ad_configs;