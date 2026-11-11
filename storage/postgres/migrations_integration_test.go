package postgres_test

import (
	"context"
	"testing"
	"time"

	"accounting_engine/storage/postgres"
)

func TestBaselineMigrationCleanRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	defer driver.Close()

	var baselineTablesExist bool
	if err := driver.Pool().QueryRow(ctx, `SELECT
		to_regclass(format('%I.schema_migrations', current_schema())) IS NOT NULL
		AND to_regclass(format('%I.account_moves', current_schema())) IS NOT NULL`,
	).Scan(&baselineTablesExist); err != nil {
		t.Fatalf("check baseline objects in isolated schema: %v", err)
	}
	if !baselineTablesExist {
		t.Fatal("baseline migration objects were not created in the isolated schema")
	}

	var applied int
	if err := driver.Pool().QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if applied == 0 {
		t.Fatal("clean schema did not apply any migrations")
	}

	var baselineName, baselineChecksum string
	if err := driver.Pool().QueryRow(ctx,
		"SELECT name, checksum FROM schema_migrations WHERE version = $1", int64(202611010001),
	).Scan(&baselineName, &baselineChecksum); err != nil {
		t.Fatalf("read baseline migration record: %v", err)
	}
	if baselineName != "202611010001_baseline_schema" || len(baselineChecksum) != 64 {
		t.Fatalf("baseline record = (%q, checksum length %d)", baselineName, len(baselineChecksum))
	}

	if err := postgres.EnsureSchema(ctx, driver.Pool()); err != nil {
		t.Fatalf("rerun migration chain: %v", err)
	}
	var appliedAfterRerun int
	if err := driver.Pool().QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&appliedAfterRerun); err != nil {
		t.Fatalf("count migrations after rerun: %v", err)
	}
	if appliedAfterRerun != applied {
		t.Fatalf("applied migration count after rerun = %d, want %d", appliedAfterRerun, applied)
	}
}
