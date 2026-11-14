// Package podman registers the real Podman adapter. Podman exposes a
// Docker-compatible Engine API on its own socket; this adapter resolves only
// the Podman socket and reports an unsupported capability when Podman is not
// installed. It never talks to the Docker daemon as a fallback.
package podman

import (
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
)

// Option customizes the podman adapter.
type Option func(*containerapi.Config)

// WithForwarders shares the worker-owned publication registry.
func WithForwarders(registry *containerapi.ForwarderRegistry) Option {
	return func(config *containerapi.Config) { config.Forwarders = registry }
}

// WithSocket overrides the worker-local podman socket path.
func WithSocket(socket string) Option {
	return func(config *containerapi.Config) {
		if socket != "" {
			config.DefaultSocket = func(string) string { return socket }
		}
	}
}

// New constructs the podman driver.
func New(options ...Option) (runtime.Driver, error) {
	config := containerapi.Config{
		DriverName:     deployment.DriverPodman,
		NodeKind:       deployment.KindContainer,
		DefaultSocket:  containerapi.DefaultPodmanSocket,
		SupportsPublic: false,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return containerapi.New(config)
}
