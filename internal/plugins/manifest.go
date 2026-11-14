package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
)

const (
	// PluginAPIVersion is the only accepted plugin API major version.
	PluginAPIVersion = "1"
	// HTTPProtocolV1 is the only accepted backend protocol.
	HTTPProtocolV1 = "http-v1"
	// MaxManifestBytes bounds manifest.yaml before YAML parsing.
	MaxManifestBytes = 256 << 10
	// MaxDescriptionBytes bounds the human-readable manifest description.
	MaxDescriptionBytes = 20_000
)

var (
	pluginIDPattern      = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)
	pluginVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	pinnedImagePattern   = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)
	checksumPattern      = regexp.MustCompile(`^(?:sha256:)?([a-f0-9]{64})$`)
	routeParamPattern    = regexp.MustCompile(`^:[A-Za-z][A-Za-z0-9]*$`)
	routeStaticPattern   = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

// ManifestDigest returns the canonical SHA-256 digest of the normalized
// manifest. It is persisted and compared on upgrade.
func ManifestDigest(m Manifest) (string, error) {
	normalized := m
	normalized.Checksum = strings.ToLower(strings.TrimSpace(normalized.Checksum))
	normalized.Dependencies = append([]Dependency(nil), m.Dependencies...)
	normalized.Permissions = append([]PermissionDecl(nil), m.Permissions...)
	normalized.Capabilities = append([]Capability(nil), m.Capabilities...)
	normalized.Routes = append([]Route(nil), m.Routes...)
	normalized.Checkers = append([]CheckerDecl(nil), m.Checkers...)
	normalized.Events = append([]string(nil), m.Events...)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("plugins: encode manifest digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ParseManifest strictly decodes manifest.yaml. Unknown YAML keys are rejected
// so a package cannot silently rely on fields core does not understand.
func ParseManifest(data []byte) (Manifest, error) {
	if len(data) == 0 {
		return Manifest{}, fmt.Errorf("%w: manifest.yaml is empty", ErrManifestInvalid)
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest.yaml exceeds %d bytes", ErrManifestInvalid, MaxManifestBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: manifest.yaml must contain exactly one YAML document", ErrManifestInvalid)
	}
	return manifest, nil
}

// ValidateManifest performs every non-executing manifest check. It returns
// findings sorted in declaration order; any error finding makes the package
// invalid.
func ValidateManifest(manifest Manifest, allowExec bool, allowTrustedJS ...bool) []Finding {
	trustedJSAllowed := true
	if len(allowTrustedJS) > 0 {
		trustedJSAllowed = allowTrustedJS[0]
	}
	findings := make([]Finding, 0, 8)
	add := func(path, code, message string) {
		findings = append(findings, Finding{Path: path, Code: code, Message: message, Severity: "error"})
	}
	warn := func(path, code, message string) {
		findings = append(findings, Finding{Path: path, Code: code, Message: message, Severity: "warning"})
	}

	if !pluginIDPattern.MatchString(string(manifest.ID)) || len(manifest.ID) > 128 {
		add("id", "invalid_id", "id must be a lowercase namespaced id such as vendor.name (max 128)")
	}
	if strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 128 {
		add("name", "invalid_name", "name is required and must be at most 128 characters")
	}
	if !pluginVersionPattern.MatchString(manifest.Version) || len(manifest.Version) > 64 {
		add("version", "invalid_version", "version must be a semantic version such as 1.0.0")
	}
	if manifest.APIVersion != PluginAPIVersion {
		add("apiVersion", "incompatible_api_version", fmt.Sprintf("apiVersion must be %q", PluginAPIVersion))
	}
	if len(manifest.Description) > MaxDescriptionBytes {
		add("description", "description_too_long", fmt.Sprintf("description exceeds %d bytes", MaxDescriptionBytes))
	}
	if len(manifest.Author) > 128 {
		add("author", "author_too_long", "author must be at most 128 characters")
	}

	switch manifest.Backend.Mode {
	case BackendOCI:
		if !pinnedImagePattern.MatchString(manifest.Backend.Image) {
			add("backend.image", "unpinned_image", "oci backend.image must be pinned as repository@sha256:<64 hex>")
		}
		if strings.TrimSpace(manifest.Backend.Entrypoint) != "" {
			if err := validateRelativePackagePath(manifest.Backend.Entrypoint); err != nil {
				add("backend.entrypoint", "invalid_entrypoint", err.Error())
			}
		}
	case BackendExec:
		if !allowExec {
			add("backend.mode", "exec_disabled", "exec backend requires the operator to set ACTFB_PLUGIN_ALLOW_EXEC=true")
		}
		if strings.TrimSpace(manifest.Backend.Image) != "" {
			add("backend.image", "unexpected_image", "exec backend must not declare an image")
		}
		if err := validateRelativePackagePath(manifest.Backend.Entrypoint); err != nil {
			add("backend.entrypoint", "invalid_entrypoint", "exec backend.entrypoint must be a relative package path")
		}
	default:
		add("backend.mode", "invalid_backend_mode", "backend.mode must be oci or exec")
	}
	if manifest.Backend.Protocol != HTTPProtocolV1 {
		add("backend.protocol", "unsupported_protocol", fmt.Sprintf("backend.protocol must be %q", HTTPProtocolV1))
	}
	if manifest.Backend.TimeoutSeconds < 0 || manifest.Backend.TimeoutSeconds > 30 {
		add("backend.timeoutSeconds", "invalid_timeout", "backend.timeoutSeconds must be between 1 and 30 (0 selects the default 5)")
	}
	if manifest.Backend.HealthPath != "" {
		if err := validateRoutePath(manifest.Backend.HealthPath); err != nil {
			add("backend.healthPath", "invalid_health_path", err.Error())
		}
	}

	switch manifest.Frontend.Mode {
	case "":
		if manifest.Frontend.Entry != "" || manifest.Assets != "" {
			add("frontend", "incomplete_frontend", "assets/entry require frontend.mode")
		}
	case FrontendIframe:
		if err := validateRelativePackagePath(manifest.Frontend.Entry); err != nil {
			add("frontend.entry", "invalid_frontend_entry", "iframe frontend.entry must be a relative .html path inside assets")
		} else if !strings.HasSuffix(strings.ToLower(manifest.Frontend.Entry), ".html") {
			add("frontend.entry", "invalid_frontend_entry", "iframe frontend.entry must be an .html file")
		}
	case FrontendTrustedJS:
		if err := validateRelativePackagePath(manifest.Frontend.Entry); err != nil {
			add("frontend.entry", "invalid_frontend_entry", "trusted-js frontend.entry must be a relative .js path inside assets")
		} else if !strings.HasSuffix(strings.ToLower(manifest.Frontend.Entry), ".js") {
			add("frontend.entry", "invalid_frontend_entry", "trusted-js frontend.entry must be a .js file")
		}
		if !trustedJSAllowed {
			add("frontend.mode", "trusted_js_disabled", "trusted-js requires the operator to set ACTFB_PLUGIN_ALLOW_TRUSTED_JS=true")
		}
		warn("frontend.mode", "trusted_js_risk", "trusted-js runs with platform-origin capabilities; it is not a sandbox and requires explicit per-install approval")
	default:
		add("frontend.mode", "invalid_frontend_mode", "frontend.mode must be iframe or trusted-js")
	}
	if manifest.Assets != "" {
		if err := validateRelativePackagePath(manifest.Assets); err != nil {
			add("assets", "invalid_assets", "assets must be a relative directory without traversal")
		}
	}
	if manifest.Migrations != "" {
		if err := validateRelativePackagePath(manifest.Migrations); err != nil {
			add("migrations", "invalid_migrations", "migrations must be a relative directory without traversal")
		}
	}

	dependencyIDs := make(map[ID]struct{}, len(manifest.Dependencies))
	for index, dependency := range manifest.Dependencies {
		path := fmt.Sprintf("dependencies[%d]", index)
		if !pluginIDPattern.MatchString(string(dependency.ID)) || len(dependency.ID) > 128 {
			add(path+".id", "invalid_dependency_id", "dependency id must be a lowercase namespaced id such as vendor.name")
		}
		if dependency.ID == manifest.ID {
			add(path+".id", "self_dependency", "a plugin cannot depend on itself")
		}
		if _, exists := dependencyIDs[dependency.ID]; exists {
			add(path+".id", "duplicate_dependency", "duplicate dependency declaration")
		}
		dependencyIDs[dependency.ID] = struct{}{}
		if _, err := versionSatisfies("0.0.0", dependency.Version); err != nil {
			add(path+".version", "invalid_dependency_range", "dependency version must be a semantic range such as ^1.0.0 or >=1.0.0 <2.0.0")
		}
	}

	permissionScopes := make(map[authorization.Permission]string, len(manifest.Permissions))
	for index, declaration := range manifest.Permissions {
		path := fmt.Sprintf("permissions[%d]", index)
		permission := authorization.Permission(strings.TrimSpace(string(declaration.ID)))
		if len(permission) > 128 {
			add(path+".id", "permission_too_long", "plugin permission must be at most 128 characters")
		}
		if !strings.HasPrefix(string(permission), "plugin."+string(manifest.ID)+".") {
			add(path+".id", "unscoped_permission", "plugin permissions must be named plugin.<id>.<name>")
		}
		if _, exists := permissionScopes[permission]; exists {
			add(path+".id", "duplicate_permission", "duplicate permission declaration")
		}
		scope := strings.TrimSpace(declaration.Scope)
		switch authorization.PermissionScope(scope) {
		case authorization.PermissionScopeGlobal, authorization.PermissionScopeBoard, authorization.PermissionScopeModule:
		default:
			add(path+".scope", "invalid_scope", "scope must be global, board or module")
		}
		permissionScopes[permission] = scope
	}

	capabilitySet := make(map[Capability]struct{}, len(manifest.Capabilities))
	for index, capability := range manifest.Capabilities {
		path := fmt.Sprintf("capabilities[%d]", index)
		switch capability {
		case CapabilityBoardsRead, CapabilitySubmissionsRead, CapabilityEventsSubscribe:
		default:
			add(path, "unknown_capability", "capability is not part of plugin api v1")
		}
		if _, exists := capabilitySet[capability]; exists {
			add(path, "duplicate_capability", "duplicate capability")
		}
		capabilitySet[capability] = struct{}{}
	}

	routeKeys := make(map[string]struct{}, len(manifest.Routes))
	for index, route := range manifest.Routes {
		path := fmt.Sprintf("routes[%d]", index)
		method := strings.ToUpper(strings.TrimSpace(route.Method))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			add(path+".method", "invalid_method", "method must be GET, POST, PUT, PATCH or DELETE")
		}
		if err := validateRoutePath(route.Path); err != nil {
			add(path+".path", "invalid_route", err.Error())
		}
		if _, exists := permissionScopes[route.Permission]; !exists {
			add(path+".permission", "undeclared_permission", "route.permission must be declared in permissions[]")
		}
		if declaredScope, exists := permissionScopes[route.Permission]; exists && route.Scope != declaredScope {
			add(path+".scope", "scope_mismatch", "route.scope must match the permission declaration scope")
		}
		hasBoardParam := routePathHasParam(route.Path, "boardUuid")
		hasModuleParam := routePathHasParam(route.Path, "moduleUuid")
		switch route.Scope {
		case "global":
			if hasBoardParam || hasModuleParam {
				add(path+".path", "unexpected_scope_params", "global routes must not carry board/module scope parameters")
			}
		case "board":
			if !hasBoardParam {
				add(path+".path", "missing_board_param", "board routes require a :boardUuid path parameter")
			}
			if hasModuleParam {
				add(path+".path", "unexpected_module_param", "a route with :moduleUuid must declare module scope")
			}
		case "module":
			if !hasBoardParam || !hasModuleParam {
				add(path+".path", "missing_scope_params", "module routes require :boardUuid and :moduleUuid path parameters")
			}
		}
		key := method + " " + route.Path
		if _, exists := routeKeys[key]; exists {
			add(path+".path", "duplicate_route", "duplicate method/path route")
		}
		routeKeys[key] = struct{}{}
	}

	checkerKeys := make(map[string]struct{}, len(manifest.Checkers))
	checkerExtensions := make(map[string]struct{}, len(manifest.Checkers))
	for index, checker := range manifest.Checkers {
		path := fmt.Sprintf("checkers[%d]", index)
		if len(checker.Key) > 128 || !strings.HasPrefix(checker.Key, "plugin."+string(manifest.ID)+".") {
			add(path+".key", "invalid_checker_key", "checker key must be namespaced as plugin.<id>.<name>")
		}
		if len(checker.ExtensionKey) > 128 || !strings.HasPrefix(checker.ExtensionKey, "plugin."+string(manifest.ID)+".") {
			add(path+".extensionKey", "invalid_extension_key", "extensionKey must be namespaced as plugin.<id>.<name>")
		}
		if _, exists := checkerKeys[checker.Key]; exists {
			add(path+".key", "duplicate_checker_key", "duplicate checker key")
		}
		checkerKeys[checker.Key] = struct{}{}
		if _, exists := checkerExtensions[checker.ExtensionKey]; exists {
			add(path+".extensionKey", "duplicate_extension_key", "duplicate extension key")
		}
		checkerExtensions[checker.ExtensionKey] = struct{}{}
		if err := validateRoutePath(checker.Route); err != nil {
			add(path+".route", "invalid_checker_route", err.Error())
		}
		if checker.Scope != "board" {
			add(path+".scope", "invalid_checker_scope", "checker scope must be board")
		}
		if declaredScope, exists := permissionScopes[checker.Permission]; !exists {
			add(path+".permission", "undeclared_permission", "checker permission must be declared in permissions[]")
		} else if declaredScope != "board" {
			add(path+".permission", "invalid_checker_permission_scope", "checker permission must be board-scoped")
		}
		if checker.TimeoutSeconds < 0 || checker.TimeoutSeconds > 30 {
			add(path+".timeoutSeconds", "invalid_checker_timeout", "checker timeout must be between 1 and 30 (0 selects the default 5)")
		}
		if checker.MaxTextBytes < 0 || checker.MaxTextBytes > 4_000_000 {
			add(path+".maxTextBytes", "invalid_checker_text_limit", "checker maxTextBytes must be between 1 and 4000000 (0 selects the default 1000000)")
		}
		routeFound := false
		for _, route := range manifest.Routes {
			if route.Method == "POST" && route.Path == checker.Route {
				routeFound = true
				if route.Permission != checker.Permission || route.Scope != checker.Scope {
					add(path+".route", "checker_route_mismatch", "checker route permission/scope must match the declaration")
				}
			}
		}
		if !routeFound {
			add(path+".route", "missing_checker_route", "checker route must be declared as a POST route")
		}
	}

	for index, eventType := range manifest.Events {
		path := fmt.Sprintf("events[%d]", index)
		if _, ok := capabilitySet[CapabilityEventsSubscribe]; !ok {
			add(path, "missing_capability", "event subscriptions require the core.events.subscribe capability")
		}
		if strings.HasPrefix(eventType, "plugin.") {
			add(path, "invalid_subscription", "plugins cannot subscribe to their own event namespace")
			continue
		}
		registry := events.CoreRegistry()
		if err := registry.Validate(eventType); err != nil {
			add(path, "unknown_event", "event type is not a documented core event")
		}
	}

	if len(manifest.ConfigSchema) > 0 {
		var document map[string]any
		if err := json.Unmarshal(manifest.ConfigSchema, &document); err != nil {
			add("configSchema", "invalid_config_schema", "configSchema must be a JSON object")
		}
		if len(manifest.ConfigSchema) > 64<<10 {
			add("configSchema", "config_schema_too_large", "configSchema exceeds 64 KiB")
		}
	}
	if manifest.Checksum != "" {
		if !checksumPattern.MatchString(strings.ToLower(strings.TrimSpace(manifest.Checksum))) {
			add("checksum", "invalid_checksum", "checksum must be sha256:<64 hex>")
		}
	}
	return findings
}

// NormalizeManifest applies deterministic defaults after validation.
func NormalizeManifest(manifest Manifest) Manifest {
	manifest.ID = ID(strings.TrimSpace(string(manifest.ID)))
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.APIVersion = strings.TrimSpace(manifest.APIVersion)
	manifest.Author = strings.TrimSpace(manifest.Author)
	manifest.Backend.Image = strings.TrimSpace(manifest.Backend.Image)
	manifest.Backend.Entrypoint = strings.TrimSpace(manifest.Backend.Entrypoint)
	manifest.Backend.Protocol = strings.TrimSpace(manifest.Backend.Protocol)
	if manifest.Backend.TimeoutSeconds == 0 {
		manifest.Backend.TimeoutSeconds = 5
	}
	if manifest.Backend.HealthPath == "" {
		manifest.Backend.HealthPath = "/v1/health"
	}
	if manifest.Frontend.Mode == "" {
		manifest.Frontend = Frontend{}
	}
	for index := range manifest.Dependencies {
		manifest.Dependencies[index].ID = ID(strings.TrimSpace(string(manifest.Dependencies[index].ID)))
		manifest.Dependencies[index].Version = strings.TrimSpace(manifest.Dependencies[index].Version)
	}
	for index := range manifest.Routes {
		manifest.Routes[index].Method = strings.ToUpper(strings.TrimSpace(manifest.Routes[index].Method))
		manifest.Routes[index].Path = strings.TrimSpace(manifest.Routes[index].Path)
	}
	for index := range manifest.Checkers {
		declaration := &manifest.Checkers[index]
		declaration.Key = strings.TrimSpace(declaration.Key)
		declaration.ExtensionKey = strings.TrimSpace(declaration.ExtensionKey)
		declaration.Route = strings.TrimSpace(declaration.Route)
		if declaration.TimeoutSeconds == 0 {
			declaration.TimeoutSeconds = 5
		}
		if declaration.MaxTextBytes == 0 {
			declaration.MaxTextBytes = 1_000_000
		}
	}
	manifest.Checksum = strings.ToLower(strings.TrimSpace(manifest.Checksum))
	return manifest
}

func hasErrorFinding(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == "error" {
			return true
		}
	}
	return false
}

func routePathHasParam(routePath, name string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(routePath, "/"), "/") {
		if segment == ":"+name {
			return true
		}
	}
	return false
}

func validateRoutePath(path string) error {
	if path == "" || len(path) > 512 {
		return errors.New("route path is required and must be at most 512 characters")
	}
	if !strings.HasPrefix(path, "/") {
		return errors.New("route path must start with /")
	}
	if strings.HasSuffix(path, "/") && path != "/" {
		return errors.New("route path must not end with /")
	}
	if strings.Contains(path, "%") || strings.Contains(path, "\\") || strings.Contains(path, "//") {
		return errors.New("route path must not contain percent escapes, backslashes or empty segments")
	}
	if strings.Contains(path, "?") || strings.Contains(path, "#") || strings.Contains(path, "*") {
		return errors.New("route path must not contain query, fragment or wildcard characters")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("route path segments must be non-empty and must not be . or ..")
		}
		if strings.HasPrefix(segment, ":") {
			if !routeParamPattern.MatchString(segment) {
				return errors.New("route parameters must look like :boardUuid")
			}
			continue
		}
		if !routeStaticPattern.MatchString(segment) {
			return errors.New("route path segments may contain only letters, digits, . _ ~ and -")
		}
	}
	return nil
}

// validateRelativePackagePath rejects absolute paths, drive letters, traversal,
// hidden/control segments and backslashes.
func validateRelativePackagePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	if len(path) > 512 {
		return errors.New("path exceeds 512 characters")
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") || strings.Contains(path, "\\") || strings.Contains(path, "%") {
		return errors.New("path must be relative and must not contain backslashes or percent escapes")
	}
	if strings.ContainsRune(path, 0) {
		return errors.New("path contains NUL")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("path contains an empty, . or .. segment")
		}
		if len(segment) > 255 {
			return errors.New("path segment exceeds 255 characters")
		}
	}
	return nil
}
