package externalconsumer_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"

	"github.com/shopspring/decimal"
)

// externalPortsAdapter is implemented entirely outside the accounting_engine
// module. It imports no internal package and knows nothing about SQL, pgx, or
// table names: it only implements the published storage ports.
type externalPortsAdapter struct {
	mu       sync.Mutex
	company  masterdata.Company
	journal  masterdata.Journal
	accounts map[types.AccountID]masterdata.Account
	year     masterdata.FiscalYear
	period   masterdata.FiscalPeriod
	entries  map[types.JournalEntryID]ledger.JournalEntry
	sequence int64
}

var (
	_ storage.DraftStore        = (*externalPortsAdapter)(nil)
	_ storage.EntryReader       = (*externalPortsAdapter)(nil)
	_ storage.PostingWriter     = (*externalPortsAdapter)(nil)
	_ storage.SequenceAllocator = (*externalPortsAdapter)(nil)
	_ storage.MasterDataReader  = (*externalPortsAdapter)(nil)
)

func (a *externalPortsAdapter) SaveDraft(_ context.Context, entry ledger.JournalEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry.ID == (types.JournalEntryID{}) {
		return fmt.Errorf("external adapter: draft entry id is required")
	}
	if _, exists := a.entries[entry.ID]; exists {
		return fmt.Errorf("external adapter: draft %s already exists", entry.ID)
	}
	a.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (a *externalPortsAdapter) GetEntry(_ context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return ledger.JournalEntry{}, fmt.Errorf("external adapter: entry %s not found for company %s", entryID, companyID)
	}
	return cloneEntry(entry), nil
}

func (a *externalPortsAdapter) CommitPosting(_ context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if commit.Operation != storage.OperationPost {
		return ledger.JournalEntry{}, fmt.Errorf("external adapter: unsupported operation %q", commit.Operation)
	}
	entry := cloneEntry(commit.Entry)
	if entry.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, fmt.Errorf("external adapter: commit requires a posted entry, got %q", entry.Status)
	}
	a.entries[entry.ID] = entry
	return cloneEntry(entry), nil
}

func (a *externalPortsAdapter) NextSequence(_ context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if companyID != a.company.ID || journalID == (types.JournalID{}) {
		return 0, fmt.Errorf("external adapter: invalid sequence scope %s/%s", companyID, journalID)
	}
	a.sequence++
	return a.sequence, nil
}

func (a *externalPortsAdapter) GetCompany(_ context.Context, companyID types.CompanyID) (masterdata.Company, error) {
	if companyID != a.company.ID {
		return masterdata.Company{}, fmt.Errorf("external adapter: company %s not found", companyID)
	}
	return a.company, nil
}

func (a *externalPortsAdapter) GetAccount(_ context.Context, companyID types.CompanyID, accountID types.AccountID) (masterdata.Account, error) {
	account, ok := a.accounts[accountID]
	if !ok || account.CompanyID != companyID {
		return masterdata.Account{}, fmt.Errorf("external adapter: account %s not found", accountID)
	}
	return account, nil
}

func (a *externalPortsAdapter) GetAccountByCode(_ context.Context, companyID types.CompanyID, code string) (masterdata.Account, error) {
	for _, account := range a.accounts {
		if account.CompanyID == companyID && account.Code == code {
			return account, nil
		}
	}
	return masterdata.Account{}, fmt.Errorf("external adapter: account code %q not found", code)
}

func (a *externalPortsAdapter) GetJournalByCode(_ context.Context, companyID types.CompanyID, code string) (masterdata.Journal, error) {
	if companyID != a.company.ID || code != a.journal.Code {
		return masterdata.Journal{}, fmt.Errorf("external adapter: journal code %q not found", code)
	}
	return a.journal, nil
}

func (a *externalPortsAdapter) HasAccountChildren(_ context.Context, companyID types.CompanyID, accountID types.AccountID) (bool, error) {
	if _, err := a.GetAccount(context.Background(), companyID, accountID); err != nil {
		return false, err
	}
	return false, nil
}

func (a *externalPortsAdapter) GetFiscalYear(_ context.Context, companyID types.CompanyID, fiscalYearID types.FiscalYearID) (masterdata.FiscalYear, error) {
	if companyID != a.company.ID || fiscalYearID != a.year.ID {
		return masterdata.FiscalYear{}, fmt.Errorf("external adapter: fiscal year %s not found", fiscalYearID)
	}
	return a.year, nil
}

func (a *externalPortsAdapter) GetPeriodForDate(_ context.Context, companyID types.CompanyID, date time.Time) (masterdata.FiscalPeriod, error) {
	if companyID != a.company.ID ||
		date.Before(a.period.StartDate) || date.After(a.period.EndDate.Add(24*time.Hour-time.Nanosecond)) {
		return masterdata.FiscalPeriod{}, fmt.Errorf("external adapter: no period for %s", date)
	}
	return a.period, nil
}

func cloneEntry(entry ledger.JournalEntry) ledger.JournalEntry {
	entry.Lines = append([]ledger.EntryLine(nil), entry.Lines...)
	return entry
}

func TestExternalPortsAdapterJourney(t *testing.T) {
	ctx := context.Background()

	companyID := types.NewCompanyID()
	cashID := types.NewAccountID()
	revenueID := types.NewAccountID()
	yearID := types.NewFiscalYearID()
	periodID := types.NewFiscalPeriodID()
	journalID := types.NewJournalID()
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	adapter := &externalPortsAdapter{
		company: masterdata.Company{
			ID: companyID, Code: "EXT", LegalName: "External Adapter Ltd",
			FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
		},
		journal: masterdata.Journal{
			ID: masterdata.JournalID(journalID), CompanyID: companyID, Code: "GEN", Name: "General Journal",
		},
		accounts: map[types.AccountID]masterdata.Account{
			cashID: {
				ID: cashID, CompanyID: companyID, Code: "1000", Name: "Cash",
				Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
				Currency: "USD", IsActive: true,
			},
			revenueID: {
				ID: revenueID, CompanyID: companyID, Code: "4000", Name: "Revenue",
				Type: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
				Currency: "USD", IsActive: true,
			},
		},
		year: masterdata.FiscalYear{
			ID: yearID, CompanyID: companyID, Name: "FY2026",
			StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
			State: masterdata.FiscalYearOpen,
		},
		period: masterdata.FiscalPeriod{
			ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
			Number: 1, Name: "January 2026", StartDate: start,
			EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
			State:   masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
		},
		entries: make(map[types.JournalEntryID]ledger.JournalEntry),
	}

	ports := storage.Ports{
		Drafts:     adapter,
		Entries:    adapter,
		Posting:    adapter,
		Sequences:  adapter,
		MasterData: adapter,
	}
	if err := ports.Validate(); err != nil {
		t.Fatalf("external adapter does not satisfy required ports: %v", err)
	}

	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(ports)).Build(ctx)
	if err != nil {
		t.Fatalf("build engine from an external ports adapter: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatalf("create amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatalf("create zero amount: %v", err)
	}
	articleDate := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)
	entry := ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: companyID, JournalID: journalID,
		FiscalPeriodID: periodID, Status: ledger.EntryDraft,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  articleDate, PostingDate: articleDate,
		Description: "External adapter posting", SourceModule: "external-consumer",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		CreatedBy: "external-consumer",
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: cashID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: revenueID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}

	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("post through an external ports adapter: %v", err)
	}
	if posted.Status != ledger.EntryPosted || posted.SequenceNumber != 1 {
		t.Fatalf("posted entry status=%q sequence=%d, want posted/1", posted.Status, posted.SequenceNumber)
	}
	readBack, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("read through an external ports adapter: %v", err)
	}
	if readBack.ID != posted.ID || readBack.CompanyID != companyID || len(readBack.Lines) != 2 {
		t.Fatalf("unexpected read-back: %+v", readBack)
	}
}
