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
	GetPeriodByDate(time.Time) (FiscalPeriod, error)
	UpdatePeriodState(uuid.UUID, PeriodState) error
	GetNextSequenceWithLock(uuid.UUID) (int64, error)
}
