package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/plugins/process"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

// PluginDatabaseOptions configures the manager/operator-owned provisioning
// connection and the runtime DSN for private plugin PostgreSQL schemas.
//
// ProvisionDSN must be an operator/manager role, never the core runtime role.
// Plugin SQL migrations always execute over a restricted per-plugin login role,
// never this connection.
type PluginDatabaseOptions struct {
	ProvisionDSN string
	Host         string
	Port         int
	Name         string
	SSLMode      string
}

// ManagerOptions are explicit constructor options. There are no hidden global
// defaults for security-sensitive dependencies: the same permission registry,
// authorizer and grant store must be passed by the boot owner.
type ManagerOptions struct {
	DB         *postgres.DB
	Root       string
	ServiceKey []byte
	Sealer     secrets.Sealer

	Permissions authorization.PermissionRegistry
	Authorizer  authorization.Authorizer
	GrantStore  *authorization.GrantStore

	Runner    process.Runner
	AllowExec bool
	// AllowTrustedJS is the operator-wide gate for accepting trusted-js
	// packages. It defaults to false and never replaces per-install approval.
	AllowTrustedJS bool
	PluginDB       PluginDatabaseOptions
	CoreSDKURL     string
	PlatformOrigin string
	IframeOrigin   string
	AssetOrigin    string
	HTTPTimeout    time.Duration
	MaxBodyBytes   int64

	NATS          *nats.Client
	Outbox        events.Outbox
	Publisher     events.Publisher
	EventRegistry *events.Registry

	Logger *slog.Logger
	Clock  func() time.Time
}

// Manager is the installable plugin registry, lifecycle supervisor and core
// proxy. The zero value is not usable; call NewManager.
type Manager struct {
	db             *postgres.DB
	root           string
	allowExec      bool
	allowTrustedJS bool
	serviceKey     []byte
	sealer         secrets.Sealer
	registry       authorization.PermissionRegistry
	authorizer     authorization.Authorizer
	grants         *authorization.GrantStore
	runner         process.Runner
	execRunner     *process.ExecRunner

	pluginDB       PluginDatabaseOptions
	coreSDKURL     string
	iframeOrigin   string
	assetOrigin    string
	platformOrigin string
	httpTimeout    time.Duration
	maxBody        int64

	nats          *nats.Client
	outbox        events.Outbox
	publisher     events.Publisher
	eventRegistry *events.Registry
	issuer        *DelegationSigner
	logger        *slog.Logger
	clock         func() time.Time

	mu      sync.RWMutex
	runtime map[ID]*runtimeBackend
}

type runtimeBackend struct {
	installed Installed
	identity  Identity
	token     string
	handle    *process.Handle
	client    *protocol.Client
	dsn       string
	schema    string
	startedAt time.Time
	health    Health
	healthMu  sync.Mutex
	cancel    context.CancelFunc
	consumers []jetstream.ConsumeContext
}

// NewManager constructs the plugin manager. It does not start any plugin and
// never executes package code.
func NewManager(options ManagerOptions) (*Manager, error) {
	if options.DB == nil || options.DB.Pool == nil {
		return nil, errors.New("plugins: ManagerOptions.DB is required")
	}
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("plugins: ManagerOptions.Root is required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, fmt.Errorf("plugins: resolve root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "incoming"), 0o750); err != nil {
		return nil, fmt.Errorf("plugins: create root: %w", err)
	}
	if len(options.ServiceKey) < 32 {
		return nil, errors.New("plugins: ManagerOptions.ServiceKey must be the decoded 32-byte ACTFB_PLUGIN_SERVICE_KEY")
	}
	if options.Sealer == nil {
		return nil, errors.New("plugins: ManagerOptions.Sealer is required for encrypted plugin settings")
	}
	if options.Permissions == nil {
		return nil, errors.New("plugins: ManagerOptions.Permissions is required (share the boot registry)")
	}
	if options.Authorizer == nil {
		return nil, errors.New("plugins: ManagerOptions.Authorizer is required")
	}
	if options.GrantStore == nil {
		return nil, errors.New("plugins: ManagerOptions.GrantStore is required")
	}
	if options.Runner != nil {
		if _, isDockerRunner := options.Runner.(*process.DockerRunner); isDockerRunner {
			return nil, errors.New("plugins: the core backend must not hold a Docker socket; use process.NewRemoteRunner with cmd/plugin-runner")
		}
	}
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = 5 * time.Second
	}
	if options.HTTPTimeout > config.MaxPluginHTTPTimeout {
		return nil, fmt.Errorf("plugins: HTTP timeout must be at most %s", config.MaxPluginHTTPTimeout)
	}
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = protocol.DefaultMaxBodyBytes
	}
	if options.MaxBodyBytes > protocol.DefaultMaxBodyBytes {
		options.MaxBodyBytes = protocol.DefaultMaxBodyBytes
	}
	if options.IframeOrigin == "" {
		options.IframeOrigin = config.DefaultPluginIframeOrigin
	}
	if err := config.ValidateAllowedOrigin(options.IframeOrigin); err != nil {
		return nil, fmt.Errorf("plugins: invalid iframe/asset origin: %w", err)
	}
	if options.AssetOrigin == "" {
		options.AssetOrigin = options.IframeOrigin
	}
	if err := config.ValidateAllowedOrigin(options.AssetOrigin); err != nil {
		return nil, fmt.Errorf("plugins: invalid asset origin: %w", err)
	}
	if !sameOrigin(options.AssetOrigin, options.IframeOrigin) {
		return nil, errors.New("plugins: iframe origin and asset origin must match")
	}
	if options.PlatformOrigin != "" {
		if err := config.ValidateAllowedOrigin(options.PlatformOrigin); err != nil {
			return nil, fmt.Errorf("plugins: invalid platform origin: %w", err)
		}
		if sameOrigin(options.PlatformOrigin, options.AssetOrigin) {
			return nil, errors.New("plugins: asset origin must be a separate origin from the platform")
		}
	}
	if options.CoreSDKURL != "" {
		parsed, err := url.Parse(options.CoreSDKURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, errors.New("plugins: CoreSDKURL must be an absolute http(s) URL")
		}
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.EventRegistry == nil {
		options.EventRegistry = events.CoreRegistry()
	}
	if options.AllowExec {
		options.Logger.Warn("plugin exec backends explicitly enabled; local executables are trusted operator packages, not an OS sandbox")
	}
	if options.AllowTrustedJS {
		options.Logger.Warn("trusted-js plugin packages explicitly accepted; a trusted bundle executes with platform-origin capabilities and is not sandboxed")
	}

	manager := &Manager{
		db:             options.DB,
		root:           root,
		allowExec:      options.AllowExec,
		allowTrustedJS: options.AllowTrustedJS,
		serviceKey:     append([]byte(nil), options.ServiceKey...),
		sealer:         options.Sealer,
		registry:       options.Permissions,
		authorizer:     options.Authorizer,
		grants:         options.GrantStore,
		runner:         options.Runner,
		pluginDB:       options.PluginDB,
		coreSDKURL:     strings.TrimRight(options.CoreSDKURL, "/"),
		iframeOrigin:   strings.TrimRight(options.IframeOrigin, "/"),
		assetOrigin:    strings.TrimRight(options.AssetOrigin, "/"),
		platformOrigin: strings.TrimRight(options.PlatformOrigin, "/"),
		httpTimeout:    options.HTTPTimeout,
		maxBody:        options.MaxBodyBytes,
		nats:           options.NATS,
		outbox:         options.Outbox,
		publisher:      options.Publisher,
		eventRegistry:  options.EventRegistry,
		logger:         options.Logger,
		clock:          options.Clock,
		runtime:        make(map[ID]*runtimeBackend),
	}
	if manager.outbox == nil {
		manager.outbox = events.NewOutbox(options.DB)
	}
	if manager.nats != nil {
		manager.publisher = events.NewNATSPublisher(options.NATS, events.WithNATSPublisherRegistry(options.EventRegistry))
	}
	issuer, err := NewDelegationIssuer(options.DB, options.ServiceKey, WithDelegationClock(options.Clock))
	if err != nil {
		return nil, err
	}
	manager.issuer = issuer
	if manager.nats != nil {
		ensureCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := events.EnsureStream(ensureCtx, options.NATS); err != nil {
			return nil, err
		}
	}
	if err := manager.loadRegisteredPermissions(context.Background()); err != nil {
		return nil, err
	}
	return manager, nil
}

// NewService returns the frozen Service view of a manager. Boot code that also
// needs grant/event administration should retain the *Manager.
func NewService(options ManagerOptions) (Service, error) {
	manager, err := NewManager(options)
	if err != nil {
		return nil, err
	}
	return manager, nil
}

var _ Service = (*Manager)(nil)

func sameOrigin(left, right string) bool {
	parse := func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	}
	return parse(left) != "" && parse(left) == parse(right)
}

func (m *Manager) now() time.Time {
	if m == nil || m.clock == nil {
		return time.Now().UTC()
	}
	return m.clock().UTC()
}

// loadRegisteredPermissions rebuilds the trusted plugin permission catalog from
// the registry table. It never grants anything.
func (m *Manager) loadRegisteredPermissions(ctx context.Context) error {
	rows, err := m.db.Pool.Query(ctx, `
		SELECT plugin_id, permission, scopes, description
		FROM plugin_permissions
		ORDER BY plugin_id, permission
	`)
	if err != nil {
		if isUndefinedTable(err) {
			return errors.New("plugins: migration 010_plugins.sql has not been applied")
		}
		return fmt.Errorf("plugins: load plugin permissions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pluginID, permission, description string
		var scopes []string
		if err := rows.Scan(&pluginID, &permission, &scopes, &description); err != nil {
			return err
		}
		definitions := make([]authorization.PermissionDefinition, 0, 1)
		definitions = append(definitions, authorization.PermissionDefinition{
			Permission:  authorization.Permission(permission),
			Source:      pluginID,
			Scopes:      permissionScopes(scopes),
			Description: description,
		})
		if err := authorization.RegisterPluginPermissions(m.registry, pluginID, definitions); err != nil {
			// An identical reload is idempotent inside Registry.Register; any
			// conflict must stop boot rather than silently using stale scopes.
			return err
		}
	}
	return rows.Err()
}

func permissionScopes(values []string) []authorization.PermissionScope {
	scopes := make([]authorization.PermissionScope, 0, len(values))
	for _, value := range values {
		scopes = append(scopes, authorization.PermissionScope(value))
	}
	return scopes
}

// requireManage enforces the core global plugins.manage permission.
func (m *Manager) requireManage(ctx context.Context, principal authorization.Principal) error {
	if principal.UserID == uuid.Nil {
		return ErrPermissionDenied
	}
	decision, err := m.authorizer.Authorize(ctx, principal, authorization.PermissionPluginsManage, authorization.Scope{})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	if !decision.Allowed {
		return ErrPermissionDenied
	}
	return nil
}

// List returns every installed plugin for a global plugins.manage holder.
func (m *Manager) List(ctx context.Context, principal authorization.Principal) ([]Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	rows, err := m.db.Pool.Query(ctx, `
		SELECT id, version, status, manifest, trusted_js_approved, installed_at, updated_at
		FROM plugins
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("plugins: list: %w", err)
	}
	defer rows.Close()
	result := make([]Installed, 0)
	for rows.Next() {
		installed, err := scanInstalled(rows)
		if err != nil {
			return nil, err
		}
		if runtime := m.runtimeFor(installed.ID); runtime != nil {
			installed.Health = runtime.healthSnapshot()
		}
		result = append(result, installed)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Get returns one plugin with a live, bounded health probe for enabled plugins.
func (m *Manager) Get(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	installed, err := m.loadInstalled(ctx, id)
	if err != nil {
		return nil, err
	}
	if installed.Status == StatusEnabled {
		if health, ok := m.probeHealth(ctx, id); ok {
			installed.Health = health
		}
	}
	return installed, nil
}

func (m *Manager) loadInstalled(ctx context.Context, id ID) (*Installed, error) {
	row := m.db.Pool.QueryRow(ctx, `
		SELECT id, version, status, manifest, trusted_js_approved, installed_at, updated_at
		FROM plugins
		WHERE id = $1
	`, string(id))
	installed, err := scanInstalled(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if runtime := m.runtimeFor(id); runtime != nil {
		installed.Health = runtime.healthSnapshot()
	}
	return &installed, nil
}

func scanInstalled(row interface {
	Scan(dest ...any) error
}) (Installed, error) {
	var installed Installed
	var rawStatus string
	var manifestRaw []byte
	if err := row.Scan(&installed.ID, &installed.Version, &rawStatus, &manifestRaw, &installed.TrustedJSApproved, &installed.InstalledAt, &installed.UpdatedAt); err != nil {
		return Installed{}, err
	}
	installed.Status = Status(rawStatus)
	if err := json.Unmarshal(manifestRaw, &installed.Manifest); err != nil {
		return Installed{}, fmt.Errorf("plugins: decode stored manifest: %w", err)
	}
	installed.Health = Health{Status: "unknown", CheckedAt: installed.UpdatedAt}
	return installed, nil
}

func (m *Manager) runtimeFor(id ID) *runtimeBackend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtime[id]
}

func (r *runtimeBackend) healthSnapshot() Health {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	return r.health
}

func (r *runtimeBackend) setHealth(health Health) {
	r.healthMu.Lock()
	r.health = health
	r.healthMu.Unlock()
}

// Visible returns only enabled, compatible plugin metadata that the caller can
// actually use. A plugin permission is never shown merely because the caller is
// an admin: an explicit live grant is required even for global admins.
func (m *Manager) Visible(ctx context.Context, principal authorization.Principal) ([]Visible, error) {
	if principal.UserID == uuid.Nil {
		return nil, ErrPermissionDenied
	}
	rows, err := m.db.Pool.Query(ctx, `
		SELECT id, version, status, manifest, trusted_js_approved, installed_at, updated_at
		FROM plugins
		WHERE status = 'enabled'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("plugins: list visible: %w", err)
	}
	defer rows.Close()

	result := make([]Visible, 0)
	for rows.Next() {
		installed, err := scanInstalled(rows)
		if err != nil {
			return nil, err
		}
		if installed.Manifest.APIVersion != PluginAPIVersion {
			continue
		}
		trusted, trustedPages, err := m.trustedVisible(ctx, installed, principal)
		if err != nil {
			return nil, err
		}
		if trusted != nil {
			result = append(result, Visible{
				ID: installed.ID, Name: installed.Manifest.Name,
				Menu: append([]VisibleItem(nil), trustedPages...), Pages: trustedPages,
				TrustedJS: trusted,
			})
			continue
		}
		pages := make([]VisibleItem, 0)
		for _, route := range installed.Manifest.Routes {
			if !route.UI || route.Method != "GET" {
				continue
			}
			granted, err := m.hasAnyActiveGrant(ctx, principal.UserID, installed.ID, route.Permission)
			if err != nil {
				return nil, err
			}
			if !granted {
				continue
			}
			iframeURL, err := m.assetURL(installed.Manifest, route)
			if err != nil {
				m.logger.Warn("skipping plugin page with invalid entry", "plugin", installed.ID, "error", err)
				continue
			}
			pages = append(pages, VisibleItem{
				Path:      "/plugins/" + string(installed.ID) + route.Path,
				Label:     routeLabel(installed.Manifest, route),
				IframeURL: iframeURL,
			})
		}
		if len(pages) == 0 {
			continue
		}
		result = append(result, Visible{
			ID:    installed.ID,
			Name:  installed.Manifest.Name,
			Menu:  append([]VisibleItem(nil), pages...),
			Pages: pages,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func routeLabel(manifest Manifest, route Route) string {
	label := manifest.Name
	if route.Path != "" {
		segments := strings.Split(strings.Trim(route.Path, "/"), "/")
		if len(segments) > 0 {
			label = manifest.Name + " · " + segments[len(segments)-1]
		}
	}
	if len(label) > 128 {
		label = label[:128]
	}
	return label
}

func (m *Manager) assetURL(manifest Manifest, route Route) (string, error) {
	if manifest.Frontend.Mode != FrontendIframe || manifest.Frontend.Entry == "" {
		return "", errors.New("plugin does not expose an iframe frontend")
	}
	entry := filepath.ToSlash(filepath.Clean(manifest.Frontend.Entry))
	if strings.HasPrefix(entry, "../") || strings.Contains(entry, "..") {
		return "", errors.New("invalid frontend entry")
	}
	base := m.assetOrigin + "/plugins/" + string(manifest.ID) + "/assets/" + entry
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func (m *Manager) hasAnyActiveGrant(ctx context.Context, userID uuid.UUID, pluginID ID, permission authorization.Permission) (bool, error) {
	if !strings.HasPrefix(string(permission), "plugin."+string(pluginID)+".") {
		return false, nil
	}
	var exists bool
	err := m.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM permission_grants
			WHERE user_id = $1 AND permission = $2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > clock_timestamp())
		)
	`, userID, string(permission)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("plugins: check visible grant: %w", err)
	}
	return exists, nil
}

func uuidFromPG(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}
