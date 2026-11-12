package events

import (
	"fmt"
	"sync"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

type PostingRule struct {
	RuleID              string
	EventType           string
	DebitAccountCode    string
	CreditAccountCode   string
	DescriptionTemplate string
}

type Router struct {
	mu    sync.RWMutex
	rules map[string][]PostingRule
}

func NewRouter() *Router {
	return &Router{
		rules: make(map[string][]PostingRule),
	}
}

func (r *Router) RegisterRule(rule PostingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.EventType] = append(r.rules[rule.EventType], rule)
}

func (r *Router) GetRules(eventType string) []PostingRule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rules[eventType]
}

func EvaluateAndConstructEntry(rule PostingRule, event AccountingEvent) (ledger.JournalEntry, error) {
	entryID := types.NewJournalEntryID()
	cur := event.Amount.Currency()
	if cur == "" {
		cur = "USD"
	}
	amt := event.Amount
	zeroAmt, _ := types.NewMoney(decimal.Zero, cur)

	debitLine := ledger.EntryLine{
		ID:                  types.AccountID(types.NewJournalEntryID()),
		AccountID:           types.NewAccountID(),
		TransactionAmount:   amt,
		TransactionCurrency: cur,
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     amt,
		FunctionalCredit:    zeroAmt,
	}

	creditLine := ledger.EntryLine{
		ID:                  types.AccountID(types.NewJournalEntryID()),
		AccountID:           types.NewAccountID(),
		TransactionAmount:   amt,
		TransactionCurrency: cur,
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     zeroAmt,
		FunctionalCredit:    amt,
	}

	desc := rule.DescriptionTemplate
	if desc == "" {
		desc = fmt.Sprintf("Event: %s (%s)", event.EventType, event.EventID)
	}

	return ledger.JournalEntry{
		ID:             entryID,
		CompanyID:      event.CompanyID,
		Status:         ledger.EntryPosted,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  event.EffectiveDate,
		PostingDate:    time.Now().UTC(),
		Description:    desc,
		SourceModule:   event.EventType,
		ReferenceID:    event.EventID,
		Lines:          []ledger.EntryLine{debitLine, creditLine},
	}, nil
}
