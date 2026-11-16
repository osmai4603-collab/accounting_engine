package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestValidateSchemaHealth_HealthyAndIncompatible(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Fatalf("failed to create isolated driver: %v", err)
	}

	// 1. Should be healthy initially because newIsolatedPostgresDriver runs MigrateUp
	if err := driver.ValidateSchema(ctx); err != nil {
		t.Fatalf("expected initial schema to be healthy, got: %v", err)
	}

	// 2. Simulate pending migration by rolling back 1 migration step
	if err := postgres.MigrateDown(ctx, driver.Pool(), 1); err != nil {
		t.Fatalf("failed to roll down 1 migration step: %v", err)
	}

	// ValidateSchema should now detect pending migrations and return ErrSchemaIncompatible
	err = driver.ValidateSchema(ctx)
	if err == nil {
		t.Fatalf("expected ValidateSchema to fail after down-migration, got nil")
	}
	if !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("expected ErrSchemaIncompatible, got %v", err)
	}

	// 3. Migrate back up and verify health is restored
	if err := postgres.MigrateUp(ctx, driver.Pool()); err != nil {
		t.Fatalf("failed to re-apply migrations: %v", err)
	}
	if err := driver.ValidateSchema(ctx); err != nil {
		t.Fatalf("expected schema to be healthy after MigrateUp, got: %v", err)
	}
}

func TestValidateSchemaHealthRejectsUnknownAppliedMigration(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Fatalf("failed to create isolated driver: %v", err)
	}

	var unknownVersion int64
	if err := driver.Pool().QueryRow(ctx,
		"SELECT COALESCE(MAX(version), 0) + 1 FROM schema_migrations").Scan(&unknownVersion); err != nil {
		t.Fatalf("find unknown migration version: %v", err)
	}
	if _, err := driver.Pool().Exec(ctx,
		"INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)",
		unknownVersion, "future_migration", strings.Repeat("f", 64)); err != nil {
		t.Fatalf("insert simulated future migration record: %v", err)
	}

	if err := driver.ValidateSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("future schema readiness error = %v, want ErrSchemaIncompatible", err)
	}
	if err := postgres.MigrateUp(ctx, driver.Pool()); err == nil {
		t.Fatal("migration runner accepted a database with an unknown applied migration")
	}
}

func TestValidateSchemaHealthRejectsChangedMigrationChecksum(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Fatalf("failed to create isolated driver: %v", err)
	}

	if _, err := driver.Pool().Exec(ctx,
		"UPDATE schema_migrations SET checksum = $1 WHERE version = (SELECT MIN(version) FROM schema_migrations)",
		strings.Repeat("f", 64)); err != nil {
		t.Fatalf("simulate changed applied checksum: %v", err)
	}
	if err := driver.ValidateSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("changed checksum readiness error = %v, want ErrSchemaIncompatible", err)
	}
	if err := postgres.MigrateUp(ctx, driver.Pool()); err == nil {
		t.Fatal("migration runner accepted a changed applied checksum")
	}
}

// TestBackupRestore_IntegrityVerification checks post-restore validation by
// reconstructing representative data in a separate schema. It does not invoke
// pg_dump, pg_restore, or PostgreSQL physical/PITR recovery.
func TestBackupRestore_IntegrityVerification(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	amount, err := financialtypes.NewMoneyFromString("500.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}

	entry, err := posting.NewJournalEntry(env.JournalID, time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC), "restore-tester", []posting.JournalEntryLine{
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = env.CompanyID
	entry.PeriodID = env.PeriodID
	entry.Reference = "RESTORE-001"
	if err := env.Driver.SaveDraft(ctx, entry); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	entry.Status = posting.EntryPosted
	entry.PostedBy = "restore-checker"
	posted, err := env.Driver.PostAndSequence(ctx, entry)
	if err != nil {
		t.Fatalf("post and sequence: %v", err)
	}

	// Verify source balance reconciliation
	if err := env.Driver.AssertBalancesMatchMoves(ctx, env.CompanyID, env.PeriodID, "USD"); err != nil {
		t.Fatalf("source balances do not match moves: %v", err)
	}

	// Create destination isolated driver (simulating restore to a separate database/schema)
	destDriver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Fatalf("failed to create destination driver: %v", err)
	}

	// Replicate master data and posted transaction to destination
	if err := destDriver.CreateCompany(ctx, masterdata.Company{
		ID: env.CompanyID, Code: fmt.Sprintf("RESTORE-%s", env.CompanyID.String()[:8]),
		LegalName: "Restored Company Corp", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("restore: create company: %v", err)
	}

	if err := destDriver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code) VALUES($1,$2,$3)`,
			env.JournalID, env.CompanyID, fmt.Sprintf("J-%s", env.JournalID.String()[:8]))
		return err
	}); err != nil {
		t.Fatalf("restore: create journal: %v", err)
	}

	for _, account := range []masterdata.Account{
		{CompanyID: env.CompanyID, ID: env.DebitAccID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: env.CompanyID, ID: env.CreditAccID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		if err := destDriver.CreateAccount(ctx, account); err != nil {
			t.Fatalf("restore: create account: %v", err)
		}
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = env.CompanyID
	year.Periods[0].ID = env.PeriodID
	if err := destDriver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatalf("restore: create fiscal year: %v", err)
	}

	entry.Status = posting.EntryDraft
	if err := destDriver.SaveDraft(ctx, entry); err != nil {
		t.Fatalf("restore: save draft: %v", err)
	}
	entry.Status = posting.EntryPosted
	entry.PostedBy = "restore-checker"
	if _, err := destDriver.PostAndSequence(ctx, entry); err != nil {
		t.Fatalf("restore: post and sequence: %v", err)
	}

	// Run Integrity Checks on restored database:
	// 1. Validate Schema Health
	if err := destDriver.ValidateSchema(ctx); err != nil {
		t.Fatalf("restored schema validation failed: %v", err)
	}
	// 2. Balance reconciliation check
	if err := destDriver.AssertBalancesMatchMoves(ctx, env.CompanyID, env.PeriodID, "USD"); err != nil {
		t.Fatalf("restored environment integrity check failed: %v", err)
	}
	// 3. Ensure entry is readable
	readBack, err := destDriver.GetEntryByID(ctx, env.CompanyID, posted.ID)
	if err != nil {
		t.Fatalf("failed to retrieve restored entry: %v", err)
	}
	if readBack.ID != posted.ID || len(readBack.Lines) != 2 {
		t.Fatalf("restored entry mismatch: %+v", readBack)
	}
}
