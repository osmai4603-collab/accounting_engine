package memory

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Driver struct {
	mu               sync.RWMutex
	accounts         map[uuid.UUID]masterdata.Account
	versions         map[uuid.UUID][]masterdata.AccountVersion
	centers          map[uuid.UUID]masterdata.CostCenter
	dimensions       map[string]masterdata.AnalyticalDimensionValue
	companies        map[uuid.UUID]masterdata.Company
	years            map[uuid.UUID]masterdata.FiscalYear
	periods          map[uuid.UUID]masterdata.FiscalPeriod
	journalSeq       map[string]int64
	entries          map[uuid.UUID]posting.JournalEntry
	balances         map[accountBalanceKey]accountBalance
	adjustingPeriods map[companyPeriodKey]bool
	events           map[string]subledgers.EconomicEvent
	eventRules       map[string][]subledgers.EventAccountingRule
	lines            map[uuid.UUID]subledgers.OpenLine
	assets           map[uuid.UUID]subledgers.FixedAsset
	bank             map[string][]subledgers.BankItem
	contracts        map[uuid.UUID]map[uuid.UUID]ifrs15.Contract
	audit            []*governance.FinancialAuditLog
	rates            *valuation.RateRegistry
}

type accountBalanceKey struct {
	companyID uuid.UUID
	accountID uuid.UUID
	periodID  uuid.UUID
	currency  string
}

type companyPeriodKey struct {
	companyID uuid.UUID
	periodID  uuid.UUID
}

type accountBalance struct {
	beginningDebit  decimal.Decimal
	beginningCredit decimal.Decimal
	periodDebit     decimal.Decimal
	periodCredit    decimal.Decimal
}

var (
	_ subledgers.Repository               = (*Driver)(nil)
	_ masterdata.ContextStorageRepository = (*Driver)(nil)
)

func New() *Driver {
	return &Driver{
		accounts:         make(map[uuid.UUID]masterdata.Account),
		versions:         make(map[uuid.UUID][]masterdata.AccountVersion),
		centers:          make(map[uuid.UUID]masterdata.CostCenter),
		dimensions:       make(map[string]masterdata.AnalyticalDimensionValue),
		companies:        make(map[uuid.UUID]masterdata.Company),
		years:            make(map[uuid.UUID]masterdata.FiscalYear),
		periods:          make(map[uuid.UUID]masterdata.FiscalPeriod),
		journalSeq:       make(map[string]int64),
		entries:          make(map[uuid.UUID]posting.JournalEntry),
		balances:         make(map[accountBalanceKey]accountBalance),
		adjustingPeriods: make(map[companyPeriodKey]bool),
		events:           make(map[string]subledgers.EconomicEvent),
		eventRules:       make(map[string][]subledgers.EventAccountingRule),
		lines:            make(map[uuid.UUID]subledgers.OpenLine),
		assets:           make(map[uuid.UUID]subledgers.FixedAsset),
		bank:             make(map[string][]subledgers.BankItem),
		contracts:        make(map[uuid.UUID]map[uuid.UUID]ifrs15.Contract),
		rates:            valuation.NewRateRegistry(),
	}
}

func (d *Driver) CreateCompany(ctx context.Context, company masterdata.Company) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (masterdata.Company, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Company{}, err
	}
	return d.GetCompanyByID(companyID)
}

func (d *Driver) CreateAccount(ctx context.Context, account masterdata.Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetAccountByID(ctx context.Context, id uuid.UUID) (masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Account{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	account, ok := d.accounts[id]
	if !ok {
		return masterdata.Account{}, masterdata.ErrAccountNotFound
	}

	return account, nil
}

func (d *Driver) GetAccountByIDForCompany(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	account, err := d.GetAccountByID(ctx, accountID)
	if err != nil {
		return masterdata.Account{}, err
	}
	if account.CompanyID != companyID {
		return masterdata.Account{}, masterdata.ErrAccountNotFound
	}
	return account, nil
}

func (d *Driver) GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	return d.GetAccountByIDForCompany(ctx, companyID, accountID)
}

func (d *Driver) GetAccountByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Account{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if account.CompanyID == companyID && strings.EqualFold(account.Code, strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return masterdata.Account{}, masterdata.ErrAccountNotFound
}

func (d *Driver) GetAccountByCode(ctx context.Context, code string) (masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Account{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if strings.EqualFold(strings.TrimSpace(account.Code), strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return masterdata.Account{}, masterdata.ErrAccountNotFound
}

func (d *Driver) ListAccounts(ctx context.Context) ([]masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	accounts := make([]masterdata.Account, 0, len(d.accounts))
	for _, account := range d.accounts {
		if account.CompanyID == companyID {
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })
	return accounts, nil
}

func (d *Driver) ListAccountsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListAccountsForCompany(companyID)
}

func (d *Driver) ListAccountsForCompanyAsOfContext(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil || asOf.IsZero() {
		return nil, masterdata.ErrCompanyScopeRequired
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	accounts := make([]masterdata.Account, 0, len(d.accounts))
	for _, account := range d.accounts {
		if account.CompanyID != companyID {
			continue
		}
		versions := d.versions[account.ID]
		if len(versions) > 0 {
			version, err := masterdata.AccountVersionAt(versions, asOf)
			if err != nil {
				return nil, err
			}
			account.Name = version.Name
			account.AccountType = version.AccountType
			account.IFRSCategory = version.IFRSCategory
		}
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })
	return accounts, nil
}

func (d *Driver) SaveCostCenter(ctx context.Context, center masterdata.CostCenter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := center.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, existing := range d.centers {
		if id != center.ID && existing.CompanyID == center.CompanyID && strings.EqualFold(existing.Code, center.Code) {
			return masterdata.ErrInvalidCostCenter
		}
	}
	if center.ParentID != nil {
		parent, ok := d.centers[*center.ParentID]
		if !ok || parent.CompanyID != center.CompanyID {
			return masterdata.ErrInvalidCostCenter
		}
		seen := map[uuid.UUID]struct{}{center.ID: {}}
		current := center.ParentID
		for current != nil {
			if _, found := seen[*current]; found {
				return masterdata.ErrCostCenterCycle
			}
			seen[*current] = struct{}{}
			ancestor, exists := d.centers[*current]
			if !exists || ancestor.CompanyID != center.CompanyID {
				return masterdata.ErrInvalidCostCenter
			}
			current = ancestor.ParentID
		}
	}
	d.centers[center.ID] = center
	return nil
}

func (d *Driver) RequiresCostCenter(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	account, ok := d.accounts[accountID]
	if !ok || account.CompanyID != companyID {
		return false, masterdata.ErrAccountNotFound
	}
	return account.RequiresCostCenter, nil
}

func (d *Driver) ValidateCostCenter(ctx context.Context, companyID, centerID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	center, ok := d.centers[centerID]
	if !ok || center.CompanyID != companyID || !center.Active {
		return masterdata.ErrInvalidCostCenter
	}
	return nil
}

func (d *Driver) SaveAnalyticalDimension(ctx context.Context, value masterdata.AnalyticalDimensionValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, existing := range d.dimensions {
		if key != analyticalDimensionKey(value.CompanyID, value.Kind, value.ID) &&
			existing.CompanyID == value.CompanyID && existing.Kind == value.Kind &&
			strings.EqualFold(existing.Code, value.Code) {
			return masterdata.ErrInvalidAnalyticalDimension
		}
	}
	d.dimensions[analyticalDimensionKey(value.CompanyID, value.Kind, value.ID)] = value
	return nil
}

func (d *Driver) ValidateAnalyticalDimension(
	ctx context.Context,
	companyID uuid.UUID,
	kind masterdata.AnalyticalDimensionKind,
	dimensionID uuid.UUID,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	value, ok := d.dimensions[analyticalDimensionKey(companyID, kind, dimensionID)]
	if !ok || !value.Active {
		return masterdata.ErrInvalidAnalyticalDimension
	}
	return nil
}

func analyticalDimensionKey(companyID uuid.UUID, kind masterdata.AnalyticalDimensionKind, id uuid.UUID) string {
	return companyID.String() + "\x00" + string(kind) + "\x00" + id.String()
}

func (d *Driver) SaveAccountVersion(ctx context.Context, version masterdata.AccountVersion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := masterdata.ValidateAccountVersions([]masterdata.AccountVersion{version}); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	account, ok := d.accounts[version.AccountID]
	if !ok || account.CompanyID != version.CompanyID {
		return masterdata.ErrAccountNotFound
	}
	versions := append([]masterdata.AccountVersion(nil), d.versions[version.AccountID]...)
	for _, current := range versions {
		if current.CompanyID != version.CompanyID {
			return masterdata.ErrInvalidEffectivePeriod
		}
	}
	versions = append(versions, version)
	if err := masterdata.ValidateAccountVersions(versions); err != nil {
		return err
	}
	d.versions[version.AccountID] = versions
	return nil
}

func (d *Driver) GetAccountVersionForCompanyAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (masterdata.AccountVersion, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.AccountVersion{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, version := range d.versions[accountID] {
		if version.CompanyID != companyID {
			return masterdata.AccountVersion{}, masterdata.ErrAccountVersionNotFound
		}
	}
	return masterdata.AccountVersionAt(d.versions[accountID], asOf)
}

func (d *Driver) HasChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if account.ParentID != nil && *account.ParentID == id {
			return true, nil
		}

	}
	return false, nil
}

func (d *Driver) CreateFiscalYear(ctx context.Context, year masterdata.FiscalYear) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if year.State == "" {
		year.State = masterdata.FiscalYearOpen
	}
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

func (d *Driver) UpdateFiscalYearState(ctx context.Context, companyID, id uuid.UUID, state masterdata.FiscalYearState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	year, ok := d.years[id]
	if !ok {
		return masterdata.ErrFiscalYearNotFound
	}
	if year.CompanyID != companyID {
		return masterdata.ErrFiscalYearNotFound
	}
	if err := masterdata.ValidateFiscalYearTransition(year.State, state); err != nil {
		return err
	}
	year.State = state
	d.years[id] = year
	return nil
}

func (d *Driver) UpdateFiscalYearStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.FiscalYearState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	year, err := d.GetFiscalYearForCompany(ctx, companyID, id)
	if err != nil {
		return err
	}
	if year.CompanyID != companyID {
		return masterdata.ErrFiscalYearNotFound
	}
	return d.UpdateFiscalYearState(ctx, companyID, id, state)
}

func (d *Driver) GetFiscalYear(ctx context.Context, id uuid.UUID) (masterdata.FiscalYear, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalYear{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	year, ok := d.years[id]
	if !ok {
		return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
	}
	return year, nil
}

func (d *Driver) GetFiscalYearForCompany(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	year, err := d.GetFiscalYear(ctx, fiscalYearID)
	if err != nil {
		return masterdata.FiscalYear{}, err
	}

	if year.CompanyID != companyID {
		return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
	}
	return year, nil
}

func (d *Driver) GetPeriodByDate(ctx context.Context, date time.Time) (masterdata.FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalPeriod{}, err
	}
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

func (d *Driver) GetPeriodByDateForCompany(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalPeriod{}, err
	}
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

func (d *Driver) GetPeriodByNumberForCompany(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalPeriod{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == fiscalYearID && period.Number == number {
			return period, nil
		}

	}
	return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
}

func (d *Driver) UpdatePeriodState(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	period, ok := d.periods[id]
	if !ok {
		return masterdata.ErrFiscalPeriodNotFound
	}
	if period.CompanyID != companyID {
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

func (d *Driver) UpdatePeriodStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.RLock()
	period, ok := d.periods[id]
	d.mu.RUnlock()
	if !ok || period.CompanyID != companyID {
		return masterdata.ErrFiscalPeriodNotFound
	}
	return d.UpdatePeriodState(ctx, companyID, id, state)
}

func (d *Driver) GetNextSequenceWithLock(ctx context.Context, companyID, id uuid.UUID) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if companyID == uuid.Nil || id == uuid.Nil {
		return 0, masterdata.ErrCompanyScopeRequired
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := companyID.String() + "\x00" + id.String()
	d.journalSeq[key]++
	return d.journalSeq[key], nil
}

func cloneEntry(entry posting.JournalEntry) posting.JournalEntry {
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	return entry
}

func cloneFixedAsset(asset subledgers.FixedAsset) subledgers.FixedAsset {
	asset.AppliedEntryIDs = append([]uuid.UUID(nil), asset.AppliedEntryIDs...)
	asset.Components = append([]subledgers.AssetComponent(nil), asset.Components...)
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

func (d *Driver) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return cloneEntry(entry), nil
}

func (d *Driver) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	return d.GetEntryByIDForCompany(ctx, companyID, entryID)
}

func (d *Driver) PostAtomic(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, exists := d.entries[entry.ID]
	if entry.Status == posting.EntryPosted {
		if !exists {
			return posting.ErrEntryNotFound
		}
		if current.Status != posting.EntryDraft {
			return posting.ErrEntryNotDraft
		}
		if !posting.DraftContentMatches(current, entry) {
			return financialtypes.ErrCannotModifyPostedMove
		}
		if err := d.updateAccountBalancesLocked(entry); err != nil {
			return err
		}
	}
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
	if err := d.updateAccountBalancesLocked(entry); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(entry)
	return cloneEntry(entry), nil
}

func (d *Driver) PostAndSequenceWithOpenLine(entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft || entry.Status != posting.EntryPosted {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	account, ok := d.accounts[attachment.ControlAccountID]
	if !ok || account.CompanyID != entry.CompanyID {
		return posting.JournalEntry{}, masterdata.ErrAccountNotFound
	}
	if _, exists := d.lines[attachment.ID]; exists {
		return posting.JournalEntry{}, subledgers.ErrAlreadyExists
	}
	partnerID := attachment.PartnerID
	line := subledgers.OpenLine{
		CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
		ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
		Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
		ResidualAmount: attachment.Amount, DocumentDate: attachment.DocumentDate,
		DueDate: attachment.DueDate, Reference: attachment.Reference,
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := d.updateAccountBalancesLocked(entry); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(entry)
	d.lines[line.ID] = line
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
	if err := d.updateAccountBalancesLocked(reversal); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(reversal.CompanyID, reversal.JournalID)
	d.journalSeq[key]++
	reversal.SequenceNumber = d.journalSeq[key]
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

func (d *Driver) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.SaveDraft(entry)
}

func (d *Driver) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.PostAtomic(entry)
}

func (d *Driver) PostAndSequenceContext(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostAndSequence(entry)
}

func (d *Driver) PostAndSequenceWithOpenLineContext(ctx context.Context, entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostAndSequenceWithOpenLine(entry, attachment)
}

func (d *Driver) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostReversalAtomic(original, reversal)
}

func (d *Driver) SaveOpenLineFromAttachment(ctx context.Context, attachment posting.OpenLineAttachment, entry posting.JournalEntry) error {
	account, ok := d.accounts[attachment.ControlAccountID]
	if !ok || account.CompanyID != entry.CompanyID {
		return masterdata.ErrAccountNotFound
	}
	if _, exists := d.lines[attachment.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	partnerID := attachment.PartnerID
	line := subledgers.OpenLine{
		CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
		ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
		Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
		ResidualAmount: attachment.Amount, DocumentDate: attachment.DocumentDate,
		DueDate: attachment.DueDate, Reference: attachment.Reference,
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	d.lines[line.ID] = line
	return nil
}

type memoryRow struct{}

func (r *memoryRow) Scan(dest ...any) error {
	return nil
}

type memoryTxAdapter struct {
	driver *Driver
}

func (a *memoryTxAdapter) Exec(ctx context.Context, sql string, args ...any) error {
	return posting.ErrUnsupportedMutationBackend
}

func (a *memoryTxAdapter) QueryRow(ctx context.Context, sql string, args ...any) posting.Row {
	return &memoryRow{}
}

func (a *memoryTxAdapter) CustomMutation(ctx context.Context, target string, fn func(state any) error) error {
	if target != "memory" {
		return posting.ErrUnsupportedMutationBackend
	}
	return fn(a.driver)
}

func (d *Driver) PostWithAttachments(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) error {
	return d.PostWithAttachmentsContext(ctx, entry, attachments)
}

func (d *Driver) PostWithAttachmentsContext(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft || entry.Status != posting.EntryPosted {
		return posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, *entry) {
		return financialtypes.ErrCannotModifyPostedMove
	}

	memAdapter := &memoryTxAdapter{driver: d}
	for _, att := range attachments {
		if att != nil {
			if err := att.ExecuteAtomicMutation(ctx, memAdapter, *entry); err != nil {
				return err
			}
		}
	}

	if err := d.updateAccountBalancesLocked(*entry); err != nil {
		return err
	}

	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(*entry)
	return nil
}

func (d *Driver) GetNextSequence(id uuid.UUID) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := sequenceKey(uuid.Nil, id)
	d.journalSeq[key]++
	return d.journalSeq[key], nil
}

func (d *Driver) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if companyID == uuid.Nil || journalID == uuid.Nil {
		return 0, masterdata.ErrCompanyScopeRequired
	}
	return d.GetNextSequenceWithLock(ctx, companyID, journalID)
}

func sequenceKey(companyID, journalID uuid.UUID) string {
	return companyID.String() + "\x00" + journalID.String()
}

func (d *Driver) PostBulk(ctx context.Context, entries []posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pending := make(map[accountBalanceKey]accountBalance)
	adjustingPeriods := make(map[companyPeriodKey]bool)
	seenEntries := make(map[uuid.UUID]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := seenEntries[entry.ID]; duplicate {
			return posting.ErrEntryNotDraft
		}
		seenEntries[entry.ID] = struct{}{}
		current, ok := d.entries[entry.ID]
		if !ok {
			return posting.ErrEntryNotFound
		}
		if current.Status != posting.EntryDraft {
			return posting.ErrEntryNotDraft
		}
		if !posting.DraftContentMatches(current, entry) {
			return financialtypes.ErrCannotModifyPostedMove
		}
		deltas, err := d.accountBalanceDeltasLocked(entry)
		if err != nil {
			return err
		}
		for key, delta := range deltas {
			aggregate := pending[key]
			aggregate.periodDebit = aggregate.periodDebit.Add(delta.periodDebit)
			aggregate.periodCredit = aggregate.periodCredit.Add(delta.periodCredit)
			pending[key] = aggregate
		}
		if entry.Classification == posting.EntryClassAdjusting && entry.CompanyID != uuid.Nil {
			adjustingPeriods[companyPeriodKey{companyID: entry.CompanyID, periodID: entry.PeriodID}] = true
		}
	}
	for key, delta := range pending {
		balance := d.balances[key]
		balance.periodDebit = balance.periodDebit.Add(delta.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(delta.periodCredit)
		d.balances[key] = balance
	}
	for key := range adjustingPeriods {
		d.adjustingPeriods[key] = true
	}
	for _, entry := range entries {
		d.entries[entry.ID] = cloneEntry(entry)
	}
	return nil
}

func (d *Driver) updateAccountBalancesLocked(entry posting.JournalEntry) error {
	deltas, err := d.accountBalanceDeltasLocked(entry)
	if err != nil {
		return err
	}
	for key, delta := range deltas {
		balance := d.balances[key]
		balance.periodDebit = balance.periodDebit.Add(delta.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(delta.periodCredit)
		d.balances[key] = balance
	}
	if entry.Classification == posting.EntryClassAdjusting && entry.CompanyID != uuid.Nil {
		d.adjustingPeriods[companyPeriodKey{companyID: entry.CompanyID, periodID: entry.PeriodID}] = true
	}
	return nil
}

func (d *Driver) accountBalanceDeltasLocked(entry posting.JournalEntry) (map[accountBalanceKey]accountBalance, error) {
	if entry.Status != posting.EntryPosted {
		return nil, posting.ErrInvalidEntryState
	}
	if entry.CompanyID == uuid.Nil {
		return map[accountBalanceKey]accountBalance{}, nil
	}
	if entry.PeriodID == uuid.Nil {
		return nil, masterdata.ErrFiscalPeriodNotFound
	}
	period, ok := d.periods[entry.PeriodID]
	if !ok || period.CompanyID != entry.CompanyID {
		return nil, masterdata.ErrFiscalPeriodNotFound
	}
	company, ok := d.companies[entry.CompanyID]
	if !ok {
		return nil, masterdata.ErrInvalidCompany
	}
	deltas := make(map[accountBalanceKey]accountBalance, len(entry.Lines))
	for _, line := range entry.Lines {
		account, ok := d.accounts[line.AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return nil, masterdata.ErrAccountNotFound
		}
		debit, credit, currency := posting.LineFunctionalAmounts(line)
		if currency != "" && currency != company.FunctionalCurrency {
			return nil, posting.ErrFunctionalCurrencyMismatch
		}
		key := accountBalanceKey{
			companyID: entry.CompanyID,
			accountID: line.AccountID,
			periodID:  entry.PeriodID,
			currency:  company.FunctionalCurrency,
		}
		delta := deltas[key]
		delta.periodDebit = delta.periodDebit.Add(debit)
		delta.periodCredit = delta.periodCredit.Add(credit)
		deltas[key] = delta
	}
	return deltas, nil
}

func (d *Driver) CarryForwardBalancesForCompanyContext(
	ctx context.Context,
	companyID, closedYearID, newPeriodID uuid.UUID,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	closedYear, ok := d.years[closedYearID]
	if !ok || closedYear.CompanyID != companyID {
		return masterdata.ErrFiscalYearNotFound
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		return masterdata.ErrInvalidFiscalYearTransition
	}
	destination, ok := d.periods[newPeriodID]
	if !ok || destination.CompanyID != companyID || destination.Number != 1 {
		return masterdata.ErrFiscalPeriodNotFound
	}
	newYear, ok := d.years[destination.FiscalYearID]
	if !ok || newYear.CompanyID != companyID || !newYear.StartDate.After(closedYear.EndDate) {
		return masterdata.ErrFiscalPeriodNotFound
	}
	var lastPeriod masterdata.FiscalPeriod
	foundLastPeriod := false
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == closedYearID &&
			(!foundLastPeriod || period.Number > lastPeriod.Number) {
			lastPeriod = period
			foundLastPeriod = true
		}
	}
	if !foundLastPeriod {
		return masterdata.ErrFiscalPeriodNotFound
	}
	type balanceKey struct {
		accountID uuid.UUID
		currency  string
	}
	sourceBalances := make(map[balanceKey]accountBalance)
	periodNumbers := make(map[uuid.UUID]int, len(d.periods))
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == closedYearID {
			periodNumbers[period.ID] = period.Number
		}
	}
	for key, source := range d.balances {
		periodNumber, inClosedYear := periodNumbers[key.periodID]
		if key.companyID != companyID || !inClosedYear || periodNumber > lastPeriod.Number {
			continue
		}
		account, ok := d.accounts[key.accountID]
		if !ok {
			return masterdata.ErrAccountNotFound
		}
		switch account.AccountType {
		case masterdata.AccountTypeAsset, masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
		default:
			continue
		}
		sourceKey := balanceKey{accountID: key.accountID, currency: key.currency}
		balance := sourceBalances[sourceKey]
		balance.beginningDebit = balance.beginningDebit.Add(source.beginningDebit)
		balance.beginningCredit = balance.beginningCredit.Add(source.beginningCredit)
		balance.periodDebit = balance.periodDebit.Add(source.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(source.periodCredit)
		sourceBalances[sourceKey] = balance
	}
	for sourceKey, source := range sourceBalances {
		destinationKey := accountBalanceKey{
			companyID: companyID,
			accountID: sourceKey.accountID,
			periodID:  newPeriodID,
			currency:  sourceKey.currency,
		}
		balance := d.balances[destinationKey]
		balance.beginningDebit = source.beginningDebit.Add(source.periodDebit)
		balance.beginningCredit = source.beginningCredit.Add(source.periodCredit)
		d.balances[destinationKey] = balance
	}
	return nil
}

func (d *Driver) ListPostedEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListEntries(ctx, start, end)
}

func (d *Driver) ListEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (d *Driver) ListEntriesForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListEntriesForCompany(companyID, start, end)
}

func (d *Driver) ReadAccountPeriodBalancesForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if companyID == uuid.Nil || end.IsZero() || (!start.IsZero() && end.Before(start)) {
		return nil, false, nil
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	company, ok := d.companies[companyID]
	if !ok {
		return nil, false, nil
	}
	if company.FunctionalCurrency != currency {
		return nil, false, nil
	}
	var year masterdata.FiscalYear
	foundYear := false
	for _, candidate := range d.years {
		if candidate.CompanyID == companyID &&
			!end.Before(candidate.StartDate) && !end.After(candidate.EndDate) {
			year, foundYear = candidate, true
			break
		}
	}
	if !foundYear {
		return nil, false, nil
	}
	if !start.IsZero() && start.Before(year.StartDate) {
		return nil, false, nil
	}

	periods := append([]masterdata.FiscalPeriod(nil), year.Periods...)
	if len(periods) == 0 {
		for _, period := range d.periods {
			if period.CompanyID == companyID && period.FiscalYearID == year.ID {
				periods = append(periods, period)
			}
		}
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].StartDate.Equal(periods[j].StartDate) {
			return periods[i].Number < periods[j].Number
		}
		return periods[i].StartDate.Before(periods[j].StartDate)
	})
	if len(periods) == 0 {
		return nil, false, nil
	}
	periodIndexes := make(map[uuid.UUID]int, len(periods))
	for i, period := range periods {
		periodIndexes[period.ID] = i
	}
	rangeStart := start
	if rangeStart.IsZero() {
		rangeStart = year.StartDate
	}
	first, last := -1, -1
	for i, period := range periods {
		if period.StartDate.Before(rangeStart) && !period.EndDate.Before(rangeStart) {
			return nil, false, nil
		}
		if !period.StartDate.After(end) && period.EndDate.After(end) {
			return nil, false, nil
		}
		if !period.StartDate.Before(rangeStart) && !period.EndDate.After(end) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 || last == -1 {
		return nil, false, nil
	}
	if !includeAdjusting {
		for i := 0; i <= last; i++ {
			if d.adjustingPeriods[companyPeriodKey{companyID: companyID, periodID: periods[i].ID}] {
				return nil, false, nil
			}
		}
	}

	balances := make(map[uuid.UUID]posting.AccountPeriodBalance, len(d.balances))
	for key, stored := range d.balances {
		if key.companyID != companyID || key.currency != currency {
			continue
		}
		periodIndex, ok := periodIndexes[key.periodID]
		if !ok || periodIndex > last {
			continue
		}
		balance := balances[key.accountID]
		balance.AccountID = key.accountID
		if !stored.beginningDebit.IsZero() {
			balance.BeginningDebit = balance.BeginningDebit.Add(stored.beginningDebit)
		}
		if !stored.beginningCredit.IsZero() {
			balance.BeginningCredit = balance.BeginningCredit.Add(stored.beginningCredit)
		}
		if periodIndex < first {
			if !stored.periodDebit.IsZero() {
				balance.BeginningDebit = balance.BeginningDebit.Add(stored.periodDebit)
			}
			if !stored.periodCredit.IsZero() {
				balance.BeginningCredit = balance.BeginningCredit.Add(stored.periodCredit)
			}
		} else {
			if balance.PeriodDebit.IsZero() {
				balance.PeriodDebit = stored.periodDebit
			} else if !stored.periodDebit.IsZero() {
				balance.PeriodDebit = balance.PeriodDebit.Add(stored.periodDebit)
			}
			if balance.PeriodCredit.IsZero() {
				balance.PeriodCredit = stored.periodCredit
			} else if !stored.periodCredit.IsZero() {
				balance.PeriodCredit = balance.PeriodCredit.Add(stored.periodCredit)
			}
		}
		balance.EndingDebit = balance.BeginningDebit
		if !balance.PeriodDebit.IsZero() {
			if balance.EndingDebit.IsZero() {
				balance.EndingDebit = balance.PeriodDebit
			} else {
				balance.EndingDebit = balance.EndingDebit.Add(balance.PeriodDebit)
			}
		}
		balance.EndingCredit = balance.BeginningCredit
		if !balance.PeriodCredit.IsZero() {
			if balance.EndingCredit.IsZero() {
				balance.EndingCredit = balance.PeriodCredit
			} else {
				balance.EndingCredit = balance.EndingCredit.Add(balance.PeriodCredit)
			}
		}
		balances[key.accountID] = balance
	}
	return balances, true, nil
}

func (d *Driver) GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	d.mu.RLock()
	account, exists := d.accounts[accountID]
	company, companyExists := d.companies[companyID]
	entries := make([]posting.JournalEntry, 0)
	for _, entry := range d.entries {
		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted {
			entries = append(entries, cloneEntry(entry))
		}
	}
	d.mu.RUnlock()
	if !exists || account.CompanyID != companyID {
		return financialtypes.Money{}, masterdata.ErrAccountNotFound
	}
	if !companyExists {
		return financialtypes.Money{}, masterdata.ErrInvalidCompany
	}
	return subledgers.ControlAccountBalanceFromEntries(account, entries, company.FunctionalCurrency)
}

func (d *Driver) SaveEvent(ctx context.Context, event subledgers.EconomicEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) SaveEventAccountingRule(ctx context.Context, rule subledgers.EventAccountingRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	key := rule.CompanyID.String() + "\x00" + rule.EventType
	rules := d.eventRules[key]
	for i := range rules {
		if rules[i].Priority == rule.Priority {
			rules[i] = rule.Clone()
			d.eventRules[key] = rules
			return nil
		}
	}
	d.eventRules[key] = append(rules, rule.Clone())
	sort.Slice(d.eventRules[key], func(i, j int) bool {
		return d.eventRules[key][i].Priority > d.eventRules[key][j].Priority
	})
	return nil
}

func (d *Driver) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	rules := d.eventRules[companyID.String()+"\x00"+eventType]
	result := make([]subledgers.EventAccountingRule, len(rules))
	for i := range rules {
		result[i] = rules[i].Clone()
	}
	return result, nil
}

func (d *Driver) SaveEventAndDraft(ctx context.Context, event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetEventByIdempotencyKey(ctx context.Context, key string) (subledgers.EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
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

func (d *Driver) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	event, ok := d.events[companyID.String()+"\x00"+key]
	return event, ok, nil
}
func (d *Driver) SaveOpenLine(ctx context.Context, line subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.lines[line.ID] = line
	return nil
}

func (d *Driver) SaveOpenLineForPostedEntry(ctx context.Context, line subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	account, accountExists := d.accounts[line.ControlAccountID]
	entry, entryExists := d.entries[line.JournalEntryID]
	if !accountExists {
		return masterdata.ErrAccountNotFound
	}
	if !entryExists {
		return posting.ErrEntryNotFound
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	if existing, exists := d.lines[line.ID]; exists && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	if _, exists := d.lines[line.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	d.lines[line.ID] = line
	return nil
}

func (d *Driver) SaveOpenLines(ctx context.Context, lines []subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) CompareAndSaveOpenLines(ctx context.Context, expected, updated []subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
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

func (d *Driver) CompareAndSaveOpenLinesAndDraft(ctx context.Context, expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetOpenLine(ctx context.Context, companyID, id uuid.UUID) (subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.OpenLine{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	line, ok := d.lines[id]
	if !ok || line.CompanyID != companyID {
		return subledgers.OpenLine{}, subledgers.ErrNotFound
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	return line, nil
}

func (d *Driver) ConfirmOpenLineWriteOff(ctx context.Context, expected, updated subledgers.OpenLine, postedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || postedEntry.CompanyID != expected.CompanyID ||
		postedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidOpenLine
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.lines[expected.ID]
	if !ok {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentMatch
	}
	actual, ok := d.entries[postedEntry.ID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	if !subledgers.FixedAssetEntryMatches(actual, postedEntry) {
		return posting.ErrEntryNotPosted
	}
	updated.MatchIDs = append([]uuid.UUID(nil), updated.MatchIDs...)
	updated.Matches = append([]subledgers.OpenLineMatch(nil), updated.Matches...)
	d.lines[updated.ID] = updated
	return nil
}

func (d *Driver) ListOpenLines(ctx context.Context, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (d *Driver) ListOpenLinesForCompany(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
func (d *Driver) SaveFixedAsset(ctx context.Context, asset subledgers.FixedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (d *Driver) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListOpenLinesForCompany(ctx, companyID, partnerID)
}

func (d *Driver) SaveFixedAssetDraft(ctx context.Context, expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:DEPRECIATION:") ||
		strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:REVALUATION:") {
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

func (d *Driver) ConfirmFixedAssetMutation(ctx context.Context, expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetFixedAsset(ctx context.Context, companyID, id uuid.UUID) (subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.FixedAsset{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	asset, ok := d.assets[id]
	if !ok || asset.CompanyID != companyID {
		return subledgers.FixedAsset{}, subledgers.ErrNotFound
	}
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset, nil
}

func (d *Driver) ListFixedAssets(ctx context.Context) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (d *Driver) ListFixedAssetsForCompany(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
func (d *Driver) SaveBankItem(ctx context.Context, item subledgers.BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
func (d *Driver) ListBankItems(ctx context.Context, accountID string) ([]subledgers.BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]subledgers.BankItem(nil), d.bank[accountID]...), nil
}

func (d *Driver) ListFixedAssetsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListFixedAssetsForCompany(ctx, companyID)
}

func (d *Driver) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (d *Driver) CompareAndSaveBankItems(ctx context.Context, expected, updated []subledgers.BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) CompareAndSaveBankItemsAndDraft(ctx context.Context, expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (d *Driver) GetRateForCompany(ctx context.Context, companyID uuid.UUID, from, to string, date time.Time) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	return d.GetRate(ctx, from, to, date)
}

func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.rates.GetRateOfType(ctx, rateType, from, to, date)
}

func (d *Driver) GetRateOfTypeForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	return d.GetRateOfType(ctx, rateType, from, to, date)
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

func (d *Driver) HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, account := range d.accounts {
		if account.CompanyID == companyID && account.ParentID != nil && *account.ParentID == accountID {
			return true, nil
		}
	}
	return false, nil
}

func (d *Driver) GetFiscalYearForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalYear{}, err
	}
	return d.GetFiscalYearForCompany(ctx, companyID, fiscalYearID)
}

func (d *Driver) IsFirstFiscalYearForCompanyContext(
	ctx context.Context,
	companyID, fiscalYearID uuid.UUID,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	year, ok := d.years[fiscalYearID]
	if !ok || year.CompanyID != companyID {
		return false, masterdata.ErrFiscalYearNotFound
	}
	for id, candidate := range d.years {
		if id != fiscalYearID && candidate.CompanyID == companyID &&
			(candidate.StartDate.Before(year.StartDate) ||
				(candidate.StartDate.Equal(year.StartDate) && id.String() < fiscalYearID.String())) {
			return false, nil
		}
	}
	return true, nil
}

func (d *Driver) GetPeriodByDateForCompanyContext(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalPeriod{}, err
	}
	return d.GetPeriodByDateForCompany(ctx, companyID, date)
}

func (d *Driver) GetPeriodByNumberForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalPeriod{}, err
	}
	return d.GetPeriodByNumberForCompany(ctx, companyID, fiscalYearID, number)
}

func (d *Driver) SaveContract(ctx context.Context, companyID uuid.UUID, contract ifrs15.Contract) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil {
		return errors.New("company id is required to save revenue contract")
	}
	if err := contract.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.contracts[companyID] == nil {
		d.contracts[companyID] = make(map[uuid.UUID]ifrs15.Contract)
	}
	d.contracts[companyID][contract.ID] = contract
	return nil
}

func (d *Driver) ListContracts(ctx context.Context, companyID uuid.UUID) ([]ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil {
		return nil, errors.New("company id is required to list revenue contracts")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	store, ok := d.contracts[companyID]
	if !ok {
		return []ifrs15.Contract{}, nil
	}
	results := make([]ifrs15.Contract, 0, len(store))
	for _, c := range store {
		results = append(results, c)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].ID.String() < results[j].ID.String()
	})
	return results, nil
}

func (d *Driver) GetContract(ctx context.Context, companyID, id uuid.UUID) (ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return ifrs15.Contract{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if store, ok := d.contracts[companyID]; ok {
		if c, found := store[id]; found {
			return c, nil
		}
	}
	return ifrs15.Contract{}, errors.New("revenue contract not found")
}

func (d *Driver) DeleteContract(ctx context.Context, companyID, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if store, ok := d.contracts[companyID]; ok {
		if _, found := store[id]; found {
			delete(store, id)
			return nil
		}
	}
	return errors.New("revenue contract not found")
}
