-- Durable MANUAL submission text with an immutable task_type snapshot.
-- 006-012 stay unchanged; down removes only 013 objects.

-- +goose Up
ALTER TABLE submissions ADD COLUMN submitted_text TEXT;
ALTER TABLE submissions ADD COLUMN task_type TEXT;

UPDATE submissions s
SET task_type = t.type
FROM tasks t
WHERE t.board_id = s.board_id
  AND t.module_id = s.module_id
  AND t.id = s.task_id;

ALTER TABLE submissions ALTER COLUMN task_type SET NOT NULL;
ALTER TABLE submissions ADD CONSTRAINT submissions_task_type_check
    CHECK (task_type IN ('FLAG','MANUAL','INSTANCE','EXPLOIT','PATCH','PENTEST_REPORT','EXTENSION'));
ALTER TABLE submissions ADD CONSTRAINT submissions_manual_text_check
    CHECK (submitted_text IS NULL OR
           (task_type = 'MANUAL' AND octet_length(submitted_text) <= 4000000));

-- Insert: derive task_type from the authoritative task; never trust the client.
-- Update: snapshot is immutable. This keeps MANUAL text readable if a task is
-- later changed, while a spoofed INSERT is rejected after derivation.
-- +goose StatementBegin
CREATE FUNCTION submissions_manual_text_guard() RETURNS trigger AS $$
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
CREATE TRIGGER submissions_manual_text_guard
    BEFORE INSERT OR UPDATE OF submitted_text, task_type ON submissions
    FOR EACH ROW EXECUTE FUNCTION submissions_manual_text_guard();

-- +goose Down
DROP TRIGGER IF EXISTS submissions_manual_text_guard ON submissions;
DROP FUNCTION IF EXISTS submissions_manual_text_guard();
ALTER TABLE submissions
    DROP CONSTRAINT IF EXISTS submissions_manual_text_check,
    DROP CONSTRAINT IF EXISTS submissions_task_type_check,
    DROP COLUMN IF EXISTS task_type,
    DROP COLUMN IF EXISTS submitted_text;