package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var ErrNotConfigured = errors.New("object storage (S3/R2) is not configured")

// ObjectStorage defines operations for uploading, deleting, and generating URLs for objects.
type ObjectStorage interface {
	IsConfigured() bool
	Provider() string
	Bucket() string
	PublicURL(objectKey string) string
	PutObject(ctx context.Context, objectKey, contentType string, data []byte) error
	DeleteObject(ctx context.Context, objectKey string) error
	PresignGetURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error)
}

type Config struct {
	Provider      string // R2 | S3 | MINIO
	Endpoint      string // e.g. https://<account_id>.r2.cloudflarestorage.com
	Region        string // e.g. auto (for Cloudflare R2)
	Bucket        string
	AccessKeyID   string
	SecretKey     string
	PublicBaseURL string // e.g. https://image.nghia196.qzz.io
	UsePathStyle  bool
}

type S3Client struct {
	cfg           Config
	client        *s3.Client
	presignClient *s3.PresignClient
}

func NewS3Client(ctx context.Context, cfg Config) (*S3Client, error) {
	cfg.Provider = strings.ToUpper(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" {
		if strings.Contains(strings.ToLower(cfg.Endpoint), "r2.cloudflarestorage.com") {
			cfg.Provider = "R2"
		} else {
			cfg.Provider = "S3"
		}
	}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")

	if cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretKey == "" {
		return &S3Client{cfg: cfg}, nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = cfg.UsePathStyle
		}
	})

	return &S3Client{
		cfg:           cfg,
		client:        client,
		presignClient: s3.NewPresignClient(client),
	}, nil
}

func (c *S3Client) IsConfigured() bool {
	return c != nil && c.client != nil && c.cfg.Bucket != ""
}

func (c *S3Client) Provider() string {
	if c == nil || c.cfg.Provider == "" {
		return "R2"
	}
	return c.cfg.Provider
}

func (c *S3Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.cfg.Bucket
}

func (c *S3Client) PublicURL(objectKey string) string {
	if c == nil || c.cfg.PublicBaseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", c.cfg.PublicBaseURL, strings.TrimLeft(objectKey, "/"))
}

func (c *S3Client) PutObject(ctx context.Context, objectKey, contentType string, data []byte) error {
	if !c.IsConfigured() {
		return ErrNotConfigured
	}
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.cfg.Bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

func (c *S3Client) DeleteObject(ctx context.Context, objectKey string) error {
	if !c.IsConfigured() {
		return ErrNotConfigured
	}
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.cfg.Bucket),
		Key:    aws.String(objectKey),
	})
	return err
}

func (c *S3Client) PresignGetURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	if !c.IsConfigured() {
		return "", ErrNotConfigured
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	out, err := c.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.cfg.Bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
