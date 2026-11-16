package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 0x414343544D494752

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var (
	ErrDownMigrationMissing = errors.New("down migration is missing")
	ErrSchemaIncompatible   = errors.New("database schema is incompatible or has pending migrations")
)

// ValidateSchemaHealth verifies that the database is reachable and that all embedded migrations
// have been applied with matching checksums.
func ValidateSchemaHealth(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("database connection pool is nil")
	}
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	const directory = "migrations"
	migrations, err := listMigrations(directory)
	if err != nil {
		return fmt.Errorf("list embedded migrations: %w", err)
	}
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return fmt.Errorf("%w: failed to verify schema migrations table: %v", ErrSchemaIncompatible, err)
	}
	pending, err := pendingMigrations(ctx, pool, migrations)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaIncompatible, err)
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w: %d pending migration(s) unapplied", ErrSchemaIncompatible, len(pending))
	}
	return nil
}

// ValidateSchema validates the schema on the driver's pool.
func (d *Driver) ValidateSchema(ctx context.Context) error {
	return ValidateSchemaHealth(ctx, d.pool)
}

type migrationFile struct {
	version  int64
	name     string
	upPath   string
	downPath string
}

func migrationDirectory() (string, error) {
	return "migrations", nil
}

func listMigrations(directory string) ([]migrationFile, error) {
	entries, err := embeddedMigrations.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*migrationFile)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".down.sql") {
			base := strings.TrimSuffix(name, ".down.sql")
			migration := byName[base]
			if migration == nil {
				migration = &migrationFile{name: base}
				byName[base] = migration
			}
			migration.downPath = path.Join(directory, name)
			continue
		}
		versionText := strings.SplitN(name, "_", 2)[0]
		version, parseErr := strconv.ParseInt(versionText, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid migration filename %q: %w", name, parseErr)
		}
		base := strings.TrimSuffix(name, ".sql")
		migration := byName[base]
		if migration == nil {
			migration = &migrationFile{name: base}
			byName[base] = migration
		}
		migration.version = version
		migration.upPath = path.Join(directory, name)
	}
	migrations := make([]migrationFile, 0, len(byName))
	for _, migration := range byName {
		if migration.upPath == "" {
			continue
		}
		migrations = append(migrations, *migration)
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].version)
		}
	}
	return migrations, nil
}

func MigrateUp(ctx context.Context, pool *pgxpool.Pool) (result error) {
	directory, err := migrationDirectory()
	if err != nil {
		return err
	}
	migrations, err := listMigrations(directory)
	if err != nil {
		return err
	}
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return err
	}
	pending, err := pendingMigrations(ctx, pool, migrations)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		// Nothing to apply: skip the advisory lock entirely so that opening a
		// new connection pool never contends on the migration lock.
		return nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return err
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID); unlockErr != nil {
			result = errors.Join(result, fmt.Errorf("release migration lock: %w", unlockErr))
		}
	}()
	// Re-evaluate pending work under the lock: a concurrent runner may have
	// applied the same migrations in the meantime.
	pending, err = pendingMigrations(ctx, pool, migrations)
	if err != nil {
		return err
	}
	for _, migration := range pending {
		content, err := embeddedMigrations.ReadFile(migration.upPath)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(content)); err == nil {
			_, err = tx.Exec(ctx,
				"INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)",
				migration.version, migration.name, migrationChecksum(content))
		}
		if err != nil {
			if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return errors.Join(err, rollbackErr)
			}
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func ensureSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM pg_catalog.pg_class AS relation
		JOIN pg_catalog.pg_namespace AS schema ON schema.oid = relation.relnamespace
		WHERE schema.nspname = current_schema()
		  AND relation.relname = 'schema_migrations'
		  AND relation.relkind = 'r'
	)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		checksum char(64) NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`)
	return err
}

// pendingMigrations validates applied checksums and returns the migrations
// that still have to run, in version order.
func pendingMigrations(ctx context.Context, pool *pgxpool.Pool, migrations []migrationFile) ([]migrationFile, error) {
	rows, err := pool.Query(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[int64]string, len(migrations))
	knownVersions := make(map[int64]struct{}, len(migrations))
	for _, migration := range migrations {
		knownVersions[migration.version] = struct{}{}
	}
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for version := range applied {
		if _, ok := knownVersions[version]; !ok {
			return nil, fmt.Errorf("database has unknown applied migration version %d", version)
		}
	}
	pending := make([]migrationFile, 0, len(migrations))
	for _, migration := range migrations {
		appliedChecksum, ok := applied[migration.version]
		if !ok {
			pending = append(pending, migration)
			continue
		}
		content, err := embeddedMigrations.ReadFile(migration.upPath)
		if err != nil {
			return nil, err
		}
		if appliedChecksum != migrationChecksum(content) {
			return nil, fmt.Errorf("applied migration %d checksum differs from source", migration.version)
		}
	}
	return pending, nil
}

func migrationChecksum(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func MigrateDown(ctx context.Context, pool *pgxpool.Pool, steps int) (result error) {
	if steps < 1 {
		return errors.New("migration rollback steps must be positive")
	}
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return err
	}
	directory, err := migrationDirectory()
	if err != nil {
		return err
	}
	migrations, err := listMigrations(directory)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return err
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID); unlockErr != nil {
			result = errors.Join(result, fmt.Errorf("release migration lock: %w", unlockErr))
		}
	}()
	for range steps {
		var version int64
		var name string
		err := conn.QueryRow(ctx,
			"SELECT version,name FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		migration, found := findMigration(migrations, version)
		if !found || migration.downPath == "" {
			return fmt.Errorf("%w: %s", ErrDownMigrationMissing, name)
		}
		content, err := embeddedMigrations.ReadFile(migration.downPath)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(content)); err == nil {
			_, err = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=$1", version)
		}
		if err != nil {
			if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return errors.Join(err, rollbackErr)
			}
			return fmt.Errorf("rollback migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit rollback of migration %s: %w", name, err)
		}
	}
	return nil
}

func findMigration(migrations []migrationFile, version int64) (migrationFile, bool) {
	for _, migration := range migrations {
		if migration.version == version {
			return migration, true
		}
	}
	return migrationFile{}, false
}

func (d *Driver) WithCompanyTx(ctx context.Context, companyID uuid.UUID, fn func(pgx.Tx) error) (result error) {
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback company transaction: %w", rollbackErr))
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	finished = true
	return nil
}
