package attackdefense

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WebhookTarget is one assignment affected by a binding push.
type WebhookTarget struct {
	AssignmentID uuid.UUID
	Module       ModuleContext
	Subject      Subject
	Kind         string
}

// WebhookTargets lists assignments that reference the binding. Source pushes
// map to PATCH; exploit pushes map to EXPLOIT. All values are read from the
// trusted binding, never from webhook JSON.
func (s *Store) WebhookTargets(ctx context.Context, q querier, bindingID uuid.UUID) ([]WebhookTarget, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.subject_type, a.subject_id,
		       (a.source_binding_id = $1) AS is_source,
		       b.id, m.id, b.participation_mode, b.status, b.starts_at, b.ends_at,
		       m.type, m.enabled, m.starts_at, m.ends_at, m.allowed_task_types
		FROM ad_repository_assignments a
		JOIN modules m ON m.id = a.module_id
		JOIN boards b ON b.id = m.board_id
		WHERE a.source_binding_id=$1 OR a.exploit_binding_id=$1
	`, bindingID)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: list webhook targets: %w", err)
	}
	defer rows.Close()
	targets := make([]WebhookTarget, 0)
	for rows.Next() {
		var target WebhookTarget
		var isSource bool
		if err := rows.Scan(&target.AssignmentID, &target.Subject.Type, &target.Subject.ID, &isSource,
			&target.Module.BoardID, &target.Module.ModuleID, &target.Module.ParticipationMode,
			&target.Module.BoardStatus, &target.Module.BoardStartsAt, &target.Module.BoardEndsAt,
			&target.Module.ModuleType, &target.Module.ModuleEnabled, &target.Module.ModuleStartsAt,
			&target.Module.ModuleEndsAt, &target.Module.AllowedTaskTypes); err != nil {
			return nil, fmt.Errorf("attackdefense: scan webhook target: %w", err)
		}
		if isSource {
			target.Kind = RunKindPatch
		} else {
			target.Kind = RunKindExploit
		}
		targets = append(targets, target)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("attackdefense: list webhook targets: %w", rows.Err())
	}
	return targets, nil
}

// Delivery is one provider delivery dedupe row.
type Delivery struct {
	ID               uuid.UUID
	BindingID        uuid.UUID
	ProviderDelivery string
	CommitSHA        string
	Status           string
}

// BeginDelivery inserts the provider delivery id or returns the existing row.
// `inserted` is false for a duplicate delivery id.
func (s *Store) BeginDelivery(ctx context.Context, tx pgx.Tx, bindingID uuid.UUID, deliveryID, commitSHA string) (*Delivery, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO git_webhook_deliveries
			(repository_binding_id, provider_delivery_id, commit_sha, status)
		VALUES ($1,$2,$3,'RECEIVED')
		ON CONFLICT (repository_binding_id, provider_delivery_id) DO NOTHING
		RETURNING id
	`, bindingID, deliveryID, commitSHA).Scan(&id)
	if err == nil {
		return &Delivery{ID: id, BindingID: bindingID, ProviderDelivery: deliveryID, CommitSHA: commitSHA, Status: DeliveryReceived}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("attackdefense: insert webhook delivery: %w", err)
	}
	var record Delivery
	err = tx.QueryRow(ctx, `
		SELECT id, repository_binding_id, provider_delivery_id, lower(commit_sha), status
		FROM git_webhook_deliveries
		WHERE repository_binding_id=$1 AND provider_delivery_id=$2
	`, bindingID, deliveryID).Scan(&record.ID, &record.BindingID, &record.ProviderDelivery, &record.CommitSHA, &record.Status)
	if err != nil {
		return nil, false, fmt.Errorf("attackdefense: read webhook delivery: %w", err)
	}
	return &record, false, nil
}

// MarkDelivery updates the delivery outcome and optional safe error code.
func (s *Store) MarkDelivery(ctx context.Context, tx pgx.Tx, deliveryID uuid.UUID, status, errorCode string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE git_webhook_deliveries
		SET status=$2, processed_at=NOW(), error=NULLIF($3,'')
		WHERE id=$1
	`, deliveryID, status, trimTo(errorCode, 512)); err != nil {
		return fmt.Errorf("attackdefense: update webhook delivery: %w", err)
	}
	return nil
}

// TargetActor resolves the trusted requested_by user for a webhook target and
// revalidates current participation. It never trusts a payload user id.
func (s *Store) TargetActor(ctx context.Context, q querier, target WebhookTarget) (*TrustedActor, error) {
	return s.subjectFromExplicit(ctx, q, &target.Module, target.Subject.Type, target.Subject.ID)
}
