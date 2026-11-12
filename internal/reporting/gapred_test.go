//go:build gapred

// Red tests for the Phase 0 traceability matrix (closing gaps). Run with:
//
//	go test -tags gapred ./internal/reporting -run TestGAP
//
// They are expected to FAIL until P2-T1/P2-T2 make fiscal-year confirmation
// content-aware and atomic.
package reporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

var errInjectedHardClose = errors.New("injected hard-close failure")

// gapClosingRepository wraps the package memory repository with a fixed fiscal
// year and the ability to fail a specific hard-close transition.
type gapClosingRepository struct {
	*memoryRepository
	year              masterdata.FiscalYear
	failHardCloseAt   int
	hardClosedPeriods []uuid.UUID
}

func (r *gapClosingRepository) GetFiscalYearForCompanyContext(
	_ context.Context,
	companyID, yearID uuid.UUID,
) (masterdata.FiscalYear, error) {
	if companyID == r.year.CompanyID && yearID == r.year.ID {
		return r.year, nil
	}
	return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
}

func (r *gapClosingRepository) UpdatePeriodStateForCompanyContext(
	_ context.Context,
	_, periodID uuid.UUID,
	state masterdata.PeriodState,
) error {
	if state == masterdata.PeriodHardClosed {
		r.hardClosedPeriods = append(r.hardClosedPeriods, periodID)
		if r.failHardCloseAt > 0 && len(r.hardClosedPeriods) == r.failHardCloseAt {
			return errInjectedHardClose
		}
	}
	return nil
}

func (r *gapClosingRepository) listClosedPeriodIDs() []uuid.UUID {
	return append([]uuid.UUID(nil), r.hardClosedPeriods...)
}

func gapClosingEnv(t *testing.T) (*gapClosingRepository, masterdata.FiscalYear, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	companyID := uuid.New()
	journalID := uuid.New()
	incomeID, retainedID, cashID := uuid.New(), uuid.New(), uuid.New()

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	for i := range year.Periods {
		if year.Periods[i].Number >= 1 && year.Periods[i].Number <= 12 {
			year.Periods[i].State = masterdata.PeriodHardClosed
		}
	}

	accounts := []masterdata.Account{
		{
			CompanyID: companyID, ID: incomeID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IFRSCategory: masterdata.IFRSCategoryOperating,
		},
		{
			CompanyID: companyID, ID: retainedID, Code: "3000", Name: "Retained Earnings",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			SubClassification: masterdata.SubClassRetainedEarnings,
		},
		{
			CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		},
	}
	incomeEntry, err := posting.NewJournalEntry(journalID, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), "maker",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: reportMoney(t, "100.00", "USD"), Credit: reportMoney(t, "0", "USD")},
			{CompanyID: companyID, ID: uuid.New(), AccountID: incomeID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100.00", "USD")},
		})
	if err != nil {
		t.Fatal(err)
	}
	incomeEntry.CompanyID = companyID
	incomeEntry.Status = posting.EntryPosted

	repo := &gapClosingRepository{
		memoryRepository: &memoryRepository{accounts: accounts, entries: []posting.JournalEntry{incomeEntry}},
		year:             year,
	}
	return repo, year, journalID, retainedID, incomeID
}

func (r *gapClosingRepository) generatedClosingEntry(
	t *testing.T,
	ctx context.Context,
	year masterdata.FiscalYear,
	journalID, retainedID uuid.UUID,
) posting.JournalEntry {
	t.Helper()
	engine := NewClosingEngine(r)
	entry, _, err := engine.GenerateClosingMoveContext(ctx, year, journalID, retainedID, "USD")
	if err != nil {
		t.Fatalf("generate closing move: %v", err)
	}
	period13, ok := findFiscalPeriod(year, 13)
	if !ok {
		t.Fatal("year has no period 13")
	}
	entry.ID = uuid.New()
	entry.Status = posting.EntryPosted
	entry.PeriodID = period13.ID
	entry.EntryDate = year.EndDate
	r.entries = append(r.entries, entry)
	return entry
}

// GAP-CLS-01: ConfirmFiscalYearCloseContext validates only the closing entry's
// metadata (classification, reference, period 13, authorization). It never
// checks that the entry actually closes the year's income and expense balances,
// so an unrelated balanced entry can be accepted as the closing move.
func TestGAP_CLS_01_ConfirmRejectsContentMismatchedClosingEntry(t *testing.T) {
	ctx := context.Background()
	repo, year, journalID, retainedID, incomeID := gapClosingEnv(t)
	period13, ok := findFiscalPeriod(year, 13)
	if !ok {
		t.Fatal("year has no period 13")
	}

	bogus, err := posting.NewJournalEntry(journalID, year.EndDate, "maker",
		[]posting.JournalEntryLine{
			{CompanyID: year.CompanyID, ID: uuid.New(), AccountID: retainedID, Debit: reportMoney(t, "100.00", "USD"), Credit: reportMoney(t, "0", "USD")},
			{CompanyID: year.CompanyID, ID: uuid.New(), AccountID: retainedID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100.00", "USD")},
		})
	if err != nil {
		t.Fatal(err)
	}
	bogus.ID = uuid.New()
	bogus.CompanyID = year.CompanyID
	bogus.Status = posting.EntryPosted
	bogus.Classification = posting.EntryClassClosing
	bogus.DocumentReference = "YEAR-END CLOSING"
	bogus.TargetPeriodNumber = 13
	bogus.AdjustmentAuthorized = true
	bogus.PeriodID = period13.ID
	bogus.EntryDate = year.EndDate
	repo.entries = append(repo.entries, bogus)

	engine := NewClosingEngine(repo)
	if err := engine.ConfirmFiscalYearCloseContext(ctx, year, bogus.ID); err == nil {
		t.Fatalf("GAP-CLS-01: accepted a closing entry that never closes income account %s", incomeID)
	}
}

// GAP-CLS-02: period hard-close and fiscal-year state transitions run as
// separate statements. A failure after period 13 is hard closed leaves the
// year partially closed. Confirmation must be all-or-nothing.
func TestGAP_CLS_02_CloseIsAtomicOnPartialFailure(t *testing.T) {
	ctx := context.Background()
	repo, year, journalID, retainedID, _ := gapClosingEnv(t)
	closing := repo.generatedClosingEntry(t, ctx, year, journalID, retainedID)

	repo.failHardCloseAt = 2
	engine := NewClosingEngine(repo)
	if err := engine.ConfirmFiscalYearCloseContext(ctx, year, closing.ID); err == nil {
		t.Fatal("GAP-CLS-02: expected the injected hard-close failure to abort confirmation")
	}
	if closed := repo.listClosedPeriodIDs(); len(closed) != 0 {
		t.Fatalf("GAP-CLS-02: %d period(s) stayed hard closed after an aborted confirmation", len(closed))
	}
}
