package attackdefense

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// WebhookRequest is the raw provider delivery. The HTTP adapter must read the
// bounded raw bytes before any parsing; service verifies the signature first.
type WebhookRequest struct {
	BindingID   uuid.UUID
	Body        []byte
	Headers     http.Header
	RemoteAddr  string
	TLS         bool
	ContentType string
}

// WebhookResult is the accepted delivery outcome.
type WebhookResult struct {
	Runs []uuid.UUID
}

// HandleWebhook authenticates, deduplicates and enqueues provider pushes. All
// repository/branch/subject/user values come from the stored binding and
// assignment rows, never from the payload.
func (s *Service) HandleWebhook(ctx context.Context, req WebhookRequest) (*WebhookResult, error) {
	if len(req.Body) == 0 || len(req.Body) > MaxWebhookBodyBytes {
		return nil, ErrPayloadTooLarge
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(req.ContentType, ";")[0]))
	if contentType != "application/json" {
		return nil, ErrUnsupportedMediaType
	}
	stored, err := s.store.WebhookBinding(ctx, req.BindingID)
	if err != nil {
		return nil, err
	}
	secret, err := s.openWebhookSecret(stored)
	if err != nil {
		return nil, ErrInvalidWebhookSignature
	}
	provider := strings.ToUpper(strings.TrimSpace(stored.Provider))
	if provider == "GITLAB" && !req.TLS && !s.loopbackWebhook(req.RemoteAddr) && !s.allowInsecureWebhook {
		return nil, ErrInvalidWebhookSignature
	}
	if err := verifyWebhookSignature(provider, secret, req.Headers, req.Body); err != nil {
		return nil, err
	}
	push, err := parsePushEvent(provider, req.Headers, req.Body)
	if err != nil {
		return nil, err
	}
	if push.DeliveryID == "" || len(push.DeliveryID) > 255 {
		return nil, ErrInvalidWebhook
	}
	if push.Repository != stored.Repository && !(provider == "GITHUB" && strings.EqualFold(push.Repository, stored.Repository)) {
		return nil, ErrInvalidWebhook
	}
	if push.Branch != stored.Branch {
		return nil, ErrInvalidWebhook
	}
	sha, err := NormalizeCommitSHA(push.CommitSHA)
	if err != nil {
		return nil, ErrInvalidCommit
	}
	if zeroSHA(sha) {
		return nil, ErrInvalidCommit
	}

	tx, err := s.store.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	delivery, inserted, err := s.store.BeginDelivery(ctx, tx, req.BindingID, push.DeliveryID, sha)
	if err != nil {
		return nil, err
	}
	if !inserted {
		if !strings.EqualFold(delivery.CommitSHA, sha) {
			if err := s.store.MarkDelivery(ctx, tx, delivery.ID, DeliveryRejected, "WEBHOOK_REPLAYED"); err != nil {
				return nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("attackdefense: commit replayed delivery: %w", err)
			}
			return nil, ErrWebhookReplayed
		}
		if err := s.store.MarkDelivery(ctx, tx, delivery.ID, DeliveryProcessed, "WEBHOOK_ALREADY_PROCESSED"); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("attackdefense: commit duplicate delivery: %w", err)
		}
		return nil, ErrWebhookAlreadyProcessed
	}

	targets, err := s.store.WebhookTargets(ctx, tx, req.BindingID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	result := &WebhookResult{Runs: make([]uuid.UUID, 0)}
	for _, target := range targets {
		if target.Module.ModuleType != ModuleTypeAttackDefense || !target.Module.Active(now) || !target.Module.AllowsKind(target.Kind) {
			continue
		}
		trusted, err := s.store.TargetActor(ctx, tx, target)
		if err != nil {
			if errors.Is(err, ErrBoardAccessDenied) || errors.Is(err, ErrBoardNotFound) {
				continue
			}
			return nil, err
		}
		deliveryID := push.DeliveryID
		run, created, err := s.store.InsertRun(ctx, tx, InsertRunParams{
			ModuleID: target.Module.ModuleID, RepositoryAssignmentID: target.AssignmentID,
			SubjectType: trusted.Subject.Type, SubjectID: trusted.Subject.ID,
			Kind: target.Kind, CommitSHA: sha, ProviderDeliveryID: &deliveryID,
			RequestedBy: trusted.RequestedBy,
		})
		if err != nil {
			return nil, err
		}
		if !created {
			continue
		}
		boardID := target.Module.BoardID
		if err := writeAudit(ctx, tx, AuditEvent{
			ActorUserID: trusted.RequestedBy, BoardID: &boardID, ModuleID: &target.Module.ModuleID,
			Action: "ad.run.webhook", ResourceType: "ad_run", ResourceID: &run.ID,
			Details: map[string]any{
				"deliveryId": push.DeliveryID, "kind": target.Kind, "commitSha": sha,
				"subjectType": trusted.Subject.Type, "subjectId": trusted.Subject.ID.String(),
			},
		}); err != nil {
			return nil, err
		}
		result.Runs = append(result.Runs, run.ID)
	}
	if err := s.store.MarkDelivery(ctx, tx, delivery.ID, DeliveryProcessed, ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit webhook: %w", err)
	}
	for _, runID := range result.Runs {
		s.publishRun(ctx, runID)
	}
	return result, nil
}

func (s *Service) loopbackWebhook(remoteAddr string) bool {
	host := remoteAddr
	if parsed, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PushEvent is the provider-normalized push. Only these fields are trusted,
// and repository/branch are checked against the stored binding.
type PushEvent struct {
	Provider   string
	DeliveryID string
	Repository string
	Branch     string
	CommitSHA  string
}

func verifyWebhookSignature(provider, secret string, headers http.Header, body []byte) error {
	if secret == "" {
		return ErrInvalidWebhookSignature
	}
	switch provider {
	case "GITHUB":
		return verifyHMACSHA256Hex(stripSHA256Prefix(headers.Get("X-Hub-Signature-256")), secret, body)
	case "GITEA":
		signature := strings.TrimSpace(headers.Get("X-Gitea-Signature"))
		if signature != "" {
			return verifyHMACSHA256Hex(signature, secret, body)
		}
		return verifyHMACSHA256Hex(stripSHA256Prefix(headers.Get("X-Hub-Signature-256")), secret, body)
	case "GITLAB":
		// GitLab's documented secret-token authentication sends the shared
		// token in X-Gitlab-Token. It is not an HMAC; do not pretend it is.
		received := headers.Get("X-Gitlab-Token")
		if received == "" {
			return ErrInvalidWebhookSignature
		}
		if subtle.ConstantTimeCompare([]byte(received), []byte(secret)) != 1 {
			return ErrInvalidWebhookSignature
		}
		return nil
	default:
		return ErrInvalidWebhookSignature
	}
}

func stripSHA256Prefix(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "sha256=") {
		return value[len("sha256="):]
	}
	return ""
}

func verifyHMACSHA256Hex(signature, secret string, body []byte) error {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return ErrInvalidWebhookSignature
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return ErrInvalidWebhookSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return ErrInvalidWebhookSignature
	}
	return nil
}

func parsePushEvent(provider string, headers http.Header, body []byte) (*PushEvent, error) {
	switch provider {
	case "GITHUB", "GITEA":
		event := headers.Get("X-GitHub-Event")
		if provider == "GITEA" && event == "" {
			event = headers.Get("X-Gitea-Event")
		}
		if event == "" || !strings.EqualFold(event, "push") {
			return nil, ErrUnsupportedGitEvent
		}
		delivery := headers.Get("X-GitHub-Delivery")
		if provider == "GITEA" && delivery == "" {
			delivery = headers.Get("X-Gitea-Delivery")
		}
		var payload struct {
			Ref        string `json:"ref"`
			After      string `json:"after"`
			Deleted    bool   `json:"deleted"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, ErrInvalidWebhook
		}
		if payload.Deleted {
			return nil, ErrInvalidWebhook
		}
		branch, err := branchFromRef(payload.Ref)
		if err != nil {
			return nil, err
		}
		return &PushEvent{
			Provider: provider, DeliveryID: delivery, Repository: payload.Repository.FullName,
			Branch: branch, CommitSHA: payload.After,
		}, nil
	case "GITLAB":
		if !strings.EqualFold(headers.Get("X-Gitlab-Event"), "Push Hook") {
			return nil, ErrUnsupportedGitEvent
		}
		delivery := headers.Get("X-Gitlab-Event-UUID")
		if delivery == "" {
			delivery = headers.Get("X-Gitlab-Webhook-UUID")
		}
		var payload struct {
			ObjectKind string `json:"object_kind"`
			Ref        string `json:"ref"`
			After      string `json:"after"`
			Project    struct {
				PathWithNamespace string `json:"path_with_namespace"`
			} `json:"project"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, ErrInvalidWebhook
		}
		if payload.ObjectKind != "" && payload.ObjectKind != "push" {
			return nil, ErrUnsupportedGitEvent
		}
		branch, err := branchFromRef(payload.Ref)
		if err != nil {
			return nil, err
		}
		return &PushEvent{
			Provider: provider, DeliveryID: delivery, Repository: payload.Project.PathWithNamespace,
			Branch: branch, CommitSHA: payload.After,
		}, nil
	default:
		return nil, ErrInvalidWebhook
	}
}

func branchFromRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", ErrInvalidWebhook
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if err := ValidateBranchRef(branch); err != nil {
		return "", ErrInvalidWebhook
	}
	return branch, nil
}

func zeroSHA(sha string) bool {
	for _, char := range sha {
		if char != '0' {
			return false
		}
	}
	return true
}
