package postgres_test

import (
	"context"
	"fmt"
	"testing"

	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newIsolatedPostgresDriver(t *testing.T, ctx context.Context) (*postgres.Driver, error) {
	t.Helper()
	databaseURL := getTestPostgresConn()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	schemaName := "accounting_test_" + uuid.NewString()
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("create isolated test schema: %w", err)
	}

	var testPool *pgxpool.Pool
	t.Cleanup(func() {
		if testPool != nil {
			testPool.Close()
		}
		if _, err := adminPool.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
		adminPool.Close()
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	testPool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	var currentSchema string
	if err := testPool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		return nil, err
	}
	if currentSchema != schemaName {
		return nil, fmt.Errorf("isolated PostgreSQL search_path selected %q, want %q", currentSchema, schemaName)
	}
	if err := postgres.EnsureSchema(ctx, testPool); err != nil {
		return nil, fmt.Errorf("initialize isolated test schema: %w", err)
	}
	return postgres.NewWithPool(testPool), nil
}
