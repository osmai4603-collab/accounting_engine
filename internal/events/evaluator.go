package events

import (
	"fmt"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

func EvaluateAndConstructEntry(rule PostingRule, event events.AccountingEvent) (ledger.JournalEntry, error) {
	entryID := types.NewJournalEntryID()
	cur := event.Amount.Currency()
	if cur == "" {
		cur = "USD"
	}
	amt := event.Amount

	debitLine := ledger.EntryLine{
		ID:                  types.AccountID(types.NewJournalEntryID()),
		AccountID:           types.NewAccountID(), // mapped or resolved by store
		TransactionAmount:   amt,
		TransactionCurrency: cur,
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     amt,
	}

	creditLine := ledger.EntryLine{
		ID:                  types.AccountID(types.NewJournalEntryID()),
		AccountID:           types.NewAccountID(),
		TransactionAmount:   amt,
		TransactionCurrency: cur,
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalCredit:    amt,
	}

	desc := rule.DescriptionTemplate
	if desc == "" {
		desc = fmt.Sprintf("Event: %s (%s)", event.EventType, event.EventID)
	}

	return ledger.JournalEntry{
		ID:            entryID,
		CompanyID:     event.CompanyID,
		Status:        ledger.EntryPosted,
		EffectiveDate: event.EffectiveDate,
		PostingDate:   time.Now().UTC(),
		Description:   desc,
		SourceModule:  event.EventType,
		ReferenceID:   event.EventID,
		Lines:         []ledger.EntryLine{debitLine, creditLine},
	}, nil
}
