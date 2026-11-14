package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func TestLoadConfigRejectsUnsafeEndpointForms(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "userinfo", endpoint: "http://user:secret@rustfs:9000"},
		{name: "query", endpoint: "http://rustfs:9000?access_key=secret"},
		{name: "fragment", endpoint: "http://rustfs:9000#secret"},
		{name: "path", endpoint: "http://rustfs:9000/prefix"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("S3_ENDPOINT", test.endpoint)
			t.Setenv("S3_ACCESS_KEY_ID", "test-access")
			t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
			t.Setenv("S3_BUCKET", "actfb")

			_, err := loadConfig()
			if err == nil {
				t.Fatal("loadConfig() error = nil, want unsafe endpoint rejection")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("loadConfig() error leaked endpoint credentials: %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsVirtualAddressing(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://rustfs:9000")
	t.Setenv("S3_ACCESS_KEY_ID", "test-access")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("S3_BUCKET", "actfb")
	t.Setenv("S3_ADDRESSING_STYLE", "virtual")

	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "path/auto") {
		t.Fatalf("loadConfig() error = %v, want path/auto restriction", err)
	}
}

func TestEnsureBucketRechecksAfterAlreadyOwned(t *testing.T) {
	var headCalls, createCalls int32
	client, _ := newTestS3Client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			count := atomic.AddInt32(&headCalls, 1)
			if count == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			atomic.AddInt32(&createCalls, 1)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `<Error><Code>BucketAlreadyOwnedByYou</Code><Message>exists</Message></Error>`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	if err := ensureBucket(context.Background(), client, "actfb"); err != nil {
		t.Fatalf("ensureBucket() error = %v", err)
	}
	if createCalls != 1 {
		t.Errorf("create calls = %d, want 1", createCalls)
	}
	if headCalls != 2 {
		t.Errorf("head calls = %d, want 2 (initial + ownership recheck)", headCalls)
	}
}

func TestEnsureBucketRejectsUnknownConflict(t *testing.T) {
	client, _ := newTestS3Client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `<Error><Code>SomeOtherConflict</Code><Message>not ours</Message></Error>`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	if err := ensureBucket(context.Background(), client, "actfb"); err == nil {
		t.Fatal("ensureBucket() error = nil, want unknown 409 rejection")
	}
}

func TestVerifyPrivateDeletesProbeOnPublicAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var deleted int32
	client, endpoint := newTestS3Client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			cancel()
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "public")
		case http.MethodDelete:
			atomic.AddInt32(&deleted, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	cfg := storageConfig{Endpoint: endpoint, Bucket: "actfb"}
	if err := verifyPrivate(ctx, client, cfg, testLogger()); err == nil {
		t.Fatal("verifyPrivate() error = nil, want public-access rejection")
	}
	if atomic.LoadInt32(&deleted) == 0 {
		t.Fatal("probe object was not deleted after privacy failure")
	}
}

func TestVerifyPrivateAcceptsDeniedAnonymousAccess(t *testing.T) {
	var deleted int32
	client, endpoint := newTestS3Client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusForbidden)
		case http.MethodDelete:
			atomic.AddInt32(&deleted, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	cfg := storageConfig{Endpoint: endpoint, Bucket: "actfb"}
	if err := verifyPrivate(context.Background(), client, cfg, testLogger()); err != nil {
		t.Fatalf("verifyPrivate() error = %v", err)
	}
	if atomic.LoadInt32(&deleted) == 0 {
		t.Fatal("probe object was not deleted")
	}
}

func newTestS3Client(t *testing.T, handler http.HandlerFunc) (*s3.Client, string) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := s3.New(s3.Options{
		Credentials:  credentials.NewStaticCredentialsProvider("test-access", "test-secret", ""),
		Region:       "us-east-1",
		UsePathStyle: true,
		BaseEndpoint: aws.String(server.URL),
		HTTPClient:   server.Client(),
	})

	return client, server.URL
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoadConfigRequiresCredentials(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://rustfs:9000")
	t.Setenv("S3_ACCESS_KEY_ID", "")
	t.Setenv("S3_SECRET_ACCESS_KEY", "")
	t.Setenv("S3_BUCKET", "actfb")

	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig() error = nil, want missing credentials error")
	}
}

func TestLoadConfigRejectsInvalidBucket(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://rustfs:9000")
	t.Setenv("S3_ACCESS_KEY_ID", "test-access")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("S3_BUCKET", "Invalid Bucket")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_ADDRESSING_STYLE", "path")

	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig() error = nil, want invalid bucket error")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://rustfs:9000/")
	t.Setenv("S3_ACCESS_KEY_ID", "test-access")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("S3_BUCKET", "actfb")
	t.Setenv("S3_REGION", "")
	t.Setenv("S3_ADDRESSING_STYLE", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", cfg.Region)
	}
	if !cfg.PathStyle {
		t.Error("PathStyle = false, want true")
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(&smithy.GenericAPIError{Code: "NoSuchBucket", Message: "missing"}) {
		t.Fatal("NoSuchBucket was not classified as not found")
	}
	if isNotFound(errors.New("other")) {
		t.Fatal("generic error was classified as not found")
	}
}

func TestIsAlreadyExists(t *testing.T) {
	if !isAlreadyExists(&smithy.GenericAPIError{Code: "BucketAlreadyOwnedByYou", Message: "exists"}) {
		t.Fatal("BucketAlreadyOwnedByYou was not classified as already exists")
	}
	if isAlreadyExists(errors.New("other")) {
		t.Fatal("generic error was classified as already exists")
	}
}
