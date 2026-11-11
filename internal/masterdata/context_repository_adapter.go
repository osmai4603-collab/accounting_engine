package masterdata

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type contextRepositoryAdapter struct {
	repository ContextStorageRepository
}

// NewRepositoryAdapter bridges contextless service methods while context-aware methods delegate directly.
func NewRepositoryAdapter(repository ContextStorageRepository) Repository {
	return contextRepositoryAdapter{repository: repository}
}

func (r contextRepositoryAdapter) CreateCompany(company Company) error {
	return r.repository.CreateCompany(context.Background(), company)
}

func (r contextRepositoryAdapter) GetCompanyByID(companyID uuid.UUID) (Company, error) {
	return r.repository.GetCompanyByIDContext(context.Background(), companyID)
}

func (r contextRepositoryAdapter) CreateAccount(account Account) error {
	return r.repository.CreateAccount(context.Background(), account)
}

func (r contextRepositoryAdapter) CreateAccountContext(ctx context.Context, account Account) error {
	return r.repository.CreateAccount(ctx, account)
}

func (r contextRepositoryAdapter) GetAccountByID(id uuid.UUID) (Account, error) {
	return r.repository.GetAccountByID(context.Background(), id)
}

func (r contextRepositoryAdapter) GetAccountByCode(code string) (Account, error) {
	return r.repository.GetAccountByCode(context.Background(), code)
}

func (r contextRepositoryAdapter) ListAccounts() ([]Account, error) {
	return r.repository.ListAccounts(context.Background())
}

func (r contextRepositoryAdapter) HasChildren(id uuid.UUID) (bool, error) {
	return r.repository.HasChildren(context.Background(), id)
}

func (r contextRepositoryAdapter) CreateFiscalYear(year FiscalYear) error {
	return r.repository.CreateFiscalYear(context.Background(), year)
}

func (r contextRepositoryAdapter) GetFiscalYear(id uuid.UUID) (FiscalYear, error) {
	return r.repository.GetFiscalYear(context.Background(), id)
}

func (r contextRepositoryAdapter) UpdateFiscalYearState(id uuid.UUID, state FiscalYearState) error {
	year, err := r.repository.GetFiscalYear(context.Background(), id)
	if err != nil {
		return err
	}
	if year.CompanyID == uuid.Nil {
		return ErrCompanyScopeRequired
	}
	return r.repository.UpdateFiscalYearState(context.Background(), year.CompanyID, id, state)
}

func (r contextRepositoryAdapter) GetPeriodByDate(date time.Time) (FiscalPeriod, error) {
	return r.repository.GetPeriodByDate(context.Background(), date)
}

func (r contextRepositoryAdapter) UpdatePeriodState(uuid.UUID, PeriodState) error {
	return ErrCompanyScopeRequired
}

func (r contextRepositoryAdapter) GetNextSequenceWithLock(uuid.UUID) (int64, error) {
	return 0, ErrCompanyScopeRequired
}

func (r contextRepositoryAdapter) GetAccountByIDForCompany(companyID, accountID uuid.UUID) (Account, error) {
	return r.repository.GetAccountByIDForCompany(context.Background(), companyID, accountID)
}

func (r contextRepositoryAdapter) GetAccountByCodeForCompany(companyID uuid.UUID, code string) (Account, error) {
	return r.repository.GetAccountByCodeForCompany(context.Background(), companyID, code)
}

func (r contextRepositoryAdapter) GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (FiscalYear, error) {
	return r.repository.GetFiscalYearForCompany(context.Background(), companyID, fiscalYearID)
}

func (r contextRepositoryAdapter) GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (FiscalPeriod, error) {
	return r.repository.GetPeriodByDateForCompany(context.Background(), companyID, date)
}

func (r contextRepositoryAdapter) GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error) {
	return r.repository.GetPeriodByNumberForCompany(context.Background(), companyID, fiscalYearID, number)
}

func (r contextRepositoryAdapter) GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (Company, error) {
	return r.repository.GetCompanyByIDContext(ctx, companyID)
}

func (r contextRepositoryAdapter) GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (Account, error) {
	return r.repository.GetAccountByIDForCompanyContext(ctx, companyID, accountID)
}

func (r contextRepositoryAdapter) GetFiscalYearForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID) (FiscalYear, error) {
	return r.repository.GetFiscalYearForCompanyContext(ctx, companyID, fiscalYearID)
}

func (r contextRepositoryAdapter) GetPeriodByDateForCompanyContext(ctx context.Context, companyID uuid.UUID, date time.Time) (FiscalPeriod, error) {
	return r.repository.GetPeriodByDateForCompany(ctx, companyID, date)
}

func (r contextRepositoryAdapter) GetPeriodByNumberForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error) {
	return r.repository.GetPeriodByNumberForCompany(ctx, companyID, fiscalYearID, number)
}

func (r contextRepositoryAdapter) HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	return r.repository.HasChildrenForCompanyContext(ctx, companyID, accountID)
}

var (
	_ Repository                     = contextRepositoryAdapter{}
	_ CompanyScopedRepository        = contextRepositoryAdapter{}
	_ ContextCompanyScopedRepository = contextRepositoryAdapter{}
	_ CompanyRepository              = contextRepositoryAdapter{}
)
