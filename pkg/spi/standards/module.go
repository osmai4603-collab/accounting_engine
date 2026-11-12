package standards

import (
	"context"

	"accounting_engine/pkg/spi/storage"
)

type StandardModule interface {
	Name() string
	ValidateDependencies(ports storage.CoreLedgerStore) error
	RegisterRules(router interface{}) error
	RunScheduledJobs(ctx context.Context, runner interface{}) error
}
