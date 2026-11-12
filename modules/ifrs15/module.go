package ifrs15

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
	return "IFRS 15"
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 15")
	}
	return nil
}

func (m *Module) RegisterRules(router interface{}) error {
	if r, ok := router.(*events.Router); ok {
		r.RegisterRule(events.PostingRule{
			RuleID:              "ifrs15-revenue",
			EventType:           "IFRS15_REVENUE_RECOGNITION",
			DebitAccountCode:    "1250",
			CreditAccountCode:   "4200",
			DescriptionTemplate: "IFRS 15 Performance Obligation Satisfied",
		})
	}
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}
