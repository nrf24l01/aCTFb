package access

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// Policy is the ABAC gate used by boards HTTP/domain. It never grants RBAC
// permissions and never trusts client-supplied scope.
type Policy interface {
	CanJoinBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error)
	CanReadBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error)
	CanAccessModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (bool, error)
	// CanAccessModuleAsParticipant applies the participant visibility rules
	// without a global-admin shortcut. Participant-own actions use it so an
	// admin who joined a board still obeys a revoked access group or audience
	// in that context.
	CanAccessModuleAsParticipant(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (bool, error)
	IsSubjectInAudience(ctx context.Context, boardID, audienceID uuid.UUID, subject Subject) (bool, error)
}

// Service is the frozen P1R access/audience service contract.
type Service interface {
	ListAccessGroups(ctx context.Context, principal authorization.Principal, q ListQuery) (GroupPage, error)
	CreateAccessGroup(ctx context.Context, principal authorization.Principal, name string) (*Group, error)
	GetAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) (*Group, error)
	UpdateAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID, name string) (*Group, error)
	DeleteAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) error

	ListAudiences(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (AudiencePage, error)
	CreateAudience(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, in AudienceInput) (*Audience, error)
	UpdateAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in AudiencePatch) (*Audience, error)
	DeleteAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID) error
	ListAudienceMembers(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, q ListQuery) (MemberPage, error)
	AddAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in MemberInput) (*AudienceMember, error)
	RemoveAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, subject Subject) error

	SetBoardAccessGroups(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, groupIDs []uuid.UUID) error
	BoardAccessGroups(ctx context.Context, boardID uuid.UUID) ([]uuid.UUID, error)
}

// UsersService is the global-admin user management contract consumed by
// internal/access/http. Password hashes are never returned.
type UsersService interface {
	ListUsers(ctx context.Context, principal authorization.Principal, q ListQuery) (AdminUserPage, error)
	CreateUser(ctx context.Context, principal authorization.Principal, in AdminUserInput) (*AdminUser, error)
	GetUser(ctx context.Context, principal authorization.Principal, id uuid.UUID) (*AdminUser, error)
	UpdateUser(ctx context.Context, principal authorization.Principal, id uuid.UUID, in AdminUserPatch) (*AdminUser, error)
}

// ServiceOption configures AccessService.
type ServiceOption func(*AccessService)

// WithArgon2 injects the deployment Argon2id parameters used only for admin
// user creation. Zero values fall back to the config defaults.
func WithArgon2(cfg config.Argon2idConfig) ServiceOption {
	return func(service *AccessService) {
		service.argon2 = normalizeArgon2(cfg)
	}
}

// AccessService implements Service, Policy, and UsersService against live
// PostgreSQL state.
type AccessService struct {
	db     *postgres.DB
	auth   authorization.Authorizer
	argon2 config.Argon2idConfig
}

// NewService constructs the access service. auth is used only for board-scoped
// RBAC checks (audiences/board access-group assignment); global access-group and
// admin-user management re-read the live global admin role directly.
func NewService(db *postgres.DB, authz authorization.Authorizer, opts ...ServiceOption) *AccessService {
	service := &AccessService{
		db:     db,
		auth:   authz,
		argon2: normalizeArgon2(config.Argon2idConfig{}),
	}
	for _, option := range opts {
		if option != nil {
			option(service)
		}
	}
	return service
}

func normalizeArgon2(cfg config.Argon2idConfig) config.Argon2idConfig {
	if cfg.Time == 0 {
		cfg.Time = config.DefaultArgon2Time
	}
	if cfg.Memory == 0 {
		cfg.Memory = config.DefaultArgon2Memory
	}
	if cfg.Threads == 0 {
		cfg.Threads = config.DefaultArgon2Threads
	}
	if cfg.KeyLen == 0 {
		cfg.KeyLen = config.DefaultArgon2KeyLen
	}
	return cfg
}

var (
	_ Service      = (*AccessService)(nil)
	_ Policy       = (*AccessService)(nil)
	_ UsersService = (*AccessService)(nil)
)
