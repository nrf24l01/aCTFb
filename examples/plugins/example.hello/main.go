// Command example.hello is an installable aCTFb plugin backend. It is built and
// distributed separately from core and speaks only the versioned http-v1
// protocol over the private Unix socket supplied by the plugin manager.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	protocolVersion = "1"
	pluginID        = "example.hello"
	socketMode      = 0o600
)

type envelope struct {
	Protocol   string              `json:"protocol"`
	RequestID  string              `json:"requestId"`
	Operation  string              `json:"operation"`
	PluginID   string              `json:"pluginId"`
	UserUUID   *string             `json:"userUuid"`
	Scope      scope               `json:"scope"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      map[string][]string `json:"query"`
	Headers    map[string]string   `json:"headers"`
	BodyBase64 string              `json:"bodyBase64"`
}

type scope struct {
	BoardUUID  *string `json:"boardUuid"`
	ModuleUUID *string `json:"moduleUuid"`
}

type reply struct {
	Protocol   string            `json:"protocol"`
	RequestID  string            `json:"requestId"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"bodyBase64"`
}

type app struct {
	db         *pgxpool.Pool
	token      string
	sdkURL     string
	pluginID   string
	httpClient *http.Client
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	socketPath := strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_SOCKET"))
	dsn := strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_DSN"))
	token := strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_SERVICE_TOKEN"))
	if socketPath == "" || dsn == "" || token == "" {
		log.Fatal("ACTFB_PLUGIN_SOCKET, ACTFB_PLUGIN_DSN and ACTFB_PLUGIN_SERVICE_TOKEN are required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect private plugin DB: %v", err)
	}
	defer pool.Close()
	application := &app{
		db:         pool,
		token:      token,
		sdkURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_CORE_SDK_URL")), "/"),
		pluginID:   pluginID,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", application.health)
	mux.HandleFunc("/v1/invoke", application.invoke)
	mux.HandleFunc("/v1/events", application.events)

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen on plugin socket: %v", err)
	}
	if err := os.Chmod(socketPath, socketMode); err != nil {
		log.Fatalf("chmod plugin socket: %v", err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("example.hello listening on %s", socketPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !a.authorizedRequest(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.db.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Actfb-Event-Id") == "" || r.Header.Get("X-Actfb-Signature") == "" {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	// example.hello has no event subscriptions; acknowledge only well-formed
	// signed deliveries so core does not retry an irrelevant event forever.
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.authorizedRequest(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request envelope
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	if request.Protocol != protocolVersion || request.RequestID == "" || (request.PluginID != a.pluginID && request.PluginID != "example.hello") {
		http.Error(w, "protocol mismatch", http.StatusBadRequest)
		return
	}
	body, err := base64.StdEncoding.DecodeString(request.BodyBase64)
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	status, contentType, responseBody := a.dispatch(r.Context(), request, body, r.Header.Get("X-Actfb-Delegation"))
	writeJSON(w, http.StatusOK, reply{
		Protocol:   protocolVersion,
		RequestID:  request.RequestID,
		Status:     status,
		Headers:    map[string]string{"Content-Type": contentType},
		BodyBase64: base64.StdEncoding.EncodeToString(responseBody),
	})
}

func (a *app) dispatch(ctx context.Context, request envelope, body []byte, delegation string) (int, string, []byte) {
	boardID, rest, ok := parseBoardPath(request.Path)
	if !ok {
		return 404, "application/json", mustJSON(map[string]string{"error": "not found"})
	}
	if request.Scope.BoardUUID == nil || *request.Scope.BoardUUID != boardID {
		return 400, "application/json", mustJSON(map[string]string{"error": "scope mismatch"})
	}
	permission := "plugin.example.hello.view"
	if strings.Contains(rest, "/admin") || strings.Contains(rest, "/settings") {
		permission = "plugin.example.hello.manage"
	}
	if a.sdkURL != "" {
		allowed, err := a.authorize(ctx, delegation, permission, boardID)
		if err != nil || !allowed {
			return 403, "application/json", mustJSON(map[string]string{"error": "permission denied"})
		}
	}

	switch {
	case request.Method == http.MethodGet && rest == "/hello":
		rows, err := a.db.Query(ctx, `
			SELECT id::text, body, COALESCE(author_user_id::text, ''), created_at
			FROM hello_messages WHERE board_id = $1 ORDER BY created_at DESC LIMIT 100
		`, boardID)
		if err != nil {
			return 500, "application/json", mustJSON(map[string]string{"error": "database error"})
		}
		defer rows.Close()
		messages := make([]map[string]string, 0)
		for rows.Next() {
			var id, text, author string
			var createdAt time.Time
			if err := rows.Scan(&id, &text, &author, &createdAt); err != nil {
				return 500, "application/json", mustJSON(map[string]string{"error": "database error"})
			}
			messages = append(messages, map[string]string{"id": id, "body": text, "authorUuid": author, "createdAt": createdAt.UTC().Format(time.RFC3339)})
		}
		return 200, "application/json", mustJSON(map[string]any{"messages": messages, "plugin": pluginID})
	case request.Method == http.MethodPost && rest == "/hello/messages":
		var payload struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Body) == "" || len(payload.Body) > 2000 {
			return 422, "application/json", mustJSON(map[string]string{"error": "body is required (max 2000 characters)"})
		}
		if request.UserUUID == nil {
			return 403, "application/json", mustJSON(map[string]string{"error": "user identity required"})
		}
		if _, err := a.db.Exec(ctx, `
			INSERT INTO hello_messages (board_id, author_user_id, body)
			VALUES ($1, $2, $3)
		`, boardID, *request.UserUUID, strings.TrimSpace(payload.Body)); err != nil {
			return 500, "application/json", mustJSON(map[string]string{"error": "database error"})
		}
		return 201, "application/json", mustJSON(map[string]string{"status": "created"})
	case request.Method == http.MethodGet && rest == "/hello/admin":
		rows, err := a.db.Query(ctx, `SELECT key, value FROM hello_settings ORDER BY key`)
		if err != nil {
			return 500, "application/json", mustJSON(map[string]string{"error": "database error"})
		}
		defer rows.Close()
		settings := make(map[string]string)
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err == nil {
				settings[key] = value
			}
		}
		return 200, "application/json", mustJSON(map[string]any{"settings": settings, "plugin": pluginID})
	case request.Method == http.MethodPost && rest == "/hello/admin/settings":
		var payload struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Key) == "" || len(payload.Key) > 64 || len(payload.Value) > 2000 {
			return 422, "application/json", mustJSON(map[string]string{"error": "key/value required"})
		}
		if _, err := a.db.Exec(ctx, `
			INSERT INTO hello_settings (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		`, strings.TrimSpace(payload.Key), payload.Value); err != nil {
			return 500, "application/json", mustJSON(map[string]string{"error": "database error"})
		}
		return 200, "application/json", mustJSON(map[string]string{"status": "saved"})
	default:
		return 404, "application/json", mustJSON(map[string]string{"error": "not found"})
	}
}

func parseBoardPath(path string) (string, string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 3 || segments[0] != "boards" || len(segments[1]) != 36 {
		return "", "", false
	}
	return segments[1], "/" + strings.Join(segments[2:], "/"), true
}

func (a *app) authorize(ctx context.Context, delegation, permission, boardID string) (bool, error) {
	if delegation == "" {
		return false, errors.New("delegation required")
	}
	payload := map[string]string{"permission": permission, "boardUuid": boardID}
	encoded, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.sdkURL+"/authorize", bytes.NewReader(encoded))
	if err != nil {
		return false, err
	}
	request.Header.Set("Authorization", "Plugin "+a.token)
	request.Header.Set("X-Actfb-Delegation", delegation)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("sdk authorize returned %d", response.StatusCode)
	}
	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return false, err
	}
	return result.Allowed, nil
}

func (a *app) authorizedRequest(r *http.Request) bool {
	provided := strings.TrimSpace(r.Header.Get("X-Actfb-Service-Token"))
	if a.token == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"error":"internal"}`)
	}
	return encoded
}
