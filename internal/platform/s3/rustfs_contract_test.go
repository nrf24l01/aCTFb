package s3

// Real S3-compatible contract suite for RustFS.
//
// The suite is opt-in. It is skipped unless ACTFB_TEST_S3_ENDPOINT is set.
// See docs/rustfs-s3-verification.md for the exact environment contract and
// for how to run the pinned official RustFS image in an isolated container.
//
// The suite exercises the product client (New, Check, Put, Head, Get,
// DownloadTo, Exists, Delete, PresignGet, PresignPut) against a real object
// store. The AWS SDK is used only for operations the product client does not
// expose: bucket creation and test-resource cleanup.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/nrf24l01/actfb/internal/config"
)

const (
	envRustFSEndpoint         = "ACTFB_TEST_S3_ENDPOINT"
	envRustFSPresignEndpoint  = "ACTFB_TEST_S3_PRESIGN_ENDPOINT"
	envRustFSAccessKeyID      = "ACTFB_TEST_S3_ACCESS_KEY_ID"
	envRustFSSecretAccessKey  = "ACTFB_TEST_S3_SECRET_ACCESS_KEY"
	envRustFSRegion           = "ACTFB_TEST_S3_REGION"
	envRustFSAddressingStyle  = "ACTFB_TEST_S3_ADDRESSING_STYLE"
	envRustFSBucketPrefix     = "ACTFB_TEST_S3_BUCKET_PREFIX"
	defaultRustFSRegion       = "us-east-1"
	defaultRustFSAddressStyle = "path"
	defaultRustFSBucketPrefix = "actfb-s3-contract"
)

type rustfsContractEnv struct {
	endpoint        string
	presignEndpoint string
	accessKeyID     string
	secretAccessKey string
	region          string
	addressingStyle string
	bucketPrefix    string
}

func loadRustFSContractEnv(t *testing.T) rustfsContractEnv {
	t.Helper()

	endpoint := strings.TrimSpace(os.Getenv(envRustFSEndpoint))
	if endpoint == "" {
		t.Skipf(
			"real S3 contract suite skipped: set %s (and %s/%s) to run against RustFS",
			envRustFSEndpoint,
			envRustFSAccessKeyID,
			envRustFSSecretAccessKey,
		)
	}

	env := rustfsContractEnv{
		endpoint:        strings.TrimRight(endpoint, "/"),
		accessKeyID:     strings.TrimSpace(os.Getenv(envRustFSAccessKeyID)),
		secretAccessKey: os.Getenv(envRustFSSecretAccessKey),
		region:          envOrDefault(envRustFSRegion, defaultRustFSRegion),
		addressingStyle: envOrDefault(envRustFSAddressingStyle, defaultRustFSAddressStyle),
		bucketPrefix:    envOrDefault(envRustFSBucketPrefix, defaultRustFSBucketPrefix),
	}

	env.presignEndpoint = strings.TrimRight(
		envOrDefault(envRustFSPresignEndpoint, env.endpoint),
		"/",
	)

	if env.accessKeyID == "" || env.secretAccessKey == "" {
		t.Fatalf(
			"%s is set but %s/%s are empty; the contract suite requires explicit test credentials",
			envRustFSEndpoint,
			envRustFSAccessKeyID,
			envRustFSSecretAccessKey,
		)
	}

	return env
}

func TestRustFS_S3Contract(t *testing.T) {
	env := loadRustFSContractEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sdk := newRustFSSDKClient(t, ctx, env)

	bucket := randomBucketName(t, env.bucketPrefix)
	createRustFSBucket(t, ctx, sdk, env, bucket)

	product, err := New(ctx, config.S3Config{
		Endpoint:        env.endpoint,
		PresignEndpoint: env.presignEndpoint,
		AccessKeyID:     env.accessKeyID,
		SecretAccessKey: env.secretAccessKey,
		Bucket:          bucket,
		Region:          env.region,
		AddressingStyle: env.addressingStyle,
	})
	if err != nil {
		t.Fatalf("create product S3 client: %v", err)
	}

	objectPrefix := "actfb-contract/" + randomHex(t, 8)

	t.Run("bucket is reachable and private", func(t *testing.T) {
		if err := product.Check(ctx); err != nil {
			t.Fatalf("product Check() error = %v", err)
		}
	})

	t.Run("product Put Head Get DownloadTo Delete", func(t *testing.T) {
		key := objectPrefix + "/product.txt"
		payload := []byte("RustFS product client contract payload\n")
		contentType := "text/plain; charset=utf-8"
		metadata := map[string]string{"contract": "product-client"}

		if err := product.PutBytes(ctx, key, payload, PutOptions{
			ContentType: contentType,
			Metadata:    metadata,
		}); err != nil {
			t.Fatalf("PutBytes() error = %v", err)
		}

		info, err := product.Head(ctx, key)
		if err != nil {
			t.Fatalf("Head() error = %v", err)
		}
		if info.Size != int64(len(payload)) {
			t.Fatalf("Head() size = %d, want %d", info.Size, len(payload))
		}
		if info.ContentType != contentType {
			t.Fatalf("Head() content type = %q, want %q", info.ContentType, contentType)
		}
		if info.Metadata["contract"] != "product-client" {
			t.Fatalf("Head() metadata = %#v, want contract=product-client", info.Metadata)
		}
		if info.ETag == "" {
			t.Fatal("Head() ETag is empty")
		}

		exists, err := product.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if !exists {
			t.Fatal("Exists() = false, want true after Put")
		}

		object, err := product.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		got, err := io.ReadAll(object.Body)
		closeErr := object.Body.Close()
		if err != nil {
			t.Fatalf("read Get() body: %v", err)
		}
		if closeErr != nil {
			t.Fatalf("close Get() body: %v", closeErr)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("Get() body = %q, want %q", got, payload)
		}

		var downloaded bytes.Buffer
		written, err := product.DownloadTo(ctx, key, &downloaded)
		if err != nil {
			t.Fatalf("DownloadTo() error = %v", err)
		}
		if written != int64(len(payload)) {
			t.Fatalf("DownloadTo() wrote %d bytes, want %d", written, len(payload))
		}
		if !bytes.Equal(downloaded.Bytes(), payload) {
			t.Fatalf("DownloadTo() body = %q, want %q", downloaded.Bytes(), payload)
		}

		if err := product.Delete(ctx, key); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		exists, err = product.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists() after Delete error = %v", err)
		}
		if exists {
			t.Fatal("Exists() = true after Delete, want false")
		}
	})

	t.Run("missing object semantics", func(t *testing.T) {
		key := objectPrefix + "/does-not-exist.txt"

		exists, err := product.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if exists {
			t.Fatal("Exists() = true for missing object")
		}

		if _, err := product.Head(ctx, key); !IsNotFound(err) {
			t.Fatalf("Head() error = %v, want IsNotFound", err)
		}

		if _, err := product.Get(ctx, key); !IsNotFound(err) {
			t.Fatalf("Get() error = %v, want IsNotFound", err)
		}

		if err := product.Delete(ctx, key); err != nil {
			t.Fatalf("Delete() missing object error = %v, want nil (idempotent)", err)
		}
	})

	t.Run("presigned browser upload and download", func(t *testing.T) {
		key := objectPrefix + "/presigned.txt"
		payload := []byte("RustFS presigned browser contract payload\n")
		contentType := "text/plain; charset=utf-8"
		contentDisposition := `attachment; filename="rustfs-contract.txt"`

		put, err := product.PresignPut(ctx, key, 5*time.Minute, PutOptions{
			ContentType:        contentType,
			ContentDisposition: contentDisposition,
			CacheControl:       "no-store",
			Metadata:           map[string]string{"contract": "presigned-browser"},
		})
		if err != nil {
			t.Fatalf("PresignPut() error = %v", err)
		}
		assertPathStylePresign(t, put.URL, env.presignEndpoint, bucket, key)

		putResp, err := doPresignedRequest(ctx, put, payload)
		if err != nil {
			t.Fatalf("presigned PUT %s: %v", redactURL(put.URL), safeHTTPError(err))
		}
		putBody := drainAndClose(t, putResp.Body)
		if putResp.StatusCode != http.StatusOK {
			t.Fatalf(
				"presigned PUT %s status = %d, want 200; body = %q",
				redactURL(put.URL),
				putResp.StatusCode,
				putBody,
			)
		}

		info, err := product.Head(ctx, key)
		if err != nil {
			t.Fatalf("Head() after presigned PUT error = %v", err)
		}
		if info.Size != int64(len(payload)) {
			t.Fatalf("Head() size after presigned PUT = %d, want %d", info.Size, len(payload))
		}
		if info.ContentType != contentType {
			t.Fatalf("Head() content type after presigned PUT = %q, want %q", info.ContentType, contentType)
		}
		if info.Metadata["contract"] != "presigned-browser" {
			t.Fatalf(
				"Head() metadata after presigned PUT = %#v, want contract=presigned-browser",
				info.Metadata,
			)
		}

		get, err := product.PresignGet(ctx, key, 5*time.Minute)
		if err != nil {
			t.Fatalf("PresignGet() error = %v", err)
		}
		assertPathStylePresign(t, get.URL, env.presignEndpoint, bucket, key)

		getResp, err := doPresignedRequest(ctx, get, nil)
		if err != nil {
			t.Fatalf("presigned GET %s: %v", redactURL(get.URL), safeHTTPError(err))
		}
		getBody := drainAndClose(t, getResp.Body)
		if getResp.StatusCode != http.StatusOK {
			t.Fatalf(
				"presigned GET %s status = %d, want 200; body = %q",
				redactURL(get.URL),
				getResp.StatusCode,
				getBody,
			)
		}
		if !bytes.Equal(getBody, payload) {
			t.Fatalf("presigned GET body = %q, want %q", getBody, payload)
		}
		if got := getResp.Header.Get("Content-Type"); got != contentType {
			t.Fatalf("presigned GET Content-Type = %q, want %q", got, contentType)
		}
		if got := getResp.Header.Get("Content-Disposition"); got != contentDisposition {
			t.Fatalf(
				"presigned GET Content-Disposition = %q, want %q",
				got,
				contentDisposition,
			)
		}
		if got := getResp.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("presigned GET Cache-Control = %q, want no-store", got)
		}
		if got := getResp.Header.Get("x-amz-meta-contract"); got != "presigned-browser" {
			t.Fatalf("presigned GET x-amz-meta-contract = %q, want presigned-browser", got)
		}

		t.Run("anonymous GET to private object is forbidden", func(t *testing.T) {
			rawURL, err := pathStyleObjectURL(env.presignEndpoint, bucket, key)
			if err != nil {
				t.Fatalf("build anonymous object URL: %v", err)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				t.Fatalf("build anonymous request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("anonymous GET %s: %v", redactURL(rawURL), safeHTTPError(err))
			}
			body := drainAndClose(t, resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf(
					"anonymous GET %s status = %d, want 403; body = %q",
					redactURL(rawURL),
					resp.StatusCode,
					body,
				)
			}
		})

		t.Run("tampered presigned GET is rejected", func(t *testing.T) {
			tampered := tamperSignature(t, get.URL)
			resp, err := doRawGET(ctx, tampered)
			if err != nil {
				t.Fatalf("tampered GET %s: %v", redactURL(tampered), safeHTTPError(err))
			}
			body := drainAndClose(t, resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf(
					"tampered GET %s status = %d, want 403; body = %q",
					redactURL(tampered),
					resp.StatusCode,
					body,
				)
			}
		})

		t.Run("tampered presigned PUT is rejected", func(t *testing.T) {
			tamperedKey := objectPrefix + "/tampered-put.txt"
			tamperedPut, err := product.PresignPut(ctx, tamperedKey, 5*time.Minute, PutOptions{
				ContentType: "text/plain",
			})
			if err != nil {
				t.Fatalf("PresignPut() for tamper test error = %v", err)
			}

			tampered := &PresignedRequest{
				URL:     tamperSignature(t, tamperedPut.URL),
				Method:  tamperedPut.Method,
				Headers: tamperedPut.Headers,
			}
			resp, err := doPresignedRequest(ctx, tampered, []byte("tampered upload"))
			if err != nil {
				t.Fatalf("tampered PUT %s: %v", redactURL(tampered.URL), safeHTTPError(err))
			}
			body := drainAndClose(t, resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf(
					"tampered PUT %s status = %d, want 403; body = %q",
					redactURL(tampered.URL),
					resp.StatusCode,
					body,
				)
			}

			exists, err := product.Exists(ctx, tamperedKey)
			if err != nil {
				t.Fatalf("Exists() after tampered PUT error = %v", err)
			}
			if exists {
				t.Fatal("tampered PUT created an object")
			}
		})

		t.Run("expired presigned GET is rejected", func(t *testing.T) {
			expired, err := product.PresignGet(ctx, key, time.Second)
			if err != nil {
				t.Fatalf("PresignGet(1s) error = %v", err)
			}

			time.Sleep(2 * time.Second)

			resp, err := doRawGET(ctx, expired.URL)
			if err != nil {
				t.Fatalf("expired GET %s: %v", redactURL(expired.URL), safeHTTPError(err))
			}
			body := drainAndClose(t, resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf(
					"expired GET %s status = %d, want 403; body = %q",
					redactURL(expired.URL),
					resp.StatusCode,
					body,
				)
			}
		})
	})
}

func newRustFSSDKClient(
	t *testing.T,
	ctx context.Context,
	env rustfsContractEnv,
) *awss3.Client {
	t.Helper()

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(env.region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				env.accessKeyID,
				env.secretAccessKey,
				"",
			),
		),
	)
	if err != nil {
		t.Fatalf("load test AWS config: %v", err)
	}

	// Match the product client's checksum compatibility settings.
	awsCfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	awsCfg.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired

	return awss3.NewFromConfig(awsCfg, func(options *awss3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(env.endpoint)
	})
}

func createRustFSBucket(
	t *testing.T,
	ctx context.Context,
	sdk *awss3.Client,
	env rustfsContractEnv,
	bucket string,
) {
	t.Helper()

	input := &awss3.CreateBucketInput{Bucket: aws.String(bucket)}
	if env.region != defaultRustFSRegion {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(env.region),
		}
	}

	if _, err := sdk.CreateBucket(ctx, input); err != nil {
		t.Fatalf("CreateBucket(%q) error = %v", bucket, err)
	}

	// Register cleanup as soon as the bucket exists so a later assertion
	// failure cannot leave the test bucket behind.
	t.Cleanup(func() {
		cleanupRustFSBucket(t, sdk, bucket)
	})

	// S3 CreateBucket is expected to be idempotent for the same owner. RustFS
	// and AWS may either succeed again or return an "already owned" error.
	if _, err := sdk.CreateBucket(ctx, input); err != nil {
		if !isBucketAlreadyOwned(err) {
			t.Fatalf("second CreateBucket(%q) error = %v, want success or already-owned", bucket, err)
		}
		t.Logf("second CreateBucket(%q) returned already-owned (idempotent S3 error)", bucket)
	} else {
		t.Logf("second CreateBucket(%q) succeeded (idempotent S3 success)", bucket)
	}

	if _, err := sdk.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("HeadBucket(%q) error = %v", bucket, err)
	}
}

func cleanupRustFSBucket(t *testing.T, sdk *awss3.Client, bucket string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		continuation *string
		deleted      int
	)
	for {
		list, err := sdk.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: continuation,
		})
		if err != nil {
			t.Errorf("cleanup: ListObjectsV2(%q) error = %v", bucket, err)
			return
		}

		for _, object := range list.Contents {
			if object.Key == nil {
				continue
			}
			if _, err := sdk.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    object.Key,
			}); err != nil {
				t.Errorf("cleanup: DeleteObject(%q/%q) error = %v", bucket, *object.Key, err)
				return
			}
			deleted++
		}

		if list.IsTruncated == nil || !*list.IsTruncated {
			break
		}
		continuation = list.NextContinuationToken
	}

	if _, err := sdk.DeleteBucket(ctx, &awss3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Errorf("cleanup: DeleteBucket(%q) after deleting %d objects error = %v", bucket, deleted, err)
	}
}

func isBucketAlreadyOwned(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.ErrorCode() {
	case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
		return true
	default:
		return false
	}
}

func assertPathStylePresign(
	t *testing.T,
	rawURL string,
	presignEndpoint string,
	bucket string,
	key string,
) {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}

	endpoint, err := url.Parse(presignEndpoint)
	if err != nil {
		t.Fatalf("parse presign endpoint: %v", err)
	}

	if u.Scheme != endpoint.Scheme || u.Host != endpoint.Host {
		t.Fatalf(
			"presigned URL endpoint = %s://%s, want %s://%s",
			u.Scheme,
			u.Host,
			endpoint.Scheme,
			endpoint.Host,
		)
	}

	wantPrefix := "/" + bucket + "/"
	if !strings.HasPrefix(u.Path, wantPrefix) {
		t.Fatalf("presigned URL path = %q, want prefix %q", u.Path, wantPrefix)
	}
	if !strings.HasSuffix(u.Path, key) {
		t.Fatalf("presigned URL path = %q, want suffix %q", u.Path, key)
	}
}

func pathStyleObjectURL(endpoint string, bucket string, key string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	u.Path = "/" + bucket + "/" + key
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

func doPresignedRequest(
	ctx context.Context,
	request *PresignedRequest,
	body []byte,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}

	for name, values := range request.Headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	return http.DefaultClient.Do(req)
}

func doRawGET(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	return http.DefaultClient.Do(req)
}

func tamperSignature(t *testing.T, rawURL string) string {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse presigned URL for tampering: %v", err)
	}

	query := u.Query()
	signature := query.Get("X-Amz-Signature")
	if signature == "" {
		t.Fatal("presigned URL has no X-Amz-Signature query parameter")
	}

	replacement := strings.Repeat("0", len(signature))
	if replacement == signature {
		replacement = strings.Repeat("1", len(signature))
	}

	query.Set("X-Amz-Signature", replacement)
	u.RawQuery = query.Encode()

	return u.String()
}

func drainAndClose(t *testing.T, body io.ReadCloser) []byte {
	t.Helper()

	if body == nil {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(body, 1<<20))
	closeErr := body.Close()
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close response body: %v", closeErr)
	}

	return data
}

// redactURL removes credentials and signing parameters before a URL is placed
// in test output. Presigned URLs are bearer credentials and must never be
// logged.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<unparseable-url>"
	}

	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil

	return u.String()
}

// safeHTTPError removes the full request URL from transport errors. net/http
// wraps failures in *url.Error, whose Error() includes the signed query string.
func safeHTTPError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s %s: %w", urlErr.Op, redactURL(urlErr.URL), urlErr.Err)
	}

	return err
}

func randomBucketName(t *testing.T, prefix string) string {
	t.Helper()

	sanitized := sanitizeBucketPrefix(prefix)
	if len(sanitized) > 40 {
		sanitized = sanitized[:40]
		sanitized = strings.Trim(sanitized, "-.")
	}
	if sanitized == "" {
		sanitized = defaultRustFSBucketPrefix
	}

	return fmt.Sprintf("%s-%s", sanitized, randomHex(t, 8))
}

func sanitizeBucketPrefix(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))

	var b strings.Builder
	lastDash := false
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(b.String(), "-")
}

func randomHex(t *testing.T, size int) string {
	t.Helper()

	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate random value: %v", err)
	}

	return hex.EncodeToString(buf)
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}
