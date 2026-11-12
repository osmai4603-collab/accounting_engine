package events

import (
	"time"

	"accounting_engine/pkg/api/v1/types"
)

type AccountingEvent struct {
	EventID        string
	EventType      string
	CompanyID      types.CompanyID
	EffectiveDate  time.Time
	Amount         types.Money
	Dimensions     map[string]string
	Payload        map[string]any
	IdempotencyKey string
}
