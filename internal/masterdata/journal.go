package masterdata

import (
	"errors"

	"github.com/google/uuid"
)

var ErrInvalidJournalType = errors.New("invalid journal type")

type JournalType string

const (
	JournalTypeGeneral  JournalType = "general"
	JournalTypeBank     JournalType = "bank"
	JournalTypeCash     JournalType = "cash"
	JournalTypeSale     JournalType = "sale"
	JournalTypePurchase JournalType = "purchase"
)

type Journal struct {
	ID         uuid.UUID
	Code       string
	Name       string
	Type       JournalType
	LastNumber int64
}

func (j Journal) Validate() error {
	switch j.Type {
	case JournalTypeGeneral, JournalTypeBank, JournalTypeCash, JournalTypeSale, JournalTypePurchase:
		return nil
	default:
		return ErrInvalidJournalType
	}
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
