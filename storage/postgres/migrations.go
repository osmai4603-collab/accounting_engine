package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 0x414343544D494752

var ErrDownMigrationMissing = errors.New("down migration is missing")

type migrationFile struct {
	version  int64
	name     string
	upPath   string
	downPath string
}

func migrationDirectory() (string, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("unable to locate PostgreSQL migrations")
	}
	return filepath.Join(filepath.Dir(source), "migrations"), nil
}

func listMigrations(directory string) ([]migrationFile, error) {
	entries, err := os.ReadDir(directory)
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
			migration.downPath = filepath.Join(directory, name)
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
		migration.upPath = filepath.Join(directory, name)
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
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		checksum char(64) NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	for _, migration := range migrations {
		content, err := os.ReadFile(migration.upPath)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		checksum := hex.EncodeToString(digest[:])
		var appliedChecksum string
		err = conn.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", migration.version).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != checksum {
				return fmt.Errorf("applied migration %d checksum differs from source", migration.version)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(content)); err == nil {
			_, err = tx.Exec(ctx,
				"INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)",
				migration.version, migration.name, checksum)
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

func MigrateDown(ctx context.Context, pool *pgxpool.Pool, steps int) (result error) {
	if steps < 1 {
		return errors.New("migration rollback steps must be positive")
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
		content, err := os.ReadFile(migration.downPath)
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

func (d *Driver) beginCompanyTx(ctx context.Context, companyID uuid.UUID) (pgx.Tx, error) {
	if companyID == uuid.Nil {
		return nil, errors.New("company transaction requires company id")
	}
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_company_id', $1, true)", companyID.String()); err != nil {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return nil, errors.Join(err, fmt.Errorf("rollback tenant context transaction: %w", rollbackErr))
		}
		return nil, err
	}
	if tp := TraceparentFromContext(ctx); tp != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.traceparent', $1, true)", tp); err != nil {
			if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return nil, errors.Join(err, fmt.Errorf("rollback traceparent context transaction: %w", rollbackErr))
			}
			return nil, err
		}
	}
	return tx, nil
}
