package reconciliation

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Reconciliation struct {
	ID               uuid.UUID
	Date             time.Time
	AccountID        uuid.UUID
	CurrencyID       string
	RealizedFXMoveID *uuid.UUID
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
	Items            []Item
}

type Item struct {
	ID               uuid.UUID
	ReconciliationID uuid.UUID
	MoveLineID       uuid.UUID
	AllocatedAmount  decimal.Decimal
	CurrencyID       string
	CreatedAt        time.Time
}

type Repository interface {
	Create(Reconciliation) error
	AddItem(uuid.UUID, Item) error
	List(uuid.UUID) ([]Reconciliation, error)
}
