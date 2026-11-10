package ifrsautomation

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrSchedulerAlreadyRunning = errors.New("automation scheduler daemon is already running")
)

// Scheduler coordinates autonomous periodic runs of IFRS accounting jobs.
// It is completely abstract, domain-agnostic, and operates strictly on defined accounting periods and runners.
type Scheduler struct {
	mu      sync.Mutex
	runner  *Runner
	jobs    []Job
	running bool
}

// NewScheduler creates an abstract periodic automation scheduler.
func NewScheduler(runner *Runner, jobs ...Job) (*Scheduler, error) {
	if runner == nil {
		return nil, ErrDependenciesRequired
	}
	return &Scheduler{
		runner: runner,
		jobs:   append([]Job(nil), jobs...),
	}, nil
}

// RunPeriod triggers all registered accounting jobs for an explicit accounting period.
func (s *Scheduler) RunPeriod(ctx context.Context, period Period) (RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runner.RunMonthly(ctx, period, s.jobs...)
}

// StartDaemon starts an autonomous background runner that checks on each tick
// for closed accounting periods and executes registered jobs idempotently.
// It exits cleanly when ctx is canceled.
func (s *Scheduler) StartDaemon(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrSchedulerAlreadyRunning
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-ticker.C:
			// Evaluate the prior closed month
			prevMonth := t.UTC().AddDate(0, -1, 0)
			period, err := Month(prevMonth.Year(), prevMonth.Month())
			if err != nil {
				continue
			}
			_, _ = s.RunPeriod(ctx, period)
		}
	}
}

// IsRunning reports whether the daemon is actively executing.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

