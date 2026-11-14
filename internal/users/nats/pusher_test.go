package nats

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"google.golang.org/protobuf/proto"
)

type fakeJetStream struct {
	subject string
	data    []byte
	opts    int
}

func (f *fakeJetStream) PublishJetStream(
	_ context.Context,
	subject string,
	data []byte,
	opts ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	f.subject = subject
	f.data = append([]byte(nil), data...)
	f.opts = len(opts)
	return &jetstream.PubAck{}, nil
}

func TestEmailTaskOneofRoundTrip(t *testing.T) {
	verification := buildEmailVerificationTask("a@example.test", "alice", "code", uuid.New())
	data, err := proto.Marshal(verification)
	if err != nil {
		t.Fatalf("marshal verification: %v", err)
	}
	var decodedVerification emailv1.EmailTask
	if err := proto.Unmarshal(data, &decodedVerification); err != nil {
		t.Fatalf("unmarshal verification: %v", err)
	}
	if decodedVerification.GetVerification() == nil || decodedVerification.GetPasswordReset() != nil {
		t.Fatalf("verification oneof round-trip mismatch: %+v", decodedVerification.Payload)
	}

	reset := buildPasswordResetTask("a@example.test", "alice", "reset", uuid.New())
	data, err = proto.Marshal(reset)
	if err != nil {
		t.Fatalf("marshal reset: %v", err)
	}
	var decodedReset emailv1.EmailTask
	if err := proto.Unmarshal(data, &decodedReset); err != nil {
		t.Fatalf("unmarshal reset: %v", err)
	}
	if decodedReset.GetPasswordReset() == nil || decodedReset.GetVerification() != nil {
		t.Fatalf("password reset oneof round-trip mismatch: %+v", decodedReset.Payload)
	}
}

func TestPublishPasswordResetTaskUsesGoogleProtobuf(t *testing.T) {
	fake := &fakeJetStream{}
	publisher := &EmailTaskPublisher{nats: fake}
	taskID := uuid.New()
	token := "random-opaque-password-reset-token"

	if err := publisher.PublishPasswordResetTask(
		context.Background(),
		"user@example.test",
		"alice",
		token,
		taskID,
	); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if fake.subject != emailTasksSubject {
		t.Fatalf("subject = %q", fake.subject)
	}
	if fake.opts != 2 {
		t.Fatalf("publish opts = %d, want 2", fake.opts)
	}

	var task emailv1.EmailTask
	if err := proto.Unmarshal(fake.data, &task); err != nil {
		t.Fatalf("google protobuf unmarshal: %v", err)
	}
	if task.GetId() != taskID.String() {
		t.Fatalf("task id = %q", task.GetId())
	}
	if task.GetTo() != "user@example.test" {
		t.Fatalf("to = %q", task.GetTo())
	}
	if task.GetVerification() != nil {
		t.Fatal("password reset task unexpectedly contains verification payload")
	}
	reset := task.GetPasswordReset()
	if reset == nil {
		t.Fatal("password_reset oneof is missing")
	}
	if reset.Username != "alice" || reset.Token != token {
		t.Fatalf("password reset = %+v", reset)
	}
}
