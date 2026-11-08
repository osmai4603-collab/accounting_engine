package reporting

import (
	"time"

	"github.com/google/uuid"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
)

// Repository is intentionally left as an open SPI. Ledger implementations can
// expose any of the typed repository capabilities below.
type Repository interface{}

type AccountRepository interface {
	ListAccounts() ([]masterdata.Account, error)
}

type LedgerRepository interface {
	ListPostedEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
}

type EntryDateRepository interface {
	ListEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
}

type FiscalYearRepository interface {
	GetFiscalYear(uuid.UUID) (masterdata.FiscalYear, error)
	UpdatePeriodState(uuid.UUID, masterdata.PeriodState) error
}
