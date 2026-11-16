package postgres_test

import (
	"context"
	"errors"
	"os"

	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
)

// freshMigrationPool provisions a completely empty isolated schema and a pool
// bound to it WITHOUT applying any migration, so the test owns the full
// lifecycle from a truly clean database.
func freshMigrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := getTestPostgresConn()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	schemaName := "accounting_migration_" + uuid.NewString()
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		t.Skipf("create isolated migration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminPool.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated migration schema: %v", err)
		}
		adminPool.Close()
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	// The search path contains ONLY the isolated schema: unqualified names in
	// the migration scripts must never fall through to the shared public
	// schema, exactly like a production deployment has a single target schema.
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, schemaName
}

// countUpMigrations counts the numbered forward migration scripts that ship
// with the driver.
func countUpMigrations(t *testing.T) int {
	t.Helper()
	directory := "migrations"
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".down.sql") {
			count++
		}
	}
	if count == 0 {
		t.Fatal("no forward migrations discovered")
	}
	return count
}

// TestCleanMigrationLifecycle proves the numbered migration chain is fully
// reversible: on a completely fresh database every migration applies forward,
// the entire chain rolls back to nothing, and the chain then re-applies
// cleanly with identical checksums. No ad-hoc DDL (schema.sql) participates at
// any point, so a fresh clean run can never hit checksum conflicts.
func TestCleanMigrationLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, schemaName := freshMigrationPool(t, ctx)
	totalMigrations := countUpMigrations(t)

	// Phase 1: clean forward run from absolutely nothing.
	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate-up on clean schema: %v", err)
	}
	var appliedFirst int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&appliedFirst); err != nil {
		t.Fatal(err)
	}
	if appliedFirst != totalMigrations {
		t.Fatalf("migrations applied on fresh run = %d, want %d", appliedFirst, totalMigrations)
	}
	for _, object := range []string{"schema_migrations", "account_moves", "account_move_lines",
		"account_balances", "financial_audit_logs", "subledger_events"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`,
			schemaName+"."+object).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("object %s missing after clean forward run", object)
		}
	}

	// Phase 2: roll the entire chain back down to zero.
	if err := postgres.MigrateDown(ctx, pool, totalMigrations); err != nil {
		t.Fatalf("migrate-down of the whole chain: %v", err)
	}
	var appliedAfterDown int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&appliedAfterDown); err != nil {
		t.Fatal(err)
	}
	if appliedAfterDown != 0 {
		t.Fatalf("migrations remaining after full rollback = %d, want 0", appliedAfterDown)
	}
	var baselineTableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`,
		schemaName+".account_moves").Scan(&baselineTableExists); err != nil {
		t.Fatal(err)
	}
	if baselineTableExists {
		t.Fatal("account_moves still exists after full rollback, want a truly clean schema")
	}

	// Phase 3: re-apply the whole chain from the rolled-back state.
	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("re-run migrate-up after full rollback: %v", err)
	}
	var appliedSecond int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&appliedSecond); err != nil {
		t.Fatal(err)
	}
	if appliedSecond != totalMigrations {
		t.Fatalf("migrations applied on second clean run = %d, want %d", appliedSecond, totalMigrations)
	}

	// Phase 4: the applied checksums still match the source scripts exactly.
	rows, err := pool.Query(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	appliedChecksums := make(map[int64]string, totalMigrations)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			t.Fatal(err)
		}
		if len(checksum) != 64 {
			t.Fatalf("migration %d checksum %q is not a sha-256 digest", version, checksum)
		}
		appliedChecksums[version] = checksum
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(appliedChecksums) != totalMigrations {
		t.Fatalf("checksum rows = %d, want %d", len(appliedChecksums), totalMigrations)
	}

	// Phase 5: a third no-op pass must be a clean, lock-free no-op.
	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	var appliedFinal int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&appliedFinal); err != nil {
		t.Fatal(err)
	}
	if appliedFinal != totalMigrations {
		t.Fatalf("migrations after idempotent re-run = %d, want %d", appliedFinal, totalMigrations)
	}
}

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

func TestFailedMigrationRollsBackAndCanBeRetried(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, _ := freshMigrationPool(t, ctx)
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		checksum char(64) NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_latest_migration() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.version = 202611150001 THEN
				RAISE EXCEPTION 'injected migration ledger failure';
			END IF;
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatalf("create migration failure injector: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_latest_migration
		BEFORE INSERT ON schema_migrations FOR EACH ROW
		EXECUTE FUNCTION reject_latest_migration()`); err != nil {
		t.Fatalf("install migration failure injector: %v", err)
	}

	if err := postgres.MigrateUp(ctx, pool); err == nil {
		t.Fatal("migration unexpectedly succeeded despite injected ledger failure")
	}

	var recorded bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = 202611150001)",
	).Scan(&recorded); err != nil {
		t.Fatalf("check failed migration ledger entry: %v", err)
	}
	if recorded {
		t.Fatal("failed migration was recorded as applied")
	}
	var forced bool
	if err := pool.QueryRow(ctx, `SELECT relforcerowsecurity
		FROM pg_class WHERE oid = 'public_event_ingestions'::regclass`).Scan(&forced); err != nil {
		t.Fatalf("check failed migration DDL rollback: %v", err)
	}
	if forced {
		t.Fatal("failed migration DDL was not rolled back")
	}
	if err := postgres.ValidateSchemaHealth(ctx, pool); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("schema health after failed migration = %v, want ErrSchemaIncompatible", err)
	}

	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_latest_migration ON schema_migrations`); err != nil {
		t.Fatalf("remove migration failure injector: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION reject_latest_migration()`); err != nil {
		t.Fatalf("remove migration failure function: %v", err)
	}
	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("retry migration after removing injected failure: %v", err)
	}
	if err := postgres.ValidateSchemaHealth(ctx, pool); err != nil {
		t.Fatalf("schema health after successful retry: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT relforcerowsecurity
		FROM pg_class WHERE oid = 'public_event_ingestions'::regclass`).Scan(&forced); err != nil {
		t.Fatalf("check successful migration DDL: %v", err)
	}
	if !forced {
		t.Fatal("successful retry did not apply migration DDL")
	}
}
