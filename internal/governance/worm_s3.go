package governance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3WORMClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type S3ObjectLockWORM struct {
	client s3WORMClient
	bucket string
}

func NewS3ObjectLockWORM(ctx context.Context, bucket string) (*S3ObjectLockWORM, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("S3 Object Lock WORM requires a bucket name")
	}
	configuration, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS default credentials and configuration: %w", err)
	}
	return &S3ObjectLockWORM{client: s3.NewFromConfig(configuration), bucket: bucket}, nil
}

func NewS3ObjectLockWORMFromEnv(ctx context.Context) (*S3ObjectLockWORM, error) {
	return NewS3ObjectLockWORM(ctx, os.Getenv(auditWORMBucketEnv))
}

func (s *S3ObjectLockWORM) PutImmutableObject(
	ctx context.Context,
	key string,
	object []byte,
	retainUntil time.Time,
) error {
	if s == nil || s.client == nil || strings.TrimSpace(s.bucket) == "" ||
		strings.TrimSpace(key) == "" || len(object) == 0 || retainUntil.IsZero() ||
		!retainUntil.After(time.Now()) {
		return errors.New("S3 Object Lock WORM requires client, bucket, key, object, and retention deadline")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(s.bucket),
		Key:                       aws.String(key),
		Body:                      bytes.NewReader(object),
		IfNoneMatch:               aws.String("*"),
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil.UTC()),
	})
	if err != nil {
		return fmt.Errorf("write S3 compliance-mode audit object: %w", err)
	}
	return nil
}

func (s *S3ObjectLockWORM) GetImmutableObject(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil || strings.TrimSpace(s.bucket) == "" || strings.TrimSpace(key) == "" {
		return nil, errors.New("S3 Object Lock WORM requires client, bucket, and key")
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("read S3 immutable audit object: %w", err)
	}
	object, err := io.ReadAll(result.Body)
	closeErr := result.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read S3 immutable audit object body: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close S3 immutable audit object body: %w", closeErr)
	}
	return object, nil
}
