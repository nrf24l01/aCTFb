package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// TestSubject is the NATS request subject used by the backend to ask the
// trusted worker for a real provider connection test. The payload contains only
// the integration id; credentials never travel over NATS.
const TestSubject = "actfb.integrations.test.request"

// Requester is the narrow NATS request contract.
type Requester interface {
	Request(ctx context.Context, subject string, data []byte) ([]byte, error)
}

// NATSTester delegates connection tests to the trusted worker.
type NATSTester struct {
	client Requester
}

// NewNATSTester constructs the worker-backed tester.
func NewNATSTester(client Requester) *NATSTester {
	return &NATSTester{client: client}
}

var _ ConnectionTester = (*NATSTester)(nil)

type testRequest struct {
	IntegrationID uuid.UUID `json:"integrationId"`
}

// Test sends the id-only request and decodes a bounded worker reply.
func (t *NATSTester) Test(ctx context.Context, integrationID uuid.UUID) (TestResult, error) {
	if t == nil || t.client == nil {
		return TestResult{}, errors.New("integrations: NATS transport is not configured")
	}
	payload, err := json.Marshal(testRequest{IntegrationID: integrationID})
	if err != nil {
		return TestResult{}, err
	}
	reply, err := t.client.Request(ctx, TestSubject, payload)
	if err != nil {
		return TestResult{}, fmt.Errorf("integrations: worker request failed: %w", err)
	}
	if len(reply) == 0 || len(reply) > 8<<10 {
		return TestResult{}, errors.New("integrations: invalid worker reply")
	}
	var result TestResult
	if err := json.Unmarshal(reply, &result); err != nil {
		return TestResult{}, errors.New("integrations: invalid worker reply")
	}
	if len(result.Message) > 1024 {
		result.Message = result.Message[:1024]
	}
	return result, nil
}
