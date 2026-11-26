package boards

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/files"
	"github.com/nrf24l01/actfb/internal/platform/crypto"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const idempotencyRequestDomain = "actfb:p1:submission-request:v1:"

// Service owns board/module/task/participation/submission policy. Every
// mutation re-checks authorization from live PostgreSQL state.
type Service struct {
	store             Store
	auth              authorization.Authorizer
	clock             Clock
	limiter           ratelimit.Limiter
	argon2id          config.Argon2idConfig
	idempotencySecret []byte
	logger            *slog.Logger
	accessPolicy      access.Policy
	accessService     access.Service
	fileService       files.Service
	sealer            secrets.Sealer
	outbox            events.Outbox
	scoreSource       ExternalScoreSource
	submissionSource  ExternalSubmissionSource
	pluginChecker     PluginChecker
	instanceBinder    InstanceBinder
}

// Option customizes the service without adding backend dependencies.
type Option func(*Service)

// WithArgon2Config sets the Argon2id cost used for accepted-answer hashes.
// The default is the approved safe configuration (t=2, m=64 MiB, p=2, 32-byte
// key). The configuration is validated by crypto.Hash on first use.
func WithArgon2Config(c config.Argon2idConfig) Option {
	return func(s *Service) {
		if c.Time > 0 && c.Memory > 0 && c.Threads > 0 && c.KeyLen > 0 {
			s.argon2id = c
		}
	}
}

// WithIdempotencySecret sets the server-side HMAC key used to store a
// domain-separated request hash instead of an unkeyed low-entropy flag digest.
// Integrations should pass the JWT refresh secret or a dedicated environment
// secret. When absent, a process-local random secret is generated; flags are
// never hashed without a key, and an unavailable secret fails closed.
func WithIdempotencySecret(secret string) Option {
	return func(s *Service) {
		s.idempotencySecret = []byte(secret)
	}
}

// WithAccessPolicy installs the live ABAC policy used for PUBLIC/PRIVATE
// access-group reads, join eligibility and audience module visibility.
func WithAccessPolicy(policy access.Policy) Option {
	return func(s *Service) { s.accessPolicy = policy }
}

// WithAccessService installs the audience/access-group service used by the
// board-scoped audience and access-group HTTP endpoints.
func WithAccessService(service access.Service) Option {
	return func(s *Service) { s.accessService = service }
}

// WithFileService installs the files worker service. Nil is safe and makes file
// features return DEPENDENCY_UNAVAILABLE; text-only MANUAL submission still works.
func WithFileService(service files.Service) Option {
	return func(s *Service) { s.fileService = service }
}

// WithSealer installs the shared AES-GCM sealer for board/team invite codes.
func WithSealer(sealer secrets.Sealer) Option {
	return func(s *Service) { s.sealer = sealer }
}

// WithOutbox installs the transactional outbox used for submission.judged.
func WithOutbox(outbox events.Outbox) Option {
	return func(s *Service) { s.outbox = outbox }
}

// WithScoreSource installs a read-only external score contribution adapter
// (Attack+Defense awards, accepted canonical PENTEST submissions, etc.).
func WithScoreSource(source ExternalScoreSource) Option {
	return func(s *Service) { s.scoreSource = source }
}

// WithSubmissionSource installs the canonical external submission adapter used
// by the existing admin submission list/get/review routes.
func WithSubmissionSource(source ExternalSubmissionSource) Option {
	return func(s *Service) { s.submissionSource = source }
}

// WithPluginChecker installs the versioned plugin checker seam. It is not a
// fake fallback: absence makes PLUGIN/EXTENSION tasks fail closed when those
// task modes become writable.
func WithPluginChecker(checker PluginChecker) Option {
	return func(s *Service) { s.pluginChecker = checker }
}

// WithInstanceBinder installs the trusted instances binding seam. Absence
// makes INSTANCE task creation fail closed; no fake instance is claimed.
func WithInstanceBinder(binder InstanceBinder) Option {
	return func(s *Service) { s.instanceBinder = binder }
}

// WithLogger sets a structured logger. The logger is never given flag text,
// answer hashes or request bodies.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// NewService constructs the board business service. The variadic options keep
// the approved constructor shape while allowing Argon2 configuration and a
// dedicated idempotency HMAC secret.
func NewService(
	store Store,
	auth authorization.Authorizer,
	clock Clock,
	limiter ratelimit.Limiter,
	options ...Option,
) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	service := &Service{
		store:   store,
		auth:    auth,
		clock:   clock,
		limiter: limiter,
		argon2id: config.Argon2idConfig{
			Time:    config.DefaultArgon2Time,
			Memory:  config.DefaultArgon2Memory,
			Threads: config.DefaultArgon2Threads,
			KeyLen:  config.DefaultArgon2KeyLen,
		},
		logger: slog.Default(),
	}
	randomSecret := make([]byte, 32)
	if _, err := rand.Read(randomSecret); err == nil {
		service.idempotencySecret = randomSecret
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) ready() error {
	if s == nil || s.store == nil {
		return dependencyUnavailable("board_store")
	}
	return nil
}

// AccessService exposes the configured audience service to the board HTTP
// adapter without creating a second service instance.
func (s *Service) AccessService() access.Service {
	if s == nil {
		return nil
	}
	return s.accessService
}

// FileService exposes the configured file adapter to the board HTTP adapter.
func (s *Service) FileService() files.Service {
	if s == nil {
		return nil
	}
	return s.fileService
}

func (s *Service) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, serviceOperationTimeout)
}

func (s *Service) authorize(
	ctx context.Context,
	principal authorization.Principal,
	permission authorization.Permission,
	scope authorization.Scope,
) (authorization.Decision, error) {
	if s.auth == nil {
		return authorization.Decision{}, dependencyUnavailable("authorizer")
	}
	decision, err := s.auth.Authorize(ctx, principal, permission, scope)
	if err != nil {
		return authorization.Decision{}, mapAuthorizationError(err)
	}
	return decision, nil
}

func (s *Service) requirePermission(
	ctx context.Context,
	principal authorization.Principal,
	permission authorization.Permission,
	scope authorization.Scope,
) error {
	decision, err := s.authorize(ctx, principal, permission, scope)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

// CheckPermission is the HTTP-facing authorization preflight. It runs the same
// live authorizer as the service methods; handlers use it before parsing bodies,
// and every mutating method re-checks before writing.
func (s *Service) CheckPermission(
	ctx context.Context,
	principal authorization.Principal,
	permission authorization.Permission,
	boardID uuid.UUID,
	moduleID *uuid.UUID,
) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if s.auth == nil {
		return dependencyUnavailable("authorizer")
	}
	return s.requirePermission(ctx, principal, permission, authorization.Scope{BoardID: boardID, ModuleID: moduleID})
}

// CheckBoardRead is the HTTP preflight for participant board/module/task reads.
// Module-scoped staff are accepted through the same live permission aggregate
// used by the service.
func (s *Service) CheckBoardRead(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if s.auth == nil {
		return dependencyUnavailable("authorizer")
	}
	return s.requireBoardRead(ctx, principal, boardID)
}

func mapAuthorizationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorization.ErrUnauthorized), errors.Is(err, authorization.ErrUserDisabled):
		return ErrUnauthorized
	case errors.Is(err, authorization.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, authorization.ErrBoardNotFound):
		return ErrBoardNotFound
	case errors.Is(err, authorization.ErrModuleNotFound):
		return ErrModuleNotFound
	case errors.Is(err, authorization.ErrNoDatabase):
		return dependencyUnavailable("database")
	case errors.Is(err, authorization.ErrNoAuthorizer):
		return dependencyUnavailable("authorizer")
	default:
		return err
	}
}

func mapFileError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, files.ErrDraftNotFound):
		return ErrDraftNotFound
	case errors.Is(err, files.ErrDraftExpired):
		return ErrDraftExpired
	case errors.Is(err, files.ErrDraftAlreadyUsed), errors.Is(err, files.ErrFileNotReady), errors.Is(err, files.ErrFileValidationPending):
		return ErrDraftNotReady
	case errors.Is(err, files.ErrDraftAlreadyOpen):
		return ErrDraftNotReady
	case errors.Is(err, files.ErrForbidden), errors.Is(err, files.ErrFileContextMismatch):
		return ErrDraftForbidden
	case errors.Is(err, files.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, files.ErrBoardNotFound):
		return ErrBoardNotFound
	case errors.Is(err, files.ErrModuleNotFound):
		return ErrModuleNotFound
	case errors.Is(err, files.ErrTaskNotFound):
		return ErrTaskNotFound
	case errors.Is(err, files.ErrDependencyUnavailable), errors.Is(err, files.ErrScannerUnavailable), errors.Is(err, files.ErrS3PresignFailed):
		return dependencyUnavailable("files_service")
	default:
		return err
	}
}

func mapAccessError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, access.ErrUnauthorized), errors.Is(err, access.ErrUserDisabled):
		return ErrUnauthorized
	case errors.Is(err, access.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, access.ErrBoardNotFound):
		return ErrBoardNotFound
	case errors.Is(err, access.ErrModuleNotFound):
		return ErrModuleNotFound
	case errors.Is(err, access.ErrNoDatabase):
		return dependencyUnavailable("database")
	case errors.Is(err, access.ErrNoAuthorizer):
		return dependencyUnavailable("authorizer")
	default:
		return err
	}
}

func (s *Service) isAdmin(ctx context.Context, principal authorization.Principal) (bool, error) {
	decision, err := s.authorize(ctx, principal, authorization.PermissionBoardCreate, authorization.Scope{})
	if err != nil {
		return false, err
	}
	return decision.Allowed, nil
}

func (s *Service) moduleInScope(moduleIDs []uuid.UUID, moduleID uuid.UUID) bool {
	if moduleIDs == nil {
		return true
	}
	for _, allowed := range moduleIDs {
		if allowed == moduleID {
			return true
		}
	}
	return false
}

func normalizePage(limit, offset int) (int, int) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *Service) hashAnswers(answers []string) ([]string, error) {
	hashes := make([]string, 0, len(answers))
	for _, answer := range answers {
		hash, err := crypto.Hash(answer, s.argon2id)
		if err != nil {
			return nil, fmt.Errorf("hash accepted answer: %w", err)
		}
		hashes = append(hashes, hash)
	}
	return hashes, nil
}

func (s *Service) requestHash(principal authorization.Principal, path TaskPath, key, text string, draftID ...string) ([]byte, error) {
	if len(s.idempotencySecret) == 0 {
		return nil, dependencyUnavailable("idempotency_secret")
	}
	mac := hmac.New(sha256.New, s.idempotencySecret)
	_, _ = mac.Write([]byte(idempotencyRequestDomain))
	_, _ = mac.Write([]byte(principal.UserID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(path.BoardID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(path.ModuleID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(path.TaskID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(key))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(text))
	if len(draftID) > 0 {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(draftID[0]))
	}
	return mac.Sum(nil), nil
}

func (s *Service) reviewRequestHash(principal authorization.Principal, boardID, submissionID uuid.UUID, decision string, message *string) []byte {
	mac := hmac.New(sha256.New, s.idempotencySecret)
	_, _ = mac.Write([]byte("actfb:p1:review-request:v1:"))
	_, _ = mac.Write([]byte(principal.UserID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(boardID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(submissionID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(decision))
	_, _ = mac.Write([]byte{0})
	if message != nil {
		_, _ = mac.Write([]byte(*message))
	}
	return mac.Sum(nil)
}

// verifyFlag compares the submitted text against every stored Argon2id hash
// without an early exit. It deliberately returns only a boolean.
func (s *Service) verifyFlag(text string, hashes []string) (bool, error) {
	matched := 0
	var firstErr error
	for _, hash := range hashes {
		ok, err := crypto.VerifyArgon2id(text, hash)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ok {
			matched = 1
		}
	}
	if firstErr != nil && matched == 0 {
		return false, fmt.Errorf("verify flag: %w", ErrCheckerError)
	}
	return matched == 1, nil
}

func constantTimeEqual(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}

func auditEvent(
	principal authorization.Principal,
	boardID, moduleID *uuid.UUID,
	action, resourceType string,
	resourceID *uuid.UUID,
	details map[string]any,
) AuditEventParams {
	return AuditEventParams{
		ActorUserID:  principal.UserID,
		BoardID:      boardID,
		ModuleID:     moduleID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      details,
	}
}

func hasPermission(permissions []authorization.Permission, target authorization.Permission) bool {
	for _, permission := range permissions {
		if permission == target {
			return true
		}
	}
	return false
}
