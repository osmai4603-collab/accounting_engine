package masterdata

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MockRepository struct {
	mu        sync.Mutex
	companies map[uuid.UUID]Company
	accounts  map[uuid.UUID]Account
	years     map[uuid.UUID]FiscalYear
	periods   map[uuid.UUID]FiscalPeriod
	sequences map[uuid.UUID]int64
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		companies: make(map[uuid.UUID]Company),
		accounts:  make(map[uuid.UUID]Account),
		years:     make(map[uuid.UUID]FiscalYear),
		periods:   make(map[uuid.UUID]FiscalPeriod),
		sequences: make(map[uuid.UUID]int64),
	}
}

func (r *MockRepository) CreateCompany(company Company) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := company.Validate(); err != nil {
		return err
	}
	for id, existing := range r.companies {
		if id != company.ID && strings.EqualFold(existing.Code, company.Code) {
			return ErrInvalidCompany
		}
	}
	r.companies[company.ID] = company
	return nil
}

func (r *MockRepository) GetCompanyByID(id uuid.UUID) (Company, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	company, ok := r.companies[id]
	if !ok {
		return Company{}, ErrInvalidCompany
	}
	return company, nil
}

func (r *MockRepository) CreateAccount(account Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	code := strings.ToUpper(strings.TrimSpace(account.Code))
	for id, existing := range r.accounts {
		if id != account.ID && existing.CompanyID == account.CompanyID && strings.EqualFold(existing.Code, code) {
			return ErrDuplicateAccountCode
		}
	}
	r.accounts[account.ID] = account
	return nil
}

func (r *MockRepository) GetAccountByID(id uuid.UUID) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, ok := r.accounts[id]
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	return account, nil
}

func (r *MockRepository) GetAccountByIDForCompany(companyID, accountID uuid.UUID) (Account, error) {
	account, err := r.GetAccountByID(accountID)
	if err != nil {
		return Account{}, err
	}
	if account.CompanyID != companyID {
		return Account{}, ErrAccountNotFound
	}
	return account, nil
}

func (r *MockRepository) GetAccountByCode(code string) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, account := range r.accounts {
		if strings.EqualFold(account.Code, strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return Account{}, ErrAccountNotFound
}

func (r *MockRepository) GetAccountByCodeForCompany(companyID uuid.UUID, code string) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, account := range r.accounts {
		if account.CompanyID == companyID && strings.EqualFold(account.Code, strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return Account{}, ErrAccountNotFound
}

func (r *MockRepository) ListAccounts() ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (r *MockRepository) HasChildren(id uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, account := range r.accounts {
		if account.ParentID != nil && *account.ParentID == id {
			return true, nil
		}
	}
	return false, nil
}

func (r *MockRepository) CreateFiscalYear(year FiscalYear) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range year.Periods {
		if year.Periods[i].CompanyID == uuid.Nil {
			year.Periods[i].CompanyID = year.CompanyID
		}
	}
	r.years[year.ID] = year
	for _, period := range year.Periods {
		r.periods[period.ID] = period
	}
	return nil
}

func (r *MockRepository) GetFiscalYear(id uuid.UUID) (FiscalYear, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	year, ok := r.years[id]
	if !ok {
		return FiscalYear{}, ErrFiscalYearNotFound
	}
	return year, nil
}

func (r *MockRepository) GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (FiscalYear, error) {
	year, err := r.GetFiscalYear(fiscalYearID)
	if err != nil {
		return FiscalYear{}, err
	}
	if year.CompanyID != companyID {
		return FiscalYear{}, ErrFiscalYearNotFound
	}
	return year, nil
}

func (r *MockRepository) GetPeriodByDate(date time.Time) (FiscalPeriod, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	date = date.UTC()
	var adjustment *FiscalPeriod
	for _, period := range r.periods {
		if period.Contains(date) {
			if period.Type == FiscalPeriodTypeNormal {
				return period, nil
			}
			candidate := period
			if adjustment == nil || candidate.Number < adjustment.Number {
				adjustment = &candidate
			}
		}
	}
	if adjustment != nil {
		return *adjustment, nil
	}
	return FiscalPeriod{}, ErrFiscalPeriodNotFound
}

func (r *MockRepository) GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (FiscalPeriod, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	date = date.UTC()
	var adjustment *FiscalPeriod
	for _, period := range r.periods {
		if period.CompanyID != companyID || !period.Contains(date) {
			continue
		}
		if period.Type == FiscalPeriodTypeNormal {
			return period, nil
		}
		candidate := period
		if adjustment == nil || candidate.Number < adjustment.Number {
			adjustment = &candidate
		}
	}
	if adjustment != nil {
		return *adjustment, nil
	}
	return FiscalPeriod{}, ErrFiscalPeriodNotFound
}

func (r *MockRepository) GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, period := range r.periods {
		if period.CompanyID == companyID && period.FiscalYearID == fiscalYearID && period.Number == number {
			return period, nil
		}
	}
	return FiscalPeriod{}, ErrFiscalPeriodNotFound
}

func (r *MockRepository) UpdatePeriodState(id uuid.UUID, state PeriodState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	period, ok := r.periods[id]
	if !ok {
		return ErrFiscalPeriodNotFound
	}
	if err := ValidatePeriodTransition(period.State, state); err != nil {
		return err
	}
	period.State = state
	r.periods[id] = period
	for yearID, year := range r.years {
		for i := range year.Periods {
			if year.Periods[i].ID == id {
				year.Periods[i] = period
				r.years[yearID] = year
			}
		}
	}
	return nil
}

func (r *MockRepository) GetNextSequenceWithLock(journalID uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequences[journalID]++
	return r.sequences[journalID], nil
}
