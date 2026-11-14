CREATE TABLE IF NOT EXISTS score_event_seen (
    event_id UUID PRIMARY KEY,
    event_type TEXT NOT NULL,
    submission_id UUID,
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    task_id UUID,
    subject_type TEXT NOT NULL,
    subject_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT '',
    score INTEGER NOT NULL DEFAULT 0,
    occurred_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS score_event_seen_board_idx ON score_event_seen (board_id, received_at DESC);

CREATE TABLE IF NOT EXISTS score_stats (
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id UUID NOT NULL,
    judged_count INTEGER NOT NULL DEFAULT 0,
    accepted_count INTEGER NOT NULL DEFAULT 0,
    total_score INTEGER NOT NULL DEFAULT 0,
    last_event_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (board_id, module_id, subject_type, subject_id)
);