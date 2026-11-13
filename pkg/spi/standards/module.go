package standards

import (
	"context"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/spi/storage"
)

type ScheduledJob interface {
	Name() string
	Run(context.Context) error
}

type ScheduledJobRunner interface {
	RunScheduledJob(ctx context.Context, job ScheduledJob) error
}

type StandardModule interface {
	Name() string
	Version() string
	Dependencies() []string
	ValidateDependencies(ports storage.CoreLedgerStore) error
	Initialize(ctx context.Context, ports storage.CoreLedgerStore) error
	RegisterRules(registrar events.RuleRegistrar) error
	RunScheduledJobs(ctx context.Context, runner ScheduledJobRunner) error
	Close(ctx context.Context) error
}
