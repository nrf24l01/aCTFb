package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	defaultBatchSize  = 50
	defaultPollPeriod = time.Second
)

// Dispatcher claims unpublished outbox rows in short database transactions,
// publishes them outside the transaction, then marks them published. Delivery
// is at-least-once; JetStream deduplicates retries by event/message ID.
type Dispatcher struct {
	outbox    *PostgresOutbox
	publisher Publisher
	batchSize int
	poll      time.Duration
	logger    *slog.Logger
}

type DispatcherOption func(*Dispatcher)

func WithBatchSize(size int) DispatcherOption {
	return func(dispatcher *Dispatcher) {
		if size > 0 {
			dispatcher.batchSize = size
		}
	}
}

func WithPollInterval(interval time.Duration) DispatcherOption {
	return func(dispatcher *Dispatcher) {
		if interval > 0 {
			dispatcher.poll = interval
		}
	}
}

func WithDispatcherLogger(logger *slog.Logger) DispatcherOption {
	return func(dispatcher *Dispatcher) {
		if logger != nil {
			dispatcher.logger = logger
		}
	}
}

func WithDispatcherRegistry(registry *Registry) DispatcherOption {
	return func(dispatcher *Dispatcher) {
		if registry != nil {
			dispatcher.outbox.registry = registry
		}
	}
}

func NewDispatcher(db *postgres.DB, publisher Publisher, opts ...DispatcherOption) *Dispatcher {
	dispatcher := &Dispatcher{
		outbox:    NewOutbox(db),
		publisher: publisher,
		batchSize: defaultBatchSize,
		poll:      defaultPollPeriod,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(dispatcher)
	}
	return dispatcher
}

// Run polls until ctx is canceled. Errors are logged; events stay unpublished
// for retry.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d == nil || d.publisher == nil {
		return errors.New("events: dispatcher publisher is not configured")
	}

	for {
		published, err := d.RunOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			d.logger.Error("events dispatcher batch failed", "error", err)
		}
		if published >= d.batchSize {
			continue
		}

		timer := time.NewTimer(d.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

type invalidOutboxRow struct {
	id  uuid.UUID
	err error
}

// RunOnce claims one batch and publishes it. It returns the number of rows
// successfully marked published.
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	if d == nil || d.outbox == nil || d.outbox.db == nil || d.outbox.db.Pool == nil {
		return 0, errors.New("events: dispatcher database is not configured")
	}
	if d.publisher == nil {
		return 0, errors.New("events: dispatcher publisher is not configured")
	}

	batch, invalid, err := d.claimBatch(ctx)
	if err != nil {
		return 0, err
	}

	published := 0
	var firstErr error

	for _, row := range invalid {
		if recordErr := d.recordFailure(ctx, row.id, row.err); recordErr != nil && firstErr == nil {
			firstErr = recordErr
		}
		if firstErr == nil {
			firstErr = row.err
		}
	}

	for _, event := range batch {
		if err := d.publisher.Publish(ctx, event); err != nil {
			if recordErr := d.recordFailure(ctx, event.ID, err); recordErr != nil && firstErr == nil {
				firstErr = recordErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		if err := d.markPublished(ctx, event.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		published++
	}

	return published, firstErr
}

func (d *Dispatcher) claimBatch(ctx context.Context) ([]Event, []invalidOutboxRow, error) {
	tx, err := d.outbox.db.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("events: begin claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload
		FROM events_outbox
		WHERE published_at IS NULL
		ORDER BY created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, d.batchSize)
	if err != nil {
		return nil, nil, fmt.Errorf("events: claim outbox rows: %w", err)
	}
	defer rows.Close()

	batch := make([]Event, 0, d.batchSize)
	invalid := make([]invalidOutboxRow, 0)
	claimedIDs := make([]uuid.UUID, 0, d.batchSize)

	for rows.Next() {
		var (
			event       Event
			boardID     pgtype.UUID
			moduleID    pgtype.UUID
			subjectType pgtype.Text
			subjectID   pgtype.UUID
			payload     []byte
		)
		if err := rows.Scan(
			&event.ID,
			&event.Type,
			&event.OccurredAt,
			&boardID,
			&moduleID,
			&subjectType,
			&subjectID,
			&payload,
		); err != nil {
			return nil, nil, fmt.Errorf("events: scan outbox row: %w", err)
		}

		event.BoardID = uuidPointer(boardID)
		event.ModuleID = uuidPointer(moduleID)
		if subjectType.Valid {
			event.SubjectType = subjectType.String
		}
		event.SubjectID = uuidPointer(subjectID)
		if len(payload) == 0 {
			event.Payload = json.RawMessage(`{}`)
		} else {
			event.Payload = json.RawMessage(payload)
		}

		claimedIDs = append(claimedIDs, event.ID)
		if err := d.outbox.registryOrDefault().ValidateEvent(event); err != nil {
			invalid = append(invalid, invalidOutboxRow{id: event.ID, err: err})
			continue
		}
		batch = append(batch, event)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("events: iterate outbox rows: %w", err)
	}

	for _, eventID := range claimedIDs {
		if _, err := tx.Exec(
			ctx,
			`UPDATE events_outbox SET attempts = attempts + 1 WHERE id = $1`,
			eventID,
		); err != nil {
			return nil, nil, fmt.Errorf("events: record claim attempt: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("events: commit claim transaction: %w", err)
	}

	return batch, invalid, nil
}

func (d *Dispatcher) markPublished(ctx context.Context, eventID uuid.UUID) error {
	_, err := d.outbox.db.Pool.Exec(ctx, `
		UPDATE events_outbox
		SET published_at = NOW(), last_error = NULL
		WHERE id = $1
	`, eventID)
	if err != nil {
		return fmt.Errorf("events: mark published %s: %w", eventID, err)
	}
	return nil
}

func (d *Dispatcher) recordFailure(ctx context.Context, eventID uuid.UUID, cause error) error {
	message := cause.Error()
	if len(message) > 1024 {
		message = message[:1024]
	}
	_, err := d.outbox.db.Pool.Exec(ctx, `
		UPDATE events_outbox SET last_error = $2 WHERE id = $1
	`, eventID, message)
	if err != nil {
		return fmt.Errorf("events: record failure %s: %w", eventID, err)
	}
	return nil
}

func uuidPointer(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}
