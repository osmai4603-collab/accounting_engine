package masterdata

import (
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
	GetPeriodByDate(time.Time) (FiscalPeriod, error)
	UpdatePeriodState(uuid.UUID, PeriodState) error
	GetNextSequenceWithLock(uuid.UUID) (int64, error)
}

type CompanyScopedRepository interface {
	GetAccountByIDForCompany(companyID, accountID uuid.UUID) (Account, error)
	GetAccountByCodeForCompany(companyID uuid.UUID, code string) (Account, error)
	GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (FiscalYear, error)
	GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (FiscalPeriod, error)
	GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (FiscalPeriod, error)
}

var ErrCompanyScopeRequired = errors.New("company-scoped repository operations are required")
