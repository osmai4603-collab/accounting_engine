// Package adapter is an experimental storage adapter written entirely against
// the public accounting-engine SPI. It deliberately imports no internal
// package, no SQL driver, and no bundled storage implementation.
//
// The store is not durable and is intended to prove injectability, not
// production storage semantics. It still enforces the guarantees the public
// ports require for the scenario: company scoping, clone-on-read/write,
// deterministic per-journal sequences, and one-lock atomic reversal.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

var (
	// ErrInvalidFixture reports incomplete or inconsistent provisioning input.
	ErrInvalidFixture = errors.New("external adapter: invalid fixture")
	// ErrTenantMismatch reports an operation scoped to another company.
	ErrTenantMismatch = errors.New("external adapter: company scope mismatch")
	// ErrEntryNotFound reports a missing journal entry.
	ErrEntryNotFound = errors.New("external adapter: journal entry not found")
	// ErrEntryConflict reports a duplicate or non-draft write.
	ErrEntryConflict = errors.New("external adapter: journal entry conflict")
	// ErrUnknownAccount reports an account outside the provisioned fixture.
	ErrUnknownAccount = errors.New("external adapter: unknown account")
	// ErrUnknownJournal reports a journal outside the provisioned fixture.
	ErrUnknownJournal = errors.New("external adapter: unknown journal")
	// ErrUnknownPeriod reports a fiscal period outside the provisioned fixture.
	ErrUnknownPeriod = errors.New("external adapter: unknown fiscal period")
	// ErrSequenceConflict reports a reused entry number in one journal.
	ErrSequenceConflict = errors.New("external adapter: sequence already used")
	// ErrUnsupportedOperation reports a posting operation this adapter rejects.
	ErrUnsupportedOperation = errors.New("external adapter: unsupported posting operation")
	// ErrReportNotSupported reports an unimplemented reporting capability.
	ErrReportNotSupported = errors.New("external adapter: report not supported")
	// ErrRateUnavailable reports a missing exchange rate.
	ErrRateUnavailable = errors.New("external adapter: exchange rate unavailable")
)

// Store is an independent in-memory implementation of the public storage
// ports plus the reporting capability used by the scenario.
type Store struct {
	mu sync.RWMutex

	company  publicmasterdata.Company
	journal  publicmasterdata.Journal
	accounts map[types.AccountID]publicmasterdata.Account
	byCode   map[string]types.AccountID
	year     publicmasterdata.FiscalYear
	period   publicmasterdata.FiscalPeriod

	entries  map[types.JournalEntryID]ledger.JournalEntry
	sequence map[types.JournalID]int64
	used     map[types.JournalID]map[int64]types.JournalEntryID

	functionalCurrency string
	exchangeRate       decimal.Decimal
}

var (
	_ storage.DraftStore        = (*Store)(nil)
	_ storage.EntryReader       = (*Store)(nil)
	_ storage.PostingWriter     = (*Store)(nil)
	_ storage.SequenceAllocator = (*Store)(nil)
	_ storage.MasterDataReader  = (*Store)(nil)
	_ storage.ReportingStore    = (*Store)(nil)
	_ storage.ExchangeRateSPI   = (*Store)(nil)
)

// New validates the fixture and provisions the experimental store.
func New(fixture Fixture) (*Store, error) {
	if err := fixture.validate(); err != nil {
		return nil, err
	}
	store := &Store{
		company:            fixture.Company,
		journal:            fixture.Journal,
		accounts:           make(map[types.AccountID]publicmasterdata.Account, len(fixture.Accounts)),
		byCode:             make(map[string]types.AccountID, len(fixture.Accounts)),
		year:               fixture.FiscalYear,
		period:             fixture.FiscalPeriod,
		entries:            make(map[types.JournalEntryID]ledger.JournalEntry),
		sequence:           make(map[types.JournalID]int64),
		used:               make(map[types.JournalID]map[int64]types.JournalEntryID),
		functionalCurrency: strings.ToUpper(strings.TrimSpace(fixture.FunctionalCurrency)),
		exchangeRate:       fixture.ExchangeRate,
	}
	store.company.FunctionalCurrency = store.functionalCurrency
	for _, account := range fixture.Accounts {
		store.accounts[account.ID] = account
		store.byCode[strings.ToUpper(strings.TrimSpace(account.Code))] = account.ID
	}
	return store, nil
}

// Ports returns the public storage ports consumed by the SDK. Reporting is a
// separate capability; the SDK is given the same store through WithReporting.
func (s *Store) Ports() storage.Ports {
	return storage.Ports{
		Drafts:     s,
		Entries:    s,
		Posting:    s,
		Sequences:  s,
		MasterData: s,
	}
}

// Reporting returns the optional reporting capability.
func (s *Store) Reporting() storage.ReportingStore { return s }

// ---------------------------------------------------------------- drafts

func (s *Store) SaveDraft(_ context.Context, entry ledger.JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertCompany(entry.CompanyID); err != nil {
		return err
	}
	if entry.ID == (types.JournalEntryID{}) {
		return fmt.Errorf("%w: draft entry id is required", ErrEntryConflict)
	}
	if _, exists := s.entries[entry.ID]; exists {
		return fmt.Errorf("%w: draft %s already exists", ErrEntryConflict, entry.ID)
	}
	if entry.JournalID != types.JournalID(s.journal.ID) {
		return fmt.Errorf("%w: journal %s", ErrUnknownJournal, entry.JournalID)
	}
	if entry.FiscalPeriodID != (types.FiscalPeriodID{}) && entry.FiscalPeriodID != s.period.ID {
		return fmt.Errorf("%w: period %s", ErrUnknownPeriod, entry.FiscalPeriodID)
	}
	for index, line := range entry.Lines {
		if _, ok := s.accounts[line.AccountID]; !ok {
			return fmt.Errorf("%w: line %d account %s", ErrUnknownAccount, index, line.AccountID)
		}
	}

	s.entries[entry.ID] = cloneEntry(entry)
	return nil
}

// ---------------------------------------------------------------- entries

func (s *Store) GetEntry(_ context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.assertCompany(companyID); err != nil {
		return ledger.JournalEntry{}, err
	}
	entry, ok := s.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return ledger.JournalEntry{}, fmt.Errorf("%w: entry %s", ErrEntryNotFound, entryID)
	}
	return cloneEntry(entry), nil
}

// ---------------------------------------------------------------- posting

// CommitPosting persists a posting or reversal atomically. For a reversal the
// original entry must still be posted and not yet reversed; the original and
// the reversal are updated under one write lock, so a concurrent second
// reversal cannot succeed.
func (s *Store) CommitPosting(_ context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertCompany(commit.Entry.CompanyID); err != nil {
		return ledger.JournalEntry{}, err
	}
	switch commit.Operation {
	case storage.OperationPost, "":
		return s.commitPosting(commit)
	case storage.OperationReverse:
		return s.commitReversal(commit)
	default:
		return ledger.JournalEntry{}, fmt.Errorf("%w: %q", ErrUnsupportedOperation, commit.Operation)
	}
}

func (s *Store) commitPosting(commit storage.PostingCommit) (ledger.JournalEntry, error) {
	entry := cloneEntry(commit.Entry)
	if entry.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, fmt.Errorf("%w: posting commit requires posted status, got %q", ErrEntryConflict, entry.Status)
	}
	existing, ok := s.entries[entry.ID]
	if !ok {
		return ledger.JournalEntry{}, fmt.Errorf("%w: draft %s", ErrEntryNotFound, entry.ID)
	}
	if existing.Status != ledger.EntryDraft {
		return ledger.JournalEntry{}, fmt.Errorf("%w: draft %s is not in draft status", ErrEntryConflict, entry.ID)
	}
	if entry.SequenceNumber <= 0 {
		return ledger.JournalEntry{}, fmt.Errorf("%w: entry %s has no reserved sequence", ErrEntryConflict, entry.ID)
	}
	if err := s.reserveSequence(entry.JournalID, entry.SequenceNumber, entry.ID); err != nil {
		return ledger.JournalEntry{}, err
	}
	s.entries[entry.ID] = entry
	return cloneEntry(entry), nil
}

func (s *Store) commitReversal(commit storage.PostingCommit) (ledger.JournalEntry, error) {
	if commit.OriginalEntryID == nil {
		return ledger.JournalEntry{}, fmt.Errorf("%w: reversal requires the original entry id", ErrEntryConflict)
	}
	reversal := cloneEntry(commit.Entry)
	if reversal.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, fmt.Errorf("%w: reversal commit requires posted status, got %q", ErrEntryConflict, reversal.Status)
	}
	if reversal.ReversalOfID == nil || *reversal.ReversalOfID != *commit.OriginalEntryID {
		return ledger.JournalEntry{}, fmt.Errorf("%w: reversal does not reference the original entry", ErrEntryConflict)
	}
	if reversal.JournalID != types.JournalID(s.journal.ID) || reversal.FiscalPeriodID != s.period.ID {
		return ledger.JournalEntry{}, fmt.Errorf("%w: reversal scope does not match the provisioned journal/period", ErrEntryConflict)
	}

	original, ok := s.entries[*commit.OriginalEntryID]
	if !ok {
		return ledger.JournalEntry{}, fmt.Errorf("%w: original entry %s", ErrEntryNotFound, *commit.OriginalEntryID)
	}
	if original.CompanyID != commit.Entry.CompanyID {
		return ledger.JournalEntry{}, ErrTenantMismatch
	}
	if original.Status == ledger.EntryReversed || original.ReversedByID != nil {
		return ledger.JournalEntry{}, fmt.Errorf("%w: entry %s is already reversed", ErrEntryConflict, original.ID)
	}
	if original.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, fmt.Errorf("%w: entry %s is not posted", ErrEntryConflict, original.ID)
	}

	if reversal.SequenceNumber <= 0 {
		// The application layer does not reserve a number for the reversal;
		// number allocation is a storage responsibility here.
		reversal.SequenceNumber = s.nextSequenceLocked(reversal.JournalID)
	}
	if err := s.reserveSequence(reversal.JournalID, reversal.SequenceNumber, reversal.ID); err != nil {
		return ledger.JournalEntry{}, err
	}

	now := time.Now().UTC()
	reversal.UpdatedAt = now
	original.Status = ledger.EntryReversed
	original.ReversedByID = &reversal.ID
	original.ReversalReason = commit.Reason
	original.UpdatedAt = now

	s.entries[reversal.ID] = cloneEntry(reversal)
	s.entries[original.ID] = cloneEntry(original)
	return cloneEntry(reversal), nil
}

func (s *Store) reserveSequence(journalID types.JournalID, sequence int64, entryID types.JournalEntryID) error {
	if s.used[journalID] == nil {
		s.used[journalID] = make(map[int64]types.JournalEntryID)
	}
	if existing, ok := s.used[journalID][sequence]; ok && existing != entryID {
		return fmt.Errorf("%w: journal %s sequence %d", ErrSequenceConflict, journalID, sequence)
	}
	s.used[journalID][sequence] = entryID
	return nil
}

func (s *Store) nextSequenceLocked(journalID types.JournalID) int64 {
	s.sequence[journalID]++
	return s.sequence[journalID]
}

// --------------------------------------------------------------- sequences

func (s *Store) NextSequence(_ context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertCompany(companyID); err != nil {
		return 0, err
	}
	if journalID != types.JournalID(s.journal.ID) {
		return 0, fmt.Errorf("%w: journal %s", ErrUnknownJournal, journalID)
	}
	return s.nextSequenceLocked(journalID), nil
}

// ------------------------------------------------------------- master data

func (s *Store) GetCompany(_ context.Context, companyID types.CompanyID) (publicmasterdata.Company, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.Company{}, err
	}
	return s.company, nil
}

func (s *Store) GetAccount(_ context.Context, companyID types.CompanyID, accountID types.AccountID) (publicmasterdata.Account, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.Account{}, err
	}
	account, ok := s.accounts[accountID]
	if !ok || account.CompanyID != companyID {
		return publicmasterdata.Account{}, fmt.Errorf("%w: account %s", ErrUnknownAccount, accountID)
	}
	return account, nil
}

func (s *Store) GetAccountByCode(_ context.Context, companyID types.CompanyID, code string) (publicmasterdata.Account, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.Account{}, err
	}
	accountID, ok := s.byCode[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return publicmasterdata.Account{}, fmt.Errorf("%w: account code %q", ErrUnknownAccount, code)
	}
	return s.GetAccount(context.Background(), companyID, accountID)
}

func (s *Store) HasAccountChildren(_ context.Context, companyID types.CompanyID, accountID types.AccountID) (bool, error) {
	if _, err := s.GetAccount(context.Background(), companyID, accountID); err != nil {
		return false, err
	}
	// The fixture contains leaf accounts only.
	return false, nil
}

func (s *Store) GetJournalByCode(_ context.Context, companyID types.CompanyID, code string) (publicmasterdata.Journal, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.Journal{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(code), s.journal.Code) {
		return publicmasterdata.Journal{}, publicmasterdata.ErrJournalNotFound
	}
	return s.journal, nil
}

func (s *Store) GetFiscalYear(_ context.Context, companyID types.CompanyID, fiscalYearID types.FiscalYearID) (publicmasterdata.FiscalYear, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.FiscalYear{}, err
	}
	if fiscalYearID != s.year.ID {
		return publicmasterdata.FiscalYear{}, fmt.Errorf("%w: fiscal year %s", ErrUnknownPeriod, fiscalYearID)
	}
	return s.year, nil
}

func (s *Store) GetPeriodForDate(_ context.Context, companyID types.CompanyID, when time.Time) (publicmasterdata.FiscalPeriod, error) {
	if err := s.assertCompany(companyID); err != nil {
		return publicmasterdata.FiscalPeriod{}, err
	}
	endOfDay := s.period.EndDate.Add(24*time.Hour - time.Nanosecond)
	if when.Before(s.period.StartDate) || when.After(endOfDay) {
		return publicmasterdata.FiscalPeriod{}, fmt.Errorf("%w: no period for %s", ErrUnknownPeriod, when.Format(time.RFC3339))
	}
	return s.period, nil
}

// ---------------------------------------------------------------- currency

// GetRate is the scenario's currency source. The fixture is single-currency;
// any cross-currency request is rejected instead of silently returning one.
func (s *Store) GetRate(_ context.Context, fromCurrency, toCurrency string, _ time.Time) (decimal.Decimal, error) {
	from := strings.ToUpper(strings.TrimSpace(fromCurrency))
	to := strings.ToUpper(strings.TrimSpace(toCurrency))
	if from == to && from == s.functionalCurrency {
		return s.exchangeRate, nil
	}
	return decimal.Decimal{}, fmt.Errorf("%w: %s -> %s", ErrRateUnavailable, from, to)
}

// --------------------------------------------------------------- reporting

// GetTrialBalance aggregates functional debit/credit amounts of every posted
// entry (including posted reversals) on or before asOf. Drafts are excluded.
func (s *Store) GetTrialBalance(_ context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.assertCompany(companyID); err != nil {
		return reporting.TrialBalance{}, err
	}
	currency = s.reportCurrency(currency)
	zero, err := types.Zero(currency)
	if err != nil {
		return reporting.TrialBalance{}, err
	}

	type totals struct{ debit, credit decimal.Decimal }
	byAccount := make(map[types.AccountID]*totals)
	for _, entry := range s.entries {
		if entry.CompanyID != companyID || entry.EffectiveDate.After(asOf) {
			continue
		}
		if entry.Status != ledger.EntryPosted && entry.Status != ledger.EntryReversed {
			continue
		}
		for _, line := range entry.Lines {
			if line.FunctionalDebit.Currency() != currency || line.FunctionalCredit.Currency() != currency {
				continue
			}
			accountTotals, ok := byAccount[line.AccountID]
			if !ok {
				accountTotals = &totals{}
				byAccount[line.AccountID] = accountTotals
			}
			accountTotals.debit = accountTotals.debit.Add(line.FunctionalDebit.Amount())
			accountTotals.credit = accountTotals.credit.Add(line.FunctionalCredit.Amount())
		}
	}

	lines := make([]reporting.TrialBalanceLine, 0, len(byAccount))
	totalDebit := decimal.Zero
	totalCredit := decimal.Zero
	for accountID, accountTotals := range byAccount {
		periodDebit, err := types.NewMoney(accountTotals.debit, currency)
		if err != nil {
			return reporting.TrialBalance{}, err
		}
		periodCredit, err := types.NewMoney(accountTotals.credit, currency)
		if err != nil {
			return reporting.TrialBalance{}, err
		}
		endingDebit, endingCredit := zero, zero
		switch net := accountTotals.debit.Sub(accountTotals.credit); {
		case net.IsPositive():
			if endingDebit, err = types.NewMoney(net, currency); err != nil {
				return reporting.TrialBalance{}, err
			}
		case net.IsNegative():
			if endingCredit, err = types.NewMoney(net.Abs(), currency); err != nil {
				return reporting.TrialBalance{}, err
			}
		}
		account := s.accounts[accountID]
		lines = append(lines, reporting.TrialBalanceLine{
			AccountID:     accountID,
			AccountCode:   account.Code,
			AccountName:   account.Name,
			AccountType:   string(account.Type),
			OpeningDebit:  zero,
			OpeningCredit: zero,
			PeriodDebit:   periodDebit,
			PeriodCredit:  periodCredit,
			EndingDebit:   endingDebit,
			EndingCredit:  endingCredit,
		})
		totalDebit = totalDebit.Add(accountTotals.debit)
		totalCredit = totalCredit.Add(accountTotals.credit)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].AccountCode < lines[j].AccountCode })

	totalDebitMoney, err := types.NewMoney(totalDebit, currency)
	if err != nil {
		return reporting.TrialBalance{}, err
	}
	totalCreditMoney, err := types.NewMoney(totalCredit, currency)
	if err != nil {
		return reporting.TrialBalance{}, err
	}
	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOfDate:    asOf,
		Currency:    currency,
		Lines:       lines,
		TotalDebit:  totalDebitMoney,
		TotalCredit: totalCreditMoney,
		IsBalanced:  totalDebit.Equal(totalCredit),
	}, nil
}

// GetGeneralLedger returns the posted movement of one account in functional
// currency. It is implemented so the reporting capability is coherent, and is
// exercised by the scenario.
func (s *Store) GetGeneralLedger(_ context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.assertCompany(companyID); err != nil {
		return reporting.GeneralLedgerReport{}, err
	}
	account, ok := s.accounts[accountID]
	if !ok || account.CompanyID != companyID {
		return reporting.GeneralLedgerReport{}, fmt.Errorf("%w: account %s", ErrUnknownAccount, accountID)
	}
	currency := s.functionalCurrency

	opening := decimal.Zero
	type movement struct {
		entry  ledger.JournalEntry
		debit  decimal.Decimal
		credit decimal.Decimal
	}
	var movements []movement
	for _, entry := range s.entries {
		if entry.CompanyID != companyID {
			continue
		}
		if entry.Status != ledger.EntryPosted && entry.Status != ledger.EntryReversed {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != accountID {
				continue
			}
			if line.FunctionalDebit.Currency() != currency {
				continue
			}
			debit := line.FunctionalDebit.Amount()
			credit := line.FunctionalCredit.Amount()
			if entry.EffectiveDate.Before(start) {
				opening = opening.Add(debit).Sub(credit)
				continue
			}
			if entry.EffectiveDate.After(end) {
				continue
			}
			movements = append(movements, movement{entry: entry, debit: debit, credit: credit})
		}
	}
	sort.SliceStable(movements, func(i, j int) bool {
		if movements[i].entry.EffectiveDate.Equal(movements[j].entry.EffectiveDate) {
			return movements[i].entry.SequenceNumber < movements[j].entry.SequenceNumber
		}
		return movements[i].entry.EffectiveDate.Before(movements[j].entry.EffectiveDate)
	})

	openingMoney, err := types.NewMoney(opening, currency)
	if err != nil {
		return reporting.GeneralLedgerReport{}, err
	}
	running := opening
	entries := make([]reporting.GeneralLedgerEntry, 0, len(movements))
	for _, item := range movements {
		running = running.Add(item.debit).Sub(item.credit)
		runningMoney, err := types.NewMoney(running, currency)
		if err != nil {
			return reporting.GeneralLedgerReport{}, err
		}
		debitMoney, err := types.NewMoney(item.debit, currency)
		if err != nil {
			return reporting.GeneralLedgerReport{}, err
		}
		creditMoney, err := types.NewMoney(item.credit, currency)
		if err != nil {
			return reporting.GeneralLedgerReport{}, err
		}
		entries = append(entries, reporting.GeneralLedgerEntry{
			EntryID:        item.entry.ID,
			EntryNumber:    item.entry.EntryNumber,
			EffectiveDate:  item.entry.EffectiveDate,
			Description:    item.entry.Description,
			Debit:          debitMoney,
			Credit:         creditMoney,
			RunningBalance: runningMoney,
		})
	}
	endingMoney, err := types.NewMoney(running, currency)
	if err != nil {
		return reporting.GeneralLedgerReport{}, err
	}
	return reporting.GeneralLedgerReport{
		CompanyID:      companyID,
		AccountID:      accountID,
		AccountCode:    account.Code,
		AccountName:    account.Name,
		StartDate:      start,
		EndDate:        end,
		OpeningBalance: openingMoney,
		Entries:        entries,
		EndingBalance:  endingMoney,
	}, nil
}

func (s *Store) GetBalanceSheet(context.Context, types.CompanyID, time.Time, string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, fmt.Errorf("%w: balance sheet", ErrReportNotSupported)
}

func (s *Store) GetIncomeStatement(context.Context, types.CompanyID, time.Time, time.Time, string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, fmt.Errorf("%w: income statement", ErrReportNotSupported)
}

// ---------------------------------------------------------------- helpers

func (s *Store) assertCompany(companyID types.CompanyID) error {
	if companyID != s.company.ID {
		return fmt.Errorf("%w: requested %s", ErrTenantMismatch, companyID)
	}
	return nil
}

func (s *Store) reportCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return s.functionalCurrency
	}
	return currency
}

func (f Fixture) validate() error {
	if f.Company.ID == (types.CompanyID{}) || !f.Company.Active {
		return fmt.Errorf("%w: an active company id is required", ErrInvalidFixture)
	}
	functionalCurrency := strings.ToUpper(strings.TrimSpace(f.FunctionalCurrency))
	if functionalCurrency == "" {
		return fmt.Errorf("%w: functional currency is required", ErrInvalidFixture)
	}
	if !strings.EqualFold(functionalCurrency, strings.TrimSpace(f.Company.FunctionalCurrency)) {
		return fmt.Errorf("%w: functional currency does not match the company", ErrInvalidFixture)
	}
	if err := f.Journal.Validate(); err != nil {
		return fmt.Errorf("%w: journal: %v", ErrInvalidFixture, err)
	}
	if f.Journal.CompanyID != f.Company.ID {
		return fmt.Errorf("%w: journal belongs to another company", ErrInvalidFixture)
	}
	if len(f.Accounts) < 2 {
		return fmt.Errorf("%w: at least two accounts are required", ErrInvalidFixture)
	}
	seenCodes := make(map[string]struct{}, len(f.Accounts))
	for _, account := range f.Accounts {
		if account.ID == (types.AccountID{}) || account.CompanyID != f.Company.ID {
			return fmt.Errorf("%w: account %q has no id or wrong company", ErrInvalidFixture, account.Code)
		}
		code := strings.ToUpper(strings.TrimSpace(account.Code))
		if code == "" {
			return fmt.Errorf("%w: account code is required", ErrInvalidFixture)
		}
		if _, duplicate := seenCodes[code]; duplicate {
			return fmt.Errorf("%w: duplicate account code %q", ErrInvalidFixture, code)
		}
		seenCodes[code] = struct{}{}
		switch account.Type {
		case publicmasterdata.AccountTypeAsset, publicmasterdata.AccountTypeLiability,
			publicmasterdata.AccountTypeEquity, publicmasterdata.AccountTypeIncome,
			publicmasterdata.AccountTypeExpense:
		default:
			return fmt.Errorf("%w: account %q has invalid type %q", ErrInvalidFixture, code, account.Type)
		}
	}
	if f.FiscalYear.ID == (types.FiscalYearID{}) || f.FiscalYear.CompanyID != f.Company.ID ||
		!f.FiscalYear.EndDate.After(f.FiscalYear.StartDate) {
		return fmt.Errorf("%w: invalid fiscal year", ErrInvalidFixture)
	}
	if f.FiscalPeriod.ID == (types.FiscalPeriodID{}) || f.FiscalPeriod.CompanyID != f.Company.ID ||
		f.FiscalPeriod.FiscalYearID != f.FiscalYear.ID ||
		!f.FiscalPeriod.EndDate.After(f.FiscalPeriod.StartDate) || f.FiscalPeriod.Number <= 0 {
		return fmt.Errorf("%w: invalid fiscal period", ErrInvalidFixture)
	}
	if f.FiscalPeriod.State != publicmasterdata.PeriodOpen {
		return fmt.Errorf("%w: scenario period must be open", ErrInvalidFixture)
	}
	if !f.ExchangeRate.IsPositive() {
		return fmt.Errorf("%w: exchange rate must be positive", ErrInvalidFixture)
	}
	return nil
}

func cloneEntry(entry ledger.JournalEntry) ledger.JournalEntry {
	clone := entry
	clone.PostedAt = cloneTime(entry.PostedAt)
	clone.ApprovedAt = cloneTime(entry.ApprovedAt)
	clone.ReversalOfID = cloneID(entry.ReversalOfID)
	clone.ReversedByID = cloneID(entry.ReversedByID)

	clone.Lines = make([]ledger.EntryLine, len(entry.Lines))
	for index, line := range entry.Lines {
		lineClone := line
		lineClone.PartnerID = cloneID(line.PartnerID)
		lineClone.CostCenterID = cloneID(line.CostCenterID)
		lineClone.ProjectID = cloneID(line.ProjectID)
		lineClone.SegmentID = cloneID(line.SegmentID)
		lineClone.BusinessUnitID = cloneID(line.BusinessUnitID)
		clone.Lines[index] = lineClone
	}
	return clone
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneID[T ~[16]byte](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
