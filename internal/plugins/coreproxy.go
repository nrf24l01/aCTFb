package plugins

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// ServiceIdentityVerifier resolves a raw plugin service token to its approved
// immutable identity. The raw token is compared against live runtime handles
// and never accepted from a browser.
type ServiceIdentityVerifier interface {
	VerifyServiceToken(ctx context.Context, token string) (Identity, error)
}

// VerifyServiceToken implements ServiceIdentityVerifier.
func (m *Manager) VerifyServiceToken(ctx context.Context, token string) (Identity, error) {
	if m == nil || token == "" {
		return Identity{}, ErrPermissionDenied
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, runtime := range m.runtime {
		// The runtime map only contains a running backend; its installed snapshot
		// may still be the pre-enable status, so do not use that stale field here.
		if runtime == nil || runtime.token == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(runtime.token), []byte(token)) == 1 {
			return runtime.identity, nil
		}
	}
	return Identity{}, ErrPermissionDenied
}

// VerifyDelegation forwards to the core delegation issuer.
func (m *Manager) VerifyDelegation(ctx context.Context, token string) (Delegation, error) {
	if m == nil || m.issuer == nil {
		return Delegation{}, ErrDelegationInvalid
	}
	return m.issuer.Verify(ctx, token)
}

// Identity returns the currently running identity for a plugin.
func (m *Manager) Identity(id ID) (Identity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtime := m.runtime[id]
	if runtime == nil {
		return Identity{}, false
	}
	return runtime.identity, true
}

// CoreProxyOptions configures the default scoped core SDK backend.
type CoreProxyOptions struct {
	DB            *postgres.DB
	Authorizer    authorization.Authorizer
	Grants        authorization.PermissionGrantChecker
	Registry      authorization.PermissionRegistry
	Publisher     events.Publisher
	EventRegistry *events.Registry
	Issuer        DelegationIssuer
}

// CoreProxyService is the default implementation of CoreProxy. It never trusts
// plugin-supplied user or scope: both come from a verified core delegation.
type CoreProxyService struct {
	db            *postgres.DB
	authorizer    authorization.Authorizer
	grants        authorization.PermissionGrantChecker
	registry      authorization.PermissionRegistry
	publisher     events.Publisher
	eventRegistry *events.Registry
	issuer        DelegationIssuer
}

// NewCoreProxy constructs the scoped plugin→core bridge.
func NewCoreProxy(options CoreProxyOptions) (*CoreProxyService, error) {
	if options.Issuer == nil {
		return nil, errors.New("plugins: core proxy issuer is required")
	}
	if options.Authorizer == nil {
		return nil, errors.New("plugins: core proxy authorizer is required")
	}
	if options.Grants == nil {
		return nil, errors.New("plugins: core proxy grant checker is required")
	}
	if options.Registry == nil {
		return nil, errors.New("plugins: core proxy permission registry is required")
	}
	eventRegistry := options.EventRegistry
	if eventRegistry == nil {
		eventRegistry = events.CoreRegistry()
	}
	return &CoreProxyService{
		db:            options.DB,
		authorizer:    options.Authorizer,
		grants:        options.Grants,
		registry:      options.Registry,
		publisher:     options.Publisher,
		eventRegistry: eventRegistry,
		issuer:        options.Issuer,
	}, nil
}

var _ CoreProxy = (*CoreProxyService)(nil)

func (p *CoreProxyService) VerifyDelegation(ctx context.Context, token string) (Delegation, error) {
	return p.issuer.Verify(ctx, token)
}

func (p *CoreProxyService) Authorize(ctx context.Context, call CallContext, permission authorization.Permission) (authorization.Decision, error) {
	if call.UserID == uuid.Nil || call.Delegation.UserID != call.UserID {
		return authorization.Decision{}, ErrPermissionDenied
	}
	if call.Delegation.PluginID != call.Identity.PluginID {
		return authorization.Decision{}, ErrPermissionDenied
	}
	definition, ok := p.registry.Lookup(permission)
	if !ok || definition.Source != string(call.Identity.PluginID) {
		return authorization.Decision{}, ErrPermissionDenied
	}
	if !containsAuthorizationPermission(call.Identity.Permissions, permission) {
		return authorization.Decision{}, ErrPermissionDenied
	}
	if !sameScope(call.Delegation.Scope, call.Scope) {
		return authorization.Decision{}, ErrPermissionDenied
	}
	granted, err := p.grants.HasGrant(ctx, call.UserID, permission, call.Scope)
	if err != nil {
		return authorization.Decision{}, err
	}
	if !granted {
		return authorization.Decision{Allowed: false, Reason: "explicit plugin grant required"}, nil
	}
	return p.authorizer.Authorize(ctx, authorization.Principal{UserID: call.UserID}, permission, call.Scope)
}

func (p *CoreProxyService) BoardContext(ctx context.Context, call CallContext, boardID uuid.UUID) (BoardContext, error) {
	if boardID == uuid.Nil || call.Delegation.UserID != call.UserID {
		return BoardContext{}, ErrPermissionDenied
	}
	if !containsCapability(call.Identity.Capabilities, CapabilityBoardsRead) {
		return BoardContext{}, ErrPermissionDenied
	}
	if call.Delegation.Scope.BoardID != uuid.Nil && call.Delegation.Scope.BoardID != boardID {
		return BoardContext{}, ErrPermissionDenied
	}
	if p.db == nil || p.db.Pool == nil {
		return BoardContext{}, errors.New("plugins: core proxy database is not configured")
	}
	var exists bool
	if err := p.db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM boards WHERE id = $1)`, boardID).Scan(&exists); err != nil {
		return BoardContext{}, err
	}
	if !exists {
		return BoardContext{}, authorization.ErrBoardNotFound
	}
	context := BoardContext{BoardID: boardID, ModuleID: call.Delegation.Scope.ModuleID}
	var teamID *uuid.UUID
	var joined bool
	if err := p.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id = $1 AND user_id = $2)
	`, boardID, call.UserID).Scan(&joined); err != nil {
		return BoardContext{}, err
	}
	if joined {
		var tm *uuid.UUID
		row := p.db.Pool.QueryRow(ctx, `
			SELECT tm.team_id
			FROM team_members tm
			WHERE tm.board_id = $1 AND tm.user_id = $2
		`, boardID, call.UserID)
		if err := row.Scan(&tm); err == nil {
			teamID = tm
		}
	}
	if teamID != nil {
		context.SubjectType = events.SubjectTypeTeam
		context.SubjectID = *teamID
	} else if joined {
		context.SubjectType = events.SubjectTypeUser
		context.SubjectID = call.UserID
	}

	permissions, err := p.authorizer.Permissions(ctx, authorization.Principal{UserID: call.UserID}, boardID)
	if err != nil {
		return BoardContext{}, err
	}
	prefix := "plugin." + string(call.Identity.PluginID) + "."
	for _, permission := range permissions.Permissions {
		if strings.HasPrefix(string(permission), prefix) {
			context.Permissions = append(context.Permissions, permission)
		}
	}
	for _, module := range permissions.Modules {
		if call.Delegation.Scope.ModuleID != nil && module.ModuleID != *call.Delegation.Scope.ModuleID {
			continue
		}
		for _, permission := range module.Permissions {
			if strings.HasPrefix(string(permission), prefix) && !containsAuthorizationPermission(context.Permissions, permission) {
				context.Permissions = append(context.Permissions, permission)
			}
		}
	}
	return context, nil
}

func (p *CoreProxyService) PublishEvent(ctx context.Context, call CallContext, event events.Event) error {
	if call.UserID == uuid.Nil || call.Delegation.UserID != call.UserID {
		return ErrPermissionDenied
	}
	if !containsCapability(call.Identity.Capabilities, CapabilityEventsSubscribe) {
		return ErrPermissionDenied
	}
	if err := p.eventRegistry.ValidatePluginPublish(string(call.Identity.PluginID), event.Type); err != nil {
		return err
	}
	if event.BoardID == nil || *event.BoardID != call.Delegation.Scope.BoardID {
		return ErrPermissionDenied
	}
	if call.Delegation.Scope.BoardID == uuid.Nil {
		return ErrPermissionDenied
	}
	if event.ID == uuid.Nil {
		return errors.New("plugins: event id is required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.SubjectType != "" {
		switch event.SubjectType {
		case events.SubjectTypeUser:
			if event.SubjectID == nil || *event.SubjectID != call.UserID {
				return ErrPermissionDenied
			}
		default:
			return ErrPermissionDenied
		}
	}
	if err := p.eventRegistry.ValidateEvent(event); err != nil {
		return err
	}
	if p.publisher == nil {
		return errors.New("plugins: event publisher is not configured")
	}
	return p.publisher.Publish(ctx, event)
}

func sameScope(left, right authorization.Scope) bool {
	if left.BoardID != right.BoardID {
		return false
	}
	if left.ModuleID == nil || right.ModuleID == nil {
		return left.ModuleID == nil && right.ModuleID == nil
	}
	return *left.ModuleID == *right.ModuleID
}

func containsAuthorizationPermission(permissions []authorization.Permission, target authorization.Permission) bool {
	for _, permission := range permissions {
		if permission == target {
			return true
		}
	}
	return false
}

func containsCapability(capabilities []Capability, target Capability) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}

// NewCoreProxy constructs the default scoped bridge from the manager's shared
// registry/authorizer/grant store. Boot code wires the returned handler under
// /api/v2/plugin-sdk/v1; browsers never receive these credentials.
func (m *Manager) NewCoreProxy() (*CoreProxyService, error) {
	if m == nil {
		return nil, errors.New("plugins: manager is nil")
	}
	return NewCoreProxy(CoreProxyOptions{
		DB:            m.db,
		Authorizer:    m.authorizer,
		Grants:        m.grants,
		Registry:      m.registry,
		Publisher:     m.publisher,
		EventRegistry: m.eventRegistry,
		Issuer:        m.issuer,
	})
}

// Issuer exposes the core delegation issuer to boot wiring without exposing the
// signing key.
func (m *Manager) Issuer() DelegationIssuer {
	if m == nil {
		return nil
	}
	return m.issuer
}
