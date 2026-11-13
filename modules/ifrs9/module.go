package ifrs9

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
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

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 9")
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
		RuleID:              "ifrs9-ecl",
		EventType:           "IFRS9_ECL_PROVISION",
		DebitAccountCode:    "5300",
		CreditAccountCode:   "1290",
		DescriptionTemplate: "IFRS 9 Expected Credit Loss Provision",
	})
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
