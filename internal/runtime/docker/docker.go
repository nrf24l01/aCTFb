// Package docker registers the real Docker Engine API adapter. It never
// pretends to work when the Docker socket is absent and never falls back to
// Podman: an unavailable daemon is reported as an unsupported provider.
package docker

import (
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
)

// Option customizes the docker adapter.
type Option func(*containerapi.Config)

// WithForwarders shares the worker-owned publication registry.
func WithForwarders(registry *containerapi.ForwarderRegistry) Option {
	return func(config *containerapi.Config) { config.Forwarders = registry }
}

// WithSocket overrides the worker-local docker socket path.
func WithSocket(socket string) Option {
	return func(config *containerapi.Config) {
		if socket != "" {
			config.DefaultSocket = func(string) string { return socket }
		}
	}
}

// New constructs the docker driver. The operator must explicitly mark the
// worker node dedicated before any workload is accepted; see
// containerapi.Settings.
func New(options ...Option) (runtime.Driver, error) {
	config := containerapi.Config{
		DriverName:     deployment.DriverDocker,
		NodeKind:       deployment.KindContainer,
		DefaultSocket:  containerapi.DefaultDockerSocket,
		SupportsPublic: true,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return containerapi.New(config)
}
