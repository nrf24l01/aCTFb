// Command example.score_events is a separately built aCTFb plugin. It consumes
// submission.judged from the core event dispatcher, writes only its own private
// schema, dedupes by event UUID and never mutates core scores or verdicts.
package main

import (
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

	"github.com/jackc/pgx/v5/pgxpool"
)

const pluginID = "example.score_events"

type invokeEnvelope struct {
	Protocol  string  `json:"protocol"`
	RequestID string  `json:"requestId"`
	Operation string  `json:"operation"`
	PluginID  string  `json:"pluginId"`
	UserUUID  *string `json:"userUuid"`
	Scope     struct {
		BoardUUID  *string `json:"boardUuid"`
		ModuleUUID *string `json:"moduleUuid"`
	} `json:"scope"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      map[string][]string `json:"query"`
	Headers    map[string]string   `json:"headers"`
	BodyBase64 string              `json:"bodyBase64"`
}

type invokeReply struct {
	Protocol   string            `json:"protocol"`
	RequestID  string            `json:"requestId"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"bodyBase64"`
}

type coreEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	OccurredAt  time.Time       `json:"occurredAt"`
	BoardID     *string         `json:"boardId"`
	ModuleID    *string         `json:"moduleId"`
	SubjectType string          `json:"subjectType"`
	SubjectID   *string         `json:"subjectId"`
	Payload     json.RawMessage `json:"payload"`
}

type judgedPayload struct {
	SubmissionUUID string    `json:"submissionUuid"`
	BoardUUID      string    `json:"boardUuid"`
	ModuleUUID     string    `json:"moduleUuid"`
	TaskUUID       string    `json:"taskUuid"`
	SubjectType    string    `json:"subjectType"`
	SubjectUUID    string    `json:"subjectUuid"`
	Status         string    `json:"status"`
	ScoreAwarded   *int      `json:"scoreAwarded"`
	OccurredAt     time.Time `json:"occurredAt"`
}

type app struct {
	db    *pgxpool.Pool
	token string
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
	application := &app{db: pool, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", application.health)
	mux.HandleFunc("/v1/invoke", application.invoke)
	mux.HandleFunc("/v1/events", application.events)
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	_ = os.Chmod(socketPath, 0o600)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("example.score_events listening on %s", socketPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
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

// events verifies the shared delivery headers, dedupes by event UUID, and
// updates only plugin-owned statistics.
func (a *app) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Actfb-Event-Id") == "" || r.Header.Get("X-Actfb-Signature") == "" {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	var envelope struct {
		Protocol string    `json:"protocol"`
		Event    coreEvent `json:"event"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&envelope); err != nil || envelope.Protocol != "1" {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	if envelope.Event.ID != r.Header.Get("X-Actfb-Event-Id") || envelope.Event.Type != "submission.judged" {
		http.Error(w, "event mismatch", http.StatusBadRequest)
		return
	}
	var payload judgedPayload
	if err := json.Unmarshal(envelope.Event.Payload, &payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if err := a.record(r.Context(), envelope.Event, payload); err != nil {
		log.Printf("record event %s: %v", envelope.Event.ID, err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) record(ctx context.Context, event coreEvent, payload judgedPayload) error {
	boardID := payload.BoardUUID
	moduleID := payload.ModuleUUID
	if envelopeBoard := event.BoardID; envelopeBoard != nil && *envelopeBoard != "" {
		boardID = *envelopeBoard
	}
	if envelopeModule := event.ModuleID; envelopeModule != nil && *envelopeModule != "" {
		moduleID = *envelopeModule
	}
	if boardID == "" || moduleID == "" || event.ID == "" {
		return errors.New("event is missing board/module/id")
	}
	subjectType := payload.SubjectType
	if subjectType == "" {
		subjectType = event.SubjectType
	}
	subjectID := payload.SubjectUUID
	if subjectID == "" && event.SubjectID != nil {
		subjectID = *event.SubjectID
	}
	if subjectType == "" || subjectID == "" {
		return errors.New("event is missing subject")
	}
	score := 0
	if payload.ScoreAwarded != nil {
		score = *payload.ScoreAwarded
	}

	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		INSERT INTO score_event_seen (event_id, event_type, submission_id, board_id, module_id, task_id, subject_type, subject_id, status, score, occurred_at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8::uuid, $9, $10, $11)
		ON CONFLICT (event_id) DO NOTHING
	`, event.ID, event.Type, payload.SubmissionUUID, boardID, moduleID, payload.TaskUUID, subjectType, subjectID, payload.Status, score, payload.OccurredAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx) // duplicate delivery: no double counting
	}
	accepted := 0
	if strings.EqualFold(payload.Status, "ACCEPTED") {
		accepted = 1
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO score_stats (board_id, module_id, subject_type, subject_id, judged_count, accepted_count, total_score, last_event_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, 1, $5, $6, now())
		ON CONFLICT (board_id, module_id, subject_type, subject_id) DO UPDATE
		SET judged_count = score_stats.judged_count + 1,
		    accepted_count = score_stats.accepted_count + EXCLUDED.accepted_count,
		    total_score = score_stats.total_score + EXCLUDED.total_score,
		    last_event_at = now()
	`, boardID, moduleID, subjectType, subjectID, accepted, score); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *app) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request invokeEnvelope
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Protocol != "1" || request.RequestID == "" {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	boardID, rest, ok := parseBoardPath(request.Path)
	if !ok || request.Scope.BoardUUID == nil || *request.Scope.BoardUUID != boardID {
		writeInvoke(w, request.RequestID, 404, map[string]string{"error": "not found"})
		return
	}
	switch {
	case request.Method == http.MethodGet && rest == "/score-events":
		a.dashboard(w, r.Context(), request.RequestID, boardID)
	case request.Method == http.MethodGet && rest == "/score-events/admin":
		a.admin(w, r.Context(), request.RequestID, boardID)
	default:
		writeInvoke(w, request.RequestID, 404, map[string]string{"error": "not found"})
	}
}

func (a *app) dashboard(w http.ResponseWriter, ctx context.Context, requestID, boardID string) {
	rows, err := a.db.Query(ctx, `
		SELECT module_id::text, subject_type, subject_id::text, judged_count, accepted_count, total_score, last_event_at
		FROM score_stats WHERE board_id = $1::uuid
		ORDER BY total_score DESC, subject_id
	`, boardID)
	if err != nil {
		writeInvoke(w, requestID, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var moduleID, subjectType, subjectID string
		var judged, accepted, score int
		var last time.Time
		if err := rows.Scan(&moduleID, &subjectType, &subjectID, &judged, &accepted, &score, &last); err != nil {
			writeInvoke(w, requestID, 500, map[string]string{"error": "database error"})
			return
		}
		items = append(items, map[string]any{
			"moduleUuid": moduleID, "subjectType": subjectType, "subjectUuid": subjectID,
			"judgedCount": judged, "acceptedCount": accepted, "totalScore": score,
			"lastEventAt": last.UTC().Format(time.RFC3339),
		})
	}
	writeInvoke(w, requestID, 200, map[string]any{"items": items, "source": "submission.judged (plugin-owned stats)"})
}

func (a *app) admin(w http.ResponseWriter, ctx context.Context, requestID, boardID string) {
	var eventCount, subjectCount int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM score_event_seen WHERE board_id = $1::uuid`, boardID).Scan(&eventCount); err != nil {
		writeInvoke(w, requestID, 500, map[string]string{"error": "database error"})
		return
	}
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM score_stats WHERE board_id = $1::uuid`, boardID).Scan(&subjectCount); err != nil {
		writeInvoke(w, requestID, 500, map[string]string{"error": "database error"})
		return
	}
	writeInvoke(w, requestID, 200, map[string]any{
		"eventsSeen": eventCount, "subjects": subjectCount,
		"dedupeKey": "event_id", "canMutateCoreScore": false,
	})
}

func parseBoardPath(path string) (string, string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 3 || segments[0] != "boards" || len(segments[1]) != 36 {
		return "", "", false
	}
	return segments[1], "/" + strings.Join(segments[2:], "/"), true
}

func writeInvoke(w http.ResponseWriter, requestID string, status int, body any) {
	encoded, _ := json.Marshal(body)
	writeJSON(w, http.StatusOK, invokeReply{
		Protocol: "1", RequestID: requestID, Status: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		BodyBase64: base64.StdEncoding.EncodeToString(encoded),
	})
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
