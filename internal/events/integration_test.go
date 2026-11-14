package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/config"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

type failingPublisher struct {
	err error
}

func (p failingPublisher) Publish(context.Context, Event) error {
	return p.err
}

func eventTestDB(t *testing.T) *postgres.DB {
	t.Helper()

	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL event tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	schema := "actfb_events_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(schema) > 63 {
		t.Fatalf("unsafe schema name %q", schema)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
		_ = admin.Close()
	})

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.UpToContext(ctx, sqlDB, migrationsDir, 8); err != nil {
		t.Fatalf("apply migrations through 008: %v", err)
	}

	return &postgres.DB{Pool: pool, SQL: sqlDB}
}

func eventTestNATS(t *testing.T) *platformnats.Client {
	t.Helper()

	host := os.Getenv("ACTFB_TEST_NATS_HOST")
	portRaw := os.Getenv("ACTFB_TEST_NATS_PORT")
	if host == "" || portRaw == "" {
		t.Skip("set ACTFB_TEST_NATS_HOST/ACTFB_TEST_NATS_PORT to run real NATS event tests")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("ACTFB_TEST_NATS_PORT: %v", err)
	}
	if _, _, err := net.SplitHostPort(net.JoinHostPort(host, portRaw)); err != nil {
		t.Fatalf("invalid NATS address: %v", err)
	}

	client, err := platformnats.New(config.NatsConfig{Host: host, Port: port})
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Close(closeCtx)
	})
	return client
}

func testJudgedEvent(t *testing.T) Event {
	t.Helper()

	payload := validJudgedPayload()
	boardID := payload.BoardUUID
	moduleID := payload.ModuleUUID
	subjectID := payload.SubjectUUID

	return Event{
		ID:          uuid.New(),
		Type:        EventSubmissionJudged,
		OccurredAt:  payload.OccurredAt,
		BoardID:     &boardID,
		ModuleID:    &moduleID,
		SubjectType: payload.SubjectType,
		SubjectID:   &subjectID,
		Payload:     judgedPayloadJSON(t, payload),
	}
}

func TestOutboxRollbackIdentityAndDispatch(t *testing.T) {
	db := eventTestDB(t)
	natsClient := eventTestNATS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := EnsureStream(ctx, natsClient); err != nil {
		t.Fatalf("EnsureStream() error = %v", err)
	}
	outbox := NewOutbox(db)
	event := testJudgedEvent(t)

	t.Run("rollback leaves no row", func(t *testing.T) {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := outbox.Enqueue(ctx, tx, event); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback: %v", err)
		}

		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE id = $1`, event.ID).Scan(&count); err != nil {
			t.Fatalf("count row: %v", err)
		}
		if count != 0 {
			t.Fatalf("rolled-back outbox rows = %d, want 0", count)
		}
	})

	t.Run("duplicate event identity inserts once", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			if err := outbox.Enqueue(ctx, tx, event); err != nil {
				t.Fatalf("Enqueue() error = %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}
		}

		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE id = $1`, event.ID).Scan(&count); err != nil {
			t.Fatalf("count row: %v", err)
		}
		if count != 1 {
			t.Fatalf("duplicate event rows = %d, want 1", count)
		}
	})

	t.Run("dispatcher retries and publishes", func(t *testing.T) {
		consumer, err := natsClient.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
			Durable:       "events_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			FilterSubject: SubjectFor(EventSubmissionJudged),
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       5 * time.Second,
			DeliverPolicy: jetstream.DeliverNewPolicy,
		})
		if err != nil {
			t.Fatalf("create consumer: %v", err)
		}

		retryErr := errors.New("nats temporarily unavailable")
		dispatcher := NewDispatcher(db, failingPublisher{err: retryErr})
		published, err := dispatcher.RunOnce(ctx)
		if err == nil || published != 0 {
			t.Fatalf("first RunOnce = (%d, %v), want failure", published, err)
		}
		var attempts int
		var publishedAt sql.NullTime
		var lastError sql.NullString
		if err := db.Pool.QueryRow(ctx, `
			SELECT attempts, published_at, last_error FROM events_outbox WHERE id = $1
		`, event.ID).Scan(&attempts, &publishedAt, &lastError); err != nil {
			t.Fatalf("read failed row: %v", err)
		}
		if attempts != 1 || publishedAt.Valid || !lastError.Valid {
			t.Fatalf("failed row attempts=%d published=%v last_error=%q", attempts, publishedAt.Valid, lastError.String)
		}

		publisher := NewNATSPublisher(natsClient)
		dispatcher = NewDispatcher(db, publisher)
		published, err = dispatcher.RunOnce(ctx)
		if err != nil || published != 1 {
			t.Fatalf("second RunOnce = (%d, %v), want one published", published, err)
		}

		var payloadText string
		if err := db.Pool.QueryRow(ctx, `
			SELECT attempts, published_at, last_error, payload::text
			FROM events_outbox WHERE id = $1
		`, event.ID).Scan(&attempts, &publishedAt, &lastError, &payloadText); err != nil {
			t.Fatalf("read published row: %v", err)
		}
		if attempts != 2 || !publishedAt.Valid || lastError.Valid {
			t.Fatalf("published row attempts=%d published=%v last_error=%q", attempts, publishedAt.Valid, lastError.String)
		}
		for _, forbidden := range []string{"flag", "hash", "text", "bytes"} {
			if strings.Contains(strings.ToLower(payloadText), forbidden) {
				t.Fatalf("published payload contains forbidden %q: %s", forbidden, payloadText)
			}
		}

		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("fetch message: %v", err)
		}
		received := 0
		for msg := range batch.Messages() {
			received++
			if subject := msg.Subject(); subject != SubjectFor(EventSubmissionJudged) {
				t.Fatalf("subject = %q", subject)
			}
			if msgID := msg.Headers().Get(jetstream.MsgIDHeader); msgID != event.ID.String() {
				t.Fatalf("Nats-Msg-Id = %q, want %q", msgID, event.ID.String())
			}
			var decoded Event
			if err := json.Unmarshal(msg.Data(), &decoded); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if decoded.ID != event.ID || decoded.Type != event.Type {
				t.Fatalf("decoded event = %+v", decoded)
			}
			if err := msg.Ack(); err != nil {
				t.Fatalf("ack: %v", err)
			}
		}
		if err := batch.Error(); err != nil {
			t.Fatalf("message batch error: %v", err)
		}
		if received != 1 {
			t.Fatalf("received = %d, want 1", received)
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		ack, err := natsClient.PublishJetStream(
			ctx,
			SubjectFor(event.Type),
			data,
			jetstream.WithMsgID(event.ID.String()),
			jetstream.WithExpectStream(StreamName),
		)
		if err != nil {
			t.Fatalf("re-publish duplicate: %v", err)
		}
		if !ack.Duplicate {
			t.Fatal("JetStream did not deduplicate the re-published event ID")
		}
	})
}
