package config

type S3Config struct {
	// Endpoint is the endpoint used by the backend itself.
	//
	// Leave empty for AWS S3 so the AWS SDK resolves the endpoint from Region.
	//
	// Examples:
	//   http://minio:9000
	//   http://garage:3900
	//   http://rustfs:9000
	//   https://s3.example.com
	Endpoint string `yaml:"endpoint" env:"S3_ENDPOINT"`

	// PresignEndpoint is the endpoint embedded into presigned URLs.
	//
	// It is useful when the backend accesses storage through an internal
	// address, while browsers/clients must use a public address.
	//
	// Example:
	//   Endpoint:        http://rustfs:9000
	//   PresignEndpoint: https://s3.example.com
	//
	// Leave empty to use Endpoint.
	PresignEndpoint string `yaml:"presign_endpoint" env:"S3_PRESIGN_ENDPOINT"`

	// PublicBaseURL is an optional public/CDN URL whose root corresponds to
	// the configured bucket.
	//
	// It is NOT used for presigned URLs.
	//
	// Example:
	//   https://cdn.example.com
	PublicBaseURL string `yaml:"public_base_url" env:"S3_PUBLIC_BASE_URL"`

	// AccessKeyID and SecretAccessKey may be left empty together.
	// In that case the standard AWS SDK credential chain is used
	// (environment, shared config, IAM/ECS/EKS role, etc.).
	AccessKeyID     string `yaml:"access_key_id" env:"S3_ACCESS_KEY_ID"`
	SecretAccessKey string `yaml:"secret_access_key" env:"S3_SECRET_ACCESS_KEY"`
	SessionToken    string `yaml:"session_token" env:"S3_SESSION_TOKEN"`

	Bucket string `yaml:"bucket" env:"S3_BUCKET"`
	Region string `yaml:"region" env:"S3_REGION"`

	// AddressingStyle controls bucket addressing.
	//
	// Supported values:
	//   auto    - virtual-hosted style for AWS, path style for custom Endpoint
	//   path    - https://endpoint/bucket/key
	//   virtual - https://bucket.endpoint/key
	AddressingStyle string `yaml:"addressing_style" env:"S3_ADDRESSING_STYLE"`
}
