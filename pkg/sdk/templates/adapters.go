// Package templates provides sector event adapters with consumer-supplied posting rules.
package templates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type RetailEffect string

const (
	RetailSale             RetailEffect = "sale"
	RetailRefund           RetailEffect = "refund"
	RetailInventoryReceipt RetailEffect = "inventory_receipt"
)

type AgricultureEffect string

const (
	AgricultureHarvest     AgricultureEffect = "harvest"
	AgricultureProduceSale AgricultureEffect = "produce_sale"
	AgricultureLivestock   AgricultureEffect = "livestock"
)

type SaaSEffect string

const (
	SaaSSubscriptionCharge SaaSEffect = "subscription_charge"
	SaaSRenewal            SaaSEffect = "renewal"
	SaaSCancellation       SaaSEffect = "cancellation"
)

// EventInput contains sector-neutral event data. Account resolution is deliberately
// left to the SDK's configured rule and authoritative company master data.
type EventInput struct {
	EventID        string
	CompanyID      types.CompanyID
	EffectiveDate  time.Time
	Amount         types.Money
	LedgerID       string
	IdempotencyKey string
}

// RetailAdapter builds public accounting events from explicitly supplied retail rules.
type RetailAdapter struct{ base *adapter }

// AgricultureAdapter builds public accounting events from explicitly supplied agriculture rules.
type AgricultureAdapter struct{ base *adapter }

// SaaSAdapter builds public accounting events from explicitly supplied SaaS rules.
type SaaSAdapter struct{ base *adapter }

func NewRetailAdapter(rules map[RetailEffect]events.PostingRule) (*RetailAdapter, error) {
	base, err := newAdapter("retail", convertRules(rules))
	if err != nil {
		return nil, err
	}
	return &RetailAdapter{base: base}, nil
}

func NewAgricultureAdapter(rules map[AgricultureEffect]events.PostingRule) (*AgricultureAdapter, error) {
	base, err := newAdapter("agriculture", convertRules(rules))
	if err != nil {
		return nil, err
	}
	return &AgricultureAdapter{base: base}, nil
}

func NewSaaSAdapter(rules map[SaaSEffect]events.PostingRule) (*SaaSAdapter, error) {
	base, err := newAdapter("saas", convertRules(rules))
	if err != nil {
		return nil, err
	}
	return &SaaSAdapter{base: base}, nil
}

func (a *RetailAdapter) Event(effect RetailEffect, input EventInput) (events.AccountingEvent, error) {
	return a.base.event(string(effect), input)
}

func (a *AgricultureAdapter) Event(effect AgricultureEffect, input EventInput) (events.AccountingEvent, error) {
	return a.base.event(string(effect), input)
}

func (a *SaaSAdapter) Event(effect SaaSEffect, input EventInput) (events.AccountingEvent, error) {
	return a.base.event(string(effect), input)
}

func (a *RetailAdapter) Rules() []events.PostingRule      { return a.base.rules() }
func (a *AgricultureAdapter) Rules() []events.PostingRule { return a.base.rules() }
func (a *SaaSAdapter) Rules() []events.PostingRule        { return a.base.rules() }

func (a *RetailAdapter) Name() string      { return a.base.name }
func (a *AgricultureAdapter) Name() string { return a.base.name }
func (a *SaaSAdapter) Name() string        { return a.base.name }

func (a *RetailAdapter) Version() string      { return "1.0.0" }
func (a *AgricultureAdapter) Version() string { return "1.0.0" }
func (a *SaaSAdapter) Version() string        { return "1.0.0" }

func (a *RetailAdapter) Dependencies() []string      { return nil }
func (a *AgricultureAdapter) Dependencies() []string { return nil }
func (a *SaaSAdapter) Dependencies() []string        { return nil }

func (a *RetailAdapter) ValidateDependencies(legacy.CoreLedgerStore) error      { return nil }
func (a *AgricultureAdapter) ValidateDependencies(legacy.CoreLedgerStore) error { return nil }
func (a *SaaSAdapter) ValidateDependencies(legacy.CoreLedgerStore) error        { return nil }

func (a *RetailAdapter) Initialize(context.Context, legacy.CoreLedgerStore) error      { return nil }
func (a *AgricultureAdapter) Initialize(context.Context, legacy.CoreLedgerStore) error { return nil }
func (a *SaaSAdapter) Initialize(context.Context, legacy.CoreLedgerStore) error        { return nil }

func (a *RetailAdapter) RegisterRules(registrar events.RuleRegistrar) error {
	return a.base.registerRules(registrar)
}

func (a *AgricultureAdapter) RegisterRules(registrar events.RuleRegistrar) error {
	return a.base.registerRules(registrar)
}

func (a *SaaSAdapter) RegisterRules(registrar events.RuleRegistrar) error {
	return a.base.registerRules(registrar)
}

func (a *RetailAdapter) RunScheduledJobs(context.Context, standards.ScheduledJobRunner) error {
	return nil
}
func (a *AgricultureAdapter) RunScheduledJobs(context.Context, standards.ScheduledJobRunner) error {
	return nil
}
func (a *SaaSAdapter) RunScheduledJobs(context.Context, standards.ScheduledJobRunner) error {
	return nil
}

func (a *RetailAdapter) Close(context.Context) error      { return nil }
func (a *AgricultureAdapter) Close(context.Context) error { return nil }
func (a *SaaSAdapter) Close(context.Context) error        { return nil }

var (
	_ standards.StandardModule = (*RetailAdapter)(nil)
	_ standards.StandardModule = (*AgricultureAdapter)(nil)
	_ standards.StandardModule = (*SaaSAdapter)(nil)
)

type adapter struct {
	name          string
	rulesByEffect map[string]events.PostingRule
}

func newAdapter(name string, rules map[string]events.PostingRule) (*adapter, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s adapter requires at least one consumer-supplied posting rule", name)
	}
	seenEventTypes := make(map[string]string, len(rules))
	for effect, rule := range rules {
		if strings.TrimSpace(effect) == "" {
			return nil, errors.New("adapter effect is required")
		}
		rule.EventType = strings.TrimSpace(rule.EventType)
		rule.DebitAccountCode = strings.TrimSpace(rule.DebitAccountCode)
		rule.CreditAccountCode = strings.TrimSpace(rule.CreditAccountCode)
		if rule.EventType == "" {
			return nil, fmt.Errorf("posting rule for effect %q requires an event type", effect)
		}
		if rule.DebitAccountCode == "" || rule.CreditAccountCode == "" {
			return nil, fmt.Errorf("posting rule for effect %q requires consumer-supplied debit and credit account codes", effect)
		}
		if prior, exists := seenEventTypes[rule.EventType]; exists {
			return nil, fmt.Errorf("effects %q and %q use duplicate event type %q", prior, effect, rule.EventType)
		}
		seenEventTypes[rule.EventType] = effect
		rules[effect] = rule
	}
	return &adapter{name: "sector-" + name, rulesByEffect: rules}, nil
}

func (a *adapter) event(effect string, input EventInput) (events.AccountingEvent, error) {
	rule, ok := a.rulesByEffect[effect]
	if !ok {
		return events.AccountingEvent{}, fmt.Errorf("no consumer-supplied posting rule for %s effect %q", a.name, effect)
	}
	if strings.TrimSpace(input.EventID) == "" {
		return events.AccountingEvent{}, errors.New("event ID is required")
	}
	if input.CompanyID == (types.CompanyID{}) {
		return events.AccountingEvent{}, errors.New("company ID is required")
	}
	if input.Amount.Currency() == "" || !input.Amount.IsPositive() {
		return events.AccountingEvent{}, errors.New("event amount must be positive and include a currency")
	}
	return events.AccountingEvent{
		EventID:        input.EventID,
		EventType:      rule.EventType,
		CompanyID:      input.CompanyID,
		EffectiveDate:  input.EffectiveDate,
		Amount:         input.Amount,
		LedgerID:       input.LedgerID,
		IdempotencyKey: input.IdempotencyKey,
	}, nil
}

func (a *adapter) rules() []events.PostingRule {
	result := make([]events.PostingRule, 0, len(a.rulesByEffect))
	for _, rule := range a.rulesByEffect {
		result = append(result, rule)
	}
	return result
}

func (a *adapter) registerRules(registrar events.RuleRegistrar) error {
	if registrar == nil {
		return errors.New("rule registrar is required")
	}
	for _, rule := range a.rules() {
		registrar.RegisterRule(rule)
	}
	return nil
}

func convertRules[K ~string](rules map[K]events.PostingRule) map[string]events.PostingRule {
	converted := make(map[string]events.PostingRule, len(rules))
	for effect, rule := range rules {
		converted[string(effect)] = rule
	}
	return converted
}
