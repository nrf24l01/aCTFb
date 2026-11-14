package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ Tx = (pgx.Tx)(nil)

type recordingTx struct {
	sql   string
	args  []any
	calls int
	err   error
}

func (t *recordingTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.calls++
	t.sql = sql
	t.args = args
	return pgconn.NewCommandTag("INSERT 0 1"), t.err
}

func TestPostgresOutboxEnqueueShapeAndIdentity(t *testing.T) {
	outbox := NewOutbox(nil)
	tx := &recordingTx{}
	payload := validJudgedPayload()
	boardID := payload.BoardUUID
	moduleID := payload.ModuleUUID
	subjectID := payload.SubjectUUID
	event := Event{
		ID:          uuid.New(),
		Type:        EventSubmissionJudged,
		OccurredAt:  payload.OccurredAt,
		BoardID:     &boardID,
		ModuleID:    &moduleID,
		SubjectType: payload.SubjectType,
		SubjectID:   &subjectID,
		Payload:     judgedPayloadJSON(t, payload),
	}

	if err := outbox.Enqueue(context.Background(), tx, event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if tx.calls != 1 {
		t.Fatalf("Exec calls = %d, want 1", tx.calls)
	}
	for _, column := range []string{
		"id", "type", "occurred_at", "board_id", "module_id",
		"subject_type", "subject_id", "payload",
	} {
		if !strings.Contains(tx.sql, column) {
			t.Fatalf("insert SQL does not contain column %q: %s", column, tx.sql)
		}
	}
	if !strings.Contains(tx.sql, "ON CONFLICT (id) DO NOTHING") {
		t.Fatalf("insert SQL is not idempotent by id: %s", tx.sql)
	}
	if len(tx.args) != 8 {
		t.Fatalf("args = %d, want 8", len(tx.args))
	}
	if got := tx.args[0].(uuid.UUID); got != event.ID {
		t.Fatalf("event id arg = %s, want %s", got, event.ID)
	}
	if got := tx.args[1].(string); got != EventSubmissionJudged {
		t.Fatalf("event type arg = %q, want %q", got, EventSubmissionJudged)
	}
	storedPayload, ok := tx.args[7].(string)
	if !ok {
		t.Fatalf("payload arg type = %T, want string", tx.args[7])
	}
	for _, forbidden := range []string{"flag", "hash", "text", "bytes"} {
		if strings.Contains(strings.ToLower(storedPayload), forbidden) {
			t.Fatalf("stored payload contains forbidden %q: %s", forbidden, storedPayload)
		}
	}
}

func TestPostgresOutboxRequiresCallerTransaction(t *testing.T) {
	outbox := NewOutbox(nil)
	event := Event{ID: uuid.New(), Type: EventInstanceCreated}
	if err := outbox.Enqueue(context.Background(), nil, event); err == nil {
		t.Fatal("Enqueue(nil tx) error = nil, want caller-transaction error")
	}
}

func TestPostgresOutboxRejectsUnknownTypesAndSecrets(t *testing.T) {
	outbox := NewOutbox(nil)
	tx := &recordingTx{}

	unknown := Event{
		ID:         uuid.New(),
		Type:       "instance.unknown",
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"instanceUuid":"x"}`),
	}
	if err := outbox.Enqueue(context.Background(), tx, unknown); !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("unknown type error = %v, want ErrUnknownEventType", err)
	}

	secret := Event{
		ID:         uuid.New(),
		Type:       EventInstanceCreated,
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"instanceUuid":"x","password":"secret"}`),
	}
	if err := outbox.Enqueue(context.Background(), tx, secret); !errors.Is(err, ErrForbiddenPayloadKey) {
		t.Fatalf("secret payload error = %v, want ErrForbiddenPayloadKey", err)
	}
	if tx.calls != 0 {
		t.Fatalf("invalid events executed %d SQL calls, want 0", tx.calls)
	}
}

func TestPostgresOutboxDefaultsTimeAndPayload(t *testing.T) {
	outbox := NewOutbox(nil)
	tx := &recordingTx{}
	event := Event{
		ID:   uuid.New(),
		Type: EventInstanceCreated,
	}

	if err := outbox.Enqueue(context.Background(), tx, event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if occurredAt, ok := tx.args[2].(time.Time); !ok || occurredAt.IsZero() {
		t.Fatalf("occurred_at arg = %#v, want non-zero time", tx.args[2])
	}
	if payload, ok := tx.args[7].(string); !ok || payload != "{}" {
		t.Fatalf("payload arg = %#v, want {}", tx.args[7])
	}
}
