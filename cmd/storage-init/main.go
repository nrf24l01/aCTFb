// Command storage-init prepares the development S3-compatible bucket.
//
// It is intentionally independent of backend configuration: it receives only
// S3 endpoint/credentials/bucket settings, creates the bucket idempotently,
// verifies that anonymous access is denied, and exits non-zero on failure.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	// Keep the normal operation plus probe cleanup within a 90-second total
	// budget even when the caller has no nearer deadline.
	readinessTimeout    = 85 * time.Second
	probeCleanupTimeout = 5 * time.Second
	retryInterval       = 1 * time.Second
	probeBody           = "actfb storage-init private-access probe"
)

var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(context.Background(), logger); err != nil {
		logger.Error("storage initialization failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(parent, readinessTimeout)
	defer cancel()

	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	if err := waitForBucket(ctx, client, cfg.Bucket, logger); err != nil {
		return err
	}
	if err := verifyPrivate(ctx, client, cfg, logger); err != nil {
		return err
	}

	logger.Info("storage initialized", "bucket", cfg.Bucket, "endpoint", cfg.Endpoint)
	return nil
}

type storageConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Region          string
	PathStyle       bool
}

func loadConfig() (storageConfig, error) {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	accessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("S3_SECRET_ACCESS_KEY"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	region := strings.TrimSpace(os.Getenv("S3_REGION"))
	style := strings.ToLower(strings.TrimSpace(os.Getenv("S3_ADDRESSING_STYLE")))

	if endpoint == "" {
		return storageConfig{}, errors.New("S3_ENDPOINT is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return storageConfig{}, errors.New("S3_ENDPOINT must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return storageConfig{}, errors.New("S3_ENDPOINT must use http or https")
	}
	if parsed.User != nil {
		return storageConfig{}, errors.New("S3_ENDPOINT must not contain userinfo")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return storageConfig{}, errors.New("S3_ENDPOINT must not contain a path prefix")
	}
	if parsed.RawQuery != "" {
		return storageConfig{}, errors.New("S3_ENDPOINT must not contain query parameters")
	}
	if parsed.Fragment != "" {
		return storageConfig{}, errors.New("S3_ENDPOINT must not contain a fragment")
	}
	if accessKey == "" || secretKey == "" {
		return storageConfig{}, errors.New("S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required")
	}
	if bucket == "" {
		return storageConfig{}, errors.New("S3_BUCKET is required")
	}
	if !bucketNamePattern.MatchString(bucket) {
		return storageConfig{}, fmt.Errorf("S3_BUCKET %q is not a valid S3 bucket name", bucket)
	}
	if region == "" {
		region = "us-east-1"
	}

	pathStyle := true
	switch style {
	case "", "auto", "path":
		pathStyle = true
	case "virtual":
		return storageConfig{}, errors.New("S3_ADDRESSING_STYLE virtual is not supported by storage-init; use path/auto addressing")
	default:
		return storageConfig{}, fmt.Errorf("S3_ADDRESSING_STYLE %q is unsupported", style)
	}

	return storageConfig{
		Endpoint:        endpoint,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		Bucket:          bucket,
		Region:          region,
		PathStyle:       pathStyle,
	}, nil
}

func newS3Client(ctx context.Context, cfg storageConfig) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, err
	}

	awsCfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	awsCfg.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired

	return s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.PathStyle
		options.BaseEndpoint = aws.String(strings.TrimRight(cfg.Endpoint, "/"))
	}), nil
}

func waitForBucket(ctx context.Context, client *s3.Client, bucket string, logger *slog.Logger) error {
	attempt := 0
	var lastErr error

	for {
		attempt++
		lastErr = ensureBucket(ctx, client, bucket)
		if lastErr == nil {
			return nil
		}

		logger.Warn("storage not ready; retrying",
			"attempt", attempt,
			"retry_in", retryInterval,
			"error", lastErr,
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("storage did not become ready: %w (last error: %v)", ctx.Err(), lastErr)
		case <-time.After(retryInterval):
		}
	}
}

func ensureBucket(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("head bucket %q: %w", bucket, err)
	}

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}
	if isAlreadyExists(err) {
		// A blanket 409 is not proof of ownership. Re-check access before
		// treating the bucket as safe to use.
		if _, headErr := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); headErr != nil {
			return fmt.Errorf("bucket %q exists but ownership/access check failed: %w", bucket, headErr)
		}
		return nil
	}

	return fmt.Errorf("create bucket %q: %w", bucket, err)
}

func verifyPrivate(ctx context.Context, client *s3.Client, cfg storageConfig, logger *slog.Logger) error {
	key := fmt.Sprintf("storage-init/private-probe-%d", time.Now().UnixNano())

	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(cfg.Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader([]byte(probeBody)),
		ContentLength: aws.Int64(int64(len(probeBody))),
		ContentType:   aws.String("text/plain"),
	}); err != nil {
		return fmt.Errorf("upload private probe object: %w", err)
	}

	cleanupProbe := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), probeCleanupTimeout)
		defer cancel()
		_, err := client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{
			Bucket: aws.String(cfg.Bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("delete private probe object: %w", err)
		}
		return nil
	}
	defer func() {
		if err := cleanupProbe(); err != nil {
			logger.Warn("failed to clean private probe object", "key", key, "error", err)
		}
	}()

	probeURL := strings.TrimRight(cfg.Endpoint, "/") + "/" + url.PathEscape(cfg.Bucket) + "/" + escapeKey(key)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return fmt.Errorf("create anonymous probe request: %w", err)
	}

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("anonymous probe request: %w", err)
	}
	response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		logger.Info("bucket private access verified", "anonymous_status", response.StatusCode)
	default:
		return fmt.Errorf(
			"anonymous access to bucket %q returned HTTP %d, want 401/403/404",
			cfg.Bucket,
			response.StatusCode,
		)
	}

	if err := cleanupProbe(); err != nil {
		return err
	}

	return nil
}

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func isNotFound(err error) bool {
	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFoundException", "NoSuchBucket", "NotFound":
			return true
		}
	}

	return false
}

func isAlreadyExists(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return true
		}
	}

	return false
}
