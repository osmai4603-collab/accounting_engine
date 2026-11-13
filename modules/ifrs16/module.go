package ifrs16

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

type Module struct{}

var _ standards.StandardModule = (*Module)(nil)

func (m *Module) Name() string {
	return "IFRS 16"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 16")
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
		RuleID:              "ifrs16-lease",
		EventType:           "IFRS16_LEASE_PAYMENT",
		DebitAccountCode:    "2300", // Lease Liability
		CreditAccountCode:   "1010", // Cash
		DescriptionTemplate: "IFRS 16 Lease Payment & Depreciation",
	})
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
