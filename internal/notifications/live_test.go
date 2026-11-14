package notifications

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nrf24l01/actfb/internal/config"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	subscriber "github.com/nrf24l01/actfb/internal/notifications/nats"
	"github.com/nrf24l01/actfb/internal/platform/email"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"google.golang.org/protobuf/proto"
)

type mailpitAddress struct {
	Address string `json:"Address"`
}

type mailpitMessageSummary struct {
	ID      string           `json:"ID"`
	To      []mailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
}

type mailpitMessageList struct {
	Messages []mailpitMessageSummary `json:"messages"`
}

type mailpitMessageDetail struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
	Text    string `json:"Text"`
	HTML    string `json:"HTML"`
}

// TestLiveEmailDelivery is opt-in because it needs the dev NATS and Mailpit
// services. Run it inside the compose notification container after stopping
// the long-running worker, with ACTFB_TEST_EMAIL_LIVE=1.
func TestLiveEmailDelivery(t *testing.T) {
	if os.Getenv("ACTFB_TEST_EMAIL_LIVE") != "1" {
		t.Skip("set ACTFB_TEST_EMAIL_LIVE=1 and provide dev NATS/Mailpit to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := natsClient.Close(closeCtx); err != nil {
			t.Errorf("close NATS: %v", err)
		}
	}()

	emailClient, err := email.NewEmailClient(ctx, &cfg.Email)
	if err != nil {
		t.Fatalf("create email client: %v", err)
	}
	defer func() {
		if err := emailClient.Close(); err != nil {
			t.Errorf("close email client: %v", err)
		}
	}()

	service := NewNotificationService(
		emailClient,
		subscriber.NewEmailTaskSubscriber(natsClient),
		NewFileTemplateRenderer(cfg.Email.TemplatesDir),
	)
	if err := service.Start(ctx, testLogger()); err != nil {
		t.Fatalf("start notification service: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown notification service: %v", err)
		}
	}()

	baseURL := os.Getenv("ACTFB_TEST_MAILPIT_URL")
	if baseURL == "" {
		baseURL = "http://mailpit:8025"
	}

	username := "live-learner"
	cases := []struct {
		name    string
		token   string
		payload func(to string) *emailv1.EmailTask
	}{
		{
			name:  "verification",
			token: "654321",
			payload: func(to string) *emailv1.EmailTask {
				return &emailv1.EmailTask{
					To: to,
					Payload: &emailv1.EmailTask_Verification{
						Verification: &emailv1.VerificationEmail{Username: username, Code: "654321"},
					},
				}
			},
		},
		{
			name:  "password_reset",
			token: opaqueToken(t),
			payload: func(to string) *emailv1.EmailTask {
				return &emailv1.EmailTask{
					To: to,
					Payload: &emailv1.EmailTask_PasswordReset{
						PasswordReset: &emailv1.PasswordResetEmail{Username: username, Token: "placeholder"},
					},
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			to := fmt.Sprintf("notify-live-%s-%s@example.test", testCase.name, uuid.NewString())
			taskID := uuid.NewString()
			task := testCase.payload(to)
			task.Id = taskID
			if reset := task.GetPasswordReset(); reset != nil {
				reset.Token = testCase.token
			}

			payload, err := proto.Marshal(task)
			if err != nil {
				t.Fatalf("marshal task: %v", err)
			}
			if _, err := natsClient.PublishJetStream(
				ctx,
				"email.tasks",
				payload,
				jetstream.WithMsgID(taskID),
				jetstream.WithExpectStream("EMAIL_TASKS"),
			); err != nil {
				t.Fatalf("publish email task: %v", err)
			}

			messageID := waitForMessage(t, ctx, baseURL, to)
			detail := fetchMessage(t, ctx, baseURL, messageID)

			if !strings.Contains(detail.Subject, username) {
				t.Errorf("subject = %q, want it to contain username %q", detail.Subject, username)
			}
			if !strings.Contains(detail.Text, testCase.token) {
				t.Errorf("text body does not contain token for %s: %q", testCase.name, detail.Text)
			}
			if !strings.Contains(detail.HTML, testCase.token) {
				t.Errorf("HTML body does not contain token for %s: %q", testCase.name, detail.HTML)
			}
		})
	}
}

func opaqueToken(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func waitForMessage(t *testing.T, ctx context.Context, baseURL, recipient string) string {
	t.Helper()

	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for email to %s: %v", recipient, ctx.Err())
		case <-ticker.C:
			response, err := client.Get(baseURL + "/api/v1/messages")
			if err != nil {
				t.Logf("query Mailpit: %v", err)
				continue
			}

			var list mailpitMessageList
			decodeErr := json.NewDecoder(response.Body).Decode(&list)
			response.Body.Close()
			if decodeErr != nil {
				t.Logf("decode Mailpit response: %v", decodeErr)
				continue
			}

			for _, message := range list.Messages {
				for _, address := range message.To {
					if strings.EqualFold(address.Address, recipient) {
						return message.ID
					}
				}
			}
		}
	}
}

func fetchMessage(t *testing.T, ctx context.Context, baseURL, messageID string) mailpitMessageDetail {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/message/"+messageID, nil)
	if err != nil {
		t.Fatalf("create Mailpit message request: %v", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("fetch Mailpit message: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetch Mailpit message status = %d", response.StatusCode)
	}

	var detail mailpitMessageDetail
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatalf("decode Mailpit message: %v", err)
	}

	return detail
}
