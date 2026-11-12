package events

import (
	"sync"
)

type PostingRule struct {
	RuleID             string
	EventType          string
	DebitAccountCode   string
	CreditAccountCode  string
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
