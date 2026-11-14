// Command example.custom_checker is a separately built, text-only plugin
// checker. It proves the versioned checker HTTP operation without executing
// student code. Any future student-code execution must run in a trusted
// isolated worker, not in this plugin process.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const pluginID = "example.custom_checker"

type invokeEnvelope struct {
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

type checkerPayload struct {
	BoardID      string          `json:"boardId"`
	ModuleID     string          `json:"moduleId"`
	TaskID       string          `json:"taskId"`
	SubmissionID string          `json:"submissionId"`
	SubjectType  string          `json:"subjectType"`
	SubjectID    string          `json:"subjectId"`
	Text         string          `json:"text"`
	MaxPoints    int             `json:"maxPoints"`
	Config       json.RawMessage `json:"config,omitempty"`
}

type checkerResult struct {
	Verdict string `json:"verdict"`
	Score   *int   `json:"score,omitempty"`
	Message string `json:"message,omitempty"`
}

type invokeReply struct {
	Protocol   string            `json:"protocol"`
	RequestID  string            `json:"requestId"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"bodyBase64"`
}

type app struct {
	token      string
	sdkURL     string
	httpClient *http.Client
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	socketPath := strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_SOCKET"))
	token := strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_SERVICE_TOKEN"))
	if socketPath == "" || token == "" {
		log.Fatal("ACTFB_PLUGIN_SOCKET and ACTFB_PLUGIN_SERVICE_TOKEN are required")
	}
	application := &app{
		token:  token,
		sdkURL: strings.TrimRight(strings.TrimSpace(os.Getenv("ACTFB_PLUGIN_CORE_SDK_URL")), "/"),
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", application.health)
	mux.HandleFunc("/v1/invoke", application.invoke)
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen on plugin socket: %v", err)
	}
	_ = os.Chmod(socketPath, 0o600)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("example.custom_checker listening on %s", socketPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request invokeEnvelope
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	if request.Protocol != "1" || request.Operation != "checker" || request.RequestID == "" ||
		(request.PluginID != pluginID && request.PluginID != "example.custom_checker") {
		http.Error(w, "protocol mismatch", http.StatusBadRequest)
		return
	}
	body, err := base64.StdEncoding.DecodeString(request.BodyBase64)
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	var payload checkerPayload
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		http.Error(w, "invalid checker payload", http.StatusBadRequest)
		return
	}
	if request.UserUUID == nil || payload.BoardID == "" || payload.ModuleID == "" || payload.TaskID == "" {
		http.Error(w, "missing checker context", http.StatusBadRequest)
		return
	}
	// Exercise the core-issued delegation when the SDK endpoint is configured.
	// The plugin never trusts a body-supplied user identity.
	if a.sdkURL != "" {
		if err := a.verifyDelegation(r.Context(), r.Header.Get("X-Actfb-Delegation"), payload.BoardID, *request.UserUUID); err != nil {
			http.Error(w, "delegation rejected", http.StatusForbidden)
			return
		}
	}
	result := decide(payload)
	encoded, _ := json.Marshal(result)
	writeJSON(w, http.StatusOK, invokeReply{
		Protocol: "1", RequestID: request.RequestID, Status: http.StatusOK,
		Headers:    map[string]string{"Content-Type": "application/json"},
		BodyBase64: base64.StdEncoding.EncodeToString(encoded),
	})
}

func decide(payload checkerPayload) checkerResult {
	text := strings.TrimSpace(payload.Text)
	switch text {
	case "BENIGN_OK":
		score := 0
		return checkerResult{Verdict: "ACCEPTED", Score: &score, Message: "benign fixture accepted"}
	case "MALICIOUS_SCORE":
		score := payload.MaxPoints + 1_000_000
		return checkerResult{Verdict: "ACCEPTED", Score: &score, Message: "attempts to award arbitrary points"}
	case "TIMEOUT":
		time.Sleep(2 * time.Second)
		return checkerResult{Verdict: "ACCEPTED", Message: "late response"}
	default:
		return checkerResult{Verdict: "REJECTED", Message: "fixture text rejected"}
	}
}

func (a *app) verifyDelegation(ctx context.Context, delegation, boardID, userID string) error {
	if delegation == "" {
		return errors.New("delegation required")
	}
	body, _ := json.Marshal(map[string]string{"boardUuid": boardID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.sdkURL+"/board-context", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Plugin "+a.token)
	request.Header.Set("X-Actfb-Delegation", delegation)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("board-context rejected")
	}
	var contextResult struct {
		SubjectType string `json:"subjectType"`
		SubjectUUID string `json:"subjectUuid"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&contextResult); err != nil {
		return err
	}
	if contextResult.SubjectType == "USER" && contextResult.SubjectUUID != userID {
		return errors.New("delegation user mismatch")
	}
	return nil
}

func (a *app) authorized(r *http.Request) bool {
	provided := strings.TrimSpace(r.Header.Get("X-Actfb-Service-Token"))
	return a.token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
