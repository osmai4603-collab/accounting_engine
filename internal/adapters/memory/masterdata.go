package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"

	"github.com/google/uuid"
)

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

func (d *Driver) GetJournalByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Journal, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Journal{}, err
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return masterdata.Journal{}, masterdata.ErrInvalidJournalType
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, journal := range d.journals {
		if journal.CompanyID == companyID && strings.EqualFold(journal.Code, trimmed) {
			return masterdata.Journal{CompanyID: journal.CompanyID, ID: journal.ID, Code: journal.Code, Name: journal.Name}, nil
		}
	}
	return masterdata.Journal{}, masterdata.ErrJournalNotFound
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
