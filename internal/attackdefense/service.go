package attackdefense

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

// JobPublisher publishes wake-up identifiers only. The database is the source
// of truth; a lost message is recovered by worker reconciliation.
type JobPublisher interface {
	PublishRun(ctx context.Context, runID uuid.UUID) error
}

// Service owns the AD business rules shared by HTTP and the worker.
type Service struct {
	db                   *postgres.DB
	store                *Store
	authz                authorization.Authorizer
	sealer               secrets.Sealer
	keyVersion           uint32
	integrations         IntegrationResolver
	providers            *GitProviderFactory
	checker              Checker
	fetcher              *GitFetcher
	runnerImages         []string
	webhookBaseURL       string
	provisionRepos       bool
	allowInsecureWebhook bool
	jobs                 JobPublisher
	logger               *slog.Logger
	now                  func() time.Time
}

// Option configures the service without changing the approved constructor.
type Option func(*Service)

// WithIntegrationResolver wires the trusted integration credential reader.
func WithIntegrationResolver(resolver IntegrationResolver) Option {
	return func(service *Service) {
		if resolver != nil {
			service.integrations = resolver
		}
	}
}

// WithGitProviderFactory wires real provider adapters.
func WithGitProviderFactory(factory *GitProviderFactory) Option {
	return func(service *Service) {
		if factory != nil {
			service.providers = factory
		}
	}
}

// WithChecker wires the isolated checker implementation.
func WithChecker(checker Checker) Option {
	return func(service *Service) { service.checker = checker }
}

// WithGitFetcher wires the exact-commit fetcher.
func WithGitFetcher(fetcher *GitFetcher) Option {
	return func(service *Service) { service.fetcher = fetcher }
}

// WithRunnerImageAllowlist sets the operator-pinned digest allowlist. Admin
// config writes fail closed unless the image is exactly listed.
func WithRunnerImageAllowlist(images []string) Option {
	return func(service *Service) {
		service.runnerImages = append([]string(nil), images...)
	}
}

// WithWebhookBaseURL sets the external HTTPS base used to register provider
// hooks, for example https://ctf.example.com/api/v2/webhooks/git.
func WithWebhookBaseURL(baseURL string) Option {
	return func(service *Service) { service.webhookBaseURL = strings.TrimRight(baseURL, "/") }
}

// WithRepositoryProvisioning enables idempotent provider-side private
// repository creation for operators who want server-setup repos. It is off by
// default: the Swagger assignment contract references existing repositories.
func WithRepositoryProvisioning(enabled bool) Option {
	return func(service *Service) { service.provisionRepos = enabled }
}

// WithJobPublisher wires the NATS/durable wake-up publisher.
func WithJobPublisher(publisher JobPublisher) Option {
	return func(service *Service) { service.jobs = publisher }
}

// WithAllowInsecureWebhookTransport permits non-TLS webhook transport from a
// loopback address for isolated local acceptance only. It never applies to a
// non-loopback remote.
func WithAllowInsecureWebhookTransport(enabled bool) Option {
	return func(service *Service) { service.allowInsecureWebhook = enabled }
}

// WithKeyVersion sets the sealing key version fallback.
func WithKeyVersion(version uint32) Option {
	return func(service *Service) {
		if version > 0 {
			service.keyVersion = version
		}
	}
}

// WithLogger sets a structured logger. Secrets, tokens and selectors are never
// logged by this package.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// WithClock overrides the UTC clock in tests.
func WithClock(clock func() time.Time) Option {
	return func(service *Service) {
		if clock != nil {
			service.now = clock
		}
	}
}

// NewService constructs the AD service. The shared sealer is mandatory because
// repository webhook secrets must never be stored in plaintext.
func NewService(db *postgres.DB, authz authorization.Authorizer, sealer secrets.Sealer, options ...Option) (*Service, error) {
	if db == nil || db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if sealer == nil {
		return nil, ErrNoSealer
	}
	store, err := NewStore(db)
	if err != nil {
		return nil, err
	}
	service := &Service{
		db:         db,
		store:      store,
		authz:      authz,
		sealer:     sealer,
		keyVersion: 1,
		providers:  &GitProviderFactory{},
		logger:     slog.Default(),
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if resolver, ok := sealer.(interface{ KeyVersion() uint32 }); ok {
		service.keyVersion = resolver.KeyVersion()
	}
	if service.integrations == nil {
		resolver, resolverErr := NewPostgresIntegrationResolver(db, sealer, service.keyVersion)
		if resolverErr != nil {
			return nil, resolverErr
		}
		service.integrations = resolver
	}
	return service, nil
}

// Store exposes the read-only score/progress store to adapters.
func (s *Service) Store() *Store { return s.store }

// RunnerImages reports the configured allowlist for boot diagnostics.
func (s *Service) RunnerImages() []string { return append([]string(nil), s.runnerImages...) }

// WebhookBaseURL reports the configured hook base (never a secret).
func (s *Service) WebhookBaseURL() string { return s.webhookBaseURL }

func (s *Service) authorize(ctx context.Context, actor authorization.Principal, permission authorization.Permission, scope authorization.Scope) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthorized
	}
	if s.authz == nil {
		return ErrNoAuthorizer
	}
	decision, err := s.authz.Authorize(ctx, actor, permission, scope)
	if err != nil {
		switch {
		case errors.Is(err, authorization.ErrUnauthorized), errors.Is(err, authorization.ErrUserDisabled):
			return ErrUnauthorized
		case errors.Is(err, authorization.ErrForbidden):
			return ErrForbidden
		default:
			return fmt.Errorf("attackdefense: authorize: %w", err)
		}
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

func (s *Service) moduleScope(module *ModuleContext) authorization.Scope {
	moduleID := module.ModuleID
	return authorization.Scope{BoardID: module.BoardID, ModuleID: &moduleID}
}

func (s *Service) adminContext(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, permission authorization.Permission) (*ModuleContext, error) {
	module, err := s.store.moduleContext(ctx, s.db.Pool, boardID, moduleID, false)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, actor, permission, s.moduleScope(module)); err != nil {
		return nil, err
	}
	return module, nil
}

func (s *Service) participantContext(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) (*ModuleContext, *TrustedActor, error) {
	if actor.UserID == uuid.Nil {
		return nil, nil, ErrUnauthorized
	}
	return s.store.participantContext(ctx, boardID, moduleID, actor.UserID)
}

var (
	vulnerabilityKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	idempotencyKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
)

func validateIdempotencyKey(key string) error {
	key = strings.TrimSpace(key)
	if len(key) < 16 || len(key) > 128 || !idempotencyKeyPattern.MatchString(key) {
		return Invalid("Idempotency-Key", "must be 16..128 characters of [A-Za-z0-9._:-]", "INVALID_FORMAT")
	}
	return nil
}

func validatePagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ValidateRunnerImage checks a pinned digest reference against an allowlist.
func ValidateRunnerImage(image string, allowlist []string) error {
	image = strings.TrimSpace(image)
	if image == "" || !dockerImageDigestPattern.MatchString(image) {
		return Invalid("runnerImage", "must be an immutable sha256 digest reference", "INVALID_FORMAT")
	}
	for _, allowed := range allowlist {
		if strings.TrimSpace(allowed) == image {
			return nil
		}
	}
	return ErrRunnerUnavailable
}
