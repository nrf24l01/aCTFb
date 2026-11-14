package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"google.golang.org/protobuf/proto"
)

type fakeMessage struct {
	data []byte

	acked      bool
	nacked     bool
	nakDelay   time.Duration
	terminated bool
	termReason string
}

func (m *fakeMessage) Metadata() (*jetstream.MsgMetadata, error) { return nil, nil }
func (m *fakeMessage) Data() []byte                              { return m.data }
func (m *fakeMessage) Headers() nats.Header                      { return nil }
func (m *fakeMessage) Subject() string                           { return "email.tasks" }
func (m *fakeMessage) Reply() string                             { return "" }

func (m *fakeMessage) Ack() error {
	m.acked = true
	return nil
}

func (m *fakeMessage) DoubleAck(context.Context) error { return nil }
func (m *fakeMessage) Nak() error                      { m.nacked = true; return nil }

func (m *fakeMessage) NakWithDelay(delay time.Duration) error {
	m.nacked = true
	m.nakDelay = delay
	return nil
}

func (m *fakeMessage) InProgress() error { return nil }

func (m *fakeMessage) Term() error {
	m.terminated = true
	return nil
}

func (m *fakeMessage) TermWithReason(reason string) error {
	m.terminated = true
	m.termReason = reason
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func validTaskBytes(t *testing.T) []byte {
	t.Helper()

	data, err := proto.Marshal(&emailv1.EmailTask{
		Id: "task-1",
		To: "learner@example.test",
		Payload: &emailv1.EmailTask_Verification{
			Verification: &emailv1.VerificationEmail{
				Username: "learner",
				Code:     "123456",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal email task: %v", err)
	}

	return data
}

func newTestSubscriber() *EmailTaskSubscriber {
	return &EmailTaskSubscriber{
		processTimeout: time.Second,
		logger:         testLogger(),
	}
}

func TestProcessMessageCancellationDoesNotAckOrNak(t *testing.T) {
	subscriber := newTestSubscriber()
	msg := &fakeMessage{data: validTaskBytes(t)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handlerCalled := false
	subscriber.processMessage(ctx, msg, func(handlerCtx context.Context, _ *emailv1.EmailTask) error {
		handlerCalled = true
		if handlerCtx.Err() == nil {
			t.Error("handler context was not canceled")
		}
		return handlerCtx.Err()
	})

	if !handlerCalled {
		t.Fatal("handler was not called")
	}
	if msg.acked {
		t.Error("canceled task was ACKed")
	}
	if msg.nacked {
		t.Error("canceled task was NAKed")
	}
	if msg.terminated {
		t.Error("canceled task was terminated")
	}
}

func TestProcessMessageAcksSuccessfulTask(t *testing.T) {
	subscriber := newTestSubscriber()
	msg := &fakeMessage{data: validTaskBytes(t)}

	subscriber.processMessage(context.Background(), msg, func(ctx context.Context, task *emailv1.EmailTask) error {
		if task.Id != "task-1" {
			t.Errorf("task ID = %q, want task-1", task.Id)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("handler context has no deadline")
		} else if time.Until(deadline) > time.Second {
			t.Errorf("handler deadline is %v away, want <= 1s", time.Until(deadline))
		}
		return nil
	})

	if !msg.acked {
		t.Error("successful task was not ACKed")
	}
	if msg.nacked || msg.terminated {
		t.Error("successful task was NAKed or terminated")
	}
}

func TestProcessMessageNaksTransientError(t *testing.T) {
	subscriber := newTestSubscriber()
	msg := &fakeMessage{data: validTaskBytes(t)}

	subscriber.processMessage(context.Background(), msg, func(context.Context, *emailv1.EmailTask) error {
		return errors.New("smtp temporarily unavailable")
	})

	if msg.acked {
		t.Error("transient failure was ACKed")
	}
	if !msg.nacked {
		t.Error("transient failure was not NAKed")
	}
	if msg.nakDelay != nakRedeliveryDelay {
		t.Errorf("NAK delay = %v, want %v", msg.nakDelay, nakRedeliveryDelay)
	}
	if msg.terminated {
		t.Error("transient failure was terminated")
	}
}

func TestProcessMessageTerminatesPermanentError(t *testing.T) {
	subscriber := newTestSubscriber()
	msg := &fakeMessage{data: validTaskBytes(t)}

	subscriber.processMessage(context.Background(), msg, func(context.Context, *emailv1.EmailTask) error {
		return errors.Join(ErrPermanent, errors.New("unknown payload"))
	})

	if msg.acked || msg.nacked {
		t.Error("permanent failure was ACKed or NAKed")
	}
	if !msg.terminated {
		t.Error("permanent failure was not terminated")
	}
}

func TestProcessMessageTerminatesUndecodableTask(t *testing.T) {
	subscriber := newTestSubscriber()
	msg := &fakeMessage{data: []byte("not protobuf")}

	subscriber.processMessage(context.Background(), msg, func(context.Context, *emailv1.EmailTask) error {
		t.Error("handler must not be called for an undecodable task")
		return nil
	})

	if msg.acked || msg.nacked {
		t.Error("undecodable task was ACKed or NAKed")
	}
	if !msg.terminated {
		t.Error("undecodable task was not terminated")
	}
}
