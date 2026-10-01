package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/nrf24l01/actfb/internal/config"
)

const maxPresignTTL = 7 * 24 * time.Hour

// Client is a generic S3 client suitable for AWS S3 and S3-compatible
// object stores.
//
// Bucket is fixed per Client instance intentionally. If an application needs
// multiple buckets, create one Client per bucket.
type Client struct {
	api       *s3.Client
	presigner *s3.PresignClient

	bucket        string
	publicBaseURL string
}

// PutOptions contains optional object metadata used during upload and
// presigned PUT generation.
type PutOptions struct {
	ContentType        string
	CacheControl       string
	ContentDisposition string
	Metadata           map[string]string
}

// Object is a streaming S3 object.
//
// The caller MUST close Body.
type Object struct {
	Body io.ReadCloser

	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// ObjectInfo is object metadata returned by Head.
type ObjectInfo struct {
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// PresignedRequest describes a presigned HTTP request.
//
// Headers contains headers that were part of the signature. A client using
// the presigned URL should send them unchanged.
type PresignedRequest struct {
	URL     string
	Method  string
	Headers http.Header
}

// New creates an S3 client.
//
// New performs configuration validation but intentionally does not perform a
// network request. Call Check separately if startup/readiness validation is
// desired.
func New(ctx context.Context, cfg config.S3Config) (*Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	usePathStyle, err := resolvePathStyle(cfg.AddressingStyle, cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}

	// Explicit static credentials are optional. If omitted, the AWS SDK uses
	// its normal credential provider chain.
	if cfg.AccessKeyID != "" {
		loadOptions = append(
			loadOptions,
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(
					cfg.AccessKeyID,
					cfg.SecretAccessKey,
					cfg.SessionToken,
				),
			),
		)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load S3 config: %w", err)
	}

	// Newer versions of AWS SDK for Go v2 calculate/validate checksums more
	// aggressively by default. WHEN_REQUIRED maximizes compatibility with
	// S3-compatible implementations that do not implement every optional
	// AWS checksum feature.
	awsCfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	awsCfg.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired

	apiClient := newS3Client(awsCfg, cfg.Endpoint, usePathStyle)

	presignEndpoint := cfg.PresignEndpoint
	if presignEndpoint == "" {
		presignEndpoint = cfg.Endpoint
	}

	presignAPIClient := apiClient
	if presignEndpoint != cfg.Endpoint {
		presignAPIClient = newS3Client(awsCfg, presignEndpoint, usePathStyle)
	}

	return &Client{
		api:           apiClient,
		presigner:     s3.NewPresignClient(presignAPIClient),
		bucket:        cfg.Bucket,
		publicBaseURL: strings.TrimRight(cfg.PublicBaseURL, "/"),
	}, nil
}

func newS3Client(
	cfg aws.Config,
	endpoint string,
	usePathStyle bool,
) *s3.Client {
	return s3.NewFromConfig(
		cfg,
		func(options *s3.Options) {
			options.UsePathStyle = usePathStyle

			if endpoint != "" {
				options.BaseEndpoint = aws.String(strings.TrimRight(endpoint, "/"))
			}
		},
	)
}

// Bucket returns the bucket configured for this client.
func (c *Client) Bucket() string {
	return c.bucket
}

// Check checks whether the configured bucket can be reached with HeadBucket.
//
// Note that HeadBucket requires suitable bucket-level permissions. If an
// application intentionally has only object-level permissions, do not use
// this method as a readiness check.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.api.HeadBucket(
		ctx,
		&s3.HeadBucketInput{
			Bucket: aws.String(c.bucket),
		},
	)
	if err != nil {
		return fmt.Errorf("check S3 bucket %q: %w", c.bucket, err)
	}

	return nil
}

// Put uploads an object.
//
// body is io.ReadSeeker intentionally. The S3 SDK may need to seek while
// preparing/signing the request. For non-seekable or very large multipart
// streams, use an S3 transfer manager/uploader instead.
func (c *Client) Put(
	ctx context.Context,
	key string,
	body io.ReadSeeker,
	size int64,
	options PutOptions,
) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if body == nil {
		return errors.New("S3 object body is nil")
	}
	if size < 0 {
		return errors.New("S3 object size must not be negative")
	}

	input := &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}

	applyPutOptions(input, options)

	_, err := c.api.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("put S3 object %q: %w", key, err)
	}

	return nil
}

// PutBytes uploads a byte slice. Prefer Put for files/streams when the object
// is already available as a seekable reader.
func (c *Client) PutBytes(
	ctx context.Context,
	key string,
	data []byte,
	options PutOptions,
) error {
	return c.Put(
		ctx,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		options,
	)
}

// Get opens an object for streaming.
//
// The caller MUST close Object.Body.
func (c *Client) Get(
	ctx context.Context,
	key string,
) (*Object, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	result, err := c.api.GetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("get S3 object %q: %w", key, err)
	}

	return &Object{
		Body:         result.Body,
		Size:         aws.ToInt64(result.ContentLength),
		ContentType:  aws.ToString(result.ContentType),
		ETag:         normalizeETag(aws.ToString(result.ETag)),
		LastModified: aws.ToTime(result.LastModified),
		Metadata:     cloneStringMap(result.Metadata),
	}, nil
}

// DownloadTo streams an object into dst without loading the whole object into
// memory. It returns the number of copied bytes.
func (c *Client) DownloadTo(
	ctx context.Context,
	key string,
	dst io.Writer,
) (int64, error) {
	if dst == nil {
		return 0, errors.New("download destination is nil")
	}

	object, err := c.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	defer object.Body.Close()

	written, err := io.Copy(dst, object.Body)
	if err != nil {
		return written, fmt.Errorf("copy S3 object %q: %w", key, err)
	}

	return written, nil
}

// Head returns object metadata without downloading the body.
func (c *Client) Head(
	ctx context.Context,
	key string,
) (*ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	result, err := c.api.HeadObject(
		ctx,
		&s3.HeadObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("head S3 object %q: %w", key, err)
	}

	return &ObjectInfo{
		Size:         aws.ToInt64(result.ContentLength),
		ContentType:  aws.ToString(result.ContentType),
		ETag:         normalizeETag(aws.ToString(result.ETag)),
		LastModified: aws.ToTime(result.LastModified),
		Metadata:     cloneStringMap(result.Metadata),
	}, nil
}

// Exists reports whether an object exists.
func (c *Client) Exists(
	ctx context.Context,
	key string,
) (bool, error) {
	_, err := c.Head(ctx, key)
	if err == nil {
		return true, nil
	}

	if IsNotFound(err) {
		return false, nil
	}

	return false, err
}

// Delete deletes an object.
//
// S3 DeleteObject is generally idempotent: deleting a missing object normally
// succeeds.
func (c *Client) Delete(
	ctx context.Context,
	key string,
) error {
	if err := validateKey(key); err != nil {
		return err
	}

	_, err := c.api.DeleteObject(
		ctx,
		&s3.DeleteObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
		},
	)
	if err != nil {
		return fmt.Errorf("delete S3 object %q: %w", key, err)
	}

	return nil
}

// PresignGet creates a presigned GET request.
//
// The returned URL is already signed for PresignEndpoint (or Endpoint when
// PresignEndpoint is empty). Do not rewrite its host/path afterwards.
func (c *Client) PresignGet(
	ctx context.Context,
	key string,
	expires time.Duration,
) (*PresignedRequest, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := validatePresignTTL(expires); err != nil {
		return nil, err
	}

	result, err := c.presigner.PresignGetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
		},
		func(options *s3.PresignOptions) {
			options.Expires = expires
		},
	)
	if err != nil {
		return nil, fmt.Errorf("presign GET S3 object %q: %w", key, err)
	}

	return &PresignedRequest{
		URL:     result.URL,
		Method:  result.Method,
		Headers: result.SignedHeader.Clone(),
	}, nil
}

// PresignPut creates a presigned PUT request.
//
// If ContentType, CacheControl, ContentDisposition or Metadata are specified,
// they may become signed headers. The client performing the upload must send
// the returned Headers unchanged.
func (c *Client) PresignPut(
	ctx context.Context,
	key string,
	expires time.Duration,
	options PutOptions,
) (*PresignedRequest, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := validatePresignTTL(expires); err != nil {
		return nil, err
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}

	applyPutOptions(input, options)

	result, err := c.presigner.PresignPutObject(
		ctx,
		input,
		func(options *s3.PresignOptions) {
			options.Expires = expires
		},
	)
	if err != nil {
		return nil, fmt.Errorf("presign PUT S3 object %q: %w", key, err)
	}

	return &PresignedRequest{
		URL:     result.URL,
		Method:  result.Method,
		Headers: result.SignedHeader.Clone(),
	}, nil
}

// PublicURL builds a public/CDN URL for key.
//
// This method does not make private objects public and does not generate a
// signature. PublicBaseURL must already expose the configured bucket root.
func (c *Client) PublicURL(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if c.publicBaseURL == "" {
		return "", errors.New("S3 public base URL is not configured")
	}

	u, err := url.Parse(c.publicBaseURL)
	if err != nil {
		return "", fmt.Errorf("parse S3 public base URL: %w", err)
	}

	basePath := strings.TrimRight(u.Path, "/")
	objectPath := strings.TrimLeft(key, "/")

	u.Path = basePath + "/" + objectPath
	u.RawPath = ""

	return u.String(), nil
}

// IsNotFound reports whether err represents an S3 HTTP 404 / not-found API
// response.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchObject":
			return true
		}
	}

	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.HTTPStatusCode() == http.StatusNotFound
	}

	return false
}

func applyPutOptions(
	input *s3.PutObjectInput,
	options PutOptions,
) {
	if options.ContentType != "" {
		input.ContentType = aws.String(options.ContentType)
	}

	if options.CacheControl != "" {
		input.CacheControl = aws.String(options.CacheControl)
	}

	if options.ContentDisposition != "" {
		input.ContentDisposition = aws.String(options.ContentDisposition)
	}

	if len(options.Metadata) != 0 {
		input.Metadata = cloneStringMap(options.Metadata)
	}
}

func validateConfig(cfg config.S3Config) error {
	if cfg.Bucket == "" {
		return errors.New("S3 bucket is empty")
	}

	if cfg.Region == "" {
		return errors.New("S3 region is empty")
	}

	if (cfg.AccessKeyID == "") != (cfg.SecretAccessKey == "") {
		return errors.New(
			"S3 access key ID and secret access key must be specified together",
		)
	}

	if cfg.SessionToken != "" && cfg.AccessKeyID == "" {
		return errors.New(
			"S3 session token requires explicit access key credentials",
		)
	}

	if cfg.Endpoint != "" {
		if err := validateEndpoint(cfg.Endpoint); err != nil {
			return fmt.Errorf("invalid S3 endpoint: %w", err)
		}
	}

	if cfg.PresignEndpoint != "" {
		if err := validateEndpoint(cfg.PresignEndpoint); err != nil {
			return fmt.Errorf("invalid S3 presign endpoint: %w", err)
		}
	}

	if cfg.PublicBaseURL != "" {
		if err := validatePublicBaseURL(cfg.PublicBaseURL); err != nil {
			return fmt.Errorf("invalid S3 public base URL: %w", err)
		}
	}

	_, err := resolvePathStyle(cfg.AddressingStyle, cfg.Endpoint)
	return err
}

func validateEndpoint(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("host is empty")
	}

	// Avoid path-prefix reverse proxies for the S3 API itself. They complicate
	// canonical URI handling and SigV4 compatibility.
	if u.Path != "" && u.Path != "/" {
		return errors.New("endpoint must not contain a path prefix")
	}

	if u.RawQuery != "" {
		return errors.New("endpoint must not contain query parameters")
	}

	if u.Fragment != "" {
		return errors.New("endpoint must not contain a fragment")
	}

	return nil
}

func validatePublicBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("host is empty")
	}

	if u.RawQuery != "" {
		return errors.New("public base URL must not contain query parameters")
	}

	if u.Fragment != "" {
		return errors.New("public base URL must not contain a fragment")
	}

	return nil
}

func resolvePathStyle(
	style string,
	endpoint string,
) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "", "auto":
		// For AWS, prefer SDK/default virtual-hosted addressing.
		// For custom S3-compatible endpoints, path-style is the safer default.
		return endpoint != "", nil

	case "path":
		return true, nil

	case "virtual":
		return false, nil

	default:
		return false, fmt.Errorf(
			"unsupported S3 addressing style %q: expected auto, path or virtual",
			style,
		)
	}
}

func validatePresignTTL(expires time.Duration) error {
	if expires <= 0 {
		return errors.New(
			"presigned URL expiration must be greater than zero",
		)
	}

	if expires > maxPresignTTL {
		return fmt.Errorf(
			"presigned URL expiration exceeds %s",
			maxPresignTTL,
		)
	}

	return nil
}

func validateKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("S3 object key is empty")
	}

	if strings.HasPrefix(key, "/") {
		return errors.New("S3 object key must not start with '/'")
	}

	return nil
}

func normalizeETag(etag string) string {
	return strings.Trim(etag, `"`)
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}

	return dst
}
