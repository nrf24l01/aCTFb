package plugins

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

const (
	maxQueryBytes = 16 << 10
	maxPathBytes  = 2048
)

// Proxy forwards one browser request to a declared plugin route after core has
// verified the explicit permission grant and parent board/module ownership.
func (m *Manager) Proxy(ctx context.Context, in ProxyRequest) (ProxyResponse, error) {
	if in.Actor.UserID == uuid.Nil {
		return ProxyResponse{}, ErrPermissionDenied
	}
	if in.PluginID == "" {
		return ProxyResponse{}, ErrNotFound
	}
	if len(in.Body) > int(m.maxBody) {
		return ProxyResponse{}, fmt.Errorf("%w: request body exceeds the plugin cap", ErrRequestRejected)
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		return ProxyResponse{}, fmt.Errorf("%w: method is required", ErrRequestRejected)
	}
	requestPath, err := pluginRelativePath(in.PluginID, in.Path)
	if err != nil {
		return ProxyResponse{}, err
	}
	installed, err := m.loadInstalled(ctx, in.PluginID)
	if err != nil {
		return ProxyResponse{}, err
	}
	if installed.Status != StatusEnabled {
		return ProxyResponse{}, ErrBackendUnavailable
	}
	route, params, ok := matchDeclaredRoute(installed.Manifest.Routes, method, requestPath)
	if !ok {
		return ProxyResponse{}, ErrRouteNotFound
	}
	scope, err := scopeFromRoute(route, params)
	if err != nil {
		return ProxyResponse{}, err
	}
	if err := validateScopeHints(in.Headers, scope); err != nil {
		return ProxyResponse{}, err
	}
	if err := m.authorizeDeclaredRoute(ctx, *installed, in.Actor, route, scope); err != nil {
		return ProxyResponse{}, err
	}

	runtime := m.runtimeFor(in.PluginID)
	if runtime == nil || runtime.client == nil {
		return ProxyResponse{}, ErrBackendUnavailable
	}
	delegation, err := m.issuer.Issue(ctx, runtime.identity, in.Actor.UserID, scope, 5*time.Minute)
	if err != nil {
		return ProxyResponse{}, fmt.Errorf("plugins: issue delegation: %w", err)
	}

	query, err := sanitizeQuery(in.Query)
	if err != nil {
		return ProxyResponse{}, err
	}
	operation := protocol.OperationAPI
	if route.UI && method == http.MethodGet {
		operation = protocol.OperationPage
	}
	requestID := uuid.NewString()
	headers := sanitizedRequestHeaders(in.Headers)
	invoke := protocol.InvokeRequest{
		Protocol:  protocol.Version,
		RequestID: requestID,
		Operation: operation,
		PluginID:  string(in.PluginID),
		UserUUID:  stringPointer(in.Actor.UserID.String()),
		Scope: protocol.Scope{
			BoardUUID:  uuidStringPointer(scope.BoardID),
			ModuleUUID: uuidPointerString(scope.ModuleID),
		},
		Method:     method,
		Path:       requestPath,
		Query:      query,
		Headers:    headers,
		BodyBase64: base64Encode(in.Body),
	}
	reply, body, err := runtime.client.InvokeWithDelegation(ctx, invoke, delegation.Token)
	if err != nil {
		m.markBackendFailure(ctx, in.PluginID, err)
		return ProxyResponse{}, fmt.Errorf("%w: %v", ErrBackendUnavailable, err)
	}
	response, err := sanitizePluginResponse(reply, body)
	if err != nil {
		return ProxyResponse{}, err
	}
	return response, nil
}

// authorizeDeclaredRoute requires a live explicit permission grant even for a
// global admin, then asks the central authorizer to verify plugin-enabled state
// and board/module parent integrity.
func (m *Manager) authorizeDeclaredRoute(
	ctx context.Context,
	installed Installed,
	actor authorization.Principal,
	route Route,
	scope authorization.Scope,
) error {
	definition, ok := m.registry.Lookup(route.Permission)
	if !ok {
		return ErrPermissionDenied
	}
	if definition.Source != string(installed.ID) {
		return ErrPermissionDenied
	}
	permissionScope := authorization.PermissionScopeGlobal
	switch {
	case scope.BoardID == uuid.Nil:
		permissionScope = authorization.PermissionScopeGlobal
	case scope.ModuleID == nil:
		permissionScope = authorization.PermissionScopeBoard
	default:
		permissionScope = authorization.PermissionScopeModule
	}
	if !authorization.DefinitionAllowsScope(definition, permissionScope) {
		return ErrPermissionDenied
	}
	if route.Scope != string(permissionScope) {
		return ErrPermissionDenied
	}
	// Master rule: explicit grant only, even for admins.
	granted, err := m.grants.HasGrant(ctx, actor.UserID, route.Permission, scope)
	if err != nil {
		return fmt.Errorf("plugins: check permission grant: %w", err)
	}
	if !granted {
		return ErrPermissionDenied
	}
	decision, err := m.authorizer.Authorize(ctx, actor, route.Permission, scope)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	if !decision.Allowed {
		return ErrPermissionDenied
	}
	return nil
}

func (m *Manager) markBackendFailure(ctx context.Context, id ID, cause error) {
	runtime := m.runtimeFor(id)
	if runtime == nil {
		return
	}
	runtime.setHealth(Health{Status: "unhealthy", Message: truncate(cause.Error(), 500), CheckedAt: m.now()})
	_, _ = m.db.Pool.Exec(context.WithoutCancel(ctx), `
		UPDATE plugins SET status = 'unhealthy', updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'enabled'
	`, string(id))
}

// pluginRelativePath accepts either a handler remainder ("/achievements") or a
// full "/api/v2/plugins/{id}/..." path and returns a canonical absolute path.
func pluginRelativePath(id ID, raw string) (string, error) {
	if raw == "" || len(raw) > maxPathBytes {
		return "", fmt.Errorf("%w: invalid path", ErrRequestRejected)
	}
	if strings.Contains(raw, "%") || strings.Contains(raw, "\\") || strings.ContainsRune(raw, 0) ||
		strings.Contains(raw, "?") || strings.Contains(raw, "#") {
		return "", fmt.Errorf("%w: encoded, backslash or NUL paths are rejected", ErrRequestRejected)
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	for _, prefix := range []string{
		"/api/v2/plugins/" + string(id),
		"/plugins/" + string(id),
	} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimPrefix(raw, prefix)
			if raw == "" {
				raw = "/"
			}
			break
		}
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if strings.Contains(raw, "//") {
		return "", fmt.Errorf("%w: empty path segments are rejected", ErrRequestRejected)
	}
	cleaned := path.Clean(raw)
	if cleaned != raw {
		return "", fmt.Errorf("%w: path traversal or dot segments are rejected", ErrRequestRejected)
	}
	if len(cleaned) > maxPathBytes {
		return "", fmt.Errorf("%w: path too long", ErrRequestRejected)
	}
	return cleaned, nil
}

func matchDeclaredRoute(routes []Route, method, requestPath string) (Route, map[string]string, bool) {
	actual := splitSegments(requestPath)
	for _, route := range routes {
		if route.Method != method {
			continue
		}
		pattern := splitSegments(route.Path)
		if len(pattern) != len(actual) {
			continue
		}
		params := make(map[string]string, 2)
		matched := true
		for index, segment := range pattern {
			if strings.HasPrefix(segment, ":") {
				if actual[index] == "" {
					matched = false
					break
				}
				params[strings.TrimPrefix(segment, ":")] = actual[index]
				continue
			}
			if segment != actual[index] {
				matched = false
				break
			}
		}
		if matched {
			return route, params, true
		}
	}
	return Route{}, nil, false
}

func splitSegments(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func scopeFromRoute(route Route, params map[string]string) (authorization.Scope, error) {
	scope := authorization.Scope{}
	switch authorization.PermissionScope(route.Scope) {
	case authorization.PermissionScopeGlobal:
		if _, hasBoard := params["boardUuid"]; hasBoard {
			return authorization.Scope{}, fmt.Errorf("%w: global routes must not carry board scope parameters", ErrRequestRejected)
		}
		if _, hasModule := params["moduleUuid"]; hasModule {
			return authorization.Scope{}, fmt.Errorf("%w: global routes must not carry module scope parameters", ErrRequestRejected)
		}
		return scope, nil
	case authorization.PermissionScopeBoard, authorization.PermissionScopeModule:
	default:
		return authorization.Scope{}, fmt.Errorf("%w: invalid route scope", ErrRequestRejected)
	}
	rawBoard, ok := params["boardUuid"]
	if !ok {
		return authorization.Scope{}, fmt.Errorf("%w: board-scoped routes require a :boardUuid path parameter", ErrRequestRejected)
	}
	boardID, err := uuid.Parse(rawBoard)
	if err != nil || boardID == uuid.Nil {
		return authorization.Scope{}, fmt.Errorf("%w: boardUuid is invalid", ErrRequestRejected)
	}
	scope.BoardID = boardID
	if authorization.PermissionScope(route.Scope) == authorization.PermissionScopeModule {
		rawModule, ok := params["moduleUuid"]
		if !ok {
			return authorization.Scope{}, fmt.Errorf("%w: module-scoped routes require a :moduleUuid path parameter", ErrRequestRejected)
		}
		moduleID, err := uuid.Parse(rawModule)
		if err != nil || moduleID == uuid.Nil {
			return authorization.Scope{}, fmt.Errorf("%w: moduleUuid is invalid", ErrRequestRejected)
		}
		scope.ModuleID = &moduleID
	}
	return scope, nil
}

// validateScopeHints makes the frontend's X-Actfb-BoardUuid/ModuleUuid
// headers a consistency hint only. The actual path params and the verified
// grant remain the authority; a mismatching hint is rejected rather than
// silently ignored.
func validateScopeHints(headers http.Header, scope authorization.Scope) error {
	if headers == nil {
		return nil
	}
	if raw := strings.TrimSpace(headerValue(headers, "X-Actfb-BoardUuid")); raw != "" {
		boardID, err := uuid.Parse(raw)
		if err != nil || scope.BoardID == uuid.Nil || boardID != scope.BoardID {
			return fmt.Errorf("%w: board hint does not match the actual route scope", ErrRequestRejected)
		}
	}
	if raw := strings.TrimSpace(headerValue(headers, "X-Actfb-ModuleUuid")); raw != "" {
		moduleID, err := uuid.Parse(raw)
		if err != nil || scope.ModuleID == nil || moduleID != *scope.ModuleID {
			return fmt.Errorf("%w: module hint does not match the actual route scope", ErrRequestRejected)
		}
	}
	return nil
}

func sanitizeQuery(values url.Values) (map[string][]string, error) {
	if len(values) == 0 {
		return map[string][]string{}, nil
	}
	total := 0
	clean := make(map[string][]string, len(values))
	for key, list := range values {
		if key == "" || len(key) > 128 || len(list) > 16 {
			return nil, fmt.Errorf("%w: invalid query parameter", ErrRequestRejected)
		}
		if strings.ContainsAny(key, "%\\\x00") {
			return nil, fmt.Errorf("%w: invalid query key", ErrRequestRejected)
		}
		total += len(key)
		copied := make([]string, 0, len(list))
		for _, value := range list {
			if len(value) > 2048 || strings.ContainsRune(value, 0) {
				return nil, fmt.Errorf("%w: query value is too large or invalid", ErrRequestRejected)
			}
			total += len(value)
			copied = append(copied, value)
		}
		clean[key] = copied
	}
	if total > maxQueryBytes {
		return nil, fmt.Errorf("%w: query string is too large", ErrRequestRejected)
	}
	return clean, nil
}

func headerValue(headers http.Header, key string) string {
	if headers == nil {
		return ""
	}
	if value := headers.Get(key); value != "" {
		return value
	}
	for candidate, values := range headers {
		if !strings.EqualFold(candidate, key) || len(values) == 0 {
			continue
		}
		return values[0]
	}
	return ""
}

func sanitizedRequestHeaders(headers http.Header) map[string]string {
	allowed := map[string]struct{}{
		"Accept":          {},
		"Accept-Language": {},
		"Content-Type":    {},
	}
	clean := make(map[string]string, len(allowed))
	for key := range allowed {
		value := strings.TrimSpace(headerValue(headers, key))
		if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
			continue
		}
		clean[key] = value
	}
	return clean
}

func sanitizePluginResponse(reply protocol.InvokeResponse, body []byte) (ProxyResponse, error) {
	if reply.Status < 200 || reply.Status >= 300 || reply.Status > 599 {
		return ProxyResponse{}, fmt.Errorf("%w: plugin returned a redirect/invalid status", ErrBackendUnavailable)
	}
	headers := make(http.Header)
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Cache-Control", "no-store")
	headers.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
	headers.Set("Referrer-Policy", "no-referrer")

	declaredType := ""
	disposition := ""
	for key, value := range reply.Headers {
		switch http.CanonicalHeaderKey(key) {
		case "Content-Type":
			declaredType = value
		case "Content-Disposition":
			disposition = value
		}
	}
	mediaType, parameters, _ := mime.ParseMediaType(declaredType)
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(disposition)), "attachment") {
		filename := sanitizeAttachmentFilename(parameters["filename"])
		headers.Set("Content-Type", "application/octet-stream")
		if filename != "" {
			headers.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		} else {
			headers.Set("Content-Disposition", "attachment")
		}
		return ProxyResponse{Status: reply.Status, Headers: headers, Body: body}, nil
	}

	switch {
	case mediaType == "" && len(body) == 0:
		headers.Set("Content-Type", "application/json")
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		headers.Set("Content-Type", mediaType)
	case mediaType == "text/plain":
		headers.Set("Content-Type", "text/plain; charset=utf-8")
	default:
		return ProxyResponse{}, fmt.Errorf("%w: plugin returned a disallowed content type %q", ErrBackendUnavailable, mediaType)
	}
	return ProxyResponse{Status: reply.Status, Headers: headers, Body: body}, nil
}

func sanitizeAttachmentFilename(value string) string {
	value = path.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(char rune) rune {
		if char < 32 || char == '"' || char == 127 {
			return -1
		}
		return char
	}, value)
	if value == "." || value == "/" {
		return ""
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func stringPointer(value string) *string { return &value }

func uuidStringPointer(value uuid.UUID) *string {
	if value == uuid.Nil {
		return nil
	}
	encoded := value.String()
	return &encoded
}

func uuidPointerString(value *uuid.UUID) *string {
	if value == nil {
		return nil
	}
	encoded := value.String()
	return &encoded
}
