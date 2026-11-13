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

// RuleRegistrar is the typed contract used by standard modules to register
// event-to-posting rules. Modules must not depend on internal router types.
type RuleRegistrar interface {
	RegisterRule(rule PostingRule)
}

type Router struct {
	mu    sync.RWMutex
	rules map[string][]PostingRule
}

var _ RuleRegistrar = (*Router)(nil)

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
	rules := r.rules[eventType]
	out := make([]PostingRule, len(rules))
	copy(out, rules)
	return out
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
		ID:                  types.NewJournalEntryLineID(),
		AccountID:           types.NewAccountID(),
		TransactionAmount:   amt,
		TransactionCurrency: cur,
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     amt,
		FunctionalCredit:    zeroAmt,
	}

	creditLine := ledger.EntryLine{
		ID:                  types.NewJournalEntryLineID(),
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
