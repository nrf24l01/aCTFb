package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/files"
)

type fakeMessage struct {
	data   []byte
	acked  bool
	nacked bool
	delay  time.Duration
	termed bool
	ackErr error
}

func (m *fakeMessage) Ack() error                         { m.acked = true; return m.ackErr }
func (m *fakeMessage) NakWithDelay(d time.Duration) error { m.nacked = true; m.delay = d; return nil }
func (m *fakeMessage) Term() error                        { m.termed = true; return nil }
func (m *fakeMessage) Data() []byte                       { return m.data }

type fakeScanner struct {
	err  error
	file uuid.UUID
}

func (s *fakeScanner) Scan(_ context.Context, fileID uuid.UUID) error {
	s.file = fileID
	return s.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestProcessMessageAckNakTerm(t *testing.T) {
	fileID := uuid.New()
	payload, _ := json.Marshal(scanRequest{FileUUID: fileID})
	logger := testLogger()

	t.Run("malformed message is terminated", func(t *testing.T) {
		msg := &fakeMessage{data: []byte("{")}
		processMessage(context.Background(), msg, &fakeScanner{}, logger)
		if !msg.termed || msg.acked || msg.nacked {
			t.Fatalf("unexpected ack state: %#v", msg)
		}
	})

	t.Run("successful scan is acked", func(t *testing.T) {
		msg := &fakeMessage{data: payload}
		scanner := &fakeScanner{}
		processMessage(context.Background(), msg, scanner, logger)
		if !msg.acked || msg.nacked || msg.termed || scanner.file != fileID {
			t.Fatalf("unexpected success state: %#v scanner=%#v", msg, scanner)
		}
	})

	t.Run("scanner unavailable is retried without fake-clean", func(t *testing.T) {
		msg := &fakeMessage{data: payload}
		processMessage(context.Background(), msg, &fakeScanner{err: files.ErrScannerUnavailable}, logger)
		if !msg.nacked || msg.acked || msg.termed || msg.delay != nakDelayUnavailable {
			t.Fatalf("unexpected unavailable state: %#v", msg)
		}
	})

	t.Run("transient error is retried", func(t *testing.T) {
		msg := &fakeMessage{data: payload}
		processMessage(context.Background(), msg, &fakeScanner{err: errors.New("db down")}, logger)
		if !msg.nacked || msg.acked || msg.termed || msg.delay != nakDelayTransient {
			t.Fatalf("unexpected transient state: %#v", msg)
		}
	})
}
