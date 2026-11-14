-- INSTANCE tasks use the same MANUAL review/text contract as MANUAL tasks.
-- 013 is never edited; this additive migration widens only the text guard.

-- +goose Up
ALTER TABLE submissions DROP CONSTRAINT IF EXISTS submissions_manual_text_check;
ALTER TABLE submissions ADD CONSTRAINT submissions_manual_text_check
    CHECK (submitted_text IS NULL OR
           (task_type IN ('MANUAL','INSTANCE') AND octet_length(submitted_text) <= 4000000));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION submissions_manual_text_guard() RETURNS trigger AS $$
DECLARE
    actual_type TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT t.type INTO actual_type
        FROM tasks t
        WHERE t.board_id = NEW.board_id
          AND t.module_id = NEW.module_id
          AND t.id = NEW.task_id;
        IF actual_type IS NULL THEN
            RAISE EXCEPTION 'submission task not found' USING ERRCODE = '23503';
        END IF;
        NEW.task_type := actual_type;
    ELSIF NEW.task_type IS DISTINCT FROM OLD.task_type THEN
        RAISE EXCEPTION 'submission task_type is immutable';
    END IF;

    IF NEW.submitted_text IS NOT NULL THEN
        IF NEW.task_type NOT IN ('MANUAL','INSTANCE') THEN
            RAISE EXCEPTION 'submitted_text is only allowed for MANUAL/INSTANCE tasks';
        END IF;
        IF octet_length(NEW.submitted_text) > 4000000 THEN
            RAISE EXCEPTION 'submitted_text exceeds 4000000 bytes';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- Refuse to silently discard INSTANCE report text; an operator must archive it.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM submissions WHERE task_type = 'INSTANCE' AND submitted_text IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back 016 while INSTANCE submission text exists';
    END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE submissions DROP CONSTRAINT IF EXISTS submissions_manual_text_check;
ALTER TABLE submissions ADD CONSTRAINT submissions_manual_text_check
    CHECK (submitted_text IS NULL OR
           (task_type = 'MANUAL' AND octet_length(submitted_text) <= 4000000));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION submissions_manual_text_guard() RETURNS trigger AS $$
DECLARE
    actual_type TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT t.type INTO actual_type
        FROM tasks t
        WHERE t.board_id = NEW.board_id
          AND t.module_id = NEW.module_id
          AND t.id = NEW.task_id;
        IF actual_type IS NULL THEN
            RAISE EXCEPTION 'submission task not found' USING ERRCODE = '23503';
        END IF;
        NEW.task_type := actual_type;
    ELSIF NEW.task_type IS DISTINCT FROM OLD.task_type THEN
        RAISE EXCEPTION 'submission task_type is immutable';
    END IF;
    IF NEW.submitted_text IS NOT NULL THEN
        IF NEW.task_type <> 'MANUAL' THEN
            RAISE EXCEPTION 'submitted_text is only allowed for MANUAL tasks';
        END IF;
        IF octet_length(NEW.submitted_text) > 4000000 THEN
            RAISE EXCEPTION 'submitted_text exceeds 4000000 bytes';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd