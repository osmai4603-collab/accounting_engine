package memory

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Driver struct {
	mu         sync.RWMutex
	accounts   map[uuid.UUID]masterdata.Account
	companies  map[uuid.UUID]masterdata.Company
	years      map[uuid.UUID]masterdata.FiscalYear
	periods    map[uuid.UUID]masterdata.FiscalPeriod
	journalSeq map[uuid.UUID]int64
	entries    map[uuid.UUID]posting.JournalEntry
	events     map[string]subledgers.EconomicEvent
	lines      map[uuid.UUID]subledgers.OpenLine
	assets     map[uuid.UUID]subledgers.FixedAsset
	bank       map[string][]subledgers.BankItem
	audit      []*governance.FinancialAuditLog
	rates      *valuation.RateRegistry
}

func New() *Driver {
	return &Driver{
		accounts:   make(map[uuid.UUID]masterdata.Account),
		companies:  make(map[uuid.UUID]masterdata.Company),
		years:      make(map[uuid.UUID]masterdata.FiscalYear),
		periods:    make(map[uuid.UUID]masterdata.FiscalPeriod),
		journalSeq: make(map[uuid.UUID]int64),
		entries:    make(map[uuid.UUID]posting.JournalEntry),
		events:     make(map[string]subledgers.EconomicEvent),
		lines:      make(map[uuid.UUID]subledgers.OpenLine),
		assets:     make(map[uuid.UUID]subledgers.FixedAsset),
		bank:       make(map[string][]subledgers.BankItem),
		rates:      valuation.NewRateRegistry(),
	}
}

func (d *Driver) CreateCompany(company masterdata.Company) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := company.Validate(); err != nil {
		return err
	}
	for id, existing := range d.companies {
		if id != company.ID && strings.EqualFold(existing.Code, company.Code) {
			return masterdata.ErrInvalidCompany
		}
	}
	d.companies[company.ID] = company
	return nil
}

func (d *Driver) GetCompanyByID(id uuid.UUID) (masterdata.Company, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	company, ok := d.companies[id]
	if !ok {
		return masterdata.Company{}, masterdata.ErrInvalidCompany
	}
	return company, nil
}

func (d *Driver) CreateAccount(account masterdata.Account) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	code := strings.ToUpper(strings.TrimSpace(account.Code))
	for id, current := range d.accounts {
		if id != account.ID && current.CompanyID == account.CompanyID &&
			strings.EqualFold(strings.TrimSpace(current.Code), code) {
			return masterdata.ErrDuplicateAccountCode
		}
	}
	d.accounts[account.ID] = account
	return nil
}

func (d *Driver) GetAccountByID(id uuid.UUID) (masterdata.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	account, ok := d.accounts[id]
	if !ok {
		return masterdata.Account{}, masterdata.ErrAccountNotFound
	}

	return account, nil
}

func (d *Driver) GetAccountByIDForCompany(companyID, accountID uuid.UUID) (masterdata.Account, error) {
	account, err := d.GetAccountByID(accountID)
	if err != nil {
		return masterdata.Account{}, err
	}
	if account.CompanyID != companyID {
		return masterdata.Account{}, masterdata.ErrAccountNotFound
	}
	return account, nil
}

func (d *Driver) GetAccountByCodeForCompany(companyID uuid.UUID, code string) (masterdata.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if account.CompanyID == companyID && strings.EqualFold(account.Code, strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return masterdata.Account{}, masterdata.ErrAccountNotFound
}

func (d *Driver) GetAccountByCode(code string) (masterdata.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if strings.EqualFold(strings.TrimSpace(account.Code), strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return masterdata.Account{}, masterdata.ErrAccountNotFound
}

func (d *Driver) ListAccounts() ([]masterdata.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	accounts := make([]masterdata.Account, 0, len(d.accounts))
	for _, account := range d.accounts {
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })
	return accounts, nil
}

func (d *Driver) ListAccountsForCompany(companyID uuid.UUID) ([]masterdata.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	accounts := make([]masterdata.Account, 0)
	for _, account := range d.accounts {
		if account.CompanyID == companyID {
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })
	return accounts, nil
}

func (d *Driver) HasChildren(id uuid.UUID) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if account.ParentID != nil && *account.ParentID == id {
			return true, nil
		}
	}
	return false, nil
}

func (d *Driver) CreateFiscalYear(year masterdata.FiscalYear) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range year.Periods {
		if year.Periods[i].CompanyID == uuid.Nil {
			year.Periods[i].CompanyID = year.CompanyID
		}
		if year.Periods[i].FiscalYearID == uuid.Nil {
			year.Periods[i].FiscalYearID = year.ID
		}
		if year.Periods[i].Type == "" {
			year.Periods[i].Type = masterdata.FiscalPeriodTypeNormal
		}
	}
	d.years[year.ID] = year
	for _, period := range year.Periods {
		d.periods[period.ID] = period
	}
	return nil
}

func (d *Driver) GetFiscalYear(id uuid.UUID) (masterdata.FiscalYear, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	year, ok := d.years[id]
	if !ok {
		return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
	}
	return year, nil
}

func (d *Driver) GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	year, err := d.GetFiscalYear(fiscalYearID)
	if err != nil {
		return masterdata.FiscalYear{}, err
	}
	if year.CompanyID != companyID {
		return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
	}
	return year, nil
}

func (d *Driver) GetPeriodByDate(date time.Time) (masterdata.FiscalPeriod, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var matched []masterdata.FiscalPeriod
	for _, period := range d.periods {
		if period.Contains(date) {
			matched = append(matched, period)
		}
	}
	if len(matched) == 0 {
		return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
	}
	// Prefer regular monthly periods over adjustment periods
	for _, period := range matched {
		if period.Type == masterdata.FiscalPeriodTypeNormal {
			return period, nil
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Number < matched[j].Number })
	return matched[0], nil
}

func (d *Driver) GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var matched []masterdata.FiscalPeriod
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.Contains(date) {
			matched = append(matched, period)
		}
	}
	if len(matched) == 0 {
		return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
	}
	for _, period := range matched {
		if period.Type == masterdata.FiscalPeriodTypeNormal {
			return period, nil
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Number < matched[j].Number })
	return matched[0], nil
}

func (d *Driver) GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == fiscalYearID && period.Number == number {
			return period, nil
		}
	}
	return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
}

func (d *Driver) UpdatePeriodState(id uuid.UUID, state masterdata.PeriodState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	period, ok := d.periods[id]
	if !ok {
		return masterdata.ErrFiscalPeriodNotFound
	}
	if err := masterdata.ValidatePeriodTransition(period.State, state); err != nil {
		return err
	}
	period.State = state
	d.periods[id] = period
	for yearID, year := range d.years {
		for i := range year.Periods {
			if year.Periods[i].ID == id {
				year.Periods[i] = period
				d.years[yearID] = year
			}
		}
	}
	return nil
}

func (d *Driver) GetNextSequenceWithLock(id uuid.UUID) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.journalSeq[id]++
	return d.journalSeq[id], nil
}

func cloneEntry(entry posting.JournalEntry) posting.JournalEntry {
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	return entry
}

func cloneFixedAsset(asset subledgers.FixedAsset) subledgers.FixedAsset {
	asset.AppliedEntryIDs = append([]uuid.UUID(nil), asset.AppliedEntryIDs...)
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset
}

func (d *Driver) SaveDraft(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if entry.CompanyID != uuid.Nil {
		if entry.ID == uuid.Nil || entry.JournalID == uuid.Nil {
			return posting.ErrInvalidEntry
		}
		for i := range entry.Lines {
			if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
				return posting.ErrInvalidEntry
			}
			account, ok := d.accounts[entry.Lines[i].AccountID]
			if !ok || account.CompanyID != entry.CompanyID {
				return masterdata.ErrAccountNotFound
			}
			entry.Lines[i].CompanyID = entry.CompanyID
		}
	}
	if current, ok := d.entries[entry.ID]; ok && current.Status != posting.EntryDraft {
		return financialtypes.ErrCannotModifyPostedMove
	}
	d.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (d *Driver) GetEntryByID(id uuid.UUID) (posting.JournalEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.entries[id]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return cloneEntry(entry), nil
}

func (d *Driver) PostAtomic(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if current, ok := d.entries[entry.ID]; ok && current.Status == posting.EntryPosted && !reflect.DeepEqual(current.Lines, entry.Lines) {
		return financialtypes.ErrCannotModifyPostedMove
	}
	d.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (d *Driver) PostAndSequence(entry posting.JournalEntry) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	d.journalSeq[entry.JournalID]++
	entry.SequenceNumber = d.journalSeq[entry.JournalID]
	d.entries[entry.ID] = cloneEntry(entry)
	return cloneEntry(entry), nil
}

func (d *Driver) PostReversalAtomic(original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	currentOriginal, ok := d.entries[original.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if currentOriginal.Status != posting.EntryPosted || currentOriginal.ReversedEntryID != nil {
		return posting.JournalEntry{}, posting.ErrEntryAlreadyReversed
	}
	currentReversal, ok := d.entries[reversal.ID]
	if !ok || currentReversal.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	d.journalSeq[reversal.JournalID]++
	reversal.SequenceNumber = d.journalSeq[reversal.JournalID]
	reversal = cloneEntry(reversal)
	reversal.Status = posting.EntryPosted
	reversalID := reversal.ID
	currentOriginal.ReversedEntryID = &reversalID
	currentOriginal.Status = posting.EntryReversed
	currentOriginal.UpdatedAt = time.Now().UTC()
	d.entries[reversal.ID] = reversal
	d.entries[original.ID] = cloneEntry(currentOriginal)
	return cloneEntry(reversal), nil
}

func (d *Driver) GetNextSequence(id uuid.UUID) (int64, error) {
	return d.GetNextSequenceWithLock(id)
}

func (d *Driver) PostBulk(entries []posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, entry := range entries {
		d.entries[entry.ID] = cloneEntry(entry)
	}
	return nil
}

func (d *Driver) ListPostedEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	return d.ListEntries(start, end)
}

func (d *Driver) ListEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entries := make([]posting.JournalEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}
		if entry.Status == posting.EntryPosted && (start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EntryDate.Before(entries[j].EntryDate) })
	return entries, nil
}

func (d *Driver) ListEntriesForCompany(companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entries := make([]posting.JournalEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}
		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted &&
			(start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EntryDate.Before(entries[j].EntryDate) })
	return entries, nil
}

func (d *Driver) SaveEvent(event subledgers.EconomicEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := event.CompanyID.String() + "\x00" + event.IdempotencyKey
	if existing, ok := d.events[key]; ok {
		if existing.ResultEntryID != event.ResultEntryID || existing.EventType != event.EventType ||
			existing.Reference != event.Reference || !existing.Amount.Equal(event.Amount) {
			return subledgers.ErrAlreadyExists
		}
		return nil
	}
	d.events[key] = event
	return nil
}

func (d *Driver) SaveEventAndDraft(event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := event.CompanyID.String() + "\x00" + event.IdempotencyKey
	if _, exists := d.events[key]; exists {
		return subledgers.ErrAlreadyExists
	}
	if _, exists := d.entries[entry.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[entry.Lines[i].AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	d.entries[entry.ID] = cloneEntry(entry)
	d.events[key] = event
	return nil
}

func (d *Driver) GetEventByIdempotencyKey(key string) (subledgers.EconomicEvent, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var event subledgers.EconomicEvent
	var ok bool
	for _, current := range d.events {
		if current.IdempotencyKey == key {
			if ok {
				return subledgers.EconomicEvent{}, false, subledgers.ErrInvalidEvent
			}
			event, ok = current, true
		}
	}
	return event, ok, nil
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	event, ok := d.events[companyID.String()+"\x00"+key]
	return event, ok, nil
}
func (d *Driver) SaveOpenLine(line subledgers.OpenLine) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.lines[line.ID] = line
	return nil
}

func (d *Driver) SaveOpenLines(lines []subledgers.OpenLine) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, line := range lines {
		if existing, ok := d.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
	}
	for _, line := range lines {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	return nil
}

func (d *Driver) CompareAndSaveOpenLines(expected, updated []subledgers.OpenLine) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldLine := range expected {
		current, ok := d.lines[oldLine.ID]
		if !ok {
			return subledgers.ErrNotFound
		}
		if current.CompanyID != oldLine.CompanyID || updated[i].ID != oldLine.ID ||
			updated[i].CompanyID != oldLine.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return subledgers.ErrConcurrentMatch
		}
	}
	for _, line := range updated {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	return nil
}

func (d *Driver) CompareAndSaveOpenLinesAndDraft(expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldLine := range expected {
		current, ok := d.lines[oldLine.ID]
		if !ok {
			return subledgers.ErrNotFound
		}
		if current.CompanyID != oldLine.CompanyID || updated[i].ID != oldLine.ID ||
			updated[i].CompanyID != oldLine.CompanyID || draft.CompanyID != oldLine.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return subledgers.ErrConcurrentMatch
		}
	}
	if current, exists := d.entries[draft.ID]; exists && current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	for i := range draft.Lines {
		if draft.Lines[i].CompanyID != uuid.Nil && draft.Lines[i].CompanyID != draft.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[draft.Lines[i].AccountID]
		if !ok || account.CompanyID != draft.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		draft.Lines[i].CompanyID = draft.CompanyID
	}
	for _, line := range updated {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	d.entries[draft.ID] = cloneEntry(draft)
	return nil
}

func (d *Driver) GetOpenLine(id uuid.UUID) (subledgers.OpenLine, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	line, ok := d.lines[id]
	if !ok {
		return subledgers.OpenLine{}, subledgers.ErrNotFound
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	return line, nil
}
func (d *Driver) ListOpenLines(partnerID string) ([]subledgers.OpenLine, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]subledgers.OpenLine, 0)
	for _, line := range d.lines {
		if partnerID == "" || (line.PartnerID != nil && line.PartnerID.String() == partnerID) {
			line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
			line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
			result = append(result, line)
		}
	}
	return result, nil
}

func (d *Driver) ListOpenLinesForCompany(companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]subledgers.OpenLine, 0)
	for _, line := range d.lines {
		if line.CompanyID != companyID || (partnerID != "" && (line.PartnerID == nil || line.PartnerID.String() != partnerID)) {
			continue
		}
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		result = append(result, line)
	}
	return result, nil
}
func (d *Driver) SaveFixedAsset(asset subledgers.FixedAsset) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (d *Driver) SaveFixedAssetDraft(expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
		return subledgers.ErrInvalidAsset
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	currentAsset, exists := d.assets[expected.ID]
	if !exists {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(currentAsset, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	if _, exists := d.entries[entry.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:DEPRECIATION:") {
		for _, current := range d.entries {
			if current.DocumentReference == entry.DocumentReference &&
				(current.Status == posting.EntryDraft || current.Status == posting.EntryPosted) {
				return subledgers.ErrAlreadyExists
			}
		}
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[entry.Lines[i].AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	d.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (d *Driver) ConfirmFixedAssetMutation(expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidAsset
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	currentAsset, exists := d.assets[expected.ID]
	if !exists {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(currentAsset, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	actualEntry, exists := d.entries[expectedEntry.ID]
	if !exists {
		return posting.ErrEntryNotFound
	}
	if !subledgers.FixedAssetEntryMatches(actualEntry, expectedEntry) {
		return posting.ErrEntryNotPosted
	}
	d.assets[updated.ID] = cloneFixedAsset(updated)
	return nil
}

func (d *Driver) GetFixedAsset(id uuid.UUID) (subledgers.FixedAsset, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	asset, ok := d.assets[id]
	if !ok {
		return subledgers.FixedAsset{}, subledgers.ErrNotFound
	}
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset, nil
}

func (d *Driver) ListFixedAssets() ([]subledgers.FixedAsset, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	assets := make([]subledgers.FixedAsset, 0, len(d.assets))
	for _, asset := range d.assets {
		if asset.DisposedAt != nil {
			disposedAt := *asset.DisposedAt
			asset.DisposedAt = &disposedAt
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func (d *Driver) ListFixedAssetsForCompany(companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	assets := make([]subledgers.FixedAsset, 0)
	for _, asset := range d.assets {
		if asset.CompanyID == companyID {
			assets = append(assets, asset)
		}
	}
	return assets, nil
}
func (d *Driver) SaveBankItem(item subledgers.BankItem) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, current := range d.bank[item.AccountID.String()] {
		if current.ID == item.ID {
			if current.CompanyID != item.CompanyID {
				return subledgers.ErrCompanyMismatch
			}
			return subledgers.ErrAlreadyExists
		}
	}
	d.bank[item.AccountID.String()] = append(d.bank[item.AccountID.String()], item)
	return nil
}
func (d *Driver) ListBankItems(accountID string) ([]subledgers.BankItem, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]subledgers.BankItem(nil), d.bank[accountID]...), nil
}

func (d *Driver) ListBankItemsForCompany(companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	items := make([]subledgers.BankItem, 0)
	for _, item := range d.bank[accountID] {
		if item.CompanyID == companyID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (d *Driver) CompareAndSaveBankItems(expected, updated []subledgers.BankItem) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID {
			return subledgers.ErrCompanyMismatch
		}
		items := d.bank[oldItem.AccountID.String()]
		found := false
		for _, current := range items {
			if current.ID != oldItem.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(current, oldItem) {
				return subledgers.ErrConcurrentBankItem
			}
			break
		}
		if !found {
			return subledgers.ErrNotFound
		}
	}
	for _, updatedItem := range updated {
		items := d.bank[updatedItem.AccountID.String()]
		for i := range items {
			if items[i].ID == updatedItem.ID {
				items[i] = updatedItem
				break
			}
		}
		d.bank[updatedItem.AccountID.String()] = items
	}
	return nil
}

func (d *Driver) CompareAndSaveBankItemsAndDraft(expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID || draft.CompanyID != oldItem.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		items := d.bank[oldItem.AccountID.String()]
		found := false
		for _, current := range items {
			if current.ID != oldItem.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(current, oldItem) {
				return subledgers.ErrConcurrentBankItem
			}
			break
		}
		if !found {
			return subledgers.ErrNotFound
		}
	}
	if current, exists := d.entries[draft.ID]; exists && current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	for i := range draft.Lines {
		if draft.Lines[i].CompanyID != uuid.Nil && draft.Lines[i].CompanyID != draft.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[draft.Lines[i].AccountID]
		if !ok || account.CompanyID != draft.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		draft.Lines[i].CompanyID = draft.CompanyID
	}
	for _, updatedItem := range updated {
		items := d.bank[updatedItem.AccountID.String()]
		for i := range items {
			if items[i].ID == updatedItem.ID {
				items[i] = updatedItem
				break
			}
		}
		d.bank[updatedItem.AccountID.String()] = items
	}
	d.entries[draft.ID] = cloneEntry(draft)
	return nil
}

func (d *Driver) Register(rate valuation.ExchangeRate) error { return d.rates.Register(rate) }
func (d *Driver) AddRate(rate valuation.ExchangeRate) error  { return d.Register(rate) }
func (d *Driver) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.rates.GetRate(ctx, from, to, date)
}
func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.rates.GetRateOfType(ctx, rateType, from, to, date)
}

func (d *Driver) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	log.ID = int64(len(d.audit) + 1)
	copyLog := log
	copyLog.PayloadBefore = append([]byte(nil), log.PayloadBefore...)
	copyLog.PayloadAfter = append([]byte(nil), log.PayloadAfter...)
	d.audit = append(d.audit, &copyLog)
	return nil
}
func (d *Driver) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.audit) == 0 {
		return nil, nil
	}
	log := *d.audit[len(d.audit)-1]
	return &log, nil
}
func (d *Driver) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	d.mu.RLock()
	logs := make([]governance.FinancialAuditLog, 0, len(d.audit))
	for _, log := range d.audit {
		if log.ID >= fromID {
			logs = append(logs, *log)
		}
	}
	d.mu.RUnlock()
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })
	for _, log := range logs {
		if err := handler(log); err != nil {
			return err
		}
	}
	return nil
}
