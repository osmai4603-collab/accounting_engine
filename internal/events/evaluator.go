package events

import (
	apiEvents "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
)

func EvaluateAndConstructEntry(rule PostingRule, event apiEvents.AccountingEvent) (ledger.JournalEntry, error) {
	return apiEvents.EvaluateAndConstructEntry(rule, event)
}
