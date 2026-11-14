CREATE TABLE IF NOT EXISTS hello_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    author_user_id UUID,
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hello_messages_board_idx ON hello_messages (board_id, created_at DESC);

CREATE TABLE IF NOT EXISTS hello_settings (
    key TEXT PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (char_length(value) <= 2000),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);