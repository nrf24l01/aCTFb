package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const (
	maxCredentials     = 16
	maxCredentialBytes = 16 << 10
	maxSettingsBytes   = 64 << 10
)

// SettingsValidator is implemented by the trusted runtime registry.
type SettingsValidator interface {
	ValidateSettings(driver string, raw json.RawMessage) []deployment.Finding
}

// Service owns integration CRUD, strict validation, credential sealing and the
// trusted-worker connection test.
type Service struct {
	store      *store
	auth       authorization.Authorizer
	sealer     secrets.Sealer
	keyVersion uint32
	policy     URLPolicy
	tester     ConnectionTester
	runtime    SettingsValidator
}

// Option configures the service.
type Option func(*Service)

// WithSealer installs the AES-256-GCM sealer and key version used for the
// credentials column.
func WithSealer(sealer secrets.Sealer, keyVersion uint32) Option {
	return func(service *Service) {
		service.sealer = sealer
		service.keyVersion = keyVersion
	}
}

// WithURLPolicy sets the provider host allowlist.
func WithURLPolicy(policy URLPolicy) Option {
	return func(service *Service) { service.policy = policy }
}

// WithConnectionTester installs the trusted-worker test transport.
func WithConnectionTester(tester ConnectionTester) Option {
	return func(service *Service) { service.tester = tester }
}

// WithRuntime installs the static driver settings validator.
func WithRuntime(validator SettingsValidator) Option {
	return func(service *Service) { service.runtime = validator }
}

// NewService constructs the integration service.
func NewService(db *postgres.DB, auth authorization.Authorizer, options ...Option) *Service {
	service := &Service{store: newStore(db), auth: auth, policy: URLPolicyFromEnv()}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// List returns a redacted page.
func (s *Service) List(ctx context.Context, actor authorization.Principal, query ListQuery) (Page, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return Page{}, err
	}
	limit, offset := normalizePagination(query.Limit, query.Offset)
	return s.store.list(ctx, limit, offset)
}

// Get returns one redacted integration.
func (s *Service) Get(ctx context.Context, actor authorization.Principal, id uuid.UUID) (*Integration, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	item, _, err := s.store.get(ctx, id)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return item, nil
}

// Create validates, seals credentials and stores the integration.
func (s *Service) Create(ctx context.Context, actor authorization.Principal, input CreateInput) (*Integration, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	category, kind, driver, err := resolveTarget(input.Type, input.Kind, input.Driver)
	if err != nil {
		return nil, err
	}
	name, err := validateName(input.Name)
	if err != nil {
		return nil, err
	}
	settings, err := s.validateSettings(category, driver, input.Settings)
	if err != nil {
		return nil, err
	}
	if err := s.policy.Validate(input.BaseURL, driver); err != nil {
		return nil, err
	}
	if err := ValidatePublication(input.Publication); err != nil {
		return nil, err
	}
	credentials, err := validateCredentials(input.Type, input.Credentials, true)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	ciphertext, err := s.sealCredentials(id, credentials)
	if err != nil {
		return nil, err
	}
	item, err := s.store.create(ctx, writeParams{
		ID: id, Category: category, Type: input.Type, Kind: kind, Driver: driver,
		Name: name, BaseURL: strings.TrimSpace(input.BaseURL), Settings: settings,
		Publication: input.Publication, Actor: actor.UserID,
		Ciphertext: ciphertext, KeyVersion: s.keyVersion,
	})
	if err != nil {
		return nil, err
	}
	s.store.audit(ctx, actor.UserID, "integration.create", item.UUID)
	return item, nil
}

// Update patches mutable metadata. Type/kind/driver are immutable so an
// integration can never be repurposed into another provider silently.
func (s *Service) Update(ctx context.Context, actor authorization.Principal, id uuid.UUID, input PatchInput) (*Integration, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	current, _, err := s.store.get(ctx, id)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if input.Type != nil && *input.Type != current.Type {
		return nil, validationError("type", "type is immutable")
	}
	if input.Kind != nil && deployment.Kind(*input.Kind) != current.Kind {
		return nil, validationError("kind", "kind is immutable")
	}
	if input.Driver != nil && *input.Driver != current.Driver {
		return nil, validationError("driver", "driver is immutable")
	}
	params := updateParams{ID: id, Publication: input.Publication}
	if input.Name != nil {
		name, err := validateName(*input.Name)
		if err != nil {
			return nil, err
		}
		params.Name = &name
	}
	if input.BaseURL != nil {
		baseURL := strings.TrimSpace(*input.BaseURL)
		if err := s.policy.Validate(baseURL, current.Driver); err != nil {
			return nil, err
		}
		if baseURL == "" {
			baseURL = ""
		}
		params.BaseURL = &baseURL
	}
	if input.Settings != nil {
		settings, err := s.validateSettings(current.Category, current.Driver, *input.Settings)
		if err != nil {
			return nil, err
		}
		params.Settings = &settings
	}
	if input.Publication != nil {
		if err := ValidatePublication(input.Publication); err != nil {
			return nil, err
		}
		params.Publication = input.Publication
	}
	if input.Credentials != nil {
		credentials, err := validateCredentials(current.Type, input.Credentials, false)
		if err != nil {
			return nil, err
		}
		ciphertext, err := s.sealCredentials(id, credentials)
		if err != nil {
			return nil, err
		}
		params.Ciphertext = ciphertext
		params.KeyVersion = s.keyVersion
	}
	item, err := s.store.update(ctx, params)
	if err != nil {
		return nil, err
	}
	s.store.audit(ctx, actor.UserID, "integration.update", item.UUID)
	return item, nil
}

// Delete refuses to remove an integration that still pins provider inventory or
// repository bindings.
func (s *Service) Delete(ctx context.Context, actor authorization.Principal, id uuid.UUID) error {
	if err := s.authorize(ctx, actor); err != nil {
		return err
	}
	used, err := s.store.inUse(ctx, id)
	if err != nil {
		return err
	}
	if used {
		return conflictError(CodeInUse, "integration is referenced by live provider resources or repository bindings")
	}
	if err := s.store.delete(ctx, id); err != nil {
		return err
	}
	s.store.audit(ctx, actor.UserID, "integration.delete", id)
	return nil
}

// TestConnection asks the trusted worker to perform a real provider check. The
// backend never decrypts compute credentials and never fabricates success.
func (s *Service) TestConnection(ctx context.Context, actor authorization.Principal, id uuid.UUID) (TestResult, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return TestResult{}, err
	}
	item, record, err := s.store.get(ctx, id)
	if err != nil {
		return TestResult{}, mapStoreError(err)
	}
	if record == nil {
		return TestResult{}, validationError("credentials", "credentials are not configured")
	}
	if s.tester == nil {
		return TestResult{}, dependencyError("the trusted worker transport is not configured")
	}
	result, testErr := s.tester.Test(ctx, item.UUID)
	if testErr != nil {
		_ = s.store.setStatus(ctx, id, StatusError)
		if errors.Is(testErr, context.DeadlineExceeded) || errors.Is(testErr, context.Canceled) {
			return TestResult{}, dependencyError("the trusted worker did not answer")
		}
		return TestResult{}, &Error{Code: CodeConnectionFailed, Message: "provider connection test failed", Status: 502}
	}
	status := StatusConnected
	if !result.OK {
		status = StatusError
	}
	_ = s.store.setStatus(ctx, id, status)
	s.store.audit(ctx, actor.UserID, "integration.test", id)
	return result, nil
}

// SealedCredentials is returned only to the trusted worker inside the same
// process boundary; HTTP never receives it.
type SealedCredentials struct {
	Ciphertext []byte
	KeyVersion uint32
}

// LoadSealed returns the ciphertext for the trusted worker. Callers must be
// worker code; it is not exported through HTTP.
func (s *Service) LoadSealed(ctx context.Context, id uuid.UUID) (*Integration, *SealedCredentials, error) {
	item, record, err := s.store.get(ctx, id)
	if err != nil {
		return nil, nil, mapStoreError(err)
	}
	if record == nil {
		return item, nil, nil
	}
	return item, &SealedCredentials{Ciphertext: record.Ciphertext, KeyVersion: record.KeyVersion}, nil
}

// OpenCredentials decrypts only inside the trusted worker. The AAD is frozen by
// CredentialAAD and is shared with the AD worker.
func OpenCredentials(sealer secrets.Sealer, integrationID uuid.UUID, sealed SealedCredentials) (map[string]string, error) {
	if sealer == nil {
		return nil, errors.New("integrations: sealer is not configured")
	}
	if len(sealed.Ciphertext) == 0 {
		return map[string]string{}, nil
	}
	plaintext, err := sealer.Open(sealed.Ciphertext, CredentialAAD(integrationID))
	if err != nil {
		return nil, errors.New("integrations: credentials could not be opened")
	}
	credentials := map[string]string{}
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return nil, errors.New("integrations: credentials envelope is invalid")
	}
	return credentials, nil
}

func (s *Service) sealCredentials(id uuid.UUID, credentials map[string]string) ([]byte, error) {
	if len(credentials) == 0 {
		return nil, nil
	}
	if s.sealer == nil {
		return nil, &Error{Code: CodeInternal, Message: "credential encryption is not configured", Status: 500}
	}
	plaintext, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.sealer.Seal(plaintext, CredentialAAD(id))
	if err != nil {
		return nil, &Error{Code: CodeInternal, Message: "credential encryption failed", Status: 500}
	}
	return ciphertext, nil
}

func (s *Service) authorize(ctx context.Context, actor authorization.Principal) error {
	if actor.UserID == uuid.Nil {
		return unauthorizedError()
	}
	if s.auth == nil {
		return &Error{Code: CodeInternal, Message: "authorization is not configured", Status: 500}
	}
	decision, err := s.auth.Authorize(ctx, actor, authorization.PermissionPlatformIntegrationsManage, authorization.Scope{})
	if err != nil {
		return &Error{Code: CodeInternal, Message: "authorization failed", Status: 500}
	}
	if !decision.Allowed {
		return forbiddenError()
	}
	return nil
}

func resolveTarget(typ Type, kindInput, driverInput string) (Category, deployment.Kind, string, error) {
	driver := strings.ToLower(strings.TrimSpace(driverInput))
	kind := strings.ToUpper(strings.TrimSpace(kindInput))
	switch typ {
	case TypeGitLab, TypeGitHub, TypeGitea:
		if kind != "" || driver != "" {
			return "", "", "", validationError("kind", "Git integrations must not declare kind or driver")
		}
		return CategoryGit, "", "", nil
	case TypeProxmox:
		if driver != "" && driver != deployment.DriverProxmox {
			return "", "", "", validationError("driver", "PROXMOX requires driver=proxmox")
		}
		if kind != "" && deployment.Kind(kind) != deployment.KindVM {
			return "", "", "", validationError("kind", "PROXMOX is a VM integration")
		}
		return CategoryCompute, deployment.KindVM, deployment.DriverProxmox, nil
	case TypeKubeVirt:
		if driver != "" && driver != deployment.DriverKubeVirt {
			return "", "", "", validationError("driver", "KUBEVIRT requires driver=kubevirt")
		}
		if kind != "" && deployment.Kind(kind) != deployment.KindVM {
			return "", "", "", validationError("kind", "KUBEVIRT is a VM integration")
		}
		return CategoryCompute, deployment.KindVM, deployment.DriverKubeVirt, nil
	case TypeDocker, TypePodman, TypeKubernetes:
		expected := strings.ToLower(string(typ))
		if driver != "" && driver != expected {
			return "", "", "", validationError("driver", "driver does not match the integration type")
		}
		if kind != "" && deployment.Kind(kind) != deployment.KindContainer {
			return "", "", "", validationError("kind", "this integration is a CONTAINER integration")
		}
		return CategoryCompute, deployment.KindContainer, expected, nil
	case TypeContainer:
		if kind != "" && deployment.Kind(kind) != deployment.KindContainer {
			return "", "", "", validationError("kind", "CONTAINER is a CONTAINER integration")
		}
		switch driver {
		case deployment.DriverDocker, deployment.DriverPodman, deployment.DriverKubernetes:
			return CategoryCompute, deployment.KindContainer, driver, nil
		default:
			return "", "", "", validationError("driver", "CONTAINER requires an explicit docker, podman or kubernetes driver")
		}
	default:
		return "", "", "", validationError("type", "unsupported integration type")
	}
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return "", validationError("name", "name must be between 1 and 128 characters")
	}
	return name, nil
}

func (s *Service) validateSettings(category Category, driver string, raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		trimmed = "{}"
	}
	if len(trimmed) > maxSettingsBytes {
		return nil, validationError("settings", "settings are too large")
	}
	if key := findSecretKey(raw); key != "" {
		return nil, validationError("settings", "provider credentials must not be placed in settings")
	}
	if category == CategoryGit {
		settings := GitSettings{}
		if err := strictJSON([]byte(trimmed), &settings); err != nil {
			return nil, validationError("settings", "unknown or invalid Git settings")
		}
		encoded, _ := json.Marshal(settings)
		return encoded, nil
	}
	if s.runtime == nil {
		return nil, validationError("settings", "driver settings schema is unavailable")
	}
	findings := s.runtime.ValidateSettings(driver, json.RawMessage(trimmed))
	for _, finding := range findings {
		if finding.Severity == deployment.SeverityError {
			return nil, validationError("settings", "driver settings are invalid: "+finding.Code)
		}
	}
	return json.RawMessage(trimmed), nil
}

func validateCredentials(typ Type, credentials map[string]string, requireSome bool) (map[string]string, error) {
	if len(credentials) == 0 {
		if requireSome {
			// Credentials may legitimately be absent for socket-local container
			// daemons, but never for Git or remote APIs.
			switch typ {
			case TypeDocker, TypePodman, TypeContainer:
				return map[string]string{}, nil
			default:
				return nil, validationError("credentials", "credentials are required for this integration type")
			}
		}
		return nil, validationError("credentials", "credentials must not be an empty object")
	}
	if len(credentials) > maxCredentials {
		return nil, validationError("credentials", "too many credential fields")
	}
	allowed := allowedCredentialKeys(typ)
	result := make(map[string]string, len(credentials))
	for key, value := range credentials {
		if _, ok := allowed[key]; !ok {
			return nil, validationError("credentials", "credential field is not allowed for this integration type")
		}
		if len(value) > maxCredentialBytes {
			return nil, validationError("credentials", "credential value is too large")
		}
		if strings.TrimSpace(value) == "" {
			return nil, validationError("credentials", "credential value must not be empty")
		}
		result[key] = value
	}
	return result, nil
}

func allowedCredentialKeys(typ Type) map[string]struct{} {
	keys := map[string]struct{}{}
	add := func(names ...string) {
		for _, name := range names {
			keys[name] = struct{}{}
		}
	}
	switch typ {
	case TypeProxmox:
		add("tokenID", "tokenSecret", "caPEM")
	case TypeKubeVirt, TypeKubernetes:
		add("token", "tokenFile", "caPEM")
	case TypeGitLab, TypeGitHub, TypeGitea:
		add("username", "password", "token", "sshPrivateKey", "webhookSecret", "caPEM")
	}
	return keys
}

func findSecretKey(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	var walk func(any) string
	walk = func(node any) string {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				lower := strings.ToLower(key)
				if lower == "secret" || lower == "password" || lower == "token" || lower == "credential" ||
					lower == "credentials" || lower == "privatekey" || strings.HasSuffix(lower, "secret") ||
					strings.HasSuffix(lower, "token") || strings.HasSuffix(lower, "password") {
					return key
				}
				if found := walk(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := walk(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return walk(value)
}

func normalizePagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return err
	}
	return notFoundError()
}

func strictJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
