package attackdefense

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// GetRepositories returns the participant's clone/web URLs and branch heads.
// Tokens never leave the trusted process.
func (s *Service) GetRepositories(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) (*Repositories, error) {
	module, trusted, err := s.participantContext(ctx, actor, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	_ = module
	assignment, err := s.store.AssignmentForSubject(ctx, moduleID, trusted.Subject)
	if err != nil {
		return nil, err
	}
	source, err := s.repositoryInfo(ctx, assignment.Source)
	if err != nil {
		return nil, err
	}
	exploit, err := s.repositoryInfo(ctx, assignment.Exploit)
	if err != nil {
		return nil, err
	}
	return &Repositories{Source: *source, Exploit: *exploit}, nil
}

func (s *Service) repositoryInfo(ctx context.Context, spec RepositorySpec) (*RepositoryInfo, error) {
	integration, provider, err := s.providerFor(ctx, spec.IntegrationID)
	if err != nil {
		if errors.Is(err, ErrIntegrationNotFound) || errors.Is(err, ErrIntegrationCredentials) {
			return nil, ErrRepositoryNotConfigured
		}
		return nil, err
	}
	info, err := provider.GetRepository(ctx, integration, spec.Repository, spec.Branch)
	if err != nil {
		if errors.Is(err, ErrRepositoryNotFound) || errors.Is(err, ErrCommitNotFound) {
			return nil, ErrRepositoryNotConfigured
		}
		return nil, err
	}
	return info, nil
}

func (s *Service) providerFor(ctx context.Context, integrationID uuid.UUID) (*Integration, GitProvider, error) {
	if s.integrations == nil {
		return nil, nil, ErrNoIntegrationResolver
	}
	if s.providers == nil {
		return nil, nil, ErrNoGitProvider
	}
	integration, err := s.integrations.ResolveIntegration(ctx, integrationID)
	if err != nil {
		return nil, nil, err
	}
	provider, err := s.providers.ProviderFor(integration)
	if err != nil {
		return nil, nil, err
	}
	return integration, provider, nil
}

// GetProgress returns the participant's discovered vulnerabilities and score
// contribution. Hidden vulnerabilities are only counted.
func (s *Service) GetProgress(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) (*Progress, error) {
	_, trusted, err := s.participantContext(ctx, actor, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	return s.store.Progress(ctx, moduleID, trusted.Subject)
}

// ListMyRuns lists runs for the caller's own scoring subject.
func (s *Service) ListMyRuns(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, filter RunFilter) ([]Run, int, error) {
	if filter.Kind != "" && filter.Kind != RunKindExploit && filter.Kind != RunKindPatch {
		return nil, 0, Invalid("kind", "must be EXPLOIT or PATCH", "INVALID_VALUE")
	}
	if filter.Status != "" && !validRunStatus(filter.Status) {
		return nil, 0, Invalid("status", "invalid run status", "INVALID_VALUE")
	}
	filter.Limit, filter.Offset = validatePagination(filter.Limit, filter.Offset)
	_, trusted, err := s.participantContext(ctx, actor, boardID, moduleID)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListRuns(ctx, moduleID, &trusted.Subject, filter)
}

// GetMyRun returns one run only when it belongs to the caller's subject.
func (s *Service) GetMyRun(ctx context.Context, actor authorization.Principal, boardID, moduleID, runID uuid.UUID) (*Run, error) {
	_, trusted, err := s.participantContext(ctx, actor, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	run, err := s.store.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.ModuleID != moduleID || run.SubjectType != trusted.Subject.Type || run.SubjectID != trusted.Subject.ID {
		return nil, ErrRunNotFound
	}
	return run, nil
}

// GetMyRunLogs returns sanitized logs for the caller's own run.
func (s *Service) GetMyRunLogs(ctx context.Context, actor authorization.Principal, boardID, moduleID, runID uuid.UUID) ([]RunLog, bool, error) {
	if _, err := s.GetMyRun(ctx, actor, boardID, moduleID, runID); err != nil {
		return nil, false, err
	}
	return s.store.Logs(ctx, runID, MaxCheckerLogLines)
}

// CreateMyRun enqueues one manual run. It checks the live board/module window,
// the current config, participation and repository assignment before any Git
// provider call. Same idempotency key + same request returns the same logical
// run; a different payload is rejected.
func (s *Service) CreateMyRun(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, kind, commitSHA, idempotencyKey string) (*Run, error) {
	if kind != RunKindExploit && kind != RunKindPatch {
		return nil, Invalid("kind", "must be EXPLOIT or PATCH", "INVALID_VALUE")
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	module, trusted, err := s.participantContext(ctx, actor, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !module.Active(now) {
		return nil, ErrBoardNotActive
	}
	if !module.AllowsKind(kind) {
		return nil, ErrRunKindNotAllowed
	}
	config, err := s.store.Config(ctx, moduleID)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return nil, ErrRepositoryNotConfigured
		}
		return nil, err
	}
	if !config.ManualRerunEnabled {
		return nil, ErrRunActionNotAllowed
	}
	assignment, err := s.store.AssignmentForSubject(ctx, moduleID, trusted.Subject)
	if err != nil {
		return nil, err
	}
	spec := triggeredSpec(assignment, kind)
	sha, err := s.resolveCommit(ctx, spec, commitSHA)
	if err != nil {
		return nil, err
	}
	run, created, err := s.enqueueManualRun(ctx, actor.UserID, module, trusted, assignment, spec, kind, sha, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if created {
		s.publishRun(ctx, run.ID)
	}
	return run, nil
}

func triggeredSpec(assignment *Assignment, kind string) RepositorySpec {
	if kind == RunKindPatch {
		return assignment.Source
	}
	return assignment.Exploit
}

func (s *Service) resolveCommit(ctx context.Context, spec RepositorySpec, requested string) (string, error) {
	integration, provider, err := s.providerFor(ctx, spec.IntegrationID)
	if err != nil {
		if errors.Is(err, ErrIntegrationNotFound) || errors.Is(err, ErrIntegrationCredentials) {
			return "", ErrRepositoryNotConfigured
		}
		return "", err
	}
	if strings.TrimSpace(requested) == "" {
		head, err := provider.BranchHead(ctx, integration, spec.Repository, spec.Branch)
		if err != nil {
			if errors.Is(err, ErrCommitNotFound) {
				return "", ErrCommitNotFound
			}
			return "", err
		}
		return NormalizeCommitSHA(head.SHA)
	}
	sha, err := NormalizeCommitSHA(requested)
	if err != nil {
		return "", err
	}
	if _, err := provider.CommitExists(ctx, integration, spec.Repository, sha); err != nil {
		if errors.Is(err, ErrCommitNotFound) {
			return "", ErrCommitNotFound
		}
		return "", err
	}
	return sha, nil
}

func (s *Service) enqueueManualRun(ctx context.Context, actorID uuid.UUID, module *ModuleContext, trusted *TrustedActor, assignment *Assignment, spec RepositorySpec, kind, sha, key string) (*Run, bool, error) {
	tx, err := s.store.begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		"ad-run:"+module.ModuleID.String()+":"+trusted.Subject.Type+":"+trusted.Subject.ID.String()); err != nil {
		return nil, false, fmt.Errorf("attackdefense: lock manual run: %w", err)
	}
	// Re-read the live window under the lock. A run queued before the board
	// ends must not slip through a stale pre-check.
	fresh, err := s.store.moduleContext(ctx, tx, module.BoardID, module.ModuleID, true)
	if err != nil {
		return nil, false, err
	}
	if !fresh.Active(s.now()) {
		return nil, false, ErrBoardNotActive
	}
	if prior, found, err := s.store.IdempotencyLookup(ctx, tx, actorID, module.ModuleID, "ad.run.manual", key); err != nil {
		return nil, false, err
	} else if found {
		priorKind, _ := prior["kind"].(string)
		priorSHA, _ := prior["commitSha"].(string)
		if priorKind != kind || !strings.EqualFold(priorSHA, sha) {
			return nil, false, ErrIdempotencyKeyReused
		}
		runID, parseErr := uuid.Parse(fmt.Sprint(prior["runId"]))
		if parseErr != nil {
			return nil, false, ErrIdempotencyKeyReused
		}
		run, getErr := s.store.runByID(ctx, tx, runID, false)
		if getErr != nil {
			return nil, false, getErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("attackdefense: commit idempotent run: %w", err)
		}
		return run, false, nil
	}
	active, err := s.store.CountActiveRuns(ctx, tx, module.ModuleID, trusted.Subject)
	if err != nil {
		return nil, false, err
	}
	config, err := s.store.Config(ctx, module.ModuleID)
	if err != nil {
		return nil, false, err
	}
	if active >= config.MaxConcurrentRunsPerSubject {
		return nil, false, ErrRunRateLimited
	}
	deliveryID := "manual:" + key
	run, created, err := s.store.InsertRun(ctx, tx, InsertRunParams{
		ModuleID: module.ModuleID, RepositoryAssignmentID: assignment.ID,
		SubjectType: trusted.Subject.Type, SubjectID: trusted.Subject.ID,
		Kind: kind, CommitSHA: sha, ProviderDeliveryID: &deliveryID, RequestedBy: trusted.RequestedBy,
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		if run.Status == RunStatusQueued || run.Status == RunStatusRunning {
			if err := tx.Commit(ctx); err != nil {
				return nil, false, fmt.Errorf("attackdefense: commit duplicate run: %w", err)
			}
			return nil, false, ErrRunAlreadyQueued
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("attackdefense: commit logical run: %w", err)
		}
		return run, false, nil
	}
	boardID := module.BoardID
	details := map[string]any{
		"idempotencyKey": key,
		"runId":          run.ID.String(),
		"kind":           kind,
		"commitSha":      sha,
		"subjectType":    trusted.Subject.Type,
		"subjectId":      trusted.Subject.ID.String(),
	}
	if err := writeAudit(ctx, tx, AuditEvent{
		ActorUserID: actorID, BoardID: &boardID, ModuleID: &module.ModuleID,
		Action: "ad.run.manual", ResourceType: "ad_run", ResourceID: &run.ID, Details: details,
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("attackdefense: commit manual run: %w", err)
	}
	return run, true, nil
}

func (s *Service) publishRun(ctx context.Context, runID uuid.UUID) {
	if s.jobs == nil {
		return
	}
	if err := s.jobs.PublishRun(ctx, runID); err != nil && s.logger != nil {
		// The run stays QUEUED; reconciliation will pick it up. Never lose
		// the database truth because a wake-up message failed.
		s.logger.Warn("attackdefense: enqueue job wake-up failed", "run", runID, "error", err)
	}
}

func validRunStatus(status string) bool {
	switch status {
	case RunStatusQueued, RunStatusRunning, RunStatusPassed, RunStatusFailed, RunStatusError, RunStatusCanceled:
		return true
	default:
		return false
	}
}
