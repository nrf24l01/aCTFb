-- P1 remaining: teams, access groups, audiences, scoped grants, TEAM score subjects.
-- Additive only. 006 stays unchanged; down removes only objects created here.

-- +goose Up
CREATE TABLE access_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE access_group_members (
    group_id UUID NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX access_group_members_user_idx ON access_group_members (user_id, group_id);

CREATE TABLE board_access_groups (
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (board_id, group_id)
);
CREATE INDEX board_access_groups_group_idx ON board_access_groups (group_id, board_id);

CREATE TABLE audiences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (board_id, name),
    UNIQUE (board_id, id)
);

CREATE TABLE audience_members (
    audience_id UUID NOT NULL REFERENCES audiences(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (audience_id, subject_type, subject_id)
);
CREATE INDEX audience_members_subject_idx ON audience_members (subject_type, subject_id, audience_id);

-- board_id is persisted so composite FKs prove module and audience belong to the same board.
CREATE TABLE module_audiences (
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    audience_id UUID NOT NULL,
    PRIMARY KEY (module_id, audience_id),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    FOREIGN KEY (board_id, audience_id) REFERENCES audiences(board_id, id) ON DELETE CASCADE
);
CREATE INDEX module_audiences_board_idx ON module_audiences (board_id, audience_id);

ALTER TABLE boards
    ADD COLUMN team_min_members INTEGER,
    ADD COLUMN team_max_members INTEGER,
    ADD COLUMN team_allow_self_create BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN team_allow_leave_after_start BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN invite_code_hash TEXT,
    ADD COLUMN invite_code_ciphertext BYTEA,
    ADD COLUMN invite_code_expires_at TIMESTAMPTZ;

-- Existing TEAM rows (if any were created manually) get a valid default before the check.
UPDATE boards
SET team_min_members = 1, team_max_members = 100
WHERE participation_mode = 'TEAM' AND team_min_members IS NULL;

ALTER TABLE boards
    ADD CONSTRAINT boards_team_settings_check CHECK (
        team_min_members IS NULL OR team_max_members IS NULL OR
        (team_min_members BETWEEN 1 AND 100 AND team_max_members BETWEEN team_min_members AND 100)
    ),
    ADD CONSTRAINT boards_ind_team_settings_check CHECK (
        participation_mode <> 'INDIVIDUAL' OR
        (team_min_members IS NULL AND team_max_members IS NULL AND
         team_allow_self_create = FALSE AND team_allow_leave_after_start = FALSE)
    );

CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 3 AND 64),
    captain_user_id UUID NOT NULL,
    invite_key_hash TEXT,
    invite_key_ciphertext BYTEA,
    invite_key_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (board_id, name),
    UNIQUE (board_id, id),
    CONSTRAINT teams_captain_participant_fk
        FOREIGN KEY (board_id, captain_user_id)
        REFERENCES board_participants(board_id, user_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE team_members (
    board_id UUID NOT NULL,
    team_id UUID NOT NULL,
    user_id UUID NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (board_id, user_id),
    FOREIGN KEY (board_id, team_id) REFERENCES teams(board_id, id) ON DELETE CASCADE,
    FOREIGN KEY (board_id, user_id) REFERENCES board_participants(board_id, user_id) ON DELETE CASCADE
);
CREATE INDEX team_members_team_idx ON team_members (board_id, team_id);

-- Explicit supplemental grants. Core role matrix is never edited by grants.
CREATE TABLE permission_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (char_length(permission) BETWEEN 1 AND 128),
    board_id UUID REFERENCES boards(id) ON DELETE CASCADE,
    module_id UUID,
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (board_id IS NOT NULL OR module_id IS NULL),
    CHECK (module_id IS NULL OR board_id IS NOT NULL),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX permission_grants_active_uq
    ON permission_grants (user_id, permission, board_id, module_id)
    NULLS NOT DISTINCT WHERE revoked_at IS NULL;
CREATE INDEX permission_grants_lookup_idx
    ON permission_grants (user_id, board_id, module_id, permission)
    WHERE revoked_at IS NULL;

ALTER TABLE submissions
    ADD COLUMN subject_type TEXT NOT NULL DEFAULT 'USER' CHECK (subject_type IN ('USER','TEAM')),
    ADD COLUMN subject_id UUID,
    ADD COLUMN submitted_by_user_id UUID;

UPDATE submissions
SET subject_id = user_id,
    submitted_by_user_id = user_id
WHERE subject_id IS NULL OR submitted_by_user_id IS NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM submissions
        WHERE status = 'ACCEPTED'
        GROUP BY subject_type, subject_id, task_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION '007: duplicate accepted submissions per subject/task; refusing to drop history';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE submissions
    ALTER COLUMN subject_id SET NOT NULL,
    ALTER COLUMN submitted_by_user_id SET NOT NULL,
    ADD CONSTRAINT submissions_submitted_by_fk
        FOREIGN KEY (submitted_by_user_id) REFERENCES users(id) ON DELETE RESTRICT;

DROP INDEX submissions_one_accepted_per_user_task;
CREATE UNIQUE INDEX submissions_one_accepted_per_subject_task
    ON submissions (subject_type, subject_id, task_id) WHERE status = 'ACCEPTED';
CREATE UNIQUE INDEX submissions_one_accepted_per_user_task
    ON submissions (board_id, submitted_by_user_id, task_id) WHERE status = 'ACCEPTED';
CREATE INDEX submissions_subject_history_idx
    ON submissions (board_id, subject_type, subject_id, created_at DESC);

-- Compatibility bridge for the accepted P1 domain until the boards worker fills
-- subject columns explicitly: legacy USER inserts inherit user_id.
-- +goose StatementBegin
CREATE FUNCTION submissions_fill_subject_defaults() RETURNS trigger AS $$
BEGIN
    IF NEW.subject_type IS NULL THEN
        NEW.subject_type := 'USER';
    END IF;
    IF NEW.subject_id IS NULL THEN
        NEW.subject_id := NEW.user_id;
    END IF;
    IF NEW.submitted_by_user_id IS NULL THEN
        NEW.submitted_by_user_id := NEW.user_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER submissions_fill_subject_defaults
    BEFORE INSERT ON submissions
    FOR EACH ROW EXECUTE FUNCTION submissions_fill_subject_defaults();

-- Backfill only audiences that actually exist for the module's board.
INSERT INTO module_audiences (board_id, module_id, audience_id)
SELECT m.board_id, m.id, a.id
FROM modules m
CROSS JOIN LATERAL unnest(m.audience_uuids) AS u(audience_id)
JOIN audiences a ON a.id = u.audience_id AND a.board_id = m.board_id
ON CONFLICT DO NOTHING;

-- +goose Down
DROP INDEX IF EXISTS submissions_subject_history_idx;
DROP INDEX IF EXISTS submissions_one_accepted_per_user_task;
DROP INDEX IF EXISTS submissions_one_accepted_per_subject_task;
CREATE UNIQUE INDEX submissions_one_accepted_per_user_task
    ON submissions (user_id, task_id) WHERE status = 'ACCEPTED';
DROP TRIGGER IF EXISTS submissions_fill_subject_defaults ON submissions;
DROP FUNCTION IF EXISTS submissions_fill_subject_defaults();
ALTER TABLE submissions
    DROP CONSTRAINT IF EXISTS submissions_submitted_by_fk,
    DROP COLUMN IF EXISTS submitted_by_user_id,
    DROP COLUMN IF EXISTS subject_id,
    DROP COLUMN IF EXISTS subject_type;

DROP TABLE IF EXISTS permission_grants;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS module_audiences;
DROP TABLE IF EXISTS audience_members;
DROP TABLE IF EXISTS audiences;
DROP TABLE IF EXISTS board_access_groups;
DROP TABLE IF EXISTS access_group_members;
DROP TABLE IF EXISTS access_groups;

ALTER TABLE boards
    DROP CONSTRAINT IF EXISTS boards_ind_team_settings_check,
    DROP CONSTRAINT IF EXISTS boards_team_settings_check,
    DROP COLUMN IF EXISTS invite_code_expires_at,
    DROP COLUMN IF EXISTS invite_code_ciphertext,
    DROP COLUMN IF EXISTS invite_code_hash,
    DROP COLUMN IF EXISTS team_allow_leave_after_start,
    DROP COLUMN IF EXISTS team_allow_self_create,
    DROP COLUMN IF EXISTS team_max_members,
    DROP COLUMN IF EXISTS team_min_members;