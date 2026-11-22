package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/admincli"
	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/files"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/instances"
	"github.com/nrf24l01/actfb/internal/integrations"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	platformredis "github.com/nrf24l01/actfb/internal/platform/redis"
	platforms3 "github.com/nrf24l01/actfb/internal/platform/s3"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/plugins"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/site"
	"github.com/nrf24l01/actfb/internal/users"
	usersnats "github.com/nrf24l01/actfb/internal/users/nats"
)

const (
	startupTimeout      = 30 * time.Second
	shutdownTimeout     = 10 * time.Second
	natsStreamName      = "EMAIL_TASKS"
	natsEmailTask       = "email.tasks"
	authRateLimitPrefix = "actfb:auth:ratelimit:v1:"
)

type mailRuntime struct {
	natsClient *platformnats.Client
	publisher  *usersnats.EmailTaskPublisher
}

type mailDeps struct {
	newClient    func(config.NatsConfig) (*platformnats.Client, error)
	ensureStream func(context.Context, *platformnats.Client) error
	newPublisher func(*platformnats.Client) *usersnats.EmailTaskPublisher
}

func main() {
	// Operator CLI commands dispatch before config validation, HTTP bootstrap,
	// migrations, SMTP/NATS/Redis/S3, or plugin initialization. A bare
	// /app/backend invocation is unchanged and still starts the server.
	if len(os.Args) > 1 {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := admincli.NewRunner().Run(ctx, os.Args[1:])
		stop()
		os.Exit(code)
	}

	logger := newLogger("info")
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("backend stopped with error", "error", err)
		os.Exit(1)
	}

	logger.Info("backend stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger = newLogger(cfg.Logging.Level)
	slog.SetDefault(logger)
	logger.Info("configuration loaded", "config", cfg.String())

	if err := cfg.ValidateBackend(); err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	db, err := postgres.NewPostgresClient(startupCtx, &cfg.Datasources.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Error("close postgres", "error", closeErr)
		}
	}()
	logger.Info("postgres connected")

	if err := postgres.RunMigrations(startupCtx, db.SQL, &cfg.Datasources.Postgres); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("migrations applied", "path", cfg.Datasources.Postgres.MigrationsPath)

	mail, err := initializeMailRuntime(startupCtx, cfg, logger, defaultMailDeps())
	if err != nil {
		return err
	}
	if mail.natsClient != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			if closeErr := mail.natsClient.Close(closeCtx); closeErr != nil {
				logger.Error("close nats", "error", closeErr)
			}
		}()
	}

	userService := users.NewUserService(db, cfg, mail.publisher)
	userService.SetLogger(logger)

	authLimiter, redisClient, err := newAuthLimiter(cfg)
	if err != nil {
		return err
	}
	if redisClient != nil {
		defer func() {
			if closeErr := redisClient.Close(); closeErr != nil {
				logger.Error("close redis", "error", closeErr)
			}
		}()
		logger.Info("auth rate limiter enabled", "backend", config.RateLimitBackendRedis)
	} else {
		logger.Info("auth rate limiter enabled", "backend", config.RateLimitBackendMemory)
	}

	registry := authorization.NewRegistry()
	grantStore := authorization.NewGrantStore(db, authorization.WithGrantRegistry(registry))
	authorizer := authorization.NewAuthorizer(
		db,
		authorization.WithPermissionRegistry(registry),
		authorization.WithGrantChecker(grantStore),
	)
	roleService := authorization.NewRoleService(db, authorizer)
	accessService := access.NewService(db, authorizer, access.WithArgon2(cfg.Argon2id))

	var sealer secrets.Sealer
	if strings.TrimSpace(cfg.Deployment.CredentialKey) != "" {
		cipher, err := secrets.NewCipher(cfg.Deployment.CredentialKey, cfg.Deployment.KeyVersion)
		if err != nil {
			return fmt.Errorf("initialize credential sealer: %w", err)
		}
		sealer = cipher
	}

	adEnabled := cfg.AttackDefense.Enabled
	integrationsEnabled := cfg.Integrations.Enabled

	// Any feature that needs the shared bus must initialize NATS even when SMTP
	// mail is disabled. This avoids the historical "SMTP-only" bootstrap bug.
	needsSharedNATS := cfg.Files.Enabled || cfg.Plugins.Enabled || cfg.Deployment.Enabled || adEnabled || integrationsEnabled
	sharedNATS := mail.natsClient
	ownsSharedNATS := false
	if needsSharedNATS && sharedNATS == nil {
		sharedNATS, err = platformnats.New(cfg.Datasources.Nats)
		if err != nil {
			return fmt.Errorf("connect shared nats: %w", err)
		}
		ownsSharedNATS = true
	}
	if ownsSharedNATS {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if closeErr := sharedNATS.Close(closeCtx); closeErr != nil {
				logger.Error("close shared nats", "error", closeErr)
			}
		}()
	}

	var fileService *files.FileService
	if cfg.Files.Enabled {
		if err := files.EnsureScanStream(startupCtx, sharedNATS); err != nil {
			return fmt.Errorf("ensure file scan stream: %w", err)
		}
		scanKey, err := files.DecodeScanKey(cfg.Files.ScanKey)
		if err != nil {
			return err
		}
		s3Client, err := platforms3.New(startupCtx, cfg.Datasources.S3)
		if err != nil {
			return fmt.Errorf("connect S3 for files: %w", err)
		}
		if err := s3Client.Check(startupCtx); err != nil {
			return fmt.Errorf("check S3 bucket for files: %w", err)
		}
		fileService, err = files.NewService(
			db,
			s3Client,
			authorizer,
			files.WithNATS(sharedNATS),
			files.WithScanKey(scanKey),
			files.WithLogger(logger),
		)
		if err != nil {
			return fmt.Errorf("initialize files service: %w", err)
		}
	}

	var runtimeRegistry *runtime.DriverRegistry
	if cfg.Deployment.Enabled || integrationsEnabled {
		runtimeRegistry, err = buildRuntimeRegistry(logger)
		if err != nil {
			return fmt.Errorf("initialize runtime driver registry: %w", err)
		}
	}

	var deploymentService *deployment.Service
	if cfg.Deployment.Enabled {
		deploymentService, err = deployment.NewService(
			db,
			authorizer,
			sealer,
			deployment.WithDriverMetadataProvider(runtimeRegistry),
			deployment.WithLogger(logger),
		)
		if err != nil {
			return fmt.Errorf("initialize deployment templates: %w", err)
		}
	}

	var instancesService *instances.Service
	if cfg.Deployment.Enabled {
		if sealer == nil {
			return errors.New("deployment.credential_key (legacy ACTFB_CREDENTIAL_ENCRYPTION_KEY) is required when runtime instances are enabled")
		}
		if sharedNATS == nil {
			return errors.New("NATS is required when runtime instances are enabled")
		}
		if _, err := sharedNATS.CreateOrUpdateStream(startupCtx, jetstream.StreamConfig{
			Name:     instances.JobStream,
			Subjects: []string{instances.JobSubject},
			Storage:  jetstream.FileStorage,
		}); err != nil {
			return fmt.Errorf("ensure instance job stream: %w", err)
		}
		instanceOptions := []instances.Option{
			instances.WithAccessPolicy(accessService),
			instances.WithPublisher(instances.NewNATSJobPublisher(sharedNATS)),
			instances.WithLogger(logger),
		}
		if fileService != nil {
			instanceOptions = append(instanceOptions, instances.WithDraftVerifier(fileService))
		}
		instancesService, err = instances.NewService(
			db,
			authorizer,
			runtimeRegistry,
			sealer,
			cfg.Deployment.KeyVersion,
			instanceOptions...,
		)
		if err != nil {
			return fmt.Errorf("initialize runtime instances: %w", err)
		}
	}

	outbox := events.NewOutbox(db)

	var pluginManager *plugins.Manager
	var pluginSDK http.Handler
	if cfg.Plugins.Enabled {
		pluginManager, err = buildPluginManager(
			cfg, db, registry, authorizer, grantStore, sealer, sharedNATS, outbox, logger,
		)
		if err != nil {
			return fmt.Errorf("initialize plugin manager: %w", err)
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if closeErr := pluginManager.Close(closeCtx); closeErr != nil {
				logger.Error("plugin runtime shutdown failed", "error", closeErr)
			}
		}()
		pluginSDK, err = buildPluginSDK(pluginManager)
		if err != nil {
			return err
		}
		assetsServer, assetsErr := buildPluginAssets(pluginManager, cfg, logger)
		if assetsErr != nil {
			return fmt.Errorf("initialize plugin assets listener: %w", assetsErr)
		}
		go func() {
			logger.Info("plugin assets listener started",
				"origin", cfg.Plugins.IframeOrigin,
				"listen", cfg.Plugins.AssetsListenAddress,
			)
			if serveErr := assetsServer.ListenAndServe(); serveErr != nil {
				logger.Error("plugin assets listener stopped", "error", serveErr)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if shutdownErr := assetsServer.Shutdown(shutdownCtx); shutdownErr != nil {
				logger.Error("plugin assets listener shutdown failed", "error", shutdownErr)
			}
		}()
		for _, recoveryErr := range pluginManager.RecoverEnabled(ctx) {
			logger.Error("plugin recovery failed", "error", recoveryErr)
		}
	}

	var integrationsService *integrations.Service
	if integrationsEnabled {
		if sealer == nil {
			return errors.New("deployment.credential_key (legacy ACTFB_CREDENTIAL_ENCRYPTION_KEY) is required when integrations are enabled")
		}
		if sharedNATS == nil {
			return errors.New("NATS is required when integrations are enabled")
		}
		integrationsService = integrations.NewService(
			db,
			authorizer,
			integrations.WithSealer(sealer, cfg.Deployment.KeyVersion),
			integrations.WithConnectionTester(integrations.NewNATSTester(sharedNATS)),
			integrations.WithRuntime(runtimeRegistry),
		)
	}

	var attackDefenseService *attackdefense.Service
	var adScoreSource attackdefense.ScoreSourceAdapter
	if adEnabled {
		if err := attackdefense.EnsureJobStream(startupCtx, sharedNATS); err != nil {
			return fmt.Errorf("ensure attack-defense job stream: %w", err)
		}
		attackDefenseService, err = buildAttackDefense(cfg, db, authorizer, sealer, sharedNATS, logger)
		if err != nil {
			return fmt.Errorf("initialize attack-defense: %w", err)
		}
		provider := attackdefense.NewModuleProvider(attackDefenseService)
		adScoreSource, err = provider.ScoreSource()
		if err != nil {
			return fmt.Errorf("initialize attack-defense score source: %w", err)
		}
		logger.Info("attack-defense module provider registered",
			"key", provider.Info().Key,
			"moduleType", provider.Info().ModuleType,
			"taskKinds", strings.Join(provider.Info().AllowedTaskKinds, ","),
		)
	}

	boardStore := boards.NewPostgresStore(db)
	boardOptions := []boards.Option{
		boards.WithArgon2Config(cfg.Argon2id),
		boards.WithIdempotencySecret(cfg.JWT.Refresh.Secret),
		boards.WithAccessPolicy(accessService),
		boards.WithAccessService(accessService),
		boards.WithOutbox(outbox),
		boards.WithLogger(logger),
	}
	if sealer != nil {
		boardOptions = append(boardOptions, boards.WithSealer(sealer))
	}
	if fileService != nil {
		boardOptions = append(boardOptions, boards.WithFileService(fileService))
	}
	if adScoreSource != nil {
		boardOptions = append(boardOptions, boards.WithScoreSource(adScoreAdapter{source: adScoreSource}))
	}
	if instancesService != nil {
		boardOptions = append(boardOptions, boards.WithSubmissionSource(instances.NewExternalSubmissionSource(instancesService)))
		boardOptions = append(boardOptions, boards.WithInstanceBinder(instanceBinderAdapter{service: instancesService}))
	}
	if pluginManager != nil {
		boardOptions = append(boardOptions, boards.WithPluginChecker(pluginCheckerAdapter{manager: pluginManager}))
	}
	boardService := boards.NewService(
		boardStore,
		authorizer,
		boards.SystemClock{},
		authLimiter,
		boardOptions...,
	)

	ready := readinessCheck(db, sharedNATS, redisClient)
	router := httpapi.NewRouter(logger, ready, cfg.WebApp.AllowedOrigins...)
	if err := httpapi.ConfigureTrustedProxies(router, cfg.WebApp.TrustedProxies); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}
	registerUserRoutes(router, userService, cfg, authLimiter)
	registerBoardRoutes(router, boardService, roleService, authorizer, grantStore, userService, fileService == nil)
	registerExtendedRoutes(router, extendedServices{
		access:        accessService,
		files:         fileService,
		deployment:    deploymentService,
		integrations:  integrationsService,
		plugins:       pluginManager,
		pluginSDK:     pluginSDK,
		attackDefense: attackDefenseService,
		instances:     instancesService,
		site:          site.NewService(db),
	}, userService)
	registerAdminAuditRoute(router, db, authorizer, userService)

	address := net.JoinHostPort(cfg.WebApp.AppHost, strconv.Itoa(cfg.WebApp.Port))
	startConfig := echo.StartConfig{
		Address:         address,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: shutdownTimeout,
		OnShutdownError: func(shutdownErr error) {
			logger.Error("graceful shutdown failed", "error", shutdownErr)
		},
		BeforeServeFunc: func(server *http.Server) error {
			server.ReadHeaderTimeout = 10 * time.Second
			server.IdleTimeout = 60 * time.Second
			return nil
		},
	}

	logger.Info("starting backend", "address", address)
	if serveErr := startConfig.Start(ctx, router); serveErr != nil && !errors.Is(serveErr, context.Canceled) {
		return fmt.Errorf("serve http: %w", serveErr)
	}

	return nil
}

func defaultMailDeps() mailDeps {
	return mailDeps{
		newClient:    platformnats.New,
		ensureStream: ensureEmailStream,
		newPublisher: usersnats.NewEmailPublisher,
	}
}

// initializeMailRuntime gates NATS and the email publisher on SMTP/email being
// enabled. Verification is only one email type: password-reset mail also needs
// the publisher when EMAIL_VERIFY_ENABLED=false.
func initializeMailRuntime(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	deps mailDeps,
) (*mailRuntime, error) {
	runtime := &mailRuntime{}
	if !cfg.Email.Enabled {
		return runtime, nil
	}
	if deps.newClient == nil || deps.newPublisher == nil {
		return nil, errors.New("mail initializer dependencies are incomplete")
	}
	if logger == nil {
		logger = slog.Default()
	}

	natsClient, err := deps.newClient(cfg.Datasources.Nats)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	if natsClient == nil {
		return nil, errors.New("nats client is nil")
	}
	runtime.natsClient = natsClient

	if deps.ensureStream != nil {
		if err := deps.ensureStream(ctx, natsClient); err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			_ = natsClient.Close(closeCtx)
			cancel()
			return nil, fmt.Errorf("ensure email stream: %w", err)
		}
	}

	runtime.publisher = deps.newPublisher(natsClient)
	logger.Info("email delivery flow enabled", "stream", natsStreamName)

	return runtime, nil
}

func ensureEmailStream(ctx context.Context, natsClient *platformnats.Client) error {
	if natsClient == nil {
		return errors.New("nats client is nil")
	}

	// The notification worker uses the same stream definition. Keep this in
	// sync with internal/notifications/nats/subscriber.go.
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := natsClient.CreateOrUpdateStream(streamCtx, jetstream.StreamConfig{
		Name:     natsStreamName,
		Subjects: []string{natsEmailTask},
		Storage:  jetstream.FileStorage,
	})

	return err
}

func readinessCheck(
	db *postgres.DB,
	natsClient *platformnats.Client,
	redisClient *platformredis.RedisClient,
) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		if err := db.Pool.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}

		if redisClient != nil {
			if err := redisClient.Check(checkCtx); err != nil {
				return fmt.Errorf("redis: %w", err)
			}
		}

		if natsClient != nil {
			if err := natsClient.CheckJetStream(checkCtx); err != nil {
				return fmt.Errorf("nats jetstream: %w", err)
			}
		}

		return nil
	}
}

func newAuthLimiter(cfg *config.Config) (ratelimit.Limiter, *platformredis.RedisClient, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Security.AuthRateLimitBackend))
	if backend == "" {
		backend = config.RateLimitBackendMemory
	}

	switch backend {
	case config.RateLimitBackendRedis:
		client, err := platformredis.NewRedisClient(&cfg.Datasources.Redis)
		if err != nil {
			return nil, nil, fmt.Errorf("connect redis for auth rate limiting: %w", err)
		}
		return ratelimit.NewRedisLimiter(client.Client(), authRateLimitPrefix), client, nil
	case config.RateLimitBackendMemory:
		return ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryMaxKeys), nil, nil
	default:
		return nil, nil, fmt.Errorf(
			"unsupported auth rate limit backend %q",
			cfg.Security.AuthRateLimitBackend,
		)
	}
}

func newLogger(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(level),
	}))
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
