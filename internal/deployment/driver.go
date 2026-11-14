package deployment

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// Built-in driver names. A driver is an execution mechanism; it is not a
// resource kind and never appears in student-facing instance shapes.
const (
	DriverProxmox    = "proxmox"
	DriverKubeVirt   = "kubevirt"
	DriverDocker     = "docker"
	DriverPodman     = "podman"
	DriverKubernetes = "kubernetes"
)

// DriverMetadata is the read-only metadata the template validator needs from a
// trusted runtime driver registry.
type DriverMetadata struct {
	Driver       string
	Kind         Kind
	Capabilities Capabilities
}

// DriverMetadataProvider is implemented by the trusted runtime registry (or a
// test double). It must perform metadata reads only; ValidateDriverOptions must
// not provision, mutate or contact a provider.
//
// A provider's ValidateDriverOptions receives only the block namespaced for
// that driver. It must decode strictly, reject unknown option fields with a
// returned Finding whose Path is relative to the block, and never echo raw
// option values. The deployment service prefixes Path with
// nodes[i].driverOptions.<driver>.
type DriverMetadataProvider interface {
	ListDriverMetadata(ctx context.Context) ([]DriverMetadata, error)
	ValidateDriverOptions(ctx context.Context, driver string, node Node, options json.RawMessage) []Finding
}

// TemplateScope describes the scope an already published template version may
// be read/bound in. It is deliberately independent from provider credentials.
type TemplateScope struct {
	BoardID  *uuid.UUID
	ModuleID *uuid.UUID
	TaskID   *uuid.UUID
}

// ReadPermissionProvider is the hook for the forthcoming deployment_bindings /
// template_allowlist scope checks (migration 012). The admin template service
// does not invent a broad read grant: when no provider is configured, only the
// central platform.deployment_templates.publish permission can read.
type ReadPermissionProvider interface {
	CanReadTemplate(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, scope TemplateScope) (bool, error)
	CanBindTemplate(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, scope TemplateScope) (bool, error)
}

// Authorizer is the narrow central-RBAC contract used by the service. It is
// satisfies by *authorization.AuthorizerService.
type Authorizer interface {
	Authorize(ctx context.Context, principal authorization.Principal, permission authorization.Permission, scope authorization.Scope) (authorization.Decision, error)
}
