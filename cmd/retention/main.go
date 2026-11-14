package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nrf24l01/actfb/internal/authretention"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	defaultRunTimeout = 5 * time.Minute
	minInterval       = time.Second
	closeTimeout      = 10 * time.Second
)

func main() {
	logger := newLogger("info")
	slog.SetDefault(logger)

	if err := run(context.Background(), logger); err != nil {
		logger.Error("retention worker stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, logger *slog.Logger) (runErr error) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet("retention", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	once := fs.Bool("once", false, "run one bounded retention pass and exit (default is daemon mode)")
	interval := fs.Duration("interval", time.Hour, "delay between passes in daemon mode")
	runTimeout := fs.Duration("run-timeout", defaultRunTimeout, "maximum duration of one retention pass")
	batchSize := fs.Int("batch-size", authretention.DefaultBatchSize, "rows deleted per transaction")
	maxBatches := fs.Int("max-batches", authretention.DefaultMaxBatches, "maximum transactions per table per pass")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *interval < minInterval {
		return fmt.Errorf("--interval must be at least %s", minInterval)
	}
	if *runTimeout < minInterval {
		return fmt.Errorf("--run-timeout must be at least %s", minInterval)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger = newLogger(cfg.Logging.Level)
	slog.SetDefault(logger)

	if err := validatePostgresConfig(&cfg.Datasources.Postgres); err != nil {
		return err
	}

	retentionDays, err := authretention.ResolveRetentionDays(os.Getenv("AUTH_RETENTION_DAYS"))
	if err != nil {
		return err
	}

	options, err := authretention.NormalizeOptions(authretention.Options{
		RetentionDays: retentionDays,
		BatchSize:     *batchSize,
		MaxBatches:    *maxBatches,
	})
	if err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()

	db, err := postgres.NewPostgresClient(startupCtx, &cfg.Datasources.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer func() {
		closed := make(chan error, 1)
		go func() {
			closed <- db.Close()
		}()

		select {
		case closeErr := <-closed:
			if closeErr != nil {
				logger.Error("close postgres", "error", closeErr)
				runErr = errors.Join(runErr, fmt.Errorf("close postgres: %w", closeErr))
			}
		case <-time.After(closeTimeout):
			logger.Error("close postgres timed out", "timeout", closeTimeout.String())
			runErr = errors.Join(runErr, fmt.Errorf("close postgres timed out after %s", closeTimeout))
		}
	}()

	logger.Info("retention worker ready",
		"postgres_host", cfg.Datasources.Postgres.Host,
		"postgres_port", cfg.Datasources.Postgres.Port,
		"postgres_db", cfg.Datasources.Postgres.DBName,
		"retention_days", options.RetentionDays,
		"batch_size", options.BatchSize,
		"max_batches", options.MaxBatches,
		"once", *once,
		"interval", interval.String(),
		"run_timeout", runTimeout.String(),
	)

	execute := func() error {
		runCtx, cancel := context.WithTimeout(ctx, *runTimeout)
		defer cancel()

		result, runErr := authretention.Run(runCtx, db.SQL, options)
		for _, table := range result.Tables {
			logger.Info("retention table pass complete",
				"table", table.Table,
				"deleted", table.Deleted,
				"batches", table.Batches,
				"truncated", table.Truncated,
				"cutoff", result.Cutoff,
			)
		}

		if runErr != nil {
			logger.Error("retention pass failed",
				"error", runErr,
				"deleted_before_error", result.Deleted,
			)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("retention pass: %w", runErr)
		}

		logger.Info("retention pass complete",
			"cutoff", result.Cutoff,
			"retention_days", result.RetentionDays,
			"deleted", result.Deleted,
			"truncated", result.Truncated,
		)
		return nil
	}

	if *once {
		if err := execute(); err != nil {
			if ctx.Err() != nil {
				logger.Info("shutdown signal received", "reason", context.Cause(ctx))
				return nil
			}
			return err
		}
		return nil
	}

	for {
		if err := execute(); err != nil {
			if ctx.Err() != nil {
				logger.Info("shutdown signal received", "reason", context.Cause(ctx))
				return nil
			}
			logger.Error("retention pass failed; will retry", "error", err, "interval", interval.String())
		}

		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received", "reason", context.Cause(ctx))
			return nil
		case <-time.After(*interval):
		}
	}
}

func validatePostgresConfig(cfg *config.PostgresConfig) error {
	if cfg == nil {
		return errors.New("postgres config is nil")
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return errors.New("postgres host is required; set datasources.postgres.host or POSTGRES_HOST")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("postgres port %d is outside the valid range 1..65535", cfg.Port)
	}
	if strings.TrimSpace(cfg.User) == "" {
		return errors.New("postgres user is required; set datasources.postgres.user or POSTGRES_USER")
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		return errors.New("postgres dbname is required; set datasources.postgres.dbname or POSTGRES_DBNAME")
	}
	if strings.TrimSpace(cfg.SSLMode) == "" {
		return errors.New("postgres sslmode is required; set datasources.postgres.sslmode or POSTGRES_SSLMODE")
	}
	if strings.TrimSpace(cfg.Timezone) == "" {
		return errors.New("postgres timezone is required; set datasources.postgres.timezone or POSTGRES_TIMEZONE")
	}
	return nil
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
