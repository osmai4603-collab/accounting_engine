package masterdata

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type contextRepositoryAdapter struct {
	repository ContextStorageRepository
}

// NewRepositoryAdapter bridges legacy contextless service methods. Service
// operations with context use the corresponding context-forwarding methods.
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

func (r contextRepositoryAdapter) GetAccountByIDContext(ctx context.Context, id uuid.UUID) (Account, error) {
	return r.repository.GetAccountByID(ctx, id)
}

func (r contextRepositoryAdapter) GetAccountByCode(code string) (Account, error) {
	return r.repository.GetAccountByCode(context.Background(), code)
}

func (r contextRepositoryAdapter) GetAccountByCodeContext(ctx context.Context, code string) (Account, error) {
	return r.repository.GetAccountByCode(ctx, code)
}

func (r contextRepositoryAdapter) ListAccounts() ([]Account, error) {
	return r.repository.ListAccounts(context.Background())
}

func (r contextRepositoryAdapter) HasChildren(id uuid.UUID) (bool, error) {
	return r.repository.HasChildren(context.Background(), id)
}

func (r contextRepositoryAdapter) HasChildrenContext(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.repository.HasChildren(ctx, id)
}

func (r contextRepositoryAdapter) CreateFiscalYear(year FiscalYear) error {
	return r.repository.CreateFiscalYear(context.Background(), year)
}

func (r contextRepositoryAdapter) GetFiscalYear(id uuid.UUID) (FiscalYear, error) {
	return r.repository.GetFiscalYear(context.Background(), id)
}

func (r contextRepositoryAdapter) GetFiscalYearContext(ctx context.Context, id uuid.UUID) (FiscalYear, error) {
	return r.repository.GetFiscalYear(ctx, id)
}

func (r contextRepositoryAdapter) UpdateFiscalYearState(id uuid.UUID, state FiscalYearState) error {
	return r.UpdateFiscalYearStateContext(context.Background(), id, state)
}

func (r contextRepositoryAdapter) UpdateFiscalYearStateContext(ctx context.Context, id uuid.UUID, state FiscalYearState) error {
	year, err := r.repository.GetFiscalYear(ctx, id)
	if err != nil {
		return err
	}
	if year.CompanyID == uuid.Nil {
		return ErrCompanyScopeRequired
	}
	return r.repository.UpdateFiscalYearState(ctx, year.CompanyID, id, state)
}

func (r contextRepositoryAdapter) GetPeriodByDate(date time.Time) (FiscalPeriod, error) {
	return r.repository.GetPeriodByDate(context.Background(), date)
}

func (r contextRepositoryAdapter) GetPeriodByDateContext(ctx context.Context, date time.Time) (FiscalPeriod, error) {
	return r.repository.GetPeriodByDate(ctx, date)
}

func (r contextRepositoryAdapter) UpdatePeriodState(uuid.UUID, PeriodState) error {
	return ErrCompanyScopeRequired
}

func (r contextRepositoryAdapter) TransitionPeriodStateContext(
	ctx context.Context,
	companyID, periodID uuid.UUID,
	state PeriodState,
	actorID uuid.UUID,
	reason string,
) error {
	repository, ok := r.repository.(PeriodLifecycleRepository)
	if !ok {
		return ErrCompanyScopeRequired
	}
	return repository.TransitionPeriodStateContext(ctx, companyID, periodID, state, actorID, reason)
}

func (r contextRepositoryAdapter) ReopenPeriodContext(
	ctx context.Context,
	companyID, periodID, actorID uuid.UUID,
	reason string,
) error {
	repository, ok := r.repository.(PeriodLifecycleRepository)
	if !ok {
		return ErrCompanyScopeRequired
	}
	return repository.ReopenPeriodContext(ctx, companyID, periodID, actorID, reason)
}

func (r contextRepositoryAdapter) GetNextSequenceWithLock(uuid.UUID) (int64, error) {
	return 0, ErrCompanyScopeRequired
}

func (r contextRepositoryAdapter) GetNextSequenceWithLockContext(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.repository.GetNextSequenceWithLock(ctx, uuid.Nil, id)
}

func (r contextRepositoryAdapter) GetAccountByIDForCompany(companyID, accountID uuid.UUID) (Account, error) {
	return r.repository.GetAccountByIDForCompany(context.Background(), companyID, accountID)
}

func (r contextRepositoryAdapter) GetAccountByCodeForCompany(companyID uuid.UUID, code string) (Account, error) {
	return r.repository.GetAccountByCodeForCompany(context.Background(), companyID, code)
}

func (r contextRepositoryAdapter) GetAccountByCodeForCompanyContext(ctx context.Context, companyID uuid.UUID, code string) (Account, error) {
	return r.repository.GetAccountByCodeForCompany(ctx, companyID, code)
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
