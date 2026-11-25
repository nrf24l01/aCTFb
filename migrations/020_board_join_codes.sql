-- +goose Up
-- Board enrollment codes. Code-only self-enrollment replaced open self-join:
-- every self-join path now presents a board code. Storage is hash-only, so the
-- catalog and join path need no credential sealer and no runtime feature flag.
-- The plaintext code is shown once to the organizer when it is generated or
-- replaced and is never persisted, logged or returned by a read route.
CREATE TABLE board_join_codes (
    board_id UUID PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE CHECK (char_length(code_hash) = 64),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS board_join_codes;
