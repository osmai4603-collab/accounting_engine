package masterdata

import (
	"context"
	"errors"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

var (
	ErrFiscalYearNotFound        = errors.New("fiscal year not found")
	ErrJournalNotFound           = errors.New("journal not found")
	ErrRepositoryState           = errors.New("repository has invalid state")
	ErrAccountNotFound           = financialtypes.ErrAccountNotFound
	ErrDuplicateAccountCode      = financialtypes.ErrDuplicateAccountCode
	ErrFiscalPeriodNotFound      = financialtypes.ErrFiscalPeriodNotFound
	ErrFiscalPeriodClosed        = financialtypes.ErrFiscalPeriodClosed
	ErrCannotPostToParentAccount = financialtypes.ErrCannotPostToParentAccount
)

type Repository interface {
	CreateAccount(Account) error
	GetAccountByID(uuid.UUID) (Account, error)
	GetAccountByCode(string) (Account, error)
	ListAccounts() ([]Account, error)
	HasChildren(uuid.UUID) (bool, error)
	CreateFiscalYear(FiscalYear) error
	GetFiscalYear(uuid.UUID) (FiscalYear, error)
	UpdateFiscalYearState(uuid.UUID, FiscalYearState) error
	GetPeriodByDate(time.Time) (FiscalPeriod, error)
	UpdatePeriodState(uuid.UUID, PeriodState) error
	GetNextSequenceWithLock(uuid.UUID) (int64, error)
}

type ContextStorageRepository interface {
	CreateCompany(context.Context, Company) error
	GetCompanyByIDContext(context.Context, uuid.UUID) (Company, error)
	CreateAccount(context.Context, Account) error
	GetAccountByID(context.Context, uuid.UUID) (Account, error)
	GetAccountByCode(context.Context, string) (Account, error)
	ListAccounts(context.Context) ([]Account, error)
	HasChildren(context.Context, uuid.UUID) (bool, error)
	CreateFiscalYear(context.Context, FiscalYear) error
	GetFiscalYear(context.Context, uuid.UUID) (FiscalYear, error)
	UpdateFiscalYearState(context.Context, uuid.UUID, uuid.UUID, FiscalYearState) error
	GetPeriodByDate(context.Context, time.Time) (FiscalPeriod, error)
	UpdatePeriodState(context.Context, uuid.UUID, uuid.UUID, PeriodState) error
	GetNextSequenceWithLock(context.Context, uuid.UUID, uuid.UUID) (int64, error)
	GetAccountByIDForCompany(context.Context, uuid.UUID, uuid.UUID) (Account, error)
	GetAccountByCodeForCompany(context.Context, uuid.UUID, string) (Account, error)
	GetFiscalYearForCompany(context.Context, uuid.UUID, uuid.UUID) (FiscalYear, error)
	GetPeriodByDateForCompany(context.Context, uuid.UUID, time.Time) (FiscalPeriod, error)
	GetPeriodByNumberForCompany(context.Context, uuid.UUID, uuid.UUID, int) (FiscalPeriod, error)
	ContextCompanyScopedRepository
}

type CompanyScopedRepository interface {
	GetAccountByIDForCompany(companyID, accountID uuid.UUID) (Account, error)
	GetAccountByCodeForCompany(companyID uuid.UUID, code string) (Account, error)
	GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (FiscalYear, error)
	GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (FiscalPeriod, error)
	GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error)
}

type ContextCompanyScopedRepository interface {
	GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (Company, error)
	GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (Account, error)
	GetFiscalYearForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID) (FiscalYear, error)
	GetPeriodByDateForCompanyContext(ctx context.Context, companyID uuid.UUID, date time.Time) (FiscalPeriod, error)
	GetPeriodByNumberForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error)
	HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error)
}

var ErrCompanyScopeRequired = errors.New("company-scoped repository operations are required")
