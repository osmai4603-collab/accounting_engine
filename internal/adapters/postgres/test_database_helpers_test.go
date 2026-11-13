package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
)

func newIsolatedPostgresDriver(t *testing.T, ctx context.Context) (*postgres.Driver, error) {
	return newIsolatedPostgresDriverWithConns(t, ctx, 0)
}

// newIsolatedPostgresDriverWithConns creates a driver over an isolated schema
// with a dedicated pool. maxConns <= 0 keeps the pgxpool default.
func newIsolatedPostgresDriverWithConns(t *testing.T, ctx context.Context, maxConns int32) (*postgres.Driver, error) {
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
	if maxConns > 0 {
		config.MaxConns = maxConns
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

// isolatedPostingEnv is a company-scoped posting environment inside an isolated
// schema: a USD company, one journal, a debit and a credit account, and a
// generated fiscal year whose first period is exposed for explicit posting.
type isolatedPostingEnv struct {
	Driver      *postgres.Driver
	CompanyID   uuid.UUID
	JournalID   uuid.UUID
	PeriodID    uuid.UUID
	DebitAccID  uuid.UUID
	CreditAccID uuid.UUID
}

func setupIsolatedPostingEnv(t *testing.T) (context.Context, isolatedPostingEnv) {
	return setupIsolatedPostingEnvWithPool(t, 0)
}

// setupIsolatedPostingEnvWithPool builds the isolated posting environment with
// a dedicated pool size (<= 0 keeps the pgxpool default).
func setupIsolatedPostingEnvWithPool(t *testing.T, maxConns int32) (context.Context, isolatedPostingEnv) {
	t.Helper()
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriverWithConns(t, ctx, maxConns)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	env := isolatedPostingEnv{
		Driver:      driver,
		CompanyID:   uuid.New(),
		JournalID:   uuid.New(),
		DebitAccID:  uuid.New(),
		CreditAccID: uuid.New(),
	}
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: env.CompanyID, Code: fmt.Sprintf("ENV-%s", env.CompanyID.String()[:8]),
		LegalName: "Isolated Posting Env Co", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code) VALUES($1,$2,$3)`,
			env.JournalID, env.CompanyID, fmt.Sprintf("J-%s", env.JournalID.String()[:8]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []masterdata.Account{
		{CompanyID: env.CompanyID, ID: env.DebitAccID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: env.CompanyID, ID: env.CreditAccID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = env.CompanyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	env.PeriodID = year.Periods[0].ID
	return ctx, env
}
