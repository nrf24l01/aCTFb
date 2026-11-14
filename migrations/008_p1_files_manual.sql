-- P1 remaining: submission drafts/files, custom MANUAL policy persistence, transactional core events.
-- Additive only. 006 stays unchanged; down removes only objects created here.

-- +goose Up
ALTER TABLE tasks
    ADD COLUMN submission_text_policy JSONB,
    ADD COLUMN submission_files_policy JSONB;

CREATE TABLE submission_drafts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    module_id UUID NOT NULL,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('TASK','PENTEST')),
    task_id UUID,
    created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','CONSUMED','EXPIRED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    FOREIGN KEY (board_id, module_id, task_id) REFERENCES tasks(board_id, module_id, id) ON DELETE CASCADE,
    CHECK ((resource_type = 'TASK' AND task_id IS NOT NULL) OR
           (resource_type = 'PENTEST' AND task_id IS NULL))
);
CREATE INDEX submission_drafts_owner_idx
    ON submission_drafts (created_by_user_id, status, expires_at);
CREATE INDEX submission_drafts_scope_idx
    ON submission_drafts (board_id, module_id, task_id);

CREATE TABLE file_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    task_id UUID,
    draft_id UUID REFERENCES submission_drafts(id) ON DELETE CASCADE,
    uploader_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('SUBMISSION_DRAFT','TASK_ATTACHMENT','PENTEST_ATTACHMENT')),
    filename TEXT NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255),
    content_type TEXT NOT NULL CHECK (char_length(content_type) BETWEEN 1 AND 255),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[A-Fa-f0-9]{64}$'),
    quarantine_bucket TEXT NOT NULL,
    quarantine_key TEXT NOT NULL,
    clean_bucket TEXT,
    clean_key TEXT,
    status TEXT NOT NULL CHECK (status IN ('AWAITING_UPLOAD','VALIDATING','READY','REJECTED','ATTACHED','EXPIRED')),
    rejection_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    validated_at TIMESTAMPTZ,
    attached_submission_id UUID REFERENCES submissions(id) ON DELETE SET NULL,
    UNIQUE (quarantine_key),
    UNIQUE (clean_key),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    FOREIGN KEY (board_id, module_id, task_id) REFERENCES tasks(board_id, module_id, id) ON DELETE CASCADE,
    CHECK ((purpose = 'SUBMISSION_DRAFT' AND draft_id IS NOT NULL) OR
           (purpose <> 'SUBMISSION_DRAFT')),
    CHECK ((purpose = 'TASK_ATTACHMENT' AND task_id IS NOT NULL) OR
           (purpose = 'PENTEST_ATTACHMENT' AND task_id IS NULL) OR
           (purpose = 'SUBMISSION_DRAFT'))
);
CREATE INDEX file_objects_draft_idx ON file_objects (draft_id, status);
CREATE INDEX file_objects_sha_idx ON file_objects (sha256);
CREATE INDEX file_objects_scope_idx ON file_objects (board_id, module_id, task_id);

CREATE TABLE submission_files (
    submission_id UUID NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES file_objects(id) ON DELETE RESTRICT,
    PRIMARY KEY (submission_id, file_id)
);

CREATE TABLE task_attachments (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES file_objects(id) ON DELETE RESTRICT,
    PRIMARY KEY (task_id, file_id)
);

CREATE TABLE events_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type TEXT NOT NULL CHECK (char_length(type) BETWEEN 1 AND 128),
    occurred_at TIMESTAMPTZ NOT NULL,
    board_id UUID,
    module_id UUID,
    subject_type TEXT CHECK (subject_type IS NULL OR subject_type IN ('USER','TEAM')),
    subject_id UUID,
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT
);
CREATE INDEX events_outbox_unpublished_idx ON events_outbox (created_at) WHERE published_at IS NULL;

ALTER TABLE submissions
    ADD COLUMN reviewed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN review_decision TEXT CHECK (review_decision IS NULL OR review_decision IN ('ACCEPT','REJECT'));

-- +goose Down
ALTER TABLE submissions
    DROP COLUMN IF EXISTS review_decision,
    DROP COLUMN IF EXISTS reviewed_by_user_id;

DROP TABLE IF EXISTS events_outbox;
DROP TABLE IF EXISTS task_attachments;
DROP TABLE IF EXISTS submission_files;
DROP TABLE IF EXISTS file_objects;
DROP TABLE IF EXISTS submission_drafts;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS submission_files_policy,
    DROP COLUMN IF EXISTS submission_text_policy;