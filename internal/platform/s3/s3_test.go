package s3

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

func TestResolvePathStyle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		style    string
		endpoint string
		want     bool
		wantErr  bool
	}{
		{
			name:     "auto without custom endpoint uses virtual-hosted style",
			style:    "auto",
			endpoint: "",
			want:     false,
		},
		{
			name:     "auto with custom endpoint uses path style",
			style:    "auto",
			endpoint: "http://127.0.0.1:9000",
			want:     true,
		},
		{
			name:     "empty style behaves like auto with custom endpoint",
			style:    "",
			endpoint: "http://127.0.0.1:9000",
			want:     true,
		},
		{
			name:  "path is always path style",
			style: "path",
			want:  true,
		},
		{
			name:  "virtual is always virtual-hosted style",
			style: "virtual",
			want:  false,
		},
		{
			name:    "unknown style is rejected",
			style:   "dns",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolvePathStyle(test.style, test.endpoint)
			if test.wantErr {
				if err == nil {
					t.Fatal("resolvePathStyle() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePathStyle() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolvePathStyle() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "query and fragment",
			raw:  "https://s3.example.com/bucket/key?X-Amz-Signature=secret&X-Amz-Credential=also-secret#fragment",
			want: "https://s3.example.com/bucket/key",
		},
		{
			name: "userinfo is removed",
			raw:  "https://user:supersecret@example.test/bucket/key?X-Amz-Signature=secret",
			want: "https://example.test/bucket/key",
		},
		{
			name: "malformed input is fully redacted",
			raw:  "http://user:supersecret@[::1",
			want: "<unparseable-url>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := redactURL(test.raw)
			if got != test.want {
				t.Fatalf("redactURL() = %q, want %q", got, test.want)
			}
			if strings.Contains(got, "supersecret") || strings.Contains(got, "secret") {
				t.Fatalf("redactURL() leaked a credential: %q", got)
			}
		})
	}
}

func TestSafeHTTPErrorRedactsURL(t *testing.T) {
	t.Parallel()

	err := &url.Error{
		Op:  "Get",
		URL: "https://user:supersecret@example.test/bucket/key?X-Amz-Signature=signed-secret",
		Err: errors.New("transport failure"),
	}

	got := safeHTTPError(err).Error()
	for _, secret := range []string{"supersecret", "signed-secret", "X-Amz-Signature"} {
		if strings.Contains(got, secret) {
			t.Fatalf("safeHTTPError() leaked %q in %q", secret, got)
		}
	}
}

func TestValidateConfigRejectsUserinfo(t *testing.T) {
	t.Parallel()

	base := config.S3Config{
		Bucket:          "contract-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}

	tests := []struct {
		name   string
		apply  func(*config.S3Config)
		secret string
	}{
		{
			name: "endpoint",
			apply: func(cfg *config.S3Config) {
				cfg.Endpoint = "http://user:endpoint-secret@rustfs:9000"
			},
			secret: "endpoint-secret",
		},
		{
			name: "presign endpoint",
			apply: func(cfg *config.S3Config) {
				cfg.PresignEndpoint = "https://user:presign-secret@s3.example.com"
			},
			secret: "presign-secret",
		},
		{
			name: "public base URL",
			apply: func(cfg *config.S3Config) {
				cfg.PublicBaseURL = "https://user:public-secret@cdn.example.com"
			},
			secret: "public-secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			test.apply(&cfg)

			err := validateConfig(cfg)
			if err == nil {
				t.Fatal("validateConfig() error = nil, want userinfo rejection")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("validateConfig() error leaked a credential: %v", err)
			}
		})
	}
}

func TestNewRejectsUserinfoBeforeClientCreation(t *testing.T) {
	t.Parallel()

	cfg := config.S3Config{
		Endpoint:        "http://user:supersecret@rustfs:9000",
		Bucket:          "contract-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}

	client, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("New() error = nil, want userinfo rejection before client creation")
	}
	if client != nil {
		t.Fatal("New() returned a client despite invalid endpoint")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("New() error leaked a credential: %v", err)
	}
}

func TestValidateConfigMalformedURLErrorsDoNotLeakCredentials(t *testing.T) {
	t.Parallel()

	base := config.S3Config{
		Bucket:          "contract-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}

	tests := []struct {
		name   string
		apply  func(*config.S3Config)
		secret string
	}{
		{
			name: "endpoint",
			apply: func(cfg *config.S3Config) {
				cfg.Endpoint = "http://user:endpoint-secret@[::1"
			},
			secret: "endpoint-secret",
		},
		{
			name: "presign endpoint",
			apply: func(cfg *config.S3Config) {
				cfg.PresignEndpoint = "https://user:presign-secret@[::1"
			},
			secret: "presign-secret",
		},
		{
			name: "public base URL",
			apply: func(cfg *config.S3Config) {
				cfg.PublicBaseURL = "https://user:public-secret@[::1"
			},
			secret: "public-secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			test.apply(&cfg)

			err := validateConfig(cfg)
			if err == nil {
				t.Fatal("validateConfig() error = nil, want malformed URL rejection")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("validateConfig() error leaked a credential: %v", err)
			}
		})
	}
}

func TestPublicURLKeepsValidPublicBase(t *testing.T) {
	t.Parallel()

	client := &Client{publicBaseURL: "https://cdn.example.com/assets"}
	got, err := client.PublicURL("module/attachment.txt")
	if err != nil {
		t.Fatalf("PublicURL() error = %v", err)
	}

	want := "https://cdn.example.com/assets/module/attachment.txt"
	if got != want {
		t.Fatalf("PublicURL() = %q, want %q", got, want)
	}
}

func TestValidateConfigEndpointContract(t *testing.T) {
	t.Parallel()

	base := config.S3Config{
		Bucket:          "contract-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}

	t.Run("empty endpoint means AWS S3", func(t *testing.T) {
		t.Parallel()

		if err := validateConfig(base); err != nil {
			t.Fatalf("validateConfig() error = %v", err)
		}
	})

	t.Run("private internal endpoint with public presign endpoint", func(t *testing.T) {
		t.Parallel()

		cfg := base
		cfg.Endpoint = "http://rustfs:9000"
		cfg.PresignEndpoint = "https://s3.example.com"
		cfg.AddressingStyle = "path"

		if err := validateConfig(cfg); err != nil {
			t.Fatalf("validateConfig() error = %v", err)
		}
	})

	t.Run("public base URL with path is accepted", func(t *testing.T) {
		t.Parallel()

		cfg := base
		cfg.PublicBaseURL = "https://cdn.example.com/assets"

		if err := validateConfig(cfg); err != nil {
			t.Fatalf("validateConfig() error = %v", err)
		}
	})

	t.Run("endpoint path prefix is rejected", func(t *testing.T) {
		t.Parallel()

		cfg := base
		cfg.Endpoint = "http://rustfs:9000/s3"

		if err := validateConfig(cfg); err == nil {
			t.Fatal("validateConfig() error = nil, want endpoint path-prefix error")
		}
	})

	t.Run("presign endpoint must be absolute HTTP", func(t *testing.T) {
		t.Parallel()

		cfg := base
		cfg.PresignEndpoint = "s3.example.com"

		if err := validateConfig(cfg); err == nil {
			t.Fatal("validateConfig() error = nil, want presign endpoint scheme error")
		}
	})
}
