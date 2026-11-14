package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
)

const (
	maxEventAttempts = 5
	deliveryPoll     = 15 * time.Second
)

// EventDelivery is the admin-facing, bounded delivery row.
type EventDelivery struct {
	EventID     uuid.UUID  `json:"eventId"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   *string    `json:"lastError,omitempty"`
	NextAttempt *time.Time `json:"nextAttemptAt,omitempty"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
}

// startEventConsumers creates one durable JetStream pull consumer per declared
// core subscription. Delivery is at-least-once; plugin_event_deliveries dedupes
// per plugin/event and the plugin is expected to dedupe by event.id as well.
func (m *Manager) startEventConsumers(ctx context.Context, runtime *runtimeBackend) error {
	subscriptions := runtime.installed.Manifest.Events
	if len(subscriptions) == 0 {
		return nil
	}
	if m.nats == nil {
		return errors.New("plugins: event subscriptions require ACTFB_EVENTS JetStream to be configured")
	}
	if m.publisher == nil {
		return errors.New("plugins: event publisher is not configured")
	}
	for _, eventType := range subscriptions {
		eventType := eventType
		durable := pluginConsumerName(runtime.installed.ID, eventType)
		cfg := jetstream.ConsumerConfig{
			Durable:           durable,
			AckPolicy:         jetstream.AckExplicitPolicy,
			FilterSubject:     events.SubjectFor(eventType),
			MaxDeliver:        maxEventAttempts,
			AckWait:           30 * time.Second,
			DeliverPolicy:     jetstream.DeliverAllPolicy,
			ReplayPolicy:      jetstream.ReplayInstantPolicy,
			MaxAckPending:     32,
			InactiveThreshold: 24 * time.Hour,
		}
		consume, err := m.nats.SubscribeJetStream(ctx, events.StreamName, cfg, func(message jetstream.Msg) {
			m.handleEventMessage(ctx, runtime, eventType, message)
		})
		if err != nil {
			return fmt.Errorf("plugins: subscribe %s for %s: %w", eventType, runtime.installed.ID, err)
		}
		runtime.consumers = append(runtime.consumers, consume)
	}
	return nil
}

func (m *Manager) handleEventMessage(ctx context.Context, runtime *runtimeBackend, subscribed string, message jetstream.Msg) {
	var event events.Event
	if err := json.Unmarshal(message.Data(), &event); err != nil {
		_ = message.Term()
		return
	}
	if event.Type != subscribed {
		_ = message.Term()
		return
	}
	if err := m.eventRegistry.ValidateEvent(event); err != nil {
		_ = message.Term()
		return
	}
	done, dead, err := m.deliverEvent(ctx, runtime, event)
	if err != nil || !done {
		if dead {
			_ = message.Ack()
			return
		}
		_ = message.NakWithDelay(backoffForAttempt(1))
		return
	}
	_ = message.Ack()
}

func backoffForAttempt(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<uint(attempt)) * 2 * time.Second
}

// deliverEvent returns (delivered, dead, error). The helper never changes a
// core score or verdict; the plugin receives a read-only event envelope.
func (m *Manager) deliverEvent(ctx context.Context, runtime *runtimeBackend, event events.Event) (bool, bool, error) {
	if event.ID == uuid.Nil {
		return false, true, errors.New("plugins: event id is required")
	}
	var status string
	err := m.db.Pool.QueryRow(ctx, `
		INSERT INTO plugin_event_deliveries (plugin_id, event_id, status, attempts)
		VALUES ($1, $2, 'pending', 0)
		ON CONFLICT (plugin_id, event_id) DO NOTHING
		RETURNING status
	`, string(runtime.installed.ID), event.ID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := m.db.Pool.QueryRow(ctx, `
			SELECT status FROM plugin_event_deliveries WHERE plugin_id = $1 AND event_id = $2
		`, string(runtime.installed.ID), event.ID).Scan(&status); err != nil {
			return false, false, err
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("plugins: claim event delivery: %w", err)
	}
	if status == "delivered" {
		return true, false, nil
	}
	if status == "dead" {
		return false, true, nil
	}

	var attempts int
	if err := m.db.Pool.QueryRow(ctx, `
		UPDATE plugin_event_deliveries
		SET attempts = attempts + 1, status = 'failed', last_error = NULL
		WHERE plugin_id = $1 AND event_id = $2
		RETURNING attempts
	`, string(runtime.installed.ID), event.ID).Scan(&attempts); err != nil {
		return false, false, err
	}
	if attempts > maxEventAttempts {
		m.markDeliveryDead(ctx, runtime.installed.ID, event.ID, "retry budget exhausted")
		return false, true, nil
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		m.markDeliveryFailed(ctx, runtime.installed.ID, event.ID, attempts, "encode event", err)
		return false, attempts >= maxEventAttempts, err
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, m.httpTimeout)
	defer cancel()
	if err := runtime.client.DeliverEvent(deliveryCtx, m.serviceKey, event.ID, encoded); err != nil {
		m.markDeliveryFailed(ctx, runtime.installed.ID, event.ID, attempts, "plugin event delivery", err)
		return false, attempts >= maxEventAttempts, err
	}
	if _, err := m.db.Pool.Exec(ctx, `
		UPDATE plugin_event_deliveries
		SET status = 'delivered', delivered_at = clock_timestamp(), next_attempt_at = NULL, last_error = NULL
		WHERE plugin_id = $1 AND event_id = $2
	`, string(runtime.installed.ID), event.ID); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (m *Manager) markDeliveryFailed(ctx context.Context, id ID, eventID uuid.UUID, attempts int, stage string, cause error) {
	message := truncate(stage+": "+cause.Error(), 2000)
	dead := attempts >= maxEventAttempts
	status := "failed"
	if dead {
		status = "dead"
	}
	next := m.now().Add(backoffForAttempt(attempts))
	_, _ = m.db.Pool.Exec(ctx, `
		UPDATE plugin_event_deliveries
		SET status = $3, last_error = $4, next_attempt_at = $5
		WHERE plugin_id = $1 AND event_id = $2
	`, string(id), eventID, status, message, next)
}

func (m *Manager) markDeliveryDead(ctx context.Context, id ID, eventID uuid.UUID, message string) {
	_, _ = m.db.Pool.Exec(ctx, `
		UPDATE plugin_event_deliveries SET status = 'dead', last_error = $3, next_attempt_at = NULL
		WHERE plugin_id = $1 AND event_id = $2
	`, string(id), eventID, truncate(message, 2000))
}

func pluginConsumerName(id ID, eventType string) string {
	raw := "plugin_" + string(id) + "_" + eventType
	var builder strings.Builder
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	name := builder.String()
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// ListEventDeliveries returns bounded delivery rows for one plugin.
func (m *Manager) ListEventDeliveries(ctx context.Context, principal authorization.Principal, id ID, limit, offset int) ([]EventDelivery, int, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, 0, err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return nil, 0, err
	}
	limit, offset = clampPage(limit, offset)
	rows, err := m.db.Pool.Query(ctx, `
		SELECT event_id, status, attempts, last_error, next_attempt_at, delivered_at
		FROM plugin_event_deliveries
		WHERE plugin_id = $1
		ORDER BY event_id
		LIMIT $2 OFFSET $3
	`, string(id), limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("plugins: list event deliveries: %w", err)
	}
	defer rows.Close()
	items := make([]EventDelivery, 0)
	for rows.Next() {
		var delivery EventDelivery
		var lastError pgtype.Text
		var nextAttempt, deliveredAt pgtype.Timestamptz
		if err := rows.Scan(&delivery.EventID, &delivery.Status, &delivery.Attempts, &lastError, &nextAttempt, &deliveredAt); err != nil {
			return nil, 0, err
		}
		if lastError.Valid {
			value := lastError.String
			delivery.LastError = &value
		}
		if nextAttempt.Valid {
			value := nextAttempt.Time
			delivery.NextAttempt = &value
		}
		if deliveredAt.Valid {
			value := deliveredAt.Time
			delivery.DeliveredAt = &value
		}
		items = append(items, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := m.db.Pool.QueryRow(ctx, `SELECT count(*) FROM plugin_event_deliveries WHERE plugin_id = $1`, string(id)).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ReplayEvent re-delivers one recorded event from the core outbox. It is an
// explicit audited global-admin action; plugins still dedupe by event UUID.
func (m *Manager) ReplayEvent(ctx context.Context, principal authorization.Principal, id ID, eventID uuid.UUID) error {
	if err := m.requireManage(ctx, principal); err != nil {
		return err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return err
	}
	runtime := m.runtimeFor(id)
	if runtime == nil {
		return ErrBackendUnavailable
	}
	var event events.Event
	var boardID, moduleID, subjectID pgtype.UUID
	err := m.db.Pool.QueryRow(ctx, `
		SELECT id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload
		FROM events_outbox
		WHERE id = $1
	`, eventID).Scan(&event.ID, &event.Type, &event.OccurredAt, &boardID, &moduleID, &event.SubjectType, &subjectID, &event.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("plugins: load event for replay: %w", err)
	}
	event.BoardID = uuidFromPG(boardID)
	event.ModuleID = uuidFromPG(moduleID)
	event.SubjectID = uuidFromPG(subjectID)
	if err := m.eventRegistry.ValidateEvent(event); err != nil {
		return fmt.Errorf("%w: stored event is invalid", ErrValidation)
	}
	if !manifestSubscribes(runtime.installed.Manifest, event.Type) {
		return fmt.Errorf("%w: plugin does not subscribe to %s", ErrValidation, event.Type)
	}
	if _, err := m.db.Pool.Exec(ctx, `
		UPDATE plugin_event_deliveries
		SET status = 'pending', attempts = 0, last_error = NULL, next_attempt_at = NULL, delivered_at = NULL
		WHERE plugin_id = $1 AND event_id = $2
	`, string(id), eventID); err != nil {
		return fmt.Errorf("plugins: reset delivery for replay: %w", err)
	}
	delivered, _, err := m.deliverEvent(ctx, runtime, event)
	if err != nil {
		return err
	}
	if !delivered {
		return ErrBackendUnavailable
	}
	if err := writeAudit(ctx, m.db.Pool, principal, auditActionEventReplay, string(id), map[string]any{
		"eventId": eventID.String(),
	}); err != nil {
		return err
	}
	return nil
}

func manifestSubscribes(manifest Manifest, eventType string) bool {
	for _, declared := range manifest.Events {
		if declared == eventType {
			return true
		}
	}
	return false
}

func clampPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// RetryDueDeliveries is an optional background retry pass used by boot code. It
// re-reads the outbox payload and delivers due failed rows. It never resets a
// delivered/dead row.
func (m *Manager) RetryDueDeliveries(ctx context.Context) {
	rows, err := m.db.Pool.Query(ctx, `
		SELECT plugin_id, event_id
		FROM plugin_event_deliveries
		WHERE status = 'failed' AND (next_attempt_at IS NULL OR next_attempt_at <= clock_timestamp())
		ORDER BY next_attempt_at NULLS FIRST
		LIMIT 25
	`)
	if err != nil {
		return
	}
	type due struct {
		id      ID
		eventID uuid.UUID
	}
	items := make([]due, 0)
	for rows.Next() {
		var pluginID string
		var eventID uuid.UUID
		if err := rows.Scan(&pluginID, &eventID); err == nil {
			items = append(items, due{id: ID(pluginID), eventID: eventID})
		}
	}
	rows.Close()
	sort.Slice(items, func(i, j int) bool { return items[i].eventID.String() < items[j].eventID.String() })
	for _, item := range items {
		runtime := m.runtimeFor(item.id)
		if runtime == nil {
			continue
		}
		var event events.Event
		var boardID, moduleID, subjectID pgtype.UUID
		err := m.db.Pool.QueryRow(ctx, `
			SELECT id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload
			FROM events_outbox WHERE id = $1
		`, item.eventID).Scan(&event.ID, &event.Type, &event.OccurredAt, &boardID, &moduleID, &event.SubjectType, &subjectID, &event.Payload)
		if err != nil {
			continue
		}
		event.BoardID = uuidFromPG(boardID)
		event.ModuleID = uuidFromPG(moduleID)
		event.SubjectID = uuidFromPG(subjectID)
		if !manifestSubscribes(runtime.installed.Manifest, event.Type) {
			continue
		}
		_, _, _ = m.deliverEvent(ctx, runtime, event)
	}
}
