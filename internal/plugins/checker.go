package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

// checkerProvider is one enabled plugin's declared checker operation.
type checkerProvider struct {
	runtime *runtimeBackend
	decl    CheckerDecl
	route   Route
}

// ValidateChecker is called by trusted board task/module configuration. It
// requires an enabled, compatible plugin plus a live explicit permission grant
// for the acting admin. It never auto-grants anything.
func (m *Manager) ValidateChecker(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	extensionKey, checkerKey string,
) error {
	if boardID == uuid.Nil || principal.UserID == uuid.Nil {
		return ErrPermissionDenied
	}
	provider, err := m.findCheckerProvider(extensionKey, checkerKey)
	if err != nil {
		return err
	}
	if extensionKey != "" && provider.decl.ExtensionKey != extensionKey {
		return ErrCheckerNotFound
	}
	if !strings.EqualFold(provider.runtime.installed.Manifest.APIVersion, PluginAPIVersion) {
		return ErrIncompatible
	}
	// Checker declarations are board-scoped; task creation still validates the
	// module parent and task/module extension-key consistency in boards.
	_ = moduleID
	if err := m.authorizeDeclaredRoute(ctx, provider.runtime.installed, principal, provider.route, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	return nil
}

// CheckChecker invokes the declared versioned HTTP checker operation. The
// plugin receives only bounded text/config and the actual end-user delegation;
// it never receives flags, accepted-answer hashes, files, provider credentials
// or student code. Core remains the sole owner of the verdict/score.
func (m *Manager) CheckChecker(ctx context.Context, request CheckerRequest) (CheckerResult, error) {
	if request.CheckerKey == "" || request.ActorUserID == uuid.Nil ||
		request.BoardID == uuid.Nil || request.ModuleID == uuid.Nil || request.TaskID == uuid.Nil {
		return CheckerResult{}, ErrCheckerInvalid
	}
	if request.MaxPoints < 0 {
		return CheckerResult{}, ErrCheckerInvalid
	}
	provider, err := m.findCheckerProvider("", request.CheckerKey)
	if err != nil {
		return CheckerResult{}, err
	}
	if len([]byte(request.Text)) > provider.decl.MaxTextBytes {
		return CheckerResult{}, fmt.Errorf("%w: submitted text exceeds the checker limit", ErrCheckerInvalid)
	}
	if !m.checkerGrantActive(ctx, provider, request.BoardID, request.ModuleID) {
		return CheckerResult{}, fmt.Errorf("%w: approved checker permission grant is not active", ErrCheckerUnavailable)
	}
	scope := authorization.Scope{BoardID: request.BoardID, ModuleID: &request.ModuleID}
	delegation, err := m.issuer.Issue(ctx, provider.runtime.identity, request.ActorUserID, scope, 5*time.Minute)
	if err != nil {
		return CheckerResult{}, fmt.Errorf("%w: issue checker delegation: %v", ErrCheckerUnavailable, err)
	}
	path := renderCheckerPath(provider.decl.Route, request.BoardID, request.ModuleID)
	body, err := json.Marshal(struct {
		BoardID      uuid.UUID       `json:"boardId"`
		ModuleID     uuid.UUID       `json:"moduleId"`
		TaskID       uuid.UUID       `json:"taskId"`
		SubmissionID uuid.UUID       `json:"submissionId"`
		SubjectType  string          `json:"subjectType"`
		SubjectID    uuid.UUID       `json:"subjectId"`
		Text         string          `json:"text"`
		MaxPoints    int             `json:"maxPoints"`
		Config       json.RawMessage `json:"config,omitempty"`
	}{
		BoardID: request.BoardID, ModuleID: request.ModuleID, TaskID: request.TaskID,
		SubmissionID: request.SubmissionID, SubjectType: request.SubjectType,
		SubjectID: request.SubjectID, Text: request.Text, MaxPoints: request.MaxPoints,
		Config: request.Config,
	})
	if err != nil {
		return CheckerResult{}, fmt.Errorf("%w: encode checker request", ErrCheckerInvalid)
	}
	timeout := time.Duration(provider.decl.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if m.httpTimeout > 0 && timeout > m.httpTimeout {
		timeout = m.httpTimeout
	}
	invokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	requestID := uuid.NewString()
	reply, responseBody, err := provider.runtime.client.InvokeWithDelegation(invokeCtx, protocol.InvokeRequest{
		Protocol: protocol.Version, RequestID: requestID, Operation: protocol.OperationChecker,
		PluginID: string(provider.runtime.installed.ID), UserUUID: stringPointer(request.ActorUserID.String()),
		Scope: protocol.Scope{
			BoardUUID: stringPointer(request.BoardID.String()), ModuleUUID: stringPointer(request.ModuleID.String()),
		},
		Method: "POST", Path: path, Query: map[string][]string{}, Headers: map[string]string{"Content-Type": "application/json"},
		BodyBase64: base64Encode(body),
	}, delegation.Token)
	if err != nil {
		if invokeCtx.Err() == context.DeadlineExceeded {
			return CheckerResult{}, fmt.Errorf("%w: checker timed out", ErrCheckerUnavailable)
		}
		return CheckerResult{}, fmt.Errorf("%w: %v", ErrCheckerUnavailable, err)
	}
	if reply.Status != 200 {
		return CheckerResult{}, fmt.Errorf("%w: checker returned HTTP %d", ErrCheckerInvalid, reply.Status)
	}
	if int64(len(responseBody)) > 64<<10 {
		return CheckerResult{}, fmt.Errorf("%w: checker result is too large", ErrCheckerInvalid)
	}
	var wire struct {
		Verdict string `json:"verdict"`
		Score   *int   `json:"score,omitempty"`
		Message string `json:"message,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return CheckerResult{}, fmt.Errorf("%w: malformed checker result", ErrCheckerInvalid)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CheckerResult{}, fmt.Errorf("%w: trailing checker result", ErrCheckerInvalid)
	}
	verdict := strings.ToUpper(strings.TrimSpace(wire.Verdict))
	switch verdict {
	case "ACCEPTED", "REJECTED":
	default:
		return CheckerResult{}, fmt.Errorf("%w: unsupported verdict", ErrCheckerInvalid)
	}
	if wire.Score != nil && (*wire.Score < 0 || *wire.Score > request.MaxPoints) {
		return CheckerResult{}, fmt.Errorf("%w: checker score is outside [0,maxPoints]", ErrCheckerInvalid)
	}
	message := strings.TrimSpace(wire.Message)
	if len(message) > 2000 {
		message = message[:2000]
	}
	return CheckerResult{Verdict: verdict, Score: wire.Score, Message: message}, nil
}

func (m *Manager) findCheckerProvider(extensionKey, checkerKey string) (checkerProvider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found *checkerProvider
	for _, runtime := range m.runtime {
		if runtime == nil || runtime.client == nil {
			continue
		}
		for _, declaration := range runtime.installed.Manifest.Checkers {
			if checkerKey != "" && declaration.Key != checkerKey {
				continue
			}
			if extensionKey != "" && declaration.ExtensionKey != extensionKey {
				continue
			}
			route, ok := checkerRoute(runtime.installed.Manifest.Routes, declaration.Route)
			if !ok {
				continue
			}
			candidate := checkerProvider{runtime: runtime, decl: declaration, route: route}
			if found != nil {
				return checkerProvider{}, fmt.Errorf("%w: ambiguous checker provider", ErrCheckerNotFound)
			}
			found = &candidate
		}
	}
	if found == nil {
		return checkerProvider{}, ErrCheckerNotFound
	}
	return *found, nil
}

func checkerRoute(routes []Route, routePath string) (Route, bool) {
	for _, route := range routes {
		if route.Method == "POST" && route.Path == routePath {
			return route, true
		}
	}
	return Route{}, false
}

func renderCheckerPath(routePath string, boardID, moduleID uuid.UUID) string {
	path := strings.ReplaceAll(routePath, ":boardUuid", boardID.String())
	path = strings.ReplaceAll(path, ":moduleUuid", moduleID.String())
	return path
}

// checkerGrantActive keeps an existing task fail-closed if the administrator
// revokes every approved grant for the checker permission.
func (m *Manager) checkerGrantActive(ctx context.Context, provider checkerProvider, boardID, moduleID uuid.UUID) bool {
	var exists bool
	err := m.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM permission_grants
			WHERE permission = $1
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > clock_timestamp())
			  AND (board_id IS NULL OR board_id = $2)
			  AND (module_id IS NULL OR module_id = $3)
		)
	`, string(provider.decl.Permission), boardID, moduleID).Scan(&exists)
	return err == nil && exists
}

// checkCheckerConflicts ensures one checker/extension key maps to one enabled
// plugin. Called before a plugin backend is launched.
func (m *Manager) checkCheckerConflicts(manifest Manifest) error {
	if len(manifest.Checkers) == 0 {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, runtime := range m.runtime {
		if runtime == nil || id == manifest.ID {
			continue
		}
		for _, existing := range runtime.installed.Manifest.Checkers {
			for _, candidate := range manifest.Checkers {
				if existing.Key == candidate.Key {
					return fmt.Errorf("%w: checker key %s is already provided by %s", ErrValidation, candidate.Key, id)
				}
				if existing.ExtensionKey == candidate.ExtensionKey {
					return fmt.Errorf("%w: extension key %s is already provided by %s", ErrValidation, candidate.ExtensionKey, id)
				}
			}
		}
	}
	return nil
}
