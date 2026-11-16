package contractparity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

var (
	ErrExternalTenantMismatch = errors.New("external adapter: tenant mismatch")
	ErrExternalEntryNotFound  = errors.New("external adapter: entry not found")
	ErrExternalConflict       = errors.New("external adapter: conflict")
	ErrExternalUnknownAccount = errors.New("external adapter: unknown account")
	ErrExternalUnknownPeriod  = errors.New("external adapter: unknown period")
)

// ExternalStore is a pure public SPI implementation of storage.
// It imports NO internal packages and proves that an independent third-party
// adapter satisfies all contracts required by the accounting engine.
type ExternalStore struct {
	mu sync.RWMutex

	// Master data
	companies map[types.CompanyID]publicmasterdata.Company
	journals  map[types.CompanyID]map[types.JournalID]publicmasterdata.Journal
	byJCode   map[types.CompanyID]map[string]types.JournalID
	accounts  map[types.CompanyID]map[types.AccountID]publicmasterdata.Account
	byACode   map[types.CompanyID]map[string]types.AccountID
	years     map[types.CompanyID]map[types.FiscalYearID]publicmasterdata.FiscalYear
	periods   map[types.CompanyID]map[types.FiscalPeriodID]publicmasterdata.FiscalPeriod

	// Transactional & Ledger data
	drafts    map[types.JournalEntryID]ledger.JournalEntry
	entries   map[types.JournalEntryID]ledger.JournalEntry
	sequences map[string]int64 // "companyID/journalID" -> currentSequence
	audits    []storage.AuditRecord

	// Idempotency stores
	storageIdem map[string]storage.IdempotencyRecord // "companyID/key" -> record
	eventIdem   map[string]publicevents.Ingestion    // "companyID/source/key" -> ingestion
	mappings    map[string]publicevents.AccountMapping
}

// NewExternalStore instantiates an empty external store.
func NewExternalStore() *ExternalStore {
	return &ExternalStore{
		companies:   make(map[types.CompanyID]publicmasterdata.Company),
		journals:    make(map[types.CompanyID]map[types.JournalID]publicmasterdata.Journal),
		byJCode:     make(map[types.CompanyID]map[string]types.JournalID),
		accounts:    make(map[types.CompanyID]map[types.AccountID]publicmasterdata.Account),
		byACode:     make(map[types.CompanyID]map[string]types.AccountID),
		years:       make(map[types.CompanyID]map[types.FiscalYearID]publicmasterdata.FiscalYear),
		periods:     make(map[types.CompanyID]map[types.FiscalPeriodID]publicmasterdata.FiscalPeriod),
		drafts:      make(map[types.JournalEntryID]ledger.JournalEntry),
		entries:     make(map[types.JournalEntryID]ledger.JournalEntry),
		sequences:   make(map[string]int64),
		storageIdem: make(map[string]storage.IdempotencyRecord),
		eventIdem:   make(map[string]publicevents.Ingestion),
		mappings:    make(map[string]publicevents.AccountMapping),
	}
}

// Ensure interface assertions at compile time
var (
	_ storage.DraftStore            = (*ExternalStore)(nil)
	_ storage.EntryReader           = (*ExternalStore)(nil)
	_ storage.PostingWriter         = (*ExternalStore)(nil)
	_ storage.SequenceAllocator     = (*ExternalStore)(nil)
	_ storage.MasterDataReader      = (*ExternalStore)(nil)
	_ storage.BalanceReader         = (*ExternalStore)(nil)
	_ storage.AuditWriter           = (*ExternalStore)(nil)
	_ storage.IdempotencyStore      = (*ExternalStore)(nil)
	_ storage.ReportingStore        = (*ExternalStore)(nil)
	_ storage.ExchangeRateSPI       = (*ExternalStore)(nil)
	_ storage.TransactionRunner     = (*ExternalStore)(nil)
	_ storage.UnitOfWorkRunner      = (*ExternalStore)(nil)
	_ publicevents.IdempotencyStore = (*ExternalStore)(nil)
	_ publicevents.MappingStore     = (*ExternalStore)(nil)
	_ publicmasterdata.Store        = (*ExternalStore)(nil)
)

// Ports returns the public storage ports for constructing the SDK Engine.
func (s *ExternalStore) Ports() storage.Ports {
	return storage.Ports{
		Drafts:      s,
		Entries:     s,
		Posting:     s,
		Sequences:   s,
		MasterData:  s,
		Balance:     s,
		Audit:       s,
		Idempotency: s,
		TxRunner:    s,
		UOWRunner:   s,
		Events: publicevents.Stores{
			Idempotency: s,
			Mappings:    s,
		},
	}
}

// Capabilities declares the guarantees of this external store.
func (s *ExternalStore) Capabilities() storage.AdapterCapabilities {
	return storage.AdapterCapabilities{
		AdapterName:            "external-inmemory",
		SupportsRepeatableRead: true,
		SupportsSerializable:   true,
		EnforcesForeignKeys:    false,
		GuaranteesDurability:   false,
		SupportsSavepoints:     false,
		DetectsDeadlocks:       false,
	}
}

// ------------------------------------------------------------- Master Data Store

func (s *ExternalStore) CreateCompany(_ context.Context, c publicmasterdata.Company) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.companies[c.ID] = c
	return nil
}

func (s *ExternalStore) GetCompany(_ context.Context, id types.CompanyID) (publicmasterdata.Company, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.companies[id]
	if !ok {
		return publicmasterdata.Company{}, fmt.Errorf("%w: company %s", ErrExternalTenantMismatch, id)
	}
	return c, nil
}

func (s *ExternalStore) CreateJournal(_ context.Context, j publicmasterdata.Journal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jID := types.JournalID(j.ID)
	if s.journals[j.CompanyID] == nil {
		s.journals[j.CompanyID] = make(map[types.JournalID]publicmasterdata.Journal)
		s.byJCode[j.CompanyID] = make(map[string]types.JournalID)
	}
	s.journals[j.CompanyID][jID] = j
	s.byJCode[j.CompanyID][j.Code] = jID
	return nil
}

func (s *ExternalStore) GetJournalByCode(_ context.Context, companyID types.CompanyID, code string) (publicmasterdata.Journal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byCode, ok := s.byJCode[companyID]
	if !ok {
		return publicmasterdata.Journal{}, fmt.Errorf("journal code %s not found", code)
	}
	id, ok := byCode[code]
	if !ok {
		return publicmasterdata.Journal{}, fmt.Errorf("journal code %s not found", code)
	}
	return s.journals[companyID][id], nil
}

func (s *ExternalStore) CreateAccount(_ context.Context, a publicmasterdata.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[a.CompanyID] == nil {
		s.accounts[a.CompanyID] = make(map[types.AccountID]publicmasterdata.Account)
		s.byACode[a.CompanyID] = make(map[string]types.AccountID)
	}
	s.accounts[a.CompanyID][a.ID] = a
	s.byACode[a.CompanyID][a.Code] = a.ID
	return nil
}

func (s *ExternalStore) GetAccount(_ context.Context, companyID types.CompanyID, accountID types.AccountID) (publicmasterdata.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	accs, ok := s.accounts[companyID]
	if !ok {
		return publicmasterdata.Account{}, fmt.Errorf("%w: %w: account %s", types.ErrAccountNotFound, ErrExternalUnknownAccount, accountID)
	}
	acc, ok := accs[accountID]
	if !ok {
		return publicmasterdata.Account{}, fmt.Errorf("%w: %w: account %s", types.ErrAccountNotFound, ErrExternalUnknownAccount, accountID)
	}
	return acc, nil
}

func (s *ExternalStore) GetAccountByCode(_ context.Context, companyID types.CompanyID, code string) (publicmasterdata.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byCode, ok := s.byACode[companyID]
	if !ok {
		return publicmasterdata.Account{}, fmt.Errorf("%w: %w: code %s", types.ErrAccountNotFound, ErrExternalUnknownAccount, code)
	}
	id, ok := byCode[code]
	if !ok {
		return publicmasterdata.Account{}, fmt.Errorf("%w: %w: code %s", types.ErrAccountNotFound, ErrExternalUnknownAccount, code)
	}
	return s.accounts[companyID][id], nil
}

func (s *ExternalStore) HasAccountChildren(_ context.Context, _ types.CompanyID, _ types.AccountID) (bool, error) {
	return false, nil
}

func (s *ExternalStore) CreateFiscalYear(_ context.Context, y publicmasterdata.FiscalYear) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.years[y.CompanyID] == nil {
		s.years[y.CompanyID] = make(map[types.FiscalYearID]publicmasterdata.FiscalYear)
	}
	s.years[y.CompanyID][y.ID] = y
	return nil
}

func (s *ExternalStore) GetFiscalYear(_ context.Context, companyID types.CompanyID, id types.FiscalYearID) (publicmasterdata.FiscalYear, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	years, ok := s.years[companyID]
	if !ok {
		return publicmasterdata.FiscalYear{}, fmt.Errorf("fiscal year not found: %s", id)
	}
	y, ok := years[id]
	if !ok {
		return publicmasterdata.FiscalYear{}, fmt.Errorf("fiscal year not found: %s", id)
	}
	return y, nil
}

func (s *ExternalStore) CreateFiscalPeriod(_ context.Context, p publicmasterdata.FiscalPeriod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.periods[p.CompanyID] == nil {
		s.periods[p.CompanyID] = make(map[types.FiscalPeriodID]publicmasterdata.FiscalPeriod)
	}
	s.periods[p.CompanyID][p.ID] = p
	return nil
}

func (s *ExternalStore) GetFiscalPeriod(_ context.Context, companyID types.CompanyID, id types.FiscalPeriodID) (publicmasterdata.FiscalPeriod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	periods, ok := s.periods[companyID]
	if !ok {
		return publicmasterdata.FiscalPeriod{}, fmt.Errorf("%w: %w: period %s", types.ErrFiscalPeriodNotFound, ErrExternalUnknownPeriod, id)
	}
	p, ok := periods[id]
	if !ok {
		return publicmasterdata.FiscalPeriod{}, fmt.Errorf("%w: %w: period %s", types.ErrFiscalPeriodNotFound, ErrExternalUnknownPeriod, id)
	}
	return p, nil
}

func (s *ExternalStore) GetPeriodForDate(_ context.Context, companyID types.CompanyID, date time.Time) (publicmasterdata.FiscalPeriod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	periods, ok := s.periods[companyID]
	if !ok {
		return publicmasterdata.FiscalPeriod{}, fmt.Errorf("%w for date %s", ErrExternalUnknownPeriod, date)
	}
	for _, p := range periods {
		if !date.Before(p.StartDate) && !date.After(p.EndDate) {
			return p, nil
		}
	}
	return publicmasterdata.FiscalPeriod{}, fmt.Errorf("%w: %w for date %s", types.ErrFiscalPeriodNotFound, ErrExternalUnknownPeriod, date)
}

// ------------------------------------------------------------- Drafts & Entries

func (s *ExternalStore) SaveDraft(_ context.Context, entry ledger.JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.ID == (types.JournalEntryID{}) {
		return fmt.Errorf("%w: draft ID is required", ErrExternalConflict)
	}
	if _, exists := s.entries[entry.ID]; exists {
		return fmt.Errorf("%w: draft already posted %s", ErrExternalConflict, entry.ID)
	}
	for _, line := range entry.Lines {
		if _, exists := s.accounts[entry.CompanyID][line.AccountID]; !exists {
			return fmt.Errorf("%w: %w: account %s", types.ErrAccountNotFound, ErrExternalUnknownAccount, line.AccountID)
		}
	}
	s.drafts[entry.ID] = cloneEntry(entry)
	return nil
}

func (s *ExternalStore) GetEntry(_ context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[entryID]
	if !ok {
		// Also check drafts if not posted
		draft, isDraft := s.drafts[entryID]
		if isDraft && draft.CompanyID == companyID {
			return cloneEntry(draft), nil
		}
		return ledger.JournalEntry{}, fmt.Errorf("%w: %w: entry %s", publicapplication.ErrNotFound, ErrExternalEntryNotFound, entryID)
	}
	if entry.CompanyID != companyID {
		return ledger.JournalEntry{}, fmt.Errorf("%w: company mismatch for %s", ErrExternalTenantMismatch, entryID)
	}
	return cloneEntry(entry), nil
}

// ------------------------------------------------------------- Posting

func (s *ExternalStore) CommitPosting(_ context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := cloneEntry(commit.Entry)
	if entry.SequenceNumber <= 0 {
		key := fmt.Sprintf("%s/%s", entry.CompanyID, entry.JournalID)
		entry.SequenceNumber = s.sequences[key] + 1
		s.sequences[key] = entry.SequenceNumber
		entry.EntryNumber = fmt.Sprintf("%d", entry.SequenceNumber)
	}

	// Validate period is not closed
	if periods, ok := s.periods[entry.CompanyID]; ok {
		if p, ok := periods[entry.FiscalPeriodID]; ok {
			if p.State == publicmasterdata.PeriodHardClosed || p.State == publicmasterdata.PeriodSoftClosed {
				return ledger.JournalEntry{}, fmt.Errorf("%w: period %s is closed", types.ErrFiscalPeriodClosed, p.ID)
			}
		}
	}

	switch commit.Operation {
	case storage.OperationPost:
		if _, exists := s.entries[entry.ID]; exists {
			return ledger.JournalEntry{}, fmt.Errorf("%w: entry already exists %s", ErrExternalConflict, entry.ID)
		}
		entry.Status = ledger.EntryPosted
		s.entries[entry.ID] = cloneEntry(entry)
		delete(s.drafts, entry.ID)

	case storage.OperationReverse:
		if commit.OriginalEntryID == nil {
			return ledger.JournalEntry{}, fmt.Errorf("%w: original entry ID required for reversal", ErrExternalConflict)
		}
		origID := *commit.OriginalEntryID
		orig, exists := s.entries[origID]
		if !exists {
			return ledger.JournalEntry{}, fmt.Errorf("%w: original entry %s not found", ErrExternalEntryNotFound, origID)
		}
		if orig.Status == ledger.EntryReversed {
			return ledger.JournalEntry{}, fmt.Errorf("%w: entry %s already reversed", ErrExternalConflict, origID)
		}

		orig.Status = ledger.EntryReversed
		reversedByID := entry.ID
		orig.ReversedByID = &reversedByID
		s.entries[origID] = cloneEntry(orig)

		entry.Status = ledger.EntryPosted
		entry.Classification = ledger.EntryClassReversing
		s.entries[entry.ID] = cloneEntry(entry)
		delete(s.drafts, entry.ID)

	default:
		return ledger.JournalEntry{}, fmt.Errorf("unsupported posting operation: %s", commit.Operation)
	}

	return cloneEntry(entry), nil
}

// ------------------------------------------------------------- Sequence Allocator

func (s *ExternalStore) NextSequence(_ context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s/%s", companyID, journalID)
	seq := s.sequences[key] + 1
	s.sequences[key] = seq
	return seq, nil
}

// ------------------------------------------------------------- Balance & Reporting

func (s *ExternalStore) GetAccountBalance(_ context.Context, companyID types.CompanyID, accountID types.AccountID, asOf time.Time, currency string) (decimal.Decimal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := decimal.Zero
	for _, entry := range s.entries {
		if entry.CompanyID != companyID || (!asOf.IsZero() && entry.EffectiveDate.After(asOf)) {
			continue
		}
		if entry.Status != ledger.EntryPosted && entry.Status != ledger.EntryReversed {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != accountID {
				continue
			}
			if currency != "" && line.FunctionalDebit.Currency() != currency {
				continue
			}
			total = total.Add(line.FunctionalDebit.Amount()).Sub(line.FunctionalCredit.Amount())
		}
	}
	return total, nil
}

func (s *ExternalStore) GetTrialBalance(_ context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	zero, err := types.Zero(currency)
	if err != nil {
		return reporting.TrialBalance{}, err
	}

	type totals struct{ debit, credit decimal.Decimal }
	byAccount := make(map[types.AccountID]*totals)

	for _, entry := range s.entries {
		if entry.CompanyID != companyID || (!asOf.IsZero() && entry.EffectiveDate.After(asOf)) {
			continue
		}
		if entry.Status != ledger.EntryPosted && entry.Status != ledger.EntryReversed {
			continue
		}
		for _, line := range entry.Lines {
			if line.FunctionalDebit.Currency() != currency {
				continue
			}
			t, ok := byAccount[line.AccountID]
			if !ok {
				t = &totals{}
				byAccount[line.AccountID] = t
			}
			t.debit = t.debit.Add(line.FunctionalDebit.Amount())
			t.credit = t.credit.Add(line.FunctionalCredit.Amount())
		}
	}

	for accountID := range s.accounts[companyID] {
		if _, ok := byAccount[accountID]; !ok {
			byAccount[accountID] = &totals{}
		}
	}

	lines := make([]reporting.TrialBalanceLine, 0, len(byAccount))
	totDebit := decimal.Zero
	totCredit := decimal.Zero

	for accID, t := range byAccount {
		pDeb, _ := types.NewMoney(t.debit, currency)
		pCred, _ := types.NewMoney(t.credit, currency)

		endDeb, endCred := zero, zero
		net := t.debit.Sub(t.credit)
		if net.IsPositive() {
			endDeb, _ = types.NewMoney(net, currency)
			totDebit = totDebit.Add(net)
		} else if net.IsNegative() {
			endCred, _ = types.NewMoney(net.Abs(), currency)
			totCredit = totCredit.Add(net.Abs())
		}

		code := uuid.UUID(accID).String()
		name := uuid.UUID(accID).String()
		accountType := ""
		if accs, ok := s.accounts[companyID]; ok {
			if acc, ok := accs[accID]; ok {
				code = acc.Code
				name = acc.Name
				accountType = string(acc.Type)
			}
		}

		lines = append(lines, reporting.TrialBalanceLine{
			AccountID:    accID,
			AccountCode:  code,
			AccountName:  name,
			AccountType:  accountType,
			PeriodDebit:  pDeb,
			PeriodCredit: pCred,
			EndingDebit:  endDeb,
			EndingCredit: endCred,
		})
	}

	sort.Slice(lines, func(i, j int) bool { return lines[i].AccountCode < lines[j].AccountCode })

	totalDebitMoney, _ := types.NewMoney(totDebit, currency)
	totalCreditMoney, _ := types.NewMoney(totCredit, currency)

	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOfDate:    asOf,
		Currency:    currency,
		Lines:       lines,
		TotalDebit:  totalDebitMoney,
		TotalCredit: totalCreditMoney,
		IsBalanced:  totDebit.Equal(totCredit),
	}, nil
}

func (s *ExternalStore) GetGeneralLedger(_ context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	currency := "USD"
	if c, ok := s.companies[companyID]; ok && c.FunctionalCurrency != "" {
		currency = c.FunctionalCurrency
	}
	account, ok := s.accounts[companyID][accountID]
	if !ok {
		return reporting.GeneralLedgerReport{}, fmt.Errorf("%w: account %s", publicapplication.ErrNotFound, accountID)
	}
	type row struct {
		entry ledger.JournalEntry
		line  ledger.EntryLine
	}
	var opening decimal.Decimal
	var rows []row
	for _, entry := range s.entries {
		if entry.CompanyID != companyID ||
			(entry.Status != ledger.EntryPosted && entry.Status != ledger.EntryReversed) {
			continue
		}
		if !end.IsZero() && entry.EffectiveDate.After(end) {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != accountID {
				continue
			}
			net := line.FunctionalDebit.Amount().Sub(line.FunctionalCredit.Amount())
			if !start.IsZero() && entry.EffectiveDate.Before(start) {
				opening = opening.Add(net)
				continue
			}
			rows = append(rows, row{entry: entry, line: line})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].entry.EffectiveDate.Equal(rows[j].entry.EffectiveDate) {
			if rows[i].entry.SequenceNumber != rows[j].entry.SequenceNumber {
				return rows[i].entry.SequenceNumber < rows[j].entry.SequenceNumber
			}
			return uuid.UUID(rows[i].entry.ID).String() < uuid.UUID(rows[j].entry.ID).String()
		}
		return rows[i].entry.EffectiveDate.Before(rows[j].entry.EffectiveDate)
	})

	running := opening
	entries := make([]reporting.GeneralLedgerEntry, 0, len(rows))
	for _, row := range rows {
		net := row.line.FunctionalDebit.Amount().Sub(row.line.FunctionalCredit.Amount())
		running = running.Add(net)
		runMoney, _ := types.NewMoney(running, currency)
		entries = append(entries, reporting.GeneralLedgerEntry{
			EntryID:        row.entry.ID,
			EntryNumber:    row.entry.EntryNumber,
			EffectiveDate:  row.entry.EffectiveDate,
			Description:    row.entry.Description,
			Debit:          row.line.FunctionalDebit,
			Credit:         row.line.FunctionalCredit,
			RunningBalance: runMoney,
		})
	}
	openingMoney, _ := types.NewMoney(opening, currency)
	endingMoney, _ := types.NewMoney(running, currency)
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

func (s *ExternalStore) GetBalanceSheet(context.Context, types.CompanyID, time.Time, string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, errors.New("external adapter: report not supported")
}

func (s *ExternalStore) GetIncomeStatement(context.Context, types.CompanyID, time.Time, time.Time, string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, errors.New("external adapter: report not supported")
}

// ------------------------------------------------------------- Audit

func (s *ExternalStore) AppendAudit(_ context.Context, record storage.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, record)
	return nil
}

// ------------------------------------------------------------- Idempotency

func (s *ExternalStore) Claim(_ context.Context, record storage.IdempotencyRecord) (storage.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", record.CompanyID, record.Key)
	existing, found := s.storageIdem[key]
	if !found {
		record.Status = storage.IdempotencyStarted
		record.CreatedAt = time.Now().UTC()
		record.UpdatedAt = record.CreatedAt
		s.storageIdem[key] = record
		return record, true, nil
	}

	if existing.PayloadHash != record.PayloadHash {
		return existing, false, storage.ErrUniqueConstraint
	}
	return existing, false, nil
}

func (s *ExternalStore) Complete(_ context.Context, companyID types.CompanyID, key, payloadHash string, entryID types.JournalEntryID, response []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%s/%s", companyID, key)
	rec, found := s.storageIdem[k]
	if !found || rec.PayloadHash != payloadHash {
		return storage.ErrUniqueConstraint
	}
	rec.Status = storage.IdempotencyCompleted
	rec.EntryID = &entryID
	rec.Response = response
	rec.UpdatedAt = time.Now().UTC()
	s.storageIdem[k] = rec
	return nil
}

func (s *ExternalStore) Fail(_ context.Context, companyID types.CompanyID, key, payloadHash, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%s/%s", companyID, key)
	rec, found := s.storageIdem[k]
	if !found || rec.PayloadHash != payloadHash {
		return storage.ErrUniqueConstraint
	}
	rec.Status = status
	rec.UpdatedAt = time.Now().UTC()
	s.storageIdem[k] = rec
	return nil
}

// ------------------------------------------------------------- Public Event Idempotency

func (s *ExternalStore) GetPublicEventIngestion(_ context.Context, companyID uuid.UUID, idempotencyKey string) (publicevents.Ingestion, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := fmt.Sprintf("%s/%s/%s", companyID, publicevents.DefaultSourceSystem, strings.TrimSpace(idempotencyKey))
	ing, ok := s.eventIdem[k]
	return ing, ok, nil
}

func (s *ExternalStore) ClaimPublicEventIngestion(_ context.Context, record publicevents.Ingestion) (publicevents.Ingestion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := publicevents.NormalizeSourceSystem(record.SourceSystem)
	k := fmt.Sprintf("%s/%s/%s", record.CompanyID, src, strings.TrimSpace(record.IdempotencyKey))
	existing, found := s.eventIdem[k]
	if !found {
		record.Status = publicevents.StatusStarted
		record.SourceSystem = src
		s.eventIdem[k] = record
		return record, true, nil
	}

	if existing.PayloadHash != record.PayloadHash {
		return existing, false, storage.ErrUniqueConstraint
	}
	return existing, false, nil
}

func (s *ExternalStore) CompletePublicEventIngestion(_ context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := fmt.Sprintf("%s/%s/%s", companyID, publicevents.DefaultSourceSystem, strings.TrimSpace(idempotencyKey))
	existing, found := s.eventIdem[k]
	if !found {
		return publicevents.ErrRecordNotFound
	}
	existing.Status = publicevents.StatusCompleted
	existing.Entry = cloneEntry(entry)
	existing.EntryID = uuid.UUID(entry.ID)
	s.eventIdem[k] = existing
	return nil
}

func (s *ExternalStore) FailPublicEventIngestion(_ context.Context, companyID uuid.UUID, idempotencyKey, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := fmt.Sprintf("%s/%s/%s", companyID, publicevents.DefaultSourceSystem, strings.TrimSpace(idempotencyKey))
	existing, found := s.eventIdem[k]
	if !found {
		return publicevents.ErrRecordNotFound
	}
	existing.Status = publicevents.StatusTerminalFailure
	existing.FailureReason = reason
	s.eventIdem[k] = existing
	return nil
}

func (s *ExternalStore) GetPublicEventAccountMapping(_ context.Context, companyID uuid.UUID, eventType, ledgerID string) (publicevents.AccountMapping, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := fmt.Sprintf("%s/%s/%s", companyID, eventType, ledgerID)
	m, ok := s.mappings[k]
	return m, ok, nil
}

func (s *ExternalStore) SavePublicEventAccountMapping(_ context.Context, mapping publicevents.AccountMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%s/%s/%s", mapping.CompanyID, mapping.EventType, mapping.LedgerID)
	s.mappings[k] = mapping
	return nil
}

// ------------------------------------------------------------- Exchange Rate SPI

func (s *ExternalStore) GetRate(_ context.Context, from, to string, _ time.Time) (decimal.Decimal, error) {
	if strings.EqualFold(from, to) {
		return decimal.NewFromInt(1), nil
	}
	return decimal.Zero, errors.New("external adapter: rate unavailable")
}

// ------------------------------------------------------------- Transactions & Unit of Work

func (s *ExternalStore) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(ctx)
}

func (s *ExternalStore) Execute(ctx context.Context, fn func(ctx context.Context, uow storage.UnitOfWork) error) error {
	return s.ExecuteWithOptions(ctx, storage.TxOptions{}, fn)
}

func (s *ExternalStore) ExecuteWithOptions(ctx context.Context, _ storage.TxOptions, fn func(ctx context.Context, uow storage.UnitOfWork) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	uow := &externalUnitOfWork{store: s}
	return fn(ctx, uow)
}

type externalUnitOfWork struct {
	store *ExternalStore
}

func (u *externalUnitOfWork) Drafts() storage.DraftStore            { return u.store }
func (u *externalUnitOfWork) Entries() storage.EntryReader          { return u.store }
func (u *externalUnitOfWork) Posting() storage.PostingWriter        { return u.store }
func (u *externalUnitOfWork) Sequences() storage.SequenceAllocator  { return u.store }
func (u *externalUnitOfWork) Audit() storage.AuditWriter            { return u.store }
func (u *externalUnitOfWork) Idempotency() storage.IdempotencyStore { return u.store }
func (u *externalUnitOfWork) Balance() storage.BalanceReader        { return u.store }

// ------------------------------------------------------------- Helpers

func cloneEntry(in ledger.JournalEntry) ledger.JournalEntry {
	out := in
	if in.Lines != nil {
		out.Lines = make([]ledger.EntryLine, len(in.Lines))
		copy(out.Lines, in.Lines)
	}
	return out
}
