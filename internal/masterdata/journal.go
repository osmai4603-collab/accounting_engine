package masterdata

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidJournalType            = errors.New("invalid journal type")
	ErrJournalInactive               = errors.New("journal is inactive")
	ErrJournalCurrencyNotAllowed     = errors.New("journal does not allow transaction currency")
	ErrInvalidJournalNumberingPolicy = errors.New("invalid journal numbering policy")
	ErrJournalSettingsInUse          = errors.New("journal settings cannot change after entries exist")
)

type JournalType string

const (
	JournalTypeGeneral  JournalType = "general"
	JournalTypeBank     JournalType = "bank"
	JournalTypeCash     JournalType = "cash"
	JournalTypeSale     JournalType = "sale"
	JournalTypePurchase JournalType = "purchase"
)

type Journal struct {
	CompanyID         uuid.UUID
	ID                uuid.UUID
	Code              string
	Name              string
	Type              JournalType
	LastNumber        int64
	Active            bool
	AllowedCurrencies []string
	NumberingPolicy   JournalNumberingPolicy
}

type JournalNumberingPolicy string

const (
	JournalNumberingPerJournal JournalNumberingPolicy = "per_journal"
)

func (j Journal) Validate() error {
	switch j.Type {
	case JournalTypeGeneral, JournalTypeBank, JournalTypeCash, JournalTypeSale, JournalTypePurchase:
	default:
		return ErrInvalidJournalType
	}
	if j.NumberingPolicy != "" && j.NumberingPolicy != JournalNumberingPerJournal {
		return ErrInvalidJournalNumberingPolicy
	}
	for _, currency := range j.AllowedCurrencies {
		if len(currency) != 3 || strings.TrimSpace(currency) != currency || strings.ToUpper(currency) != currency {
			return ErrJournalCurrencyNotAllowed
		}
	}
	return nil
}

type LedgerType string

const (
	LedgerPrimary   LedgerType = "primary"
	LedgerSecondary LedgerType = "secondary"
)

type Ledger struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Type      LedgerType
	IsPrimary bool
	Currency  string
}
