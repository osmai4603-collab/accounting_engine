package ifrs15

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type Module struct{}

var (
	_ standards.StandardModule  = (*Module)(nil)
	_ standards.DescribedModule = (*Module)(nil)
)

func (m *Module) Descriptor() standards.ModuleDescriptor {
	return standards.ModuleDescriptor{
		ID:          "ifrs15",
		Name:        "IFRS 15",
		Version:     "1.0.0",
		Provides:    []standards.Capability{"revenue_recognition"},
		Description: "IFRS 15 Revenue from Contracts with Customers",
	}
}

func (m *Module) Name() string {
	return "IFRS 15"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 15")
	}
	return nil
}

func (m *Module) Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error {
	return m.ValidateDependencies(ports)
}

func (m *Module) RegisterRules(registrar events.RuleRegistrar) error {
	if registrar == nil {
		return errors.New("rule registrar is required")
	}
	registrar.RegisterRule(events.PostingRule{
		RuleID:              "ifrs15-revenue",
		EventType:           "IFRS15_REVENUE_RECOGNITION",
		DebitAccountCode:    "1250",
		CreditAccountCode:   "4200",
		DescriptionTemplate: "IFRS 15 Performance Obligation Satisfied",
	})
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
