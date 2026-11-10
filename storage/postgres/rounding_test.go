package postgres_test

import (
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TestRoundingDifferenceHandling enforces the precision contract end to end:
// a draft whose functional legs carry fractions below the currency minor unit
// cannot be posted silently, and once the residual is settled on a dedicated
// rounding difference account the move posts, the balances reflect the rounded
// minor units, and the balance reconciliation assertion holds.
func TestRoundingDifferenceHandling(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, debitAccountID, debitAccountTwoID := setupPostgresPostingEnv(t)
	defer driver.Close()

	// Dedicated accounts for this scenario.
	incomeAccountID, roundingAccountID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{
			CompanyID: companyID, ID: incomeAccountID, Code: "4000", Name: "Rounding Scenario Income",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true,
		},
		{
			CompanyID: companyID, ID: roundingAccountID, Code: "5900", Name: "Rounding Difference",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
		},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}

	entryDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	period, err := driver.GetPeriodByDateForCompany(ctx, companyID, entryDate)
	if err != nil {
		t.Fatal(err)
	}

	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	legAmount, err := financialtypes.NewMoneyFromString("10.005", "USD")
	if err != nil {
		t.Fatal(err)
	}
	creditAmount, err := financialtypes.NewMoneyFromString("20.010", "USD")
	if err != nil {
		t.Fatal(err)
	}
	transactionLeg, err := financialtypes.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	transactionCredit, err := financialtypes.NewMoneyFromString("20.01", "USD")
	if err != nil {
		t.Fatal(err)
	}
	fxRate := decimal.RequireFromString("1.0005")

	// buildEntry returns a balanced entry whose functional debit legs are
	// 10.005 USD each and whose functional credit leg is 20.010 USD. The
	// amounts balance exactly at InternalScale but not in minor units.
	buildEntry := func(reference string) posting.JournalEntry {
		t.Helper()
		entry, err := posting.NewJournalEntry(journalID, entryDate, "maker", []posting.JournalEntryLine{
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID,
				TransactionAmount: transactionLeg, TransactionCurrency: "USD",
				ExchangeRate: fxRate, FunctionalDebit: legAmount, FunctionalCredit: zero,
			},
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountTwoID,
				TransactionAmount: transactionLeg, TransactionCurrency: "USD",
				ExchangeRate: fxRate, FunctionalDebit: legAmount, FunctionalCredit: zero,
			},
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: incomeAccountID,
				TransactionAmount: transactionCredit, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: creditAmount,
			},
		})
		if err != nil {
			t.Fatalf("NewJournalEntry: %v", err)
		}
		entry.CompanyID = companyID
		entry.PeriodID = period.ID
		entry.Reference = reference
		return entry
	}

	// 1) Storing the unrounded draft must be rejected by the storage
	// precision guard, leaving no row and no consumed journal sequence.
	unrounded := buildEntry("ROUND-UNROUNDED")
	err = engine.CreateDraftContext(ctx, unrounded)
	if !errors.Is(err, financialtypes.ErrSubMinorUnitAmount) {
		t.Fatalf("saving unrounded draft error = %v, want ErrSubMinorUnitAmount", err)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 0)
	if _, err := driver.GetEntryByIDForCompany(ctx, companyID, unrounded.ID); !errors.Is(err, posting.ErrEntryNotFound) {
		t.Fatalf("unrounded draft lookup error = %v, want ErrEntryNotFound", err)
	}

	// 2) Settle the residual on the rounding difference account, then post.
	settled := buildEntry("ROUND-SETTLED")
	if err := posting.RoundEntryToMinorUnits(&settled, roundingAccountID); err != nil {
		t.Fatalf("RoundEntryToMinorUnits: %v", err)
	}
	if len(settled.Lines) != 4 {
		t.Fatalf("expected a settlement line, got %d lines", len(settled.Lines))
	}
	settlement := settled.Lines[len(settled.Lines)-1]
	if settlement.AccountID != roundingAccountID ||
		!settlement.FunctionalDebit.Amount().Equal(decimal.RequireFromString("0.01")) {
		t.Fatalf("settlement line = account %s debit %s, want rounding account debit 0.01",
			settlement.AccountID, settlement.FunctionalDebit)
	}
	if err := engine.CreateDraftContext(ctx, settled); err != nil {
		t.Fatalf("CreateDraftContext(settled): %v", err)
	}
	if err := engine.PostForCompany(ctx, companyID, settled.ID, "poster"); err != nil {
		t.Fatalf("posting settled entry: %v", err)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 1)

	// 3) The materialized balances must carry the rounded minor units and the
	// rounding difference must live on its dedicated account.
	type balance struct{ debit, credit decimal.Decimal }
	balances := make(map[uuid.UUID]balance)
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT account_id, period_debit, period_credit
			FROM account_balances WHERE company_id=$1 AND fiscal_period_id=$2`,
			companyID, period.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var accountID uuid.UUID
			var value balance
			if err := rows.Scan(&accountID, &value.debit, &value.credit); err != nil {
				return err
			}
			balances[accountID] = value
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	for _, leg := range settled.Lines[:2] {
		got, ok := balances[leg.AccountID]
		if !ok || !got.debit.Equal(decimal.RequireFromString("10.00")) || !got.credit.IsZero() {
			t.Fatalf("debit leg %s balance = %+v, want debit 10.00", leg.AccountID, got)
		}
	}
	if got := balances[incomeAccountID]; !got.credit.Equal(decimal.RequireFromString("20.01")) || !got.debit.IsZero() {
		t.Fatalf("income balance = %+v, want credit 20.01", got)
	}
	if got := balances[roundingAccountID]; !got.debit.Equal(decimal.RequireFromString("0.01")) || !got.credit.IsZero() {
		t.Fatalf("rounding difference balance = %+v, want debit 0.01", got)
	}

	// 4) The reconciliation assertion must pass, then fail once a balance row
	// is corrupted, proving it really compares posted lines to balances.
	if err := driver.AssertBalancesMatchMoves(ctx, companyID, period.ID, "USD"); err != nil {
		t.Fatalf("AssertBalancesMatchMoves: %v", err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_balances SET period_debit = period_debit + 0.01
			WHERE company_id=$1 AND account_id=$2 AND fiscal_period_id=$3`,
			companyID, settled.Lines[0].AccountID, period.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.AssertBalancesMatchMoves(ctx, companyID, period.ID, "USD"); err == nil {
		t.Fatal("expected reconciliation assertion to detect the corrupted balance")
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_balances SET period_debit = period_debit - 0.01
			WHERE company_id=$1 AND account_id=$2 AND fiscal_period_id=$3`,
			companyID, settled.Lines[0].AccountID, period.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.AssertBalancesMatchMoves(ctx, companyID, period.ID, "USD"); err != nil {
		t.Fatalf("reconciliation assertion after restoring the balance: %v", err)
	}
}
