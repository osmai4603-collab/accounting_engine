package governance

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	auditWORMProviderEnv  = "AUDIT_WORM_PROVIDER"
	auditWORMDirectoryEnv = "AUDIT_WORM_DIR"
	auditWORMBucketEnv    = "AUDIT_WORM_BUCKET_NAME"
	auditWORMRetentionEnv = "AUDIT_WORM_RETENTION_DAYS"
	auditSigningKeyEnv    = "AUDIT_SIGNING_KEY"
	defaultAuditWORMPath  = "storage/worm_vault"
	defaultAuditRetention = 2555
)

type LocalDirectoryWORM struct {
	root string
}

func NewLocalDirectoryWORM(root string) (*LocalDirectoryWORM, error) {
	if strings.TrimSpace(root) == "" {
		root = defaultAuditWORMPath
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local WORM directory: %w", err)
	}
	if err := os.MkdirAll(absoluteRoot, 0700); err != nil {
		return nil, fmt.Errorf("create local WORM directory: %w", err)
	}
	info, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect local WORM directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("local WORM root must be a real directory, not a symlink")
	}
	return &LocalDirectoryWORM{root: absoluteRoot}, nil
}

func (s *LocalDirectoryWORM) PutImmutableObject(
	ctx context.Context,
	key string,
	object []byte,
	retainUntil time.Time,
) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	relativePath, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if retainUntil.IsZero() || len(object) == 0 {
		return errors.New("local WORM object and retention deadline are required")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return fmt.Errorf("open local WORM root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close local WORM root: %w", err))
		}
	}()
	directoryPath := filepath.Dir(relativePath)
	if directoryPath != "." {
		if err := root.MkdirAll(directoryPath, 0700); err != nil {
			return fmt.Errorf("create local WORM directory: %w", err)
		}
	}
	file, err := root.OpenFile(relativePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return fmt.Errorf("create immutable local WORM object: %w", err)
	}
	created := true
	closed := false
	defer func() {
		if !created {
			return
		}
		if !closed {
			if err := file.Close(); err != nil {
				result = errors.Join(result, fmt.Errorf("close incomplete local WORM object: %w", err))
			}
		}
		if err := root.Remove(relativePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove incomplete local WORM object: %w", err))
		}
	}()
	if _, err := file.Write(object); err != nil {
		return fmt.Errorf("write local WORM object: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local WORM object: %w", err)
	}
	if err := file.Chmod(0400); err != nil {
		return fmt.Errorf("protect local WORM object: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("close local WORM object: %w", err)
	}
	closed = true
	directory, err := root.Open(directoryPath)
	if err != nil {
		return fmt.Errorf("open local WORM directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		return errors.Join(fmt.Errorf("sync local WORM directory: %w", err), closeErr)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close local WORM directory: %w", err)
	}
	created = false
	return nil
}

func (s *LocalDirectoryWORM) GetImmutableObject(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relativePath, err := s.objectPath(key)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open local WORM root: %w", err)
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read local WORM object: %w", err), root.Close())
	}
	object, readErr := io.ReadAll(file)
	fileCloseErr := file.Close()
	rootCloseErr := root.Close()
	if err := errors.Join(
		wrapIfNotNil("read local WORM object", readErr),
		wrapIfNotNil("close local WORM object", fileCloseErr),
		wrapIfNotNil("close local WORM root", rootCloseErr),
	); err != nil {
		return nil, err
	}
	return object, nil
}

func (s *LocalDirectoryWORM) objectPath(key string) (string, error) {
	if s == nil || s.root == "" || strings.TrimSpace(key) == "" {
		return "", errors.New("local WORM directory and object key are required")
	}
	relative := filepath.Clean(filepath.FromSlash(key))
	if !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("local WORM object key must be a non-empty relative path")
	}
	return relative, nil
}

func NewAuditWORMStorageFromEnv(ctx context.Context) (WORMStorageClient, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv(auditWORMProviderEnv)))
	if provider == "" {
		provider = "local"
	}
	switch provider {
	case "local":
		return NewLocalDirectoryWORM(os.Getenv(auditWORMDirectoryEnv))
	case "s3":
		if strings.TrimSpace(os.Getenv(auditWORMBucketEnv)) == "" {
			return nil, fmt.Errorf("%s is required for the %s audit WORM provider", auditWORMBucketEnv, provider)
		}
		return NewS3ObjectLockWORMFromEnv(ctx)
	case "gcs":
		if strings.TrimSpace(os.Getenv(auditWORMBucketEnv)) == "" {
			return nil, fmt.Errorf("%s is required for the %s audit WORM provider", auditWORMBucketEnv, provider)
		}
		return nil, errors.New("GCS audit WORM provider is not configured; use s3 or local")
	default:
		return nil, fmt.Errorf("unsupported %s value %q", auditWORMProviderEnv, provider)
	}
}

func LoadAuditSigningKeyFromEnv() (ed25519.PrivateKey, error) {
	encoded := strings.TrimSpace(os.Getenv(auditSigningKeyEnv))
	if encoded == "" {
		return nil, fmt.Errorf("%s is required", auditSigningKeyEnv)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", auditSigningKeyEnv, err)
	}
	switch len(key) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(key), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(key), nil
	default:
		return nil, fmt.Errorf("%s must encode an Ed25519 seed or private key", auditSigningKeyEnv)
	}
}

func AuditRetentionFromEnv() (time.Duration, error) {
	days := defaultAuditRetention
	if configured := strings.TrimSpace(os.Getenv(auditWORMRetentionEnv)); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive number of days", auditWORMRetentionEnv)
		}
		days = parsed
	}
	retention := time.Duration(days) * 24 * time.Hour
	if retention <= 0 {
		return 0, fmt.Errorf("%s exceeds the supported retention range", auditWORMRetentionEnv)
	}
	return retention, nil
}

func wrapIfNotNil(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func NewAuditExporterFromEnv(ctx context.Context, repository AuditExportRepository) (*AuditExporter, error) {
	storage, err := NewAuditWORMStorageFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	privateKey, err := LoadAuditSigningKeyFromEnv()
	if err != nil {
		return nil, err
	}
	retention, err := AuditRetentionFromEnv()
	if err != nil {
		return nil, err
	}
	return NewAuditExporter(repository, storage, privateKey, retention)
}
