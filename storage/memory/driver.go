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

func (d *Driver) CreateAccount(account masterdata.Account) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	code := strings.ToUpper(strings.TrimSpace(account.Code))
	for id, current := range d.accounts {
		if id != account.ID && strings.EqualFold(strings.TrimSpace(current.Code), code) {
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

func (d *Driver) GetPeriodByDate(date time.Time) (masterdata.FiscalPeriod, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, period := range d.periods {
		if period.Contains(date) {
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

func (d *Driver) SaveDraft(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
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

func (d *Driver) SaveEvent(event subledgers.EconomicEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events[event.IdempotencyKey] = event
	return nil
}
func (d *Driver) GetEventByIdempotencyKey(key string) (subledgers.EconomicEvent, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	event, ok := d.events[key]
	return event, ok, nil
}
func (d *Driver) SaveOpenLine(line subledgers.OpenLine) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines[line.ID] = line
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
	return line, nil
}
func (d *Driver) ListOpenLines(partnerID string) ([]subledgers.OpenLine, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]subledgers.OpenLine, 0)
	for _, line := range d.lines {
		if partnerID == "" || (line.PartnerID != nil && line.PartnerID.String() == partnerID) {
			line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
			result = append(result, line)
		}
	}
	return result, nil
}
func (d *Driver) SaveFixedAsset(asset subledgers.FixedAsset) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.assets[asset.ID] = asset
	return nil
}
func (d *Driver) GetFixedAsset(id uuid.UUID) (subledgers.FixedAsset, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	asset, ok := d.assets[id]
	if !ok {
		return subledgers.FixedAsset{}, subledgers.ErrNotFound
	}
	return asset, nil
}

func (d *Driver) ListFixedAssets() ([]subledgers.FixedAsset, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	assets := make([]subledgers.FixedAsset, 0, len(d.assets))
	for _, asset := range d.assets {
		assets = append(assets, asset)
	}
	return assets, nil
}
func (d *Driver) SaveBankItem(item subledgers.BankItem) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bank[item.AccountID.String()] = append(d.bank[item.AccountID.String()], item)
	return nil
}
func (d *Driver) ListBankItems(accountID string) ([]subledgers.BankItem, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]subledgers.BankItem(nil), d.bank[accountID]...), nil
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
