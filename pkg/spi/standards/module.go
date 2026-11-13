package standards

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
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
	ValidateDependencies(ports legacy.CoreLedgerStore) error
	Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error
	RegisterRules(registrar events.RuleRegistrar) error
	RunScheduledJobs(ctx context.Context, runner ScheduledJobRunner) error
	Close(ctx context.Context) error
}
