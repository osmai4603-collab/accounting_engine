package standards

import (
	"context"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/spi/storage"
)

type StandardModule interface {
	Name() string
	ValidateDependencies(ports storage.CoreLedgerStore) error
	RegisterRules(registrar events.RuleRegistrar) error
	RunScheduledJobs(ctx context.Context, runner interface{}) error
}
