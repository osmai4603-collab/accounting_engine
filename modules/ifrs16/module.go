package ifrs16

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
	return "IFRS 16"
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 16")
	}
	return nil
}

func (m *Module) RegisterRules(router interface{}) error {
	if r, ok := router.(*events.Router); ok {
		r.RegisterRule(events.PostingRule{
			RuleID:              "ifrs16-lease",
			EventType:           "IFRS16_LEASE_PAYMENT",
			DebitAccountCode:    "2300", // Lease Liability
			CreditAccountCode:   "1010", // Cash
			DescriptionTemplate: "IFRS 16 Lease Payment & Depreciation",
		})
	}
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}
