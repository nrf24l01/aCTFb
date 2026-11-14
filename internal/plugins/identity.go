package plugins

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
)

// Identity is the immutable, manager-approved plugin runtime identity.
type Identity struct {
	PluginID     ID
	Capabilities []Capability
	Permissions  []authorization.Permission
}

// Delegation is a core-issued short-lived HMAC token binding a plugin, end
// user, scope, expiry and one-time nonce. The raw token is never persisted.
type Delegation struct {
	Token     string
	PluginID  ID
	UserID    uuid.UUID
	Scope     authorization.Scope
	Nonce     uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// DelegationIssuer issues and verifies core↔plugin end-user delegations.
type DelegationIssuer interface {
	Issue(ctx context.Context, identity Identity, userID uuid.UUID, scope authorization.Scope, ttl time.Duration) (Delegation, error)
	Verify(ctx context.Context, token string) (Delegation, error)
}

// CallContext is passed by value and is immutable. UserID and Scope always come
// from a verified delegation, never from a plugin-supplied body.
type CallContext struct {
	Identity   Identity
	Delegation Delegation
	UserID     uuid.UUID
	Scope      authorization.Scope
	RequestID  string
}

// BoardContext is the minimal, capability-filtered board projection exposed to
// a plugin. It intentionally carries no flags, answers or credentials.
type BoardContext struct {
	BoardID     uuid.UUID
	ModuleID    *uuid.UUID
	SubjectType string
	SubjectID   uuid.UUID
	Permissions []authorization.Permission
}

// CoreProxy is the only data/action path from a plugin into core.
type CoreProxy interface {
	VerifyDelegation(ctx context.Context, token string) (Delegation, error)
	Authorize(ctx context.Context, call CallContext, permission authorization.Permission) (authorization.Decision, error)
	BoardContext(ctx context.Context, call CallContext, boardID uuid.UUID) (BoardContext, error)
	PublishEvent(ctx context.Context, call CallContext, event events.Event) error
}
