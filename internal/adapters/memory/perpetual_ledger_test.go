package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type perpetualLedgerFixture struct {
	driver    *Driver
	companyID uuid.UUID
	journalID uuid.UUID
	year      masterdata.FiscalYear
	debitID   uuid.UUID
	creditID  uuid.UUID
	revenueID uuid.UUID
	amount    financialtypes.Money
	zero      financialtypes.Money
}

func newPerpetualLedgerFixture(t testing.TB) perpetualLedgerFixture {
	t.Helper()
	ctx := context.Background()
	driver := New()
	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "BAL-" + companyID.String()[:8], LegalName: "Balance Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	year := masterdata.GenerateFiscalYear(start)
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	accounts := []masterdata.Account{
		{CompanyID: companyID, ID: uuid.New(), Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit},
		{CompanyID: companyID, ID: uuid.New(), Code: "3000", Name: "Capital", AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit},
		{CompanyID: companyID, ID: uuid.New(), Code: "4000", Name: "Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit},
	}
	for _, account := range accounts {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	journalID := uuid.New()
	driver.journals[journalID] = journalRecord{ID: journalID, CompanyID: companyID}
	amount, err := financialtypes.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	return perpetualLedgerFixture{
		driver: driver, companyID: companyID, journalID: journalID, year: year,
		debitID: accounts[0].ID, creditID: accounts[1].ID, revenueID: accounts[2].ID,
		amount: amount, zero: zero,
	}
}

func (f perpetualLedgerFixture) entry(date time.Time, periodID uuid.UUID, lines ...posting.JournalEntryLine) posting.JournalEntry {
	for i := range lines {
		lines[i].CompanyID = f.companyID
	}
	return posting.JournalEntry{
		CompanyID: f.companyID, ID: uuid.New(), JournalID: f.journalID, PeriodID: periodID,
		EntryDate: date, Date: date, Status: posting.EntryDraft, CreatedBy: "test",
		Classification: posting.EntryClassRegular, Lines: lines,
	}
}

func (f perpetualLedgerFixture) post(t *testing.T, entry posting.JournalEntry) posting.JournalEntry {
	t.Helper()
	if err := f.driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	posted, err := f.driver.PostAndSequence(entry)
	if err != nil {
		t.Fatal(err)
	}
	return posted
}

func TestPostingUpdatesMaterializedBalancesAndReversalOffsets(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	period := fixture.year.Periods[0]
	entry := fixture.entry(period.StartDate.Add(time.Hour), period.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)
	posted := fixture.post(t, entry)

	debitKey := accountBalanceKey{fixture.companyID, fixture.debitID, period.ID, "USD"}
	creditKey := accountBalanceKey{fixture.companyID, fixture.creditID, period.ID, "USD"}
	if got := fixture.driver.balances[debitKey].periodDebit; !got.Equal(fixture.amount.Amount()) {
		t.Fatalf("debit materialized balance = %s, want %s", got, fixture.amount.Amount())
	}

	reversal := fixture.entry(period.StartDate.Add(2*time.Hour), period.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.zero, Credit: fixture.amount},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.amount, Credit: fixture.zero},
	)
	reversal.OriginalEntryID = &posted.ID
	if err := fixture.driver.SaveDraft(reversal); err != nil {
		t.Fatal(err)
	}
	reversal.Status = posting.EntryPosted
	if _, err := fixture.driver.PostReversalAtomic(posted, reversal); err != nil {
		t.Fatal(err)
	}
	if got := fixture.driver.balances[debitKey].periodDebit.Sub(fixture.driver.balances[debitKey].periodCredit); !got.IsZero() {
		t.Fatalf("reversed debit account balance = %s, want zero", got)
	}
	if got := fixture.driver.balances[creditKey].periodCredit.Sub(fixture.driver.balances[creditKey].periodDebit); !got.IsZero() {
		t.Fatalf("reversed credit account balance = %s, want zero", got)
	}
}

func TestDraftRejectsJournalAndPeriodOutsideCompany(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	period := fixture.year.Periods[0]
	entry := fixture.entry(period.StartDate.Add(time.Hour), period.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)

	wrongJournal := entry
	wrongJournal.ID = uuid.New()
	wrongJournal.JournalID = uuid.New()
	foreignCompanyID := uuid.New()
	fixture.driver.journals[wrongJournal.JournalID] = journalRecord{
		ID: wrongJournal.JournalID, CompanyID: foreignCompanyID,
	}
	if err := fixture.driver.SaveDraft(wrongJournal); !errors.Is(err, masterdata.ErrJournalNotFound) {
		t.Fatalf("SaveDraft() with another company's journal error = %v, want ErrJournalNotFound", err)
	}

	wrongPeriod := entry
	wrongPeriod.ID = uuid.New()
	wrongPeriod.PeriodID = uuid.New()
	fixture.driver.periods[wrongPeriod.PeriodID] = masterdata.FiscalPeriod{
		ID: wrongPeriod.PeriodID, CompanyID: foreignCompanyID,
	}
	if err := fixture.driver.SaveDraft(wrongPeriod); !errors.Is(err, masterdata.ErrFiscalPeriodNotFound) {
		t.Fatalf("SaveDraft() with another company's period error = %v, want ErrFiscalPeriodNotFound", err)
	}

	foreignAccountID := uuid.New()
	fixture.driver.accounts[foreignAccountID] = masterdata.Account{
		ID: foreignAccountID, CompanyID: foreignCompanyID,
	}
	wrongAccount := entry
	wrongAccount.ID = uuid.New()
	wrongAccount.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	wrongAccount.Lines[0].AccountID = foreignAccountID
	if err := fixture.driver.SaveDraft(wrongAccount); !errors.Is(err, masterdata.ErrAccountNotFound) {
		t.Fatalf("SaveDraft() with another company's account error = %v, want ErrAccountNotFound", err)
	}

	wrongCompany := entry
	wrongCompany.ID = uuid.New()
	wrongCompany.CompanyID = foreignCompanyID
	if err := fixture.driver.SaveDraft(wrongCompany); !errors.Is(err, masterdata.ErrInvalidCompany) {
		t.Fatalf("SaveDraft() with unknown company error = %v, want ErrInvalidCompany", err)
	}
}

func TestFailedPostingDoesNotChangeMaterializedBalances(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	period := fixture.year.Periods[0]
	entry := fixture.entry(period.StartDate.Add(time.Hour), period.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)
	if err := fixture.driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	delete(fixture.driver.periods, period.ID)
	entry.Status = posting.EntryPosted
	if _, err := fixture.driver.PostAndSequence(entry); !errors.Is(err, masterdata.ErrFiscalPeriodNotFound) {
		t.Fatalf("posting with unknown period error = %v, want fiscal period not found", err)
	}
	if len(fixture.driver.balances) != 0 {
		t.Fatalf("failed posting changed materialized balances: %+v", fixture.driver.balances)
	}
}

func TestFailedBulkPostingDoesNotPartiallyUpdateBalances(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	firstPeriod, secondPeriod := fixture.year.Periods[0], fixture.year.Periods[1]
	valid := fixture.entry(firstPeriod.StartDate.Add(time.Hour), firstPeriod.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)
	invalid := fixture.entry(secondPeriod.StartDate.Add(2*time.Hour), secondPeriod.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)
	if err := fixture.driver.SaveDraft(valid); err != nil {
		t.Fatal(err)
	}
	if err := fixture.driver.SaveDraft(invalid); err != nil {
		t.Fatal(err)
	}
	secondPeriod.CompanyID = uuid.New()
	fixture.driver.periods[secondPeriod.ID] = secondPeriod
	valid.Status = posting.EntryPosted
	invalid.Status = posting.EntryPosted
	if err := fixture.driver.PostBulk(context.Background(), []posting.JournalEntry{valid, invalid}); !errors.Is(err, masterdata.ErrFiscalPeriodNotFound) {
		t.Fatalf("bulk posting error = %v, want fiscal period not found", err)
	}
	if len(fixture.driver.balances) != 0 || len(fixture.driver.entries) != 2 {
		t.Fatalf("failed bulk posting partially changed state: balances=%d entries=%d",
			len(fixture.driver.balances), len(fixture.driver.entries))
	}
	for _, entry := range []posting.JournalEntry{valid, invalid} {
		stored, err := fixture.driver.GetEntryByID(entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != posting.EntryDraft {
			t.Fatalf("failed bulk posting changed entry %s to %s", entry.ID, stored.Status)
		}
	}
}

func TestBulkPostingRejectsDuplicateEntriesWithoutChangingBalances(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	period := fixture.year.Periods[0]
	entry := fixture.entry(period.StartDate.Add(time.Hour), period.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	)
	if err := fixture.driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if err := fixture.driver.PostBulk(context.Background(), []posting.JournalEntry{entry, entry}); !errors.Is(err, posting.ErrEntryNotDraft) {
		t.Fatalf("duplicate bulk entry error = %v, want %v", err, posting.ErrEntryNotDraft)
	}
	if len(fixture.driver.balances) != 0 {
		t.Fatalf("duplicate bulk posting changed materialized balances: %+v", fixture.driver.balances)
	}
	stored, err := fixture.driver.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("duplicate bulk posting changed entry state to %s", stored.Status)
	}
}

func TestConcurrentPostingKeepsMaterializedDebitsAndCreditsEqual(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	const entries = 1000
	period := fixture.year.Periods[0]
	var wait sync.WaitGroup
	errorsFound := make(chan error, entries)
	for i := 0; i < entries; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			entry := fixture.entry(period.StartDate.Add(time.Hour), period.ID,
				posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
				posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
			)
			if err := fixture.driver.SaveDraft(entry); err != nil {
				errorsFound <- err
				return
			}
			entry.Status = posting.EntryPosted
			if _, err := fixture.driver.PostAndSequence(entry); err != nil {
				errorsFound <- err
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	debit := fixture.driver.balances[accountBalanceKey{fixture.companyID, fixture.debitID, period.ID, "USD"}]
	credit := fixture.driver.balances[accountBalanceKey{fixture.companyID, fixture.creditID, period.ID, "USD"}]
	want := fixture.amount.Amount().Mul(decimal.NewFromInt(entries))
	if !debit.periodDebit.Equal(want) || !credit.periodCredit.Equal(want) {
		t.Fatalf("concurrent totals debit=%s credit=%s; want both %s", debit.periodDebit, credit.periodCredit, want)
	}
}

func TestCarryForwardCopiesOnlyBalanceSheetAccounts(t *testing.T) {
	fixture := newPerpetualLedgerFixture(t)
	oldYear := fixture.year
	lastPeriod := oldYear.Periods[len(oldYear.Periods)-1]
	five, err := financialtypes.NewMoneyFromString("5.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	firstPeriod := oldYear.Periods[0]
	fixture.post(t, fixture.entry(firstPeriod.StartDate.Add(time.Hour), firstPeriod.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: fixture.amount},
	))
	fixture.post(t, fixture.entry(lastPeriod.StartDate.Add(time.Minute), lastPeriod.ID,
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.debitID, Debit: fixture.amount, Credit: fixture.zero},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.creditID, Debit: fixture.zero, Credit: five},
		posting.JournalEntryLine{ID: uuid.New(), AccountID: fixture.revenueID, Debit: fixture.zero, Credit: five},
	))

	if err := fixture.driver.UpdateFiscalYearState(context.Background(), fixture.companyID, oldYear.ID, masterdata.FiscalYearSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.driver.UpdateFiscalYearState(context.Background(), fixture.companyID, oldYear.ID, masterdata.FiscalYearHardClosed); err != nil {
		t.Fatal(err)
	}
	oldYear.State = masterdata.FiscalYearHardClosed
	newYear := masterdata.GenerateFiscalYear(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	newYear.CompanyID = fixture.companyID
	if err := fixture.driver.CreateFiscalYear(context.Background(), newYear); err != nil {
		t.Fatal(err)
	}
	if err := fixture.driver.CarryForwardBalancesForCompanyContext(
		context.Background(), fixture.companyID, oldYear.ID, newYear.Periods[0].ID,
	); err != nil {
		t.Fatal(err)
	}

	newBalance := fixture.driver.balances[accountBalanceKey{
		fixture.companyID, fixture.debitID, newYear.Periods[0].ID, "USD",
	}]
	wantAssetBalance := fixture.amount.Amount().Mul(decimal.NewFromInt(2))
	if !newBalance.beginningDebit.Equal(wantAssetBalance) {
		t.Fatalf("carried asset beginning balance = %s, want %s", newBalance.beginningDebit, wantAssetBalance)
	}
	newEquityBalance := fixture.driver.balances[accountBalanceKey{
		fixture.companyID, fixture.creditID, newYear.Periods[0].ID, "USD",
	}]
	if !newEquityBalance.beginningCredit.Equal(fixture.amount.Amount().Add(five.Amount())) {
		t.Fatalf("carried equity beginning balance = %s", newEquityBalance.beginningCredit)
	}
	if _, exists := fixture.driver.balances[accountBalanceKey{
		fixture.companyID, fixture.revenueID, newYear.Periods[0].ID, "USD",
	}]; exists {
		t.Fatal("P&L account was carried forward")
	}
	materialized, available, err := fixture.driver.ReadAccountPeriodBalancesForCompanyContext(
		context.Background(), fixture.companyID, newYear.Periods[0].StartDate,
		newYear.Periods[0].EndDate, "USD", true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !available || !materialized[fixture.debitID].EndingDebit.Equal(wantAssetBalance) {
		t.Fatalf("new-year materialized balance = %+v available=%v", materialized[fixture.debitID], available)
	}
}

func BenchmarkMaterializedBalanceRead10KAccounts(b *testing.B) {
	fixture := newPerpetualLedgerFixture(b)
	period := fixture.year.Periods[0]
	for i := range 10_000 {
		fixture.driver.balances[accountBalanceKey{
			companyID: fixture.companyID, accountID: uuid.New(), periodID: period.ID, currency: "USD",
		}] = accountBalance{periodDebit: decimal.NewFromInt(int64(i)), periodCredit: decimal.NewFromInt(int64(i))}
	}
	start, end := period.StartDate, period.EndDate

	for b.Loop() {
		if _, ok, err := fixture.driver.ReadAccountPeriodBalancesForCompanyContext(
			context.Background(), fixture.companyID, start, end, "USD", true,
		); err != nil || !ok {
			b.Fatalf("materialized read unavailable: ok=%v err=%v", ok, err)
		}
	}
}
