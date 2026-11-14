package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func TestClosingChecklist_BlockersPreventFiscalYearClose(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()

	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "CHK-CO", LegalName: "Checklist Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	cashID, revenueID, retainedEarningsID := uuid.New(), uuid.New(), uuid.New()
	for _, acc := range []masterdata.Account{
		{CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Sales Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
		{CompanyID: companyID, ID: retainedEarningsID, Code: "3000", Name: "Retained Earnings", AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassRetainedEarnings, IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
	}

	closingEngine := reporting.NewClosingEngine(driver)

	// Scenario 1: Initial state - periods 1-12 are still OPEN -> Blocker
	report, err := closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatalf("EvaluateClosingChecklist: %v", err)
	}
	if report.CanClose {
		t.Fatalf("expected CanClose=false when periods 1-12 are open")
	}
	if report.BlockersCount == 0 {
		t.Fatalf("expected at least 1 blocker for open monthly periods")
	}

	// Attempting CloseFiscalYearWithChecklist should fail with ErrClosingPreCheckFailed
	actorID := uuid.New()
	_, _, err = closingEngine.CloseFiscalYearWithChecklist(ctx, year, journalID, retainedEarningsID, actorID, "USD", "Year-end closing attempt")
	if !errors.Is(err, reporting.ErrClosingPreCheckFailed) {
		t.Fatalf("expected ErrClosingPreCheckFailed, got %v", err)
	}

	// Scenario 2: Unposted draft entry exists while periods 1-12 are open
	amt, _ := financialtypes.NewMoneyFromString("500.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	draftEntry, err := posting.NewJournalEntry(journalID, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), "test-draft",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: amt, Credit: zero, TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amt, FunctionalCredit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: amt, TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amt},
		})
	if err != nil {
		t.Fatal(err)
	}
	draftEntry.CompanyID = companyID
	draftEntry.PeriodID = year.Periods[5].ID
	if err := driver.SaveDraft(draftEntry); err != nil {
		t.Fatal(err)
	}

	report, err = closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatal(err)
	}
	if report.CanClose {
		t.Fatalf("expected CanClose=false when draft entry exists")
	}
	var foundDraftBlocker bool
	for _, item := range report.Items {
		if item.Code == reporting.CheckCodeDraftEntriesPending && item.Status == reporting.ChecklistFailed {
			foundDraftBlocker = true
			break
		}
	}
	if !foundDraftBlocker {
		t.Fatalf("expected CheckCodeDraftEntriesPending blocker in checklist")
	}

	// Post the draft entry
	draftEntry.Status = posting.EntryPosted
	draftEntry.PostedBy = "controller"
	if err := driver.PostAtomic(draftEntry); err != nil {
		t.Fatal(err)
	}

	// Close monthly periods 1-12 (must step open -> soft_closed -> hard_closed)
	for _, p := range year.Periods {
		if p.Number <= 12 {
			if err := driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodSoftClosed); err != nil {
				t.Fatal(err)
			}
			if err := driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodHardClosed); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Reload updated year
	year, err = driver.GetFiscalYearForCompanyContext(ctx, companyID, year.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Scenario 3: Unresolved subledger reconciliation exception exists -> Blocker
	unresolvedException := subledgers.ReconciliationException{
		ID:               uuid.New(),
		CompanyID:        companyID,
		Domain:           "receivables",
		ControlAccountID: cashID,
		AsOfDate:         year.EndDate,
		FunctionalCurr:   "USD",
		SubledgerBalance: decimal.RequireFromString("1000.00"),
		GLBalance:        decimal.RequireFromString("500.00"),
		Difference:       decimal.RequireFromString("500.00"),
		Status:           subledgers.ReconciliationExceptionUnmatched,
		ActorID:          actorID,
		Reason:           "Unmatched subledger variance",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := driver.CreateReconciliationException(ctx, unresolvedException); err != nil {
		t.Fatal(err)
	}

	report, err = closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatal(err)
	}
	if report.CanClose {
		t.Fatalf("expected CanClose=false when subledger exception is unmatched")
	}

	// Resolve the subledger exception
	if err := driver.TransitionReconciliationException(ctx, companyID, unresolvedException.ID,
		subledgers.ReconciliationExceptionUnmatched, subledgers.ReconciliationExceptionMatching,
		actorID, "Matching in progress", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := driver.TransitionReconciliationException(ctx, companyID, unresolvedException.ID,
		subledgers.ReconciliationExceptionMatching, subledgers.ReconciliationExceptionResolved,
		actorID, "Reconciled variance verified by auditor", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Scenario 4: All blockers cleared -> CanClose should be TRUE!
	report, err = closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatal(err)
	}
	if !report.CanClose {
		t.Fatalf("expected CanClose=true after resolving all blockers, summary: %s", report.Summary())
	}
	if report.BlockersCount != 0 {
		t.Fatalf("expected 0 blockers, got %d", report.BlockersCount)
	}

	// Execute Close with checklist
	closingEntry, closeReport, err := closingEngine.CloseFiscalYearWithChecklist(
		ctx, year, journalID, retainedEarningsID, actorID, "USD", "Annual closing 2026",
	)
	if err != nil {
		t.Fatalf("CloseFiscalYearWithChecklist: %v", err)
	}
	if !closeReport.CanClose {
		t.Fatalf("closeReport.CanClose should be true")
	}

	// Post the closing entry into Period 13
	p13 := year.Periods[12]
	closingEntry.PeriodID = p13.ID
	if err := driver.SaveDraft(closingEntry); err != nil {
		t.Fatalf("SaveDraft closing entry: %v", err)
	}
	closingEntry.Status = posting.EntryPosted
	closingEntry.PostedBy = "controller"
	if err := driver.PostAtomic(closingEntry); err != nil {
		t.Fatalf("PostAtomic closing entry: %v", err)
	}

	// Confirm fiscal year close -> hard closes year and Period 13
	if err := closingEngine.ConfirmFiscalYearClose(year, closingEntry.ID); err != nil {
		t.Fatalf("ConfirmFiscalYearClose: %v", err)
	}

	closedYear, err := driver.GetFiscalYearForCompanyContext(ctx, companyID, year.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		t.Fatalf("expected FiscalYearHardClosed, got %s", closedYear.State)
	}

	// Scenario 5: Audited Reopening Lifecycle
	// Reopening without actor or reason must fail
	if err := closingEngine.ReopenFiscalYear(ctx, companyID, year.ID, uuid.Nil, "reason"); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("reopen with nil actor must fail with ErrPeriodReopenRequiresAudit, got %v", err)
	}
	if err := closingEngine.ReopenFiscalYear(ctx, companyID, year.ID, actorID, ""); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("reopen with empty reason must fail with ErrPeriodReopenRequiresAudit, got %v", err)
	}

	// Valid audited reopen
	if err := closingEngine.ReopenFiscalYear(ctx, companyID, year.ID, actorID, "Authorized post-audit adjustment"); err != nil {
		t.Fatalf("ReopenFiscalYear: %v", err)
	}

	reopenedYear, err := driver.GetFiscalYearForCompanyContext(ctx, companyID, year.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedYear.State != masterdata.FiscalYearOpen {
		t.Fatalf("expected FiscalYearOpen after reopen, got %s", reopenedYear.State)
	}
	// Period 13 must also be reopened
	p13Updated, ok := reporting.FindFiscalPeriod(reopenedYear, 13)
	if !ok || p13Updated.State != masterdata.PeriodOpen {
		t.Fatalf("expected Period 13 to be Open, got ok=%v, state=%s", ok, p13Updated.State)
	}
}

func TestClosingChecklist_SuspenseAccountBlocker(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()

	_ = driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "SUSP-CO", LegalName: "Suspense Test Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	})
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	_ = driver.CreateFiscalYear(ctx, year)

	journalID := uuid.New()
	_ = driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true,
	})

	suspenseID, cashID := uuid.New(), uuid.New()
	_ = driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: suspenseID, Code: "9999", Name: "Suspense Clearing Account",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	})
	_ = driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	})

	// Post an entry leaving 150.00 in Suspense while period is open
	amt, _ := financialtypes.NewMoneyFromString("150.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	entry, _ := posting.NewJournalEntry(journalID, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "suspense-post",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: suspenseID, Debit: amt, Credit: zero, TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amt, FunctionalCredit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: zero, Credit: amt, TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amt},
		})
	entry.CompanyID = companyID
	entry.PeriodID = year.Periods[2].ID
	if err := driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	entry.PostedBy = "controller"
	if err := driver.PostAtomic(entry); err != nil {
		t.Fatal(err)
	}

	// Now close periods 1-12
	for _, p := range year.Periods {
		if p.Number <= 12 {
			_ = driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodSoftClosed)
			_ = driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodHardClosed)
		}
	}
	year, _ = driver.GetFiscalYearForCompanyContext(ctx, companyID, year.ID)

	closingEngine := reporting.NewClosingEngine(driver)
	report, err := closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatal(err)
	}

	if report.CanClose {
		t.Fatalf("expected CanClose=false when suspense account has non-zero balance")
	}

	var foundSuspenseBlocker bool
	for _, item := range report.Items {
		if item.Code == reporting.CheckCodeSuspenseAccountsNonZero && item.Status == reporting.ChecklistFailed {
			foundSuspenseBlocker = true
			break
		}
	}
	if !foundSuspenseBlocker {
		t.Fatalf("expected CheckCodeSuspenseAccountsNonZero blocker in report")
	}
}

func TestClosedPeriodFinancialReportsReproducibility(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()

	_ = driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "REP-CO", LegalName: "Reports Reproducibility Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	})
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	_ = driver.CreateFiscalYear(ctx, year)

	journalID := uuid.New()
	_ = driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true,
	})

	cashID, revenueID, expenseID := uuid.New(), uuid.New(), uuid.New()
	for _, acc := range []masterdata.Account{
		{CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Sales Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
		{CompanyID: companyID, ID: expenseID, Code: "5000", Name: "Operating Expenses", AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		_ = driver.CreateAccount(ctx, acc)
	}

	// Post sales in P1
	saleAmt, _ := financialtypes.NewMoneyFromString("1000.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	saleEntry, _ := posting.NewJournalEntry(journalID, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), "sale",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: saleAmt, Credit: zero, TransactionAmount: saleAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: saleAmt, FunctionalCredit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: saleAmt, TransactionAmount: saleAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: saleAmt},
		})
	saleEntry.CompanyID = companyID
	saleEntry.PeriodID = year.Periods[0].ID
	_ = driver.SaveDraft(saleEntry)
	saleEntry.Status = posting.EntryPosted
	saleEntry.PostedBy = "controller"
	_ = driver.PostAtomic(saleEntry)

	// Post expense in P2
	expAmt, _ := financialtypes.NewMoneyFromString("400.00", "USD")
	expEntry, _ := posting.NewJournalEntry(journalID, time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC), "exp",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: expenseID, Debit: expAmt, Credit: zero, TransactionAmount: expAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: expAmt, FunctionalCredit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: zero, Credit: expAmt, TransactionAmount: expAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: expAmt},
		})
	expEntry.CompanyID = companyID
	expEntry.PeriodID = year.Periods[1].ID
	_ = driver.SaveDraft(expEntry)
	expEntry.Status = posting.EntryPosted
	expEntry.PostedBy = "controller"
	_ = driver.PostAtomic(expEntry)

	// Close periods 1-12
	for _, p := range year.Periods {
		if p.Number <= 12 {
			_ = driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodSoftClosed)
			_ = driver.UpdatePeriodStateForCompanyContext(ctx, companyID, p.ID, masterdata.PeriodHardClosed)
		}
	}

	// Generating financial reports at multiple subsequent times yields strictly identical figures
	tbEngine := reporting.NewTrialBalanceEngine(driver)
	isEngine := reporting.NewIncomeStatementEngine(driver)

	tb1, err := tbEngine.GenerateForCompany(companyID, year.StartDate, year.EndDate, "USD")
	if err != nil {
		t.Fatal(err)
	}
	tb2, err := tbEngine.GenerateForCompany(companyID, year.StartDate, year.EndDate, "USD")
	if err != nil {
		t.Fatal(err)
	}

	if !tb1.Balanced || !tb2.Balanced {
		t.Fatalf("expected balanced trial balance for closed period")
	}
	if !tb1.DebitTotal.Equal(tb2.DebitTotal) || !tb1.CreditTotal.Equal(tb2.CreditTotal) {
		t.Fatalf("trial balance not reproducible across runs: %s vs %s", tb1.DebitTotal, tb2.DebitTotal)
	}

	is1, err := isEngine.Generate(year.StartDate, year.EndDate, "USD")
	if err != nil {
		t.Fatal(err)
	}
	is2, err := isEngine.Generate(year.StartDate, year.EndDate, "USD")
	if err != nil {
		t.Fatal(err)
	}

	if !is1.NetProfit.Equal(is2.NetProfit) || !is1.OperatingProfit.Equal(is2.OperatingProfit) {
		t.Fatalf("income statement not reproducible: %s vs %s", is1.NetProfit, is2.NetProfit)
	}

	// Verify posting attempt into closed period is rejected
	lateEntry, _ := posting.NewJournalEntry(journalID, time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC), "late-attempt",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: saleAmt, Credit: zero, TransactionAmount: saleAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: saleAmt, FunctionalCredit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: saleAmt, TransactionAmount: saleAmt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: saleAmt},
		})
	lateEntry.CompanyID = companyID
	lateEntry.PeriodID = year.Periods[0].ID
	lateEntry.Status = posting.EntryDraft
	if err := driver.SaveDraft(lateEntry); err != nil {
		t.Fatalf("failed to save draft lateEntry: %v", err)
	}
	lateEntry.Status = posting.EntryPosted
	if err := driver.PostAtomic(lateEntry); !errors.Is(err, masterdata.ErrFiscalPeriodClosed) {
		t.Fatalf("expected ErrFiscalPeriodClosed for posting into closed period, got %v", err)
	}
}
