-- P1 core slice schema: boards, modules, tasks, participation, submissions, flags, scoped roles, audit.
-- Additive only. Do not edit 001-005. Down drops only the objects created here.

-- +goose Up
CREATE TABLE boards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    participation_mode TEXT NOT NULL CHECK (participation_mode IN ('INDIVIDUAL','TEAM')),
    registration_mode TEXT NOT NULL CHECK (registration_mode IN ('OPEN','INVITE_ONLY','CLOSED')),
    visibility TEXT NOT NULL CHECK (visibility IN ('PUBLIC','PRIVATE')),
    status TEXT NOT NULL DEFAULT 'DRAFT'
        CHECK (status IN ('DRAFT','SCHEDULED','ACTIVE','FINISHED','ARCHIVED')),
    rating_visibility TEXT NOT NULL DEFAULT 'PUBLIC'
        CHECK (rating_visibility IN ('PUBLIC','HIDDEN')),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);
CREATE INDEX boards_status_idx ON boards (status, starts_at, ends_at);

CREATE TABLE modules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (type IN ('TASKS','ATTACK_DEFENSE','PENTEST')),
    allowed_task_types TEXT[] NOT NULL CHECK (cardinality(allowed_task_types) > 0),
    audience_uuids UUID[] NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (board_id, name),
    UNIQUE (board_id, id),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);
CREATE INDEX modules_board_order_idx ON modules (board_id, sort_order, id);

CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 128),
    category TEXT NOT NULL CHECK (char_length(category) BETWEEN 1 AND 64),
    description TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (type IN ('FLAG','MANUAL','INSTANCE','EXPLOIT','PATCH','PENTEST_REPORT')),
    points INTEGER NOT NULL CHECK (points BETWEEN 0 AND 1000000000),
    visible BOOLEAN NOT NULL DEFAULT TRUE,
    submission_mode TEXT NOT NULL CHECK (submission_mode IN ('NONE','TEXT','FILES','TEXT_AND_FILES','TEXT_OR_FILES')),
    submission_checker TEXT NOT NULL CHECK (submission_checker IN ('AUTOMATIC','MANUAL')),
    instance_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    UNIQUE (board_id, module_id, id),
    CHECK (type <> 'FLAG' OR (submission_mode = 'TEXT' AND submission_checker = 'AUTOMATIC'))
);
CREATE INDEX tasks_module_visible_idx ON tasks (board_id, module_id, visible, id);

CREATE TABLE task_flags (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    answer_hash TEXT NOT NULL,
    PRIMARY KEY (task_id, answer_hash)
);

CREATE TABLE board_participants (
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX board_participants_user_idx ON board_participants (user_id, board_id);

CREATE TABLE submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    task_id UUID NOT NULL,
    user_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PENDING','CHECKING','PENDING_REVIEW','ACCEPTED','REJECTED','ERROR')),
    score_awarded INTEGER CHECK (score_awarded IS NULL OR score_awarded >= 0),
    message TEXT,
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
    request_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ,
    FOREIGN KEY (board_id, module_id, task_id) REFERENCES tasks(board_id, module_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (board_id, user_id) REFERENCES board_participants(board_id, user_id) ON DELETE RESTRICT,
    UNIQUE (user_id, task_id, idempotency_key)
);
CREATE UNIQUE INDEX submissions_one_accepted_per_user_task
    ON submissions (user_id, task_id) WHERE status = 'ACCEPTED';
CREATE INDEX submissions_task_status_idx ON submissions (task_id, status, created_at DESC);
CREATE INDEX submissions_user_history_idx ON submissions (user_id, created_at DESC);

CREATE TABLE board_role_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    module_id UUID,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('board_owner','board_editor','reviewer')),
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    CHECK (role <> 'board_owner' OR module_id IS NULL),
    CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE UNIQUE INDEX board_role_bindings_active_uq
    ON board_role_bindings (board_id, module_id, user_id, role)
    NULLS NOT DISTINCT WHERE revoked_at IS NULL;
CREATE INDEX board_role_bindings_actor_scope_idx
    ON board_role_bindings (user_id, board_id, module_id) WHERE revoked_at IS NULL;

CREATE TABLE audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    board_id UUID REFERENCES boards(id) ON DELETE SET NULL,
    module_id UUID,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    details JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX audit_events_board_time_idx ON audit_events (board_id, created_at DESC);
CREATE INDEX audit_events_actor_time_idx ON audit_events (actor_user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS board_role_bindings;
DROP TABLE IF EXISTS submissions;
DROP TABLE IF EXISTS board_participants;
DROP TABLE IF EXISTS task_flags;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS modules;
DROP TABLE IF EXISTS boards;