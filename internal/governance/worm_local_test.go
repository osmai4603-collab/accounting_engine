package governance

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocalDirectoryWORMStoresReadOnlyObjectsWithoutOverwrite(t *testing.T) {
	storage, err := NewLocalDirectoryWORM(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "company/date/audit-anchor.json"
	object := []byte(`{"anchor":"signed"}`)
	if err := storage.PutImmutableObject(context.Background(), key, object, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storage.root, filepath.FromSlash(key))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0400 {
		t.Fatalf("object permissions = %04o, want 0400", info.Mode().Perm())
	}
	stored, err := storage.GetImmutableObject(context.Background(), key)
	if err != nil || !bytes.Equal(stored, object) {
		t.Fatalf("stored object = %q, %v", stored, err)
	}
	if err := storage.PutImmutableObject(context.Background(), key, []byte("replacement"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected duplicate immutable object write to fail")
	}
	stored, err = storage.GetImmutableObject(context.Background(), key)
	if err != nil || !bytes.Equal(stored, object) {
		t.Fatalf("object changed after duplicate write: %q, %v", stored, err)
	}
	if err := storage.PutImmutableObject(context.Background(), "../outside", object, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected parent traversal key to be rejected")
	}
}

func TestLocalDirectoryWORMHonorsCancelledContext(t *testing.T) {
	storage, err := NewLocalDirectoryWORM(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := storage.PutImmutableObject(ctx, "anchor.json", []byte("anchor"), time.Now().Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write error = %v, want context cancellation", err)
	}
	if _, err := storage.GetImmutableObject(ctx, "anchor.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error = %v, want context cancellation", err)
	}
}

func TestAuditWORMConfigurationUsesEnvironmentAndDefaults(t *testing.T) {
	t.Setenv(auditWORMProviderEnv, "")
	directory := t.TempDir()
	t.Setenv(auditWORMDirectoryEnv, directory)
	t.Setenv(auditWORMBucketEnv, "")
	storage, err := NewAuditWORMStorageFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*LocalDirectoryWORM); !ok {
		t.Fatalf("default WORM provider type = %T, want local directory", storage)
	}

	retention, err := AuditRetentionFromEnv()
	if err != nil || retention != defaultAuditRetention*24*time.Hour {
		t.Fatalf("default retention = %s, %v", retention, err)
	}

	seed := make([]byte, ed25519.SeedSize)
	t.Setenv(auditSigningKeyEnv, base64.StdEncoding.EncodeToString(seed))
	key, err := LoadAuditSigningKeyFromEnv()
	if err != nil || len(key) != ed25519.PrivateKeySize {
		t.Fatalf("loaded signing key length = %d, %v", len(key), err)
	}

	repository := &auditExportRepositoryStub{rootHash: "root-hash", count: 8}
	exporter, err := NewAuditExporterFromEnv(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	companyID := uuid.New()
	if err := exporter.ExportDailyAnchor(context.Background(), companyID, date); err != nil {
		t.Fatal(err)
	}
	if err := exporter.VerifyDailyAnchor(context.Background(), companyID, date); err != nil {
		t.Fatalf("verify local signed anchor: %v", err)
	}
	anchorPath := filepath.Join(directory, companyID.String(), date.Format("2006-01-02"), "audit-anchor.json")
	info, err := os.Stat(anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0400 {
		t.Fatalf("export permissions = %04o, want 0400", info.Mode().Perm())
	}
}

func TestAuditWORMConfigurationRejectsMissingS3Bucket(t *testing.T) {
	t.Setenv(auditWORMProviderEnv, "s3")
	t.Setenv(auditWORMBucketEnv, "")
	if _, err := NewAuditWORMStorageFromEnv(context.Background()); err == nil {
		t.Fatal("expected S3 provider without a bucket to fail explicitly")
	}
	t.Setenv(auditWORMRetentionEnv, strconv.Itoa(0))
	if _, err := AuditRetentionFromEnv(); err == nil {
		t.Fatal("expected zero retention to be rejected")
	}
}

func TestAuditWORMConfigurationCreatesS3ObjectLockProvider(t *testing.T) {
	t.Setenv(auditWORMProviderEnv, "s3")
	t.Setenv(auditWORMBucketEnv, "audit-bucket")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	storage, err := NewAuditWORMStorageFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*S3ObjectLockWORM); !ok {
		t.Fatalf("configured S3 provider type = %T, want S3 Object Lock", storage)
	}
}
