-- Operator-only admin CLI audit rows have no authenticated user actor.
-- Migration 006 declared audit_events.actor_user_id NOT NULL; making this one
-- column nullable is the minimal compatible change. Existing authenticated
-- writers still supply real users. No system/fake user row is created.

-- +goose Up
ALTER TABLE audit_events ALTER COLUMN actor_user_id DROP NOT NULL;

-- +goose Down
-- Do not delete operator audit history during a rollback. Refuse instead of
-- silently destroying CLI audit rows.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM audit_events WHERE actor_user_id IS NULL) THEN
        RAISE EXCEPTION 'cannot restore audit_events.actor_user_id NOT NULL while operator CLI audit rows exist';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE audit_events ALTER COLUMN actor_user_id SET NOT NULL;