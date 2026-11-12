package ias12

import (
	"context"
	"errors"

	"accounting_engine/internal/events"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
)

type Module struct{}

var _ standards.StandardModule = (*Module)(nil)

func (m *Module) Name() string {
	return "IAS 12"
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IAS 12")
	}
	return nil
}

func (m *Module) RegisterRules(router interface{}) error {
	if r, ok := router.(*events.Router); ok {
		r.RegisterRule(events.PostingRule{
			RuleID:              "ias12-tax",
			EventType:           "IAS12_DEFERRED_TAX",
			DebitAccountCode:    "5400", // Income Tax Expense
			CreditAccountCode:   "2400", // Deferred Tax Liability
			DescriptionTemplate: "IAS 12 Deferred Tax Accounting",
		})
	}
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}
