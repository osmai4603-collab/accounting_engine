package events

import (
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
)

// AccountingEvent is the canonical public ingestion event.
// It can be processed either by resolving a configured per-company account mapping/rule
// or by providing a ready JournalDraft (preferred for adapters with full accounting context).
type AccountingEvent struct {
	EventID        string
	EventType      string
	CompanyID      types.CompanyID
	// LedgerID selects a company/ledger-scoped mapping when multiple mappings exist.
	// Empty means default mapping scope.
	LedgerID       string
	EffectiveDate  time.Time
	Amount         types.Money
	Dimensions     map[string]string
	Payload        map[string]any
	IdempotencyKey string
	// JournalDraft, when provided, is posted as-is (after validation by the posting path)
	// and mapping lookup is skipped. This allows adapters to submit canonical/ready entries.
	JournalDraft *ledger.JournalEntry
}

