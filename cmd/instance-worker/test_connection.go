package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/integrations"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type integrationRecord struct {
	ID         uuid.UUID
	Category   string
	Type       string
	Driver     string
	Kind       string
	BaseURL    string
	Settings   json.RawMessage
	Ciphertext []byte
	KeyVersion int
}

func (t *integrationTester) test(ctx context.Context, rawID string) error {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return errors.New("invalid integration id")
	}
	record := integrationRecord{}
	err = t.db.Pool.QueryRow(ctx, `
		SELECT id, category, type, COALESCE(driver,''), COALESCE(kind,''), COALESCE(base_url,''),
		       settings, credentials_ciphertext, COALESCE(credentials_key_version,0)
		FROM integrations WHERE id = $1
	`, id).Scan(&record.ID, &record.Category, &record.Type, &record.Driver, &record.Kind,
		&record.BaseURL, &record.Settings, &record.Ciphertext, &record.KeyVersion)
	if err != nil {
		return err
	}
	credentials := map[string]string{}
	if len(record.Ciphertext) > 0 {
		if t.sealer == nil {
			return errors.New("credential sealer is not configured")
		}
		plaintext, err := t.sealer.Open(record.Ciphertext, integrations.CredentialAAD(record.ID))
		if err != nil {
			return errors.New("credentials cannot be decrypted")
		}
		if err := json.Unmarshal(plaintext, &credentials); err != nil {
			return errors.New("credentials envelope is invalid")
		}
	}
	if record.Category == "GIT" {
		return testGit(ctx, record, credentials)
	}
	if record.Driver == "" {
		return errors.New("compute integration has no driver")
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: record.Settings})
	if err != nil {
		return err
	}
	target := runtime.Target{
		IntegrationUUID: record.ID,
		Driver:          record.Driver,
		Kind:            deployment.Kind(record.Kind),
		BaseURL:         record.BaseURL,
		Credentials:     credentials,
		Settings:        settings,
	}
	return t.registry.Probe(ctx, target)
}

// testGit performs a bounded authenticated HTTPS request. 2xx/3xx is a real
// successful connection; auth failures and provider errors fail the test. The
// response body is discarded and never returned.
func testGit(ctx context.Context, record integrationRecord, credentials map[string]string) error {
	policy := integrations.URLPolicyFromEnv()
	if err := policy.Validate(record.BaseURL, "git"); err != nil {
		return errors.New("git base URL is not allowed")
	}
	if record.Driver != "" {
		return errors.New("git integration must not declare a driver")
	}
	parsed, err := url.Parse(strings.TrimSpace(record.BaseURL))
	if err != nil {
		return errors.New("git base URL is invalid")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if caPEM := strings.TrimSpace(credentials["caPEM"]); caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return errors.New("git CA bundle is invalid")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("git redirects are not allowed")
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "actfb-integration-test/1")
	if token := strings.TrimSpace(credentials["token"]); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	} else if username := strings.TrimSpace(credentials["username"]); username != "" {
		request.SetBasicAuth(username, credentials["password"])
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("git connection failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 400 {
		return fmt.Errorf("git provider returned HTTP %d", response.StatusCode)
	}
	return nil
}
