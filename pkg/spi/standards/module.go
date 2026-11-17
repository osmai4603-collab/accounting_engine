package standards

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
)

type ScheduledJob interface {
	Name() string
	Run(context.Context) error
}

type ScheduledJobRunner interface {
	RunScheduledJob(ctx context.Context, job ScheduledJob) error
}

// StandardModule is the public contract implemented by every standards module
// (IFRS/IAS and host-provided sector modules).
//
// Modules receive ModulePorts instead of a concrete storage implementation:
// they may read entries, balances and master data, and they must submit drafts
// and postings through the application layer. The interface therefore never
// imports legacy storage contracts or internal packages.
type StandardModule interface {
	Name() string
	Version() string
	Dependencies() []string
	ValidateDependencies(ports ModulePorts) error
	Initialize(ctx context.Context, ports ModulePorts) error
	RegisterRules(registrar events.RuleRegistrar) error
	RunScheduledJobs(ctx context.Context, runner ScheduledJobRunner) error
	Close(ctx context.Context) error
}
