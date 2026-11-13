package ias12

import (
	"context"
	"errors"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
)

type Module struct{}

var _ standards.StandardModule = (*Module)(nil)

func (m *Module) Name() string {
	return "IAS 12"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IAS 12")
	}
	return nil
}

func (m *Module) Initialize(ctx context.Context, ports storage.CoreLedgerStore) error {
	return m.ValidateDependencies(ports)
}

func (m *Module) RegisterRules(registrar events.RuleRegistrar) error {
	if registrar == nil {
		return errors.New("rule registrar is required")
	}
	registrar.RegisterRule(events.PostingRule{
		RuleID:              "ias12-tax",
		EventType:           "IAS12_DEFERRED_TAX",
		DebitAccountCode:    "5400", // Income Tax Expense
		CreditAccountCode:   "2400", // Deferred Tax Liability
		DescriptionTemplate: "IAS 12 Deferred Tax Accounting",
	})
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
