package ifrs9

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
	return "IFRS 9"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 9")
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
		RuleID:              "ifrs9-ecl",
		EventType:           "IFRS9_ECL_PROVISION",
		DebitAccountCode:    "5300",
		CreditAccountCode:   "1290",
		DescriptionTemplate: "IFRS 9 Expected Credit Loss Provision",
	})
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
