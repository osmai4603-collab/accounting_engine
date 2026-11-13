package postgres_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	accountingengine "github.com/osmai4603-collab/accounting_engine"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// TestReconciliationDetectsDriftAndZeroTolerance proves the continuous balance
// reconciliation engine compares the materialized account_balances rows against
// the posted account_move_lines in exact integer minor units with zero
// tolerance:
//   - a healthy ledger reconciles with a zero-drift report;
//   - tampering with account_balances by one minor unit is detected with the
//     exact per-account drift;
//   - tampering by a tenth of a minor unit is still detected (no tolerance);
//   - tampering with a posted line behind the immutability trigger's back is
//     detected from the ledger side;
//   - restoring the tampered value returns the system to zero drift.
//
// The balance side is mutated directly through the pool because
// account_balances has no immutability trigger (it is the drift vector), and
// the ledger side is mutated only after disabling trg_protect_posted_move_lines
// inside a company transaction, proving the reconciliation engine is the safety
// net beneath the database guards.
func TestReconciliationDetectsDriftAndZeroTolerance(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	// Post five varied entries through the real posting path so both the
	// ledger and the materialized balances carry non-trivial amounts.
	amounts := []string{"100.12", "250.34", "999.99", "0.01", "500.50"}
	expectedDebitMinor := decimal.Zero
	entryDate := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	postedEntryIDs := make([]uuid.UUID, 0, len(amounts))
	for i, amountText := range amounts {
		amount, err := financialtypes.NewMoneyFromString(amountText, "USD")
		if err != nil {
			t.Fatal(err)
		}
		zero, err := financialtypes.Zero("USD")
		if err != nil {
			t.Fatal(err)
		}
		minor, err := amount.MinorUnitsExact()
		if err != nil {
			t.Fatal(err)
		}
		entry, err := posting.NewJournalEntry(env.JournalID, entryDate, "reconcile-maker", []posting.JournalEntryLine{
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
		})
		if err != nil {
			t.Fatal(err)
		}
		entry.CompanyID = env.CompanyID
		entry.PeriodID = env.PeriodID
		entry.Reference = fmt.Sprintf("RECON-%02d", i)
		if err := env.Driver.SaveDraft(ctx, entry); err != nil {
			t.Fatalf("save draft %d: %v", i, err)
		}
		entry.Status = posting.EntryPosted
		entry.PostedBy = "reconcile-checker"
		posted, err := env.Driver.PostAndSequence(ctx, entry)
		if err != nil {
			t.Fatalf("post entry %d: %v", i, err)
		}
		postedEntryIDs = append(postedEntryIDs, posted.ID)
		expectedDebitMinor = expectedDebitMinor.Add(decimal.NewFromInt(minor))
	}

	engine, err := accountingengine.New(env.Driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	assertZeroDrift := func(stage string) {
		t.Helper()
		report, err := engine.ReconcileBalances(ctx, env.CompanyID, env.PeriodID, "USD")
		if err != nil {
			t.Fatalf("%s: reconcile balances: %v", stage, err)
		}
		if report == nil || !report.ZeroDrift || report.DriftedAccounts() != 0 {
			t.Fatalf("%s: report = %+v, want zero drift", stage, report)
		}
		if report.AccountsChecked != 2 {
			t.Fatalf("%s: accounts checked = %d, want 2 (debit + credit)", stage, report.AccountsChecked)
		}
		if report.CurrencyID != "USD" || report.CompanyID != env.CompanyID || report.FiscalPeriodID != env.PeriodID {
			t.Fatalf("%s: report scope = %+v", stage, report)
		}
		detail, err := env.Driver.BuildBalanceReconciliationReport(ctx, env.CompanyID, env.PeriodID, "USD")
		if err != nil {
			t.Fatalf("%s: build detailed report: %v", stage, err)
		}
		if !detail.ZeroDrift || len(detail.Drifts) != 0 {
			t.Fatalf("%s: detailed report = %+v, want zero drift", stage, detail)
		}
		if err := env.Driver.AssertBalancesMatchMoves(ctx, env.CompanyID, env.PeriodID, "USD"); err != nil {
			t.Fatalf("%s: assert balances match moves: %v", stage, err)
		}
	}

	driftReport := func(stage string) reporting.ReconciliationReport {
		t.Helper()
		report, err := engine.ReconcileBalances(ctx, env.CompanyID, env.PeriodID, "USD")
		if !errors.Is(err, reporting.ErrBalanceDriftDetected) {
			t.Fatalf("%s: reconcile error = %v, want ErrBalanceDriftDetected", stage, err)
		}
		if report != nil {
			t.Fatalf("%s: facade report = %+v, want nil on drift", stage, report)
		}
		detail, err := env.Driver.BuildBalanceReconciliationReport(ctx, env.CompanyID, env.PeriodID, "USD")
		if err != nil {
			t.Fatalf("%s: build detailed report: %v", stage, err)
		}
		if detail.ZeroDrift {
			t.Fatalf("%s: detailed report reports zero drift", stage)
		}
		if detail.AccountsChecked != 2 || detail.DriftedAccounts() != 1 {
			t.Fatalf("%s: detailed report checked %d accounts with %d drifts, want 2/1",
				stage, detail.AccountsChecked, detail.DriftedAccounts())
		}
		if err := env.Driver.AssertBalancesMatchMoves(ctx, env.CompanyID, env.PeriodID, "USD"); err == nil {
			t.Fatalf("%s: assert balances match moves passed, want failure", stage)
		}
		return detail
	}

	// Healthy state: the facade, the detailed report, and the assertion all
	// agree that balances equal the posted lines exactly.
	assertZeroDrift("initial clean state")

	// Drift vector 1: inflate the materialized debit balance by exactly one
	// minor unit (0.01 USD) directly in the database.
	var balanceDebitBackup, balanceCreditBackup decimal.Decimal
	if err := env.Driver.Pool().QueryRow(ctx,
		`SELECT period_debit, period_credit FROM account_balances
		 WHERE company_id=$1 AND fiscal_period_id=$2 AND currency_id='USD' AND account_id=$3`,
		env.CompanyID, env.PeriodID, env.DebitAccID).Scan(&balanceDebitBackup, &balanceCreditBackup); err != nil {
		t.Fatal(err)
	}
	if !balanceDebitBackup.Equal(expectedDebitMinor.Div(decimal.NewFromInt(100))) {
		t.Fatalf("posted period_debit = %s, want %s", balanceDebitBackup, expectedDebitMinor)
	}
	restoreBalance := func() {
		if _, err := env.Driver.Pool().Exec(ctx,
			`UPDATE account_balances SET period_debit=$4, period_credit=$5
			 WHERE company_id=$1 AND fiscal_period_id=$2 AND account_id=$3`,
			env.CompanyID, env.PeriodID, env.DebitAccID, balanceDebitBackup, balanceCreditBackup); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := env.Driver.Pool().Exec(ctx,
		`UPDATE account_balances SET period_debit = period_debit + 0.01
		 WHERE company_id=$1 AND fiscal_period_id=$2 AND currency_id='USD' AND account_id=$3`,
		env.CompanyID, env.PeriodID, env.DebitAccID); err != nil {
		t.Fatal(err)
	}
	detail := driftReport("one minor unit balance drift")
	drift := detail.Drifts[0]
	if drift.AccountID != env.DebitAccID {
		t.Fatalf("drift account = %s, want the tampered debit account %s", drift.AccountID, env.DebitAccID)
	}
	if !drift.LedgerDebitMinor.Equal(expectedDebitMinor) {
		t.Fatalf("ledger debit minor = %s, want %s", drift.LedgerDebitMinor, expectedDebitMinor)
	}
	if !drift.BalanceDebitMinor.Equal(expectedDebitMinor.Add(decimal.NewFromInt(1))) {
		t.Fatalf("balance debit minor = %s, want %s", drift.BalanceDebitMinor, expectedDebitMinor.Add(decimal.NewFromInt(1)))
	}
	if !drift.DebitDriftMinor.Equal(decimal.NewFromInt(-1)) {
		t.Fatalf("debit drift minor = %s, want -1", drift.DebitDriftMinor)
	}
	if !drift.CreditDriftMinor.IsZero() {
		t.Fatalf("credit drift minor = %s, want 0", drift.CreditDriftMinor)
	}
	restoreBalance()
	assertZeroDrift("after restoring the one minor unit drift")

	// Drift vector 2: zero tolerance — a tenth of a minor unit (0.0001 in
	// currency units) must still fail the reconciliation.
	if _, err := env.Driver.Pool().Exec(ctx,
		`UPDATE account_balances SET period_debit = period_debit + 0.0001
		 WHERE company_id=$1 AND fiscal_period_id=$2 AND currency_id='USD' AND account_id=$3`,
		env.CompanyID, env.PeriodID, env.DebitAccID); err != nil {
		t.Fatal(err)
	}
	detail = driftReport("sub-minor-unit balance drift")
	drift = detail.Drifts[0]
	if !drift.DebitDriftMinor.Equal(decimal.RequireFromString("-0.01")) {
		t.Fatalf("sub-minor debit drift = %s, want -0.01 minor units", drift.DebitDriftMinor)
	}
	restoreBalance()
	assertZeroDrift("after restoring the sub-minor-unit drift")

	// Drift vector 3: tamper with the authoritative ledger side. The
	// immutability trigger must reject the direct update first, so the drift
	// can only be injected by disabling the guard — exactly the scenario the
	// reconciliation engine exists to catch.
	ledgerTamperSQL := `UPDATE account_move_lines SET functional_debit_minor = functional_debit_minor + 1
		WHERE company_id=$1 AND account_id=$2 AND move_id=$3`
	err = env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, ledgerTamperSQL, env.CompanyID, env.DebitAccID, postedEntryIDs[0])
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23001" {
		t.Fatalf("posted line tamper error = %v, want SQLSTATE 23001 from the immutability trigger", err)
	}

	alterLedgerGuard := func(tx pgx.Tx, enable bool) error {
		action := "DISABLE"
		if enable {
			action = "ENABLE"
		}
		_, err := tx.Exec(ctx, "ALTER TABLE account_move_lines "+action+" TRIGGER trg_protect_posted_move_lines")
		return err
	}
	applyLedgerDelta := func(moveID uuid.UUID, delta int64) {
		t.Helper()
		err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
			if err := alterLedgerGuard(tx, false); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE account_move_lines SET functional_debit_minor = functional_debit_minor + $4
				 WHERE company_id=$1 AND account_id=$2 AND move_id=$3`,
				env.CompanyID, env.DebitAccID, moveID, delta); err != nil {
				return err
			}
			return alterLedgerGuard(tx, true)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	applyLedgerDelta(postedEntryIDs[0], 1)
	detail = driftReport("posted line tamper behind the guard")
	drift = detail.Drifts[0]
	if !drift.LedgerDebitMinor.Equal(expectedDebitMinor.Add(decimal.NewFromInt(1))) {
		t.Fatalf("tampered ledger debit minor = %s, want %s", drift.LedgerDebitMinor, expectedDebitMinor.Add(decimal.NewFromInt(1)))
	}
	if !drift.BalanceDebitMinor.Equal(expectedDebitMinor) {
		t.Fatalf("balance debit minor = %s, want %s", drift.BalanceDebitMinor, expectedDebitMinor)
	}
	if !drift.DebitDriftMinor.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("ledger debit drift minor = %s, want 1", drift.DebitDriftMinor)
	}
	applyLedgerDelta(postedEntryIDs[0], -1)
	assertZeroDrift("after restoring the tampered posted line")
}
