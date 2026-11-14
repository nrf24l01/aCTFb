package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const enqueueSQL = `
INSERT INTO events_outbox
    (id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING
`

// PostgresOutbox writes to events_outbox in the caller's transaction. It never
// begins, commits, or rolls back that transaction.
type PostgresOutbox struct {
	db       *postgres.DB
	registry *Registry
}

type OutboxOption func(*PostgresOutbox)

func WithRegistry(registry *Registry) OutboxOption {
	return func(outbox *PostgresOutbox) {
		if registry != nil {
			outbox.registry = registry
		}
	}
}

// NewOutbox constructs an outbox. db may be nil for enqueue-only use; dispatcher
// methods require it.
func NewOutbox(db *postgres.DB, opts ...OutboxOption) *PostgresOutbox {
	outbox := &PostgresOutbox{
		db:       db,
		registry: CoreRegistry(),
	}
	for _, opt := range opts {
		opt(outbox)
	}
	return outbox
}

var _ Outbox = (*PostgresOutbox)(nil)

func (o *PostgresOutbox) Enqueue(ctx context.Context, tx Tx, event Event) error {
	if tx == nil {
		return errors.New("events: enqueue requires a caller transaction")
	}

	event = normalizeEvent(event)
	if err := o.registryOrDefault().ValidateEvent(event); err != nil {
		return err
	}

	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	var subjectType any
	if event.SubjectType != "" {
		subjectType = event.SubjectType
	}

	if _, err := tx.Exec(
		ctx,
		enqueueSQL,
		event.ID,
		event.Type,
		event.OccurredAt,
		event.BoardID,
		event.ModuleID,
		subjectType,
		event.SubjectID,
		string(payload),
	); err != nil {
		return fmt.Errorf("events: enqueue %s: %w", event.ID, err)
	}

	return nil
}

func (o *PostgresOutbox) registryOrDefault() *Registry {
	if o != nil && o.registry != nil {
		return o.registry
	}
	return CoreRegistry()
}

func normalizeEvent(event Event) Event {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	return event
}
