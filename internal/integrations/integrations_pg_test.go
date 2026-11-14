package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/docker"
	"github.com/nrf24l01/actfb/internal/runtime/kubernetes"
	"github.com/nrf24l01/actfb/internal/runtime/kubevirt"
	"github.com/nrf24l01/actfb/internal/runtime/proxmox"
)

type fakeTester struct {
	ok     bool
	fail   error
	lastID uuid.UUID
	called int
}

func (f *fakeTester) Test(_ context.Context, id uuid.UUID) (TestResult, error) {
	f.called++
	f.lastID = id
	if f.fail != nil {
		return TestResult{}, f.fail
	}
	return TestResult{OK: f.ok, Message: "tested"}, nil
}

func newIntegrationService(t *testing.T, db *postgres.DB, auth authorization.Authorizer, tester ConnectionTester) *Service {
	t.Helper()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	sealer, err := secrets.NewCipher(key, 3)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := runtime.NewRegistry(mustDrivers(t)...)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, auth, WithSealer(sealer, 3),
		WithURLPolicy(URLPolicy{AllowedHosts: []string{"pve.example.com"}}),
		WithConnectionTester(tester), WithRuntime(registry))
}

func mustDrivers(t *testing.T) []runtime.Driver {
	t.Helper()
	dockerDriver, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	return []runtime.Driver{dockerDriver, proxmox.New(), kubernetes.New(), kubevirt.New()}
}

func seedAdmin(t *testing.T, db *postgres.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['admin']::text[])
	`, id, "admin-"+id.String()[:8], id.String()[:8]+"@example.test"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIntegrationCRUDEncryptedCredentialsRealPostgres(t *testing.T) {
	db, cleanup := testIntegrationDB(t)
	defer cleanup()
	admin := seedAdmin(t, db)
	auth := authorization.NewAuthorizer(db)
	tester := &fakeTester{ok: true}
	service := newIntegrationService(t, db, auth, tester)
	actor := authorization.Principal{UserID: admin}
	ctx := context.Background()

	created, err := service.Create(ctx, actor, CreateInput{
		Type: TypeProxmox, Name: "pve-main", BaseURL: "https://pve.example.com:8006",
		Credentials: map[string]string{"tokenID": "root@pam!actfb", "tokenSecret": "s3cret"},
		Settings:    json.RawMessage(`{"node":"pve","vmidPool":{"from":9000,"to":9010},"templateMap":{"debian-base":9000},"network":{"kind":"bridge","bridge":"vmbr1"}}`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.UUID == uuid.Nil || !created.CredentialsConfigured || created.Status != StatusUnknown {
		t.Fatalf("unexpected integration: %+v", created)
	}
	var ciphertext []byte
	var plaintextCheck string
	if err := db.Pool.QueryRow(ctx, `SELECT credentials_ciphertext, credentials_ciphertext::text FROM integrations WHERE id=$1`, created.UUID).Scan(&ciphertext, &plaintextCheck); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || contains(plaintextCheck, "s3cret") {
		t.Fatal("credentials must be stored as ciphertext only")
	}
	// Service read model never returns credentials.
	read, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(read)
	if contains(string(serialized), "s3cret") {
		t.Fatalf("GET leaked credentials: %s", serialized)
	}
	// Only the worker helper can open the ciphertext, under the frozen AAD.
	item, sealed, err := service.LoadSealed(ctx, created.UUID)
	if err != nil || sealed == nil {
		t.Fatalf("load sealed: %v", err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	sealer, _ := secrets.NewCipher(key, 3)
	opened, err := OpenCredentials(sealer, item.UUID, *sealed)
	if err != nil || opened["tokenSecret"] != "s3cret" {
		t.Fatalf("open credentials: %v %+v", err, opened)
	}
	if _, err := OpenCredentials(sealer, uuid.New(), *sealed); err == nil {
		t.Fatal("ciphertext must not open under another integration AAD")
	}

	result, err := service.TestConnection(ctx, actor, created.UUID)
	if err != nil || !result.OK || tester.called != 1 || tester.lastID != created.UUID {
		t.Fatalf("test connection: %v %+v", err, result)
	}
	afterTest, _ := service.Get(ctx, actor, created.UUID)
	if afterTest.Status != StatusConnected {
		t.Fatalf("status = %s", afterTest.Status)
	}

	// Unknown settings fields are rejected before touching the database.
	if _, err := service.Create(ctx, actor, CreateInput{
		Type: TypeProxmox, Name: "pve-bad", BaseURL: "https://pve.example.com",
		Credentials: map[string]string{"tokenID": "a", "tokenSecret": "b"},
		Settings:    json.RawMessage(`{"node":"pve","vmidPool":{"from":1,"to":2},"templateMap":{},"unknown":1}`),
	}); err == nil {
		t.Fatal("unknown settings field must be rejected")
	}
	// Non-allowlisted provider host is rejected.
	if _, err := service.Create(ctx, actor, CreateInput{
		Type: TypeProxmox, Name: "pve-other", BaseURL: "https://evil.example.com",
		Credentials: map[string]string{"tokenID": "a", "tokenSecret": "b"},
		Settings:    json.RawMessage(`{"node":"pve","vmidPool":{"from":9000,"to":9010},"templateMap":{"x":9000}}`),
	}); err == nil || !IsCode(err, CodeBaseURLNotAllowed) {
		t.Fatalf("SSRF host must be rejected, got %v", err)
	}

	// An integration referenced by a repository binding cannot be deleted.
	var repositoryBindingID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO repository_bindings (integration_id, repository_ref, branch, created_by)
		VALUES ($1,'team/repo','main',$2) RETURNING id
	`, created.UUID, admin).Scan(&repositoryBindingID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, actor, created.UUID); err == nil || !IsCode(err, CodeInUse) {
		t.Fatalf("in-use integration must not be deleted, got %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM repository_bindings WHERE id=$1`, repositoryBindingID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, actor, created.UUID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := service.Get(ctx, actor, created.UUID); err == nil {
		t.Fatal("deleted integration is still readable")
	}
}

func TestIntegrationFailedTestMarksError(t *testing.T) {
	db, cleanup := testIntegrationDB(t)
	defer cleanup()
	admin := seedAdmin(t, db)
	tester := &fakeTester{fail: errors.New("provider offline")}
	service := newIntegrationService(t, db, authorization.NewAuthorizer(db), tester)
	actor := authorization.Principal{UserID: admin}
	created, err := service.Create(context.Background(), actor, CreateInput{
		Type: TypeKubernetes, Name: "k8s", BaseURL: "https://pve.example.com",
		Credentials: map[string]string{"token": "token"},
		Settings:    json.RawMessage(`{"namespace":"actfb","podPidsLimitVerified":true,"network":{"kind":"namespace"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestConnection(context.Background(), actor, created.UUID); err == nil || !IsCode(err, CodeConnectionFailed) {
		t.Fatalf("failed connection must be an error, got %v", err)
	}
	after, _ := service.Get(context.Background(), actor, created.UUID)
	if after.Status != StatusError {
		t.Fatalf("status = %s", after.Status)
	}
}

func TestIntegrationNoTesterIsDependencyFailure(t *testing.T) {
	db, cleanup := testIntegrationDB(t)
	defer cleanup()
	admin := seedAdmin(t, db)
	service := newIntegrationService(t, db, authorization.NewAuthorizer(db), nil)
	actor := authorization.Principal{UserID: admin}
	created, err := service.Create(context.Background(), actor, CreateInput{
		Type: TypeKubernetes, Name: "k8s2", BaseURL: "https://pve.example.com",
		Credentials: map[string]string{"token": "token"},
		Settings:    json.RawMessage(`{"namespace":"actfb","podPidsLimitVerified":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.TestConnection(context.Background(), actor, created.UUID)
	if err == nil || !IsCode(err, CodeDependencyUnavail) {
		t.Fatalf("missing worker transport must be a dependency error, got %v", err)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

var _ = deployment.DriverProxmox
