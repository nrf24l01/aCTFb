// Command plugin-runner is the separate trusted supervisor for OCI plugin
// backends. The core backend never receives DOCKER_HOST or a Docker socket.
//
// Required configuration:
//
//	plugins.runner_token        shared control token (>= 32 characters)
//
// Optional configuration:
//
//	plugins.runner_listen_address  loopback control address (default 127.0.0.2:8092)
//	plugins.runner_id              stable runner identity
//	ACTFB_DOCKER_SOCKET            Docker API unix socket (default /var/run/docker.sock)
//	ACTFB_PLUGIN_RUNNER_NETWORK    Docker network (default bridge)
//
// Legacy environment overrides for the YAML fields remain supported.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/plugins/process"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(2)
	}
	token := strings.TrimSpace(cfg.Plugins.RunnerToken)
	if len(token) < 32 {
		logger.Error("plugins.runner_token (legacy ACTFB_PLUGIN_RUNNER_TOKEN) must be at least 32 characters")
		os.Exit(2)
	}
	addr := strings.TrimSpace(cfg.Plugins.RunnerListenAddress)
	if addr == "" {
		addr = config.DefaultPluginRunnerListenAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		logger.Error("invalid plugins.runner_listen_address (legacy ACTFB_PLUGIN_RUNNER_ADDR)", "error", err)
		os.Exit(2)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		logger.Error("plugins.runner_listen_address (legacy ACTFB_PLUGIN_RUNNER_ADDR) must bind a loopback address")
		os.Exit(2)
	}

	dockerSocket := strings.TrimSpace(os.Getenv("ACTFB_DOCKER_SOCKET"))
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}
	runnerID := strings.TrimSpace(cfg.Plugins.RunnerID)
	if runnerID == "" {
		runnerID = "default"
	}
	runner, err := process.NewDockerRunner(process.DockerOptions{
		SocketPath:  dockerSocket,
		NetworkMode: os.Getenv("ACTFB_PLUGIN_RUNNER_NETWORK"),
		RunnerID:    runnerID,
	})
	if err != nil {
		logger.Error("create docker runner", "error", err)
		os.Exit(1)
	}
	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := runner.ReconcileOrphans(reconcileCtx); err != nil {
		logger.Warn("plugin-runner orphan reconciliation failed", "error", err)
	}
	reconcileCancel()
	supervisor, err := process.NewSupervisor(runner, token)
	if err != nil {
		logger.Error("create supervisor", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           supervisor.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("plugin-runner listening", "addr", addr, "dockerSocketConfigured", dockerSocket != "")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("plugin-runner stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("plugin-runner shutdown", "error", err)
	}
	if err := runner.StopAll(shutdownCtx); err != nil {
		logger.Error("plugin-runner container shutdown", "error", err)
	}
}
