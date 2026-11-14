// Package events provides the transactional outbox, event registry, and
// JetStream publisher used by core workers and the plugin proxy.
package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// StreamName is the shared JetStream stream for core/plugin events.
	StreamName = "ACTFB_EVENTS"
	// SubjectPrefix is prepended to every event type.
	SubjectPrefix = "actfb.events."

	EventSubmissionJudged = "submission.judged"

	EventInstanceCreated   = "instance.created"
	EventInstanceStarted   = "instance.started"
	EventInstanceStopped   = "instance.stopped"
	EventInstanceDestroyed = "instance.destroyed"
	EventInstanceExpired   = "instance.expired"

	SubjectTypeUser = "USER"
	SubjectTypeTeam = "TEAM"
)

// Event is the core outbox/event envelope. Payload must never contain flags,
// answers, hashes, submitted text, passwords/tokens, or file bytes.
type Event struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"`
	OccurredAt  time.Time       `json:"occurredAt"`
	BoardID     *uuid.UUID      `json:"boardId,omitempty"`
	ModuleID    *uuid.UUID      `json:"moduleId,omitempty"`
	SubjectType string          `json:"subjectType,omitempty"`
	SubjectID   *uuid.UUID      `json:"subjectId,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// SubmissionJudgedPayload is the frozen safe payload for submission.judged.
type SubmissionJudgedPayload struct {
	SubmissionUUID uuid.UUID `json:"submissionUuid"`
	BoardUUID      uuid.UUID `json:"boardUuid"`
	ModuleUUID     uuid.UUID `json:"moduleUuid"`
	TaskUUID       uuid.UUID `json:"taskUuid"`
	SubjectType    string    `json:"subjectType"`
	SubjectUUID    uuid.UUID `json:"subjectUuid"`
	Status         string    `json:"status"`
	ScoreAwarded   *int      `json:"scoreAwarded"`
	OccurredAt     time.Time `json:"occurredAt"`
}

// Tx is the subset of pgx.Tx required to enqueue inside a caller transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Outbox persists events in the caller's transaction.
type Outbox interface {
	Enqueue(ctx context.Context, tx Tx, event Event) error
}

// Publisher delivers an event. Delivery is at-least-once.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// SubjectFor returns the NATS subject for an event type.
func SubjectFor(eventType string) string {
	return SubjectPrefix + eventType
}
