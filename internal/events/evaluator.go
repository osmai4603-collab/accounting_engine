package events

import (
	apiEvents "accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
)

func EvaluateAndConstructEntry(rule PostingRule, event apiEvents.AccountingEvent) (ledger.JournalEntry, error) {
	return apiEvents.EvaluateAndConstructEntry(rule, event)
}
