package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/users"
)

// rawInstanceBody parses a JSON body into the raw presence map the handler uses
// to distinguish an omitted field from an explicit null.
func rawInstanceBody(t *testing.T, body string) rawBody {
	t.Helper()
	var raw rawBody
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	return raw
}

func TestTaskInstanceInputLifetimePresence(t *testing.T) {
	// Omitted: nothing is set, so create inherits and patch preserves.
	var omitted models.TaskInstanceConfig
	if err := json.Unmarshal([]byte(`{"enabled":true}`), &omitted); err != nil {
		t.Fatal(err)
	}
	input := taskInstanceInput(&omitted, rawInstanceBody(t, `{"instance":{"enabled":true}}`))
	if input.TTLSeconds.Set || input.MaxTTLSeconds.Set || input.ExtensionEnabled.Set {
		t.Fatalf("omitted lifetime fields must stay unset: %+v", input)
	}

	// Explicit null: set with a nil value, which resets to inherit.
	var nulls models.TaskInstanceConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"ttlSeconds":null,"extension":null}`), &nulls); err != nil {
		t.Fatal(err)
	}
	input = taskInstanceInput(&nulls, rawInstanceBody(t, `{"instance":{"enabled":true,"ttlSeconds":null,"extension":null}}`))
	if !input.TTLSeconds.Set || input.TTLSeconds.Value != nil {
		t.Fatalf("explicit null ttlSeconds must be a set/nil override: %+v", input.TTLSeconds)
	}
	if !input.ExtensionEnabled.Set || input.ExtensionEnabled.Value != nil {
		t.Fatalf("explicit null extension must be a set/nil override: %+v", input.ExtensionEnabled)
	}
	if !input.ExtensionSeconds.Set || !input.ExtensionMaxCount.Set {
		t.Fatalf("explicit null extension must reset every extension field: %+v", input)
	}

	// Present values.
	var values models.TaskInstanceConfig
	body := `{"enabled":true,"ttlSeconds":1800,"maxTtlSeconds":7200,"extension":{"enabled":true,"seconds":900,"maxCount":6}}`
	if err := json.Unmarshal([]byte(body), &values); err != nil {
		t.Fatal(err)
	}
	input = taskInstanceInput(&values, rawInstanceBody(t, `{"instance":`+body+`}`))
	if !input.TTLSeconds.Set || input.TTLSeconds.Value == nil || *input.TTLSeconds.Value != 1800 {
		t.Fatalf("ttlSeconds not mapped: %+v", input.TTLSeconds)
	}
	if !input.MaxTTLSeconds.Set || input.MaxTTLSeconds.Value == nil || *input.MaxTTLSeconds.Value != 7200 {
		t.Fatalf("maxTtlSeconds not mapped: %+v", input.MaxTTLSeconds)
	}
	if !input.ExtensionEnabled.Set || input.ExtensionEnabled.Value == nil || !*input.ExtensionEnabled.Value {
		t.Fatalf("extension.enabled not mapped: %+v", input.ExtensionEnabled)
	}
	if input.ExtensionSeconds.Value == nil || *input.ExtensionSeconds.Value != 900 {
		t.Fatalf("extension.seconds not mapped: %+v", input.ExtensionSeconds)
	}
	if input.ExtensionMaxCount.Value == nil || *input.ExtensionMaxCount.Value != 6 {
		t.Fatalf("extension.maxCount not mapped: %+v", input.ExtensionMaxCount)
	}
}

func TestModuleInstanceSettingsMapping(t *testing.T) {
	if moduleInstanceSettings(nil) != nil {
		t.Fatal("nil settings must stay nil")
	}
	enabled := true
	seconds := int64(900)
	count := int64(6)
	defaultTTL := int64(1800)
	maxTTL := int64(7200)
	concurrent := int64(3)
	settings := moduleInstanceSettings(&models.TaskInstanceModuleSettings{
		DefaultTTLSeconds: &defaultTTL, MaxTTLSeconds: &maxTTL,
		MaxConcurrentInstancesPerSubject: &concurrent, ResetEnabled: &enabled,
		Extension: &models.TaskInstanceModuleSettingsExtension{Enabled: &enabled, Seconds: &seconds, MaxCount: &count},
	})
	if settings.DefaultTTLSeconds != 1800 || settings.MaxTTLSeconds != 7200 || settings.MaxConcurrentInstancesPerSubject != 3 {
		t.Fatalf("settings not mapped: %+v", settings)
	}
	if !settings.ResetEnabled || settings.Extension == nil || settings.Extension.MaxCount != 6 {
		t.Fatalf("extension not mapped: %+v", settings.Extension)
	}

	roundTrip := taskInstanceSettingsModel(settings)
	if roundTrip == nil || roundTrip.MaxTTLSeconds == nil || *roundTrip.MaxTTLSeconds != 7200 {
		t.Fatalf("reverse mapping failed: %+v", roundTrip)
	}
	if roundTrip.Extension == nil || roundTrip.Extension.Seconds == nil || *roundTrip.Extension.Seconds != 900 {
		t.Fatalf("reverse extension mapping failed: %+v", roundTrip.Extension)
	}
}

type httpTestBinder struct{ calls int }

func (b *httpTestBinder) BindTaskInstance(context.Context, authorization.Principal, uuid.UUID, uuid.UUID, uuid.UUID, boards.InstanceBindingInput) (uuid.UUID, error) {
	b.calls++
	return uuid.New(), nil
}

// N-B over real HTTP: a zero-budget module must accept a task whose lifetime
// override merely narrows extensions off, and still reject one that grants credits.
func TestHTTPZeroBudgetModuleAcceptsNarrowingTaskOverride(t *testing.T) {
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the instance lifetime HTTP test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	defer cleanup()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "life-http-access", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "life-http-refresh", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)), 1)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(512),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("life-http-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc),
		boards.WithSealer(sealer), boards.WithOutbox(events.NewOutbox(db)),
		boards.WithInstanceBinder(&httpTestBinder{}))
	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)

	_, adminToken := registerHTTPUser(t, ctx, userService, db, "life_http_admin", true)
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	board := decode[oapimodels.AdminBoard](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "life-http", "name": "Life HTTP", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE", "startsAt": start, "endsAt": end,
		"rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	module := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Instances", "type": "TASKS", "allowedTaskTypes": []string{"INSTANCE"}, "enabled": true,
			"taskInstanceSettings": map[string]any{
				"maxConcurrentInstancesPerSubject": 2, "defaultTtlSeconds": 3600, "maxTtlSeconds": 14400,
				"resetEnabled": true,
				"extension":    map[string]any{"enabled": true, "seconds": 900, "maxCount": 0},
			},
		}, nil, http.StatusCreated), http.StatusCreated)
	moduleID := string(*module.UUID)
	if module.TaskInstanceSettings == nil || module.TaskInstanceSettings.Extension == nil ||
		*module.TaskInstanceSettings.Extension.MaxCount != 0 {
		t.Fatalf("zero-budget module settings not persisted: %+v", module.TaskInstanceSettings)
	}

	path := "/api/v2/admin/boards/" + boardID + "/modules/" + moduleID + "/tasks"
	templateID := uuid.New().String()
	base := map[string]any{
		"title": "Narrowed", "type": "INSTANCE", "category": "web", "description": "d",
		"points": 10, "visible": true,
		"submission": map[string]any{"mode": "TEXT", "checker": "MANUAL", "text": map[string]any{"required": true, "minLength": 1, "maxLength": 1000}},
	}
	withInstance := func(extension map[string]any) map[string]any {
		body := map[string]any{}
		for key, value := range base {
			body[key] = value
		}
		body["instance"] = map[string]any{
			"enabled": true, "deploymentTemplateUuid": templateID, "templateVersion": 1,
			"ttlSeconds": 1800, "maxTtlSeconds": 3600, "extension": extension,
		}
		return body
	}

	// Positive: extensions explicitly off grants nothing.
	created := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodPost, path, adminToken,
		withInstance(map[string]any{"enabled": false, "seconds": 900, "maxCount": 0}), nil, http.StatusCreated), http.StatusCreated)
	if created.InstanceConfig == nil || created.InstanceConfig.Extension == nil ||
		*created.InstanceConfig.Extension.MaxCount != 0 {
		t.Fatalf("narrowing override not returned: %+v", created.InstanceConfig)
	}

	// Negative: one credit over a zero budget is still a 422.
	doJSON(t, router, http.MethodPost, path, adminToken,
		withInstance(map[string]any{"enabled": false, "seconds": 900, "maxCount": 1}), nil, http.StatusUnprocessableEntity)

	// A module update that disables extensions is accepted while the only task
	// override narrows.
	updated := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPatch,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID, adminToken, map[string]any{
			"taskInstanceSettings": map[string]any{
				"maxConcurrentInstancesPerSubject": 2, "defaultTtlSeconds": 3600, "maxTtlSeconds": 14400,
				"resetEnabled": true,
				"extension":    map[string]any{"enabled": false, "seconds": 900, "maxCount": 0},
			},
		}, nil, http.StatusOK), http.StatusOK)
	if updated.TaskInstanceSettings == nil || updated.TaskInstanceSettings.Extension == nil ||
		*updated.TaskInstanceSettings.Extension.MaxCount != 0 {
		t.Fatalf("module update did not persist maxCount 0: %+v", updated.TaskInstanceSettings)
	}
}
