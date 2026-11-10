package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotImplemented = errors.New("postgres operation not implemented")

type Driver struct {
	pool *pgxpool.Pool
}

var _ posting.Repository = (*Driver)(nil)
var _ subledgers.Repository = (*Driver)(nil)
var _ masterdata.ContextStorageRepository = (*Driver)(nil)
var _ reporting.Repository = (*Driver)(nil)

func New(ctx context.Context, connectionString string) (*Driver, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := seedCurrencyRegistry(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Driver{pool: pool}, nil
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("unable to locate postgres schema files")
	}
	baseDir := filepath.Dir(file)
	content, err := os.ReadFile(filepath.Join(baseDir, "schema.sql"))
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, string(content)); err != nil {
		return err
	}
	return MigrateUp(ctx, pool)
}

func NewWithPool(pool *pgxpool.Pool) *Driver { return &Driver{pool: pool} }
func (d *Driver) Close()                     { d.pool.Close() }
func (d *Driver) Pool() *pgxpool.Pool        { return d.pool }

func seedCurrencyRegistry(ctx context.Context, pool *pgxpool.Pool) error {
	batch := &pgx.Batch{}
	currencies := financialtypes.AllCurrencies()
	for _, currency := range currencies {
		batch.Queue(`INSERT INTO currencies(code,numeric_code,name,minor_units)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(code) DO UPDATE SET numeric_code=EXCLUDED.numeric_code,name=EXCLUDED.name,minor_units=EXCLUDED.minor_units`,
			string(currency.Code), currency.NumericCode, currency.Name, currency.MinorUnit)
	}
	results := pool.SendBatch(ctx, batch)
	defer results.Close()
	for range currencies {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func nullableString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func defaultTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}
