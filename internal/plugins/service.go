package plugins

import (
	"context"
	"net/http"
	"net/url"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// Service is the frozen plugin manager contract used by the HTTP layer and
// integration tests. All mutating operations require the global
// plugins.manage permission and are audited by the implementation.
type Service interface {
	List(ctx context.Context, principal authorization.Principal) ([]Installed, error)
	Validate(ctx context.Context, principal authorization.Principal, packagePath string) (Validation, error)
	Install(ctx context.Context, principal authorization.Principal, in InstallRequest) (*Installed, error)
	Get(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Enable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Disable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Upgrade(ctx context.Context, principal authorization.Principal, id ID, packagePath string) (*Installed, error)
	Remove(ctx context.Context, principal authorization.Principal, id ID) error
	Logs(ctx context.Context, principal authorization.Principal, id ID, limit int) ([]string, error)
	Visible(ctx context.Context, principal authorization.Principal) ([]Visible, error)
	Proxy(ctx context.Context, in ProxyRequest) (ProxyResponse, error)
}

// ProxyRequest is one core-authorized call into a declared plugin route.
type ProxyRequest struct {
	PluginID ID
	Actor    authorization.Principal
	Method   string
	Path     string
	Query    url.Values
	Headers  http.Header
	Body     []byte
}

// ProxyResponse is the sanitized plugin response returned to the browser.
type ProxyResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}
