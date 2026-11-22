package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	accesshttp "github.com/nrf24l01/actfb/internal/access/http"
	"github.com/nrf24l01/actfb/internal/attackdefense"
	attackdefensehttp "github.com/nrf24l01/actfb/internal/attackdefense/http"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/deployment"
	deploymenthttp "github.com/nrf24l01/actfb/internal/deployment/http"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/files"
	fileshttp "github.com/nrf24l01/actfb/internal/files/http"
	"github.com/nrf24l01/actfb/internal/instances"
	instanceshttp "github.com/nrf24l01/actfb/internal/instances/http"
	"github.com/nrf24l01/actfb/internal/integrations"
	integrationshttp "github.com/nrf24l01/actfb/internal/integrations/http"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/plugins"
	"github.com/nrf24l01/actfb/internal/plugins/assets"
	pluginshttp "github.com/nrf24l01/actfb/internal/plugins/http"
	"github.com/nrf24l01/actfb/internal/plugins/process"
	pluginsdk "github.com/nrf24l01/actfb/internal/plugins/sdk"
	"github.com/nrf24l01/actfb/internal/runtime"
	runtimedocker "github.com/nrf24l01/actfb/internal/runtime/docker"
	runtimekubernetes "github.com/nrf24l01/actfb/internal/runtime/kubernetes"
	runtimekubevirt "github.com/nrf24l01/actfb/internal/runtime/kubevirt"
	runtimepodman "github.com/nrf24l01/actfb/internal/runtime/podman"
	runtimeproxmox "github.com/nrf24l01/actfb/internal/runtime/proxmox"
	"github.com/nrf24l01/actfb/internal/site"
	sitehttp "github.com/nrf24l01/actfb/internal/site/http"
	"github.com/nrf24l01/actfb/internal/users"
)

// extendedServices carries the optional feature adapters that are enabled by an
// explicit operator flag. Nil always means "feature disabled"; no disabled
// feature advertises a route that would return a fake success.
type extendedServices struct {
	access        *access.AccessService
	files         *files.FileService
	deployment    *deployment.Service
	integrations  *integrations.Service
	plugins       *plugins.Manager
	pluginSDK     http.Handler
	attackDefense *attackdefense.Service
	instances     *instances.Service
	site          *site.Service
}

// registerExtendedRoutes installs every ready /api/v2 HTTP adapter. It is
// intentionally additive: the existing P0/P1 routers are untouched.
func registerExtendedRoutes(e *echo.Echo, services extendedServices, userService *users.UserService) {
	if services.access != nil {
		accesshttp.NewHandler(services.access, userService).RegisterRoutes(e)
	}
	if services.files != nil {
		fileshttp.NewHandler(services.files, userService).RegisterRoutes(e)
	}
	if services.deployment != nil {
		deploymenthttp.NewHandler(services.deployment, userService).RegisterRoutes(e)
	}
	if services.integrations != nil {
		integrationshttp.NewHandler(services.integrations, userService).RegisterRoutes(e)
	}
	if services.plugins != nil {
		pluginshttp.NewHandler(services.plugins, userService).RegisterRoutes(e)
	}
	if services.pluginSDK != nil {
		handler := http.StripPrefix("/api/v2/plugin-sdk/v1", services.pluginSDK)
		e.POST("/api/v2/plugin-sdk/v1/authorize", echo.WrapHandler(handler))
		e.POST("/api/v2/plugin-sdk/v1/board-context", echo.WrapHandler(handler))
		e.POST("/api/v2/plugin-sdk/v1/events", echo.WrapHandler(handler))
	}
	if services.attackDefense != nil {
		attackdefensehttp.NewHandler(services.attackDefense, userService).RegisterRoutes(e)
	}
	if services.instances != nil {
		instanceshttp.NewHandler(services.instances, userService).RegisterRoutes(e)
	}
	if services.site != nil {
		sitehttp.NewHandler(services.site, userService).RegisterRoutes(e)
	}
}

// buildRuntimeRegistry registers all five trusted static adapters. Capabilities
// still perform their own live provider checks; this only builds metadata.
func buildRuntimeRegistry(logger *slog.Logger) (*runtime.DriverRegistry, error) {
	drivers := make([]runtime.Driver, 0, 5)
	dockerDriver, err := runtimedocker.New()
	if err != nil {
		return nil, fmt.Errorf("docker driver: %w", err)
	}
	podmanDriver, err := runtimepodman.New()
	if err != nil {
		return nil, fmt.Errorf("podman driver: %w", err)
	}
	drivers = append(drivers, dockerDriver, podmanDriver, runtimeproxmox.New(), runtimekubernetes.New(), runtimekubevirt.New())

	registry, err := runtime.NewRegistry(drivers...)
	if err != nil {
		return nil, err
	}
	if logger != nil {
		logger.Info("runtime driver registry initialized", "drivers", len(drivers))
	}
	return registry, nil
}

// buildPluginManager constructs the plugin supervisor. It never starts plugin
// code and never receives a Docker socket; OCI execution is delegated to the
// optional loopback plugin-runner control URL.
func buildPluginManager(
	cfg *config.Config,
	db *postgres.DB,
	registry authorization.PermissionRegistry,
	authorizer authorization.Authorizer,
	grantStore *authorization.GrantStore,
	sealer secrets.Sealer,
	sharedNATS *platformnats.Client,
	outbox events.Outbox,
	logger *slog.Logger,
) (*plugins.Manager, error) {
	if sealer == nil {
		return nil, errors.New("deployment.credential_key (legacy ACTFB_CREDENTIAL_ENCRYPTION_KEY) is required when plugins are enabled")
	}
	serviceKey, err := decodeBase64Key(cfg.Plugins.ServiceKey)
	if err != nil {
		return nil, fmt.Errorf("decode plugins.service_key (legacy ACTFB_PLUGIN_SERVICE_KEY): %w", err)
	}
	platformOrigin := platformOrigin(cfg)
	if platformOrigin == "" {
		return nil, errors.New("web_app.allowed_origins must contain the platform origin when plugins are enabled")
	}

	var runner process.Runner
	runnerURL := strings.TrimSpace(cfg.Plugins.RunnerURL)
	runnerToken := strings.TrimSpace(cfg.Plugins.RunnerToken)
	if runnerURL != "" || runnerToken != "" {
		if runnerURL == "" || runnerToken == "" {
			return nil, errors.New("plugins.runner_url and plugins.runner_token must be set together")
		}
		remote, err := process.NewRemoteRunner(runnerURL, runnerToken)
		if err != nil {
			return nil, fmt.Errorf("initialize plugin runner client: %w", err)
		}
		runner = remote
	}

	manager, err := plugins.NewManager(plugins.ManagerOptions{
		DB:             db,
		Root:           cfg.Plugins.Root,
		ServiceKey:     serviceKey,
		Sealer:         sealer,
		Permissions:    registry,
		Authorizer:     authorizer,
		GrantStore:     grantStore,
		Runner:         runner,
		AllowExec:      cfg.Plugins.AllowExec,
		AllowTrustedJS: cfg.Plugins.AllowTrustedJS,
		IframeOrigin:   cfg.Plugins.IframeOrigin,
		AssetOrigin:    cfg.Plugins.IframeOrigin,
		PlatformOrigin: platformOrigin,
		CoreSDKURL:     strings.TrimSpace(cfg.Plugins.CoreSDKURL),
		HTTPTimeout:    cfg.Plugins.HTTPTimeout,
		MaxBodyBytes:   cfg.Plugins.MaxBodyBytes,
		NATS:           sharedNATS,
		Outbox:         outbox,
		PluginDB: plugins.PluginDatabaseOptions{
			ProvisionDSN: postgresProvisionDSN(cfg.Datasources.Postgres),
			Host:         cfg.Datasources.Postgres.Host,
			Port:         cfg.Datasources.Postgres.Port,
			Name:         cfg.Datasources.Postgres.DBName,
			SSLMode:      cfg.Datasources.Postgres.SSLMode,
		},
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	return manager, nil
}

// buildPluginSDK mounts the service-token + delegation protected SDK. The
// browser never receives these credentials.
func buildPluginSDK(manager *plugins.Manager) (http.Handler, error) {
	if manager == nil {
		return nil, nil
	}
	proxy, err := manager.NewCoreProxy()
	if err != nil {
		return nil, fmt.Errorf("initialize plugin core proxy: %w", err)
	}
	server, err := pluginsdk.NewServer(manager, proxy)
	if err != nil {
		return nil, fmt.Errorf("initialize plugin sdk: %w", err)
	}
	return server.Handler(), nil
}

// buildPluginAssets constructs the separate asset-origin listener. The listener
// is bound only to the configured loopback asset address; core API HTML is never
// served from the platform origin.
func buildPluginAssets(manager *plugins.Manager, cfg *config.Config, logger *slog.Logger) (*assets.Server, error) {
	platform := platformOrigin(cfg)
	if platform == "" {
		return nil, errors.New("web_app.allowed_origins must contain the platform origin when plugins are enabled")
	}
	return assets.NewServer(assets.Options{
		Manager:        manager,
		Origin:         cfg.Plugins.IframeOrigin,
		ListenAddress:  cfg.Plugins.AssetsListenAddress,
		PlatformOrigin: platform,
		Logger:         logger,
	})
}

// buildAttackDefense constructs the HTTP-facing A+D service. Checker and Git
// fetcher stay in cmd/ad-worker; the backend only queues ids-only jobs.
func buildAttackDefense(
	cfg *config.Config,
	db *postgres.DB,
	authorizer authorization.Authorizer,
	sealer secrets.Sealer,
	sharedNATS *platformnats.Client,
	logger *slog.Logger,
) (*attackdefense.Service, error) {
	if sealer == nil {
		return nil, errors.New("deployment.credential_key (legacy ACTFB_CREDENTIAL_ENCRYPTION_KEY) is required when attack-defense is enabled")
	}
	if sharedNATS == nil {
		return nil, errors.New("NATS is required when attack-defense is enabled")
	}
	publisher, err := attackdefense.NewNATSJobPublisher(sharedNATS)
	if err != nil {
		return nil, fmt.Errorf("initialize attack-defense job publisher: %w", err)
	}
	options := []attackdefense.Option{
		attackdefense.WithJobPublisher(publisher),
		attackdefense.WithAllowInsecureWebhookTransport(cfg.AttackDefense.AllowInsecureLoopback),
		attackdefense.WithKeyVersion(cfg.Deployment.KeyVersion),
		attackdefense.WithLogger(logger),
	}
	if images := cfg.AttackDefense.RunnerImages; len(images) > 0 {
		options = append(options, attackdefense.WithRunnerImageAllowlist(images))
	}
	if baseURL := strings.TrimSpace(cfg.AttackDefense.WebhookBaseURL); baseURL != "" {
		options = append(options, attackdefense.WithWebhookBaseURL(baseURL))
	}
	service, err := attackdefense.NewService(db, authorizer, sealer, options...)
	if err != nil {
		return nil, fmt.Errorf("initialize attack-defense service: %w", err)
	}
	return service, nil
}

// platformOrigin returns the first browser-facing allowed origin. It never
// invents a public hostname and never uses 0.0.0.0 as a browser origin.
func platformOrigin(cfg *config.Config) string {
	for _, origin := range cfg.WebApp.AllowedOrigins {
		trimmed := strings.TrimRight(strings.TrimSpace(origin), "/")
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// decodeBase64Key decodes an operator-provided base64 32-byte key.
func decodeBase64Key(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, errors.New("key is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, err
		}
	}
	if len(decoded) != 32 {
		return nil, errors.New("key must decode to exactly 32 bytes")
	}
	return decoded, nil
}

// envBool reads an explicit opt-in flag. Missing or malformed values are false
// so a typo can never enable a privileged feature.
func envBool(name string, fallback bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off", "":
		return false
	default:
		return fallback
	}
}

// adScoreAdapter maps the A+D read-only award adapter onto the boards score
// seam. It never writes a score row; boards owns the canonical total.
type adScoreAdapter struct {
	source attackdefense.ScoreSourceAdapter
}

func (a adScoreAdapter) ListBoardScores(ctx context.Context, boardID uuid.UUID) ([]boards.ExternalSubjectScore, error) {
	scores, err := a.source.ListBoardScores(ctx, boardID)
	if err != nil {
		return nil, err
	}
	result := make([]boards.ExternalSubjectScore, 0, len(scores))
	for _, score := range scores {
		result = append(result, boards.ExternalSubjectScore{
			SubjectType: score.SubjectType,
			SubjectID:   score.SubjectID,
			Name:        score.Name,
			Points:      score.Points,
		})
	}
	return result, nil
}

func (a adScoreAdapter) SubjectScore(ctx context.Context, boardID uuid.UUID, subjectType string, subjectID uuid.UUID) (int, error) {
	return a.source.SubjectScore(ctx, boardID, subjectType, subjectID)
}

// instanceBinderAdapter bridges boards' INSTANCE task creation to the trusted
// instances binder. The immutable template version and allowlist are checked
// by instances, not by the board HTTP handler.
type instanceBinderAdapter struct {
	service *instances.Service
}

func (a instanceBinderAdapter) BindTaskInstance(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID, taskID uuid.UUID,
	input boards.InstanceBindingInput,
) (uuid.UUID, error) {
	task := taskID
	result, err := a.service.BindInstance(ctx, principal, instances.BindInput{
		BoardID:          boardID,
		ModuleID:         moduleID,
		TaskID:           &task,
		Scope:            instances.ScopeTask,
		TemplateID:       input.TemplateID,
		TemplateVersion:  input.TemplateVersion,
		Parameters:       input.Parameters,
		SecretParameters: input.SecretParameters,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return result.BindingID, nil
}

// postgresProvisionDSN builds the bootstrap DSN used only to create/rotate the
// restricted per-plugin role and private schema. It is never given to a
// plugin; buildPluginDSN replaces the credentials with the plugin role.
func postgresProvisionDSN(cfg config.PostgresConfig) string {
	values := url.Values{}
	if strings.TrimSpace(cfg.SSLMode) != "" {
		values.Set("sslmode", cfg.SSLMode)
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.DBName,
		RawQuery: values.Encode(),
	}).String()
}

// envList reads a comma-separated allowlist.
func envList(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	values := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}
