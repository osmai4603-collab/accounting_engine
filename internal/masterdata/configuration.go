package masterdata

import (
	"time"

	"github.com/google/uuid"
)

type AccountingConfiguration struct {
	ID                        uuid.UUID
	RetainedEarningsAccountID uuid.UUID
	FXGainAccountID           uuid.UUID
	FXLossAccountID           uuid.UUID
	FXReconciliationJournalID uuid.UUID
	FunctionalCurrency        string
	UpdatedAt                 time.Time
}

type ConfigurationRepository interface {
	Get() (AccountingConfiguration, error)
	Update(AccountingConfiguration) error
}
