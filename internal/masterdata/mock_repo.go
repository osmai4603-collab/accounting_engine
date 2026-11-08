package masterdata

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MockRepository struct {
	mu        sync.Mutex
	accounts  map[uuid.UUID]Account
	years     map[uuid.UUID]FiscalYear
	periods   map[uuid.UUID]FiscalPeriod
	sequences map[uuid.UUID]int64
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		accounts:  make(map[uuid.UUID]Account),
		years:     make(map[uuid.UUID]FiscalYear),
		periods:   make(map[uuid.UUID]FiscalPeriod),
		sequences: make(map[uuid.UUID]int64),
	}
}

func (r *MockRepository) CreateAccount(account Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	code := strings.ToUpper(strings.TrimSpace(account.Code))
	for id, existing := range r.accounts {
		if id != account.ID && strings.EqualFold(existing.Code, code) {
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
	r.years[year.ID] = year
	for _, period := range year.Periods {
		r.periods[period.ID] = period
	}
	return nil
}

func (r *MockRepository) GetPeriodByDate(date time.Time) (FiscalPeriod, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	date = date.UTC()
	for _, period := range r.periods {
		if period.Contains(date) {
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
