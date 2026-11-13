package events

import (
	"sync"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type PostingRule struct {
	RuleID              string
	EventType           string
	JournalCode         string
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
	return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
}
