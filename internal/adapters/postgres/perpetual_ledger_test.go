package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/shopspring/decimal"
)

func TestPostgresMaterializedBalancesAreAtomicWithPosting(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL account-balance tests: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyID, journalID, periodID, debitAccountID, creditAccountID := setupBenchmarkCompany(t, driver)
	var closedYearID uuid.UUID
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM fiscal_years WHERE company_id=$1 AND year=2026`, companyID).
			Scan(&closedYearID)
	}); err != nil {
		t.Fatal(err)
	}
	isFirstYear, err := driver.IsFirstFiscalYearForCompanyContext(ctx, companyID, closedYearID)
	if err != nil || !isFirstYear {
		t.Fatalf("first-year check = %v, %v", isFirstYear, err)
	}
	amount, err := financialtypes.NewMoneyFromString("100.00", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("SAR")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(journalID, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), "test",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID, entry.PeriodID = companyID, periodID
	if err := driver.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(ctx, entry); err != nil {
		t.Fatal(err)
	}

	assertBalance := func(accountID uuid.UUID, wantDebit, wantCredit decimal.Decimal) {
		t.Helper()
		if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
			var debit, credit decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT period_debit,period_credit
				FROM account_balances WHERE company_id=$1 AND account_id=$2
				  AND fiscal_period_id=$3 AND currency_id='SAR'`,
				companyID, accountID, periodID).Scan(&debit, &credit); err != nil {
				return err
			}
			if !debit.Equal(wantDebit) || !credit.Equal(wantCredit) {
				t.Fatalf("balance for account %s = debit %s credit %s; want debit %s credit %s",
					accountID, debit, credit, wantDebit, wantCredit)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertBalance(debitAccountID, amount.Amount(), decimal.Zero)
	assertBalance(creditAccountID, decimal.Zero, amount.Amount())

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	balances, available, err := driver.ReadAccountPeriodBalancesForCompanyContext(
		ctx, companyID, start, end, "SAR", true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !available || len(balances) != 2 {
		t.Fatalf("materialized balances available=%v rows=%d", available, len(balances))
	}

	failedEntry, err := posting.NewJournalEntry(journalID, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC), "test",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	failedEntry.CompanyID = companyID
	if err := driver.SaveDraft(ctx, failedEntry); err != nil {
		t.Fatal(err)
	}
	failedEntry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(ctx, failedEntry); err == nil {
		t.Fatal("posting without a fiscal period succeeded")
	}
	stored, err := driver.GetEntryByIDForCompany(ctx, companyID, failedEntry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("failed transaction left move in state %q", stored.Status)
	}
	assertBalance(debitAccountID, amount.Amount(), decimal.Zero)
	assertBalance(creditAccountID, decimal.Zero, amount.Amount())

	for _, state := range []masterdata.FiscalYearState{
		masterdata.FiscalYearSoftClosed, masterdata.FiscalYearHardClosed,
	} {
		if err := driver.UpdateFiscalYearState(ctx, companyID, closedYearID, state); err != nil {
			t.Fatal(err)
		}
	}
	newYearStart := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	newYearEnd := time.Date(2027, 12, 31, 23, 59, 59, 0, time.UTC)
	newPeriodID := uuid.New()
	newYear := masterdata.FiscalYear{
		ID: uuid.New(), CompanyID: companyID, Name: "FY2027",
		StartDate: newYearStart, EndDate: newYearEnd,
		Periods: []masterdata.FiscalPeriod{{
			ID: newPeriodID, CompanyID: companyID, Number: 1,
			StartDate: newYearStart, EndDate: newYearEnd, State: masterdata.PeriodOpen,
		}},
	}
	if err := driver.CreateFiscalYear(ctx, newYear); err != nil {
		t.Fatal(err)
	}
	if isFirstYear, err := driver.IsFirstFiscalYearForCompanyContext(ctx, companyID, newYear.ID); err != nil || isFirstYear {
		t.Fatalf("subsequent-year check = %v, %v; want false", isFirstYear, err)
	}
	if err := driver.CarryForwardBalancesForCompanyContext(ctx, companyID, closedYearID, newPeriodID); err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var beginningDebit decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT beginning_debit FROM account_balances
			WHERE company_id=$1 AND account_id=$2 AND fiscal_period_id=$3 AND currency_id='SAR'`,
			companyID, debitAccountID, newPeriodID).Scan(&beginningDebit); err != nil {
			return err
		}
		if !beginningDebit.Equal(amount.Amount()) {
			t.Fatalf("carried asset balance = %s, want %s", beginningDebit, amount.Amount())
		}
		var pnlRows int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM account_balances
			WHERE company_id=$1 AND account_id=$2 AND fiscal_period_id=$3`,
			companyID, creditAccountID, newPeriodID).Scan(&pnlRows); err != nil {
			return err
		}
		if pnlRows != 0 {
			t.Fatalf("carried P&L rows = %d, want zero", pnlRows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
