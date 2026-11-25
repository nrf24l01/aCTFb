package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/instances"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// countingPublisher records queued jobs so a denied action can be proven to
// publish nothing.
type countingPublisher struct {
	jobs []instances.Job
}

func (p *countingPublisher) PublishJob(_ context.Context, job instances.Job) error {
	p.jobs = append(p.jobs, job)
	return nil
}

// TestParticipantOwnABACWithRoutesRegistered is the B11 gate. The instance
// routes are really registered (no 404 shortcut) and the fixture uses a static
// driver registry with a counting publisher, so a denied start is proven by a
// 403 plus zero queued jobs and zero provider calls.
func TestParticipantOwnABACWithRoutesRegistered(t *testing.T) {
	fixture := newHTTPTestFixture(t, nil)
	ctx := context.Background()

	registry, err := runtime.NewRegistry(&httpFakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	sealer, err := secrets.NewCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &countingPublisher{}
	service, err := instances.NewService(fixture.db, authorization.NewAuthorizer(fixture.db), registry, sealer, 1,
		instances.WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}

	routerFor := func(userID uuid.UUID) *echo.Echo {
		e := echo.New()
		NewHandler(service, testAuth{userID: userID}).RegisterRoutes(e)
		return e
	}
	// The pentest environment is a participant-own instance start
	// (instance.start_own). Prove the route is really registered instead of
	// mistaking a handler 404 for an absent route.
	startPath := fixture.boardBase(fixture.moduleID) + "/pentest/environment"
	registered := false
	for _, route := range routerFor(fixture.userID).Router().Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/environment" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("participant-own instance start route is not registered")
	}

	// 1. A global admin who never joined is not a participant: start is denied
	//    and nothing is queued.
	nonParticipantAdmin := seedHTTPUser(t, fixture.db)
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE users SET roles=ARRAY['user','admin']::text[] WHERE id=$1`, nonParticipantAdmin); err != nil {
		t.Fatal(err)
	}
	before := len(publisher.jobs)
	response := requestJSON(t, routerFor(nonParticipantAdmin), http.MethodPost, startPath, "token", nil,
		map[string]string{"Idempotency-Key": "abac-nonparticipant-0001"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-participant admin start = %d, want 403: %s", response.Code, response.Body.String())
	}
	if len(publisher.jobs) != before {
		t.Fatalf("denied start queued %d job(s)", len(publisher.jobs)-before)
	}

	// 2. A joined admin whose access group was revoked loses the own action.
	//    The fixture board is PUBLIC, so make it PRIVATE + group-gated first.
	var groupID uuid.UUID
	if err := fixture.db.Pool.QueryRow(ctx, `
		INSERT INTO access_groups (name) VALUES ('abac-group-' || $1) RETURNING id
	`, uuid.NewString()[:8]).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		UPDATE boards SET visibility='PRIVATE' WHERE id=$1
	`, fixture.boardID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO board_access_groups (board_id, group_id) VALUES ($1,$2)
	`, fixture.boardID, groupID); err != nil {
		t.Fatal(err)
	}
	revokedAdmin := seedHTTPUser(t, fixture.db)
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE users SET roles=ARRAY['user','admin']::text[] WHERE id=$1`, revokedAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)
	`, fixture.boardID, revokedAdmin); err != nil {
		t.Fatal(err)
	}
	// Group member: the action is allowed.
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
	`, groupID, revokedAdmin); err != nil {
		t.Fatal(err)
	}
	allowed := requestJSON(t, routerFor(revokedAdmin), http.MethodPost, startPath, "token", nil,
		map[string]string{"Idempotency-Key": "abac-member-start-0001"})
	if allowed.Code != http.StatusAccepted && allowed.Code != http.StatusOK {
		t.Fatalf("eligible participant start = %d, want 2xx: %s", allowed.Code, allowed.Body.String())
	}
	// Revoking the group is immediate: the same session cannot start another.
	if _, err := fixture.db.Pool.Exec(ctx, `
		DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2
	`, groupID, revokedAdmin); err != nil {
		t.Fatal(err)
	}
	before = len(publisher.jobs)
	denied := requestJSON(t, routerFor(revokedAdmin), http.MethodPost, startPath, "token", nil,
		map[string]string{"Idempotency-Key": "abac-revoked-start-0001"})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("revoked-group admin start = %d, want 403: %s", denied.Code, denied.Body.String())
	}
	if len(publisher.jobs) != before {
		t.Fatalf("revoked start queued %d job(s)", len(publisher.jobs)-before)
	}

	// 3. The same revoked participant cannot read own instance state either.
	getDenied := requestJSON(t, routerFor(revokedAdmin), http.MethodGet, startPath, "token", nil, nil)
	if getDenied.Code != http.StatusForbidden {
		t.Fatalf("revoked-group own instance read = %d, want 403: %s", getDenied.Code, getDenied.Body.String())
	}

	// 4. A revoked module audience is an independent gate for an otherwise
	//    eligible participant.
	audienceUser := seedHTTPUser(t, fixture.db)
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)
	`, fixture.boardID, audienceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
	`, groupID, audienceUser); err != nil {
		t.Fatal(err)
	}
	var audienceID uuid.UUID
	if err := fixture.db.Pool.QueryRow(ctx, `
		INSERT INTO audiences (board_id, name) VALUES ($1,'abac-audience-' || $2) RETURNING id
	`, fixture.boardID, uuid.NewString()[:8]).Scan(&audienceID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'USER',$2)
	`, audienceID, audienceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3)
	`, fixture.boardID, fixture.moduleID, audienceID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		DELETE FROM audience_members WHERE audience_id=$1 AND subject_id=$2
	`, audienceID, audienceUser); err != nil {
		t.Fatal(err)
	}
	before = len(publisher.jobs)
	audienceDenied := requestJSON(t, routerFor(audienceUser), http.MethodPost, startPath, "token", nil,
		map[string]string{"Idempotency-Key": "abac-audience-start-0001"})
	if audienceDenied.Code != http.StatusForbidden {
		t.Fatalf("revoked-audience start = %d, want 403: %s", audienceDenied.Code, audienceDenied.Body.String())
	}
	if len(publisher.jobs) != before {
		t.Fatalf("revoked-audience start queued %d job(s)", len(publisher.jobs)-before)
	}

	// 5. Staff management of another subject's instance stays a separate,
	//    explicit lane. A global admin destroys the participant's instance
	//    through the admin route even though they are not a participant.
	created := requestJSON(t, routerFor(audienceUser), http.MethodPost, startPath, "token", nil,
		map[string]string{"Idempotency-Key": "abac-admin-destroy-0001"})
	if created.Code != http.StatusAccepted && created.Code != http.StatusOK {
		// The audience was revoked above, so re-add it to create the instance
		// the admin will destroy.
		if _, err := fixture.db.Pool.Exec(ctx, `
			INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'USER',$2)
		`, audienceID, audienceUser); err != nil {
			t.Fatal(err)
		}
		created = requestJSON(t, routerFor(audienceUser), http.MethodPost, startPath, "token", nil,
			map[string]string{"Idempotency-Key": "abac-admin-destroy-0002"})
	}
	if created.Code != http.StatusAccepted && created.Code != http.StatusOK {
		t.Fatalf("participant start for admin cleanup = %d: %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.UUID == "" {
		t.Fatalf("decode created instance: %v: %s", err, created.Body.String())
	}
	instanceUUID := createdBody.UUID
	adminAction := requestJSON(t, routerFor(nonParticipantAdmin), http.MethodPost,
		"/api/v2/admin/instances/"+instanceUUID+"/actions", "token",
		map[string]any{"action": "DESTROY"}, map[string]string{"Idempotency-Key": "abac-admin-destroy-0003"})
	if adminAction.Code != http.StatusAccepted && adminAction.Code != http.StatusOK {
		t.Fatalf("admin destroy of another subject's instance = %d, want 2xx: %s", adminAction.Code, adminAction.Body.String())
	}
}


