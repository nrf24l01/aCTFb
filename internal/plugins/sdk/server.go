// Package sdk implements the plugin→core scoped SDK transport. Every call
// requires both the plugin service token and a core-issued short-lived
// delegation; user identity and scope are derived only from that delegation.
package sdk

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/plugins"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

const (
	maxSDKBody = 256 << 10

	PathAuthorize    = "/authorize"
	PathBoardContext = "/board-context"
	PathEvents       = "/events"
)

// Server is the core-side SDK handler.
type Server struct {
	verifier plugins.ServiceIdentityVerifier
	proxy    plugins.CoreProxy
	maxBody  int64
}

// NewServer requires both a service-token verifier and scoped core proxy.
func NewServer(verifier plugins.ServiceIdentityVerifier, proxy plugins.CoreProxy) (*Server, error) {
	if verifier == nil {
		return nil, errors.New("plugins/sdk: service token verifier is required")
	}
	if proxy == nil {
		return nil, errors.New("plugins/sdk: core proxy is required")
	}
	return &Server{verifier: verifier, proxy: proxy, maxBody: maxSDKBody}, nil
}

// Handler mounts the three SDK operations. It must be registered behind the
// core API origin; plugins call it over loopback/private network only.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PathAuthorize, s.handleAuthorize)
	mux.HandleFunc(PathBoardContext, s.handleBoardContext)
	mux.HandleFunc(PathEvents, s.handleEvents)
	return mux
}

type authorizeRequest struct {
	Permission string  `json:"permission"`
	BoardUUID  *string `json:"boardUuid,omitempty"`
	ModuleUUID *string `json:"moduleUuid,omitempty"`
}

type authorizeResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type boardContextRequest struct {
	BoardUUID  string  `json:"boardUuid"`
	ModuleUUID *string `json:"moduleUuid,omitempty"`
}

type boardContextResponse struct {
	BoardUUID   string   `json:"boardUuid"`
	ModuleUUID  *string  `json:"moduleUuid,omitempty"`
	SubjectType string   `json:"subjectType,omitempty"`
	SubjectUUID string   `json:"subjectUuid,omitempty"`
	Permissions []string `json:"permissions"`
}

type publishEventRequest struct {
	Event events.Event `json:"event"`
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var request authorizeRequest
	if !decode(w, r, s.maxBody, &request) {
		return
	}
	permission := authorization.Permission(strings.TrimSpace(request.Permission))
	if permission == "" {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "permission is required")
		return
	}
	if !scopeHintMatches(call.Delegation.Scope, request.BoardUUID, request.ModuleUUID) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "requested scope does not match the delegation")
		return
	}
	decision, err := s.proxy.Authorize(r.Context(), call, permission)
	if err != nil {
		if errors.Is(err, plugins.ErrPermissionDenied) || errors.Is(err, authorization.ErrInvalidPermission) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "permission denied")
			return
		}
		writeError(w, http.StatusUnavailableForLegalReasons, "DEPENDENCY_UNAVAILABLE", "authorization unavailable")
		return
	}
	writeJSON(w, http.StatusOK, authorizeResponse{Allowed: decision.Allowed, Reason: decision.Reason})
}

func (s *Server) handleBoardContext(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var request boardContextRequest
	if !decode(w, r, s.maxBody, &request) {
		return
	}
	boardID, err := uuid.Parse(request.BoardUUID)
	if err != nil || boardID == uuid.Nil {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "boardUuid is required")
		return
	}
	if call.Delegation.Scope.BoardID == uuid.Nil || call.Delegation.Scope.BoardID != boardID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "board is outside the delegation scope")
		return
	}
	if request.ModuleUUID != nil {
		moduleID, err := uuid.Parse(*request.ModuleUUID)
		if err != nil || moduleID == uuid.Nil {
			writeError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "moduleUuid is invalid")
			return
		}
		if call.Delegation.Scope.ModuleID == nil || *call.Delegation.Scope.ModuleID != moduleID {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "module is outside the delegation scope")
			return
		}
	}
	context, err := s.proxy.BoardContext(r.Context(), call, boardID)
	if err != nil {
		if errors.Is(err, plugins.ErrPermissionDenied) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "permission denied")
			return
		}
		if errors.Is(err, authorization.ErrBoardNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "board not found")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "board context unavailable")
		return
	}
	response := boardContextResponse{
		BoardUUID:   context.BoardID.String(),
		SubjectType: context.SubjectType,
		Permissions: make([]string, 0, len(context.Permissions)),
	}
	if context.ModuleID != nil {
		value := context.ModuleID.String()
		response.ModuleUUID = &value
	}
	if context.SubjectID != uuid.Nil {
		response.SubjectUUID = context.SubjectID.String()
	}
	for _, permission := range context.Permissions {
		response.Permissions = append(response.Permissions, string(permission))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var request publishEventRequest
	if !decode(w, r, s.maxBody, &request) {
		return
	}
	if err := s.proxy.PublishEvent(r.Context(), call, request.Event); err != nil {
		if errors.Is(err, plugins.ErrPermissionDenied) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "event publication denied")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "event rejected")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// authenticate requires service token + delegation and proves both belong to
// the same plugin. It never reads a user/scope from the request body.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (plugins.CallContext, bool) {
	parts := strings.Fields(strings.TrimSpace(r.Header.Get("Authorization")))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Plugin") || parts[1] == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "plugin service token required")
		return plugins.CallContext{}, false
	}
	identity, err := s.verifier.VerifyServiceToken(r.Context(), parts[1])
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "plugin service token invalid")
		return plugins.CallContext{}, false
	}
	delegationToken := strings.TrimSpace(r.Header.Get(protocol.HeaderDelegation))
	if delegationToken == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "core delegation token required")
		return plugins.CallContext{}, false
	}
	delegation, err := s.proxy.VerifyDelegation(r.Context(), delegationToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "delegation token invalid or expired")
		return plugins.CallContext{}, false
	}
	if delegation.PluginID != identity.PluginID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "delegation does not belong to this plugin")
		return plugins.CallContext{}, false
	}
	return plugins.CallContext{
		Identity:   identity,
		Delegation: delegation,
		UserID:     delegation.UserID,
		Scope:      delegation.Scope,
		RequestID:  r.Header.Get(protocol.HeaderRequestID),
	}, true
}

func scopeHintMatches(scope authorization.Scope, boardUUID, moduleUUID *string) bool {
	if boardUUID != nil {
		boardID, err := uuid.Parse(*boardUUID)
		if err != nil || boardID != scope.BoardID {
			return false
		}
	} else if scope.BoardID != uuid.Nil {
		return false
	}
	if moduleUUID != nil {
		moduleID, err := uuid.Parse(*moduleUUID)
		if err != nil || scope.ModuleID == nil || *scope.ModuleID != moduleID {
			return false
		}
	} else if scope.ModuleID != nil {
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, maxBody int64, target any) bool {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
	})
}
