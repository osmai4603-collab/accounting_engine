package ifrsautomation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	logger  *slog.Logger
	running bool
}

// NewScheduler creates an abstract periodic automation scheduler.
func NewScheduler(runner *Runner, jobs ...Job) (*Scheduler, error) {
	return NewSchedulerWithLogger(runner, slog.Default(), jobs...)
}

func NewSchedulerWithLogger(runner *Runner, logger *slog.Logger, jobs ...Job) (*Scheduler, error) {
	if runner == nil {
		return nil, ErrDependenciesRequired
	}
	if logger == nil {
		return nil, errors.New("IFRS automation logger is required")
	}
	return &Scheduler{
		runner: runner,
		jobs:   append([]Job(nil), jobs...),
		logger: logger,
	}, nil
}

// RunPeriod triggers all registered accounting jobs for an explicit accounting period.
func (s *Scheduler) RunPeriod(ctx context.Context, period Period) (RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := RunResult{Period: period}
	if err := period.Validate(); err != nil {
		return result, err
	}
	if !period.IsFullMonth() {
		return result, ErrInvalidPeriod
	}
	jobs := append([]Job(nil), s.jobs...)
	for _, job := range jobs {
		if job == nil || job.Key() == "" {
			return result, ErrInvalidJob
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Key() < jobs[j].Key() })
	if len(jobs) == 0 {
		s.logger.Warn("IFRS automation period has no registered jobs",
			"period_start", period.Start, "period_end", period.End)
		return result, nil
	}

	var failures []error
	for _, job := range jobs {
		jobResult, err := s.runner.RunMonthly(ctx, period, job)
		result.PostedEntryIDs = append(result.PostedEntryIDs, jobResult.PostedEntryIDs...)
		result.AlreadyPosted = append(result.AlreadyPosted, jobResult.AlreadyPosted...)
		if err != nil {
			wrapped := fmt.Errorf("run IFRS automation job %s: %w", job.Key(), err)
			failures = append(failures, wrapped)
			s.logger.Error("IFRS automation job failed", "job_key", job.Key(),
				"period_start", period.Start, "period_end", period.End, "error", err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		s.logger.Info("IFRS automation job completed", "job_key", job.Key(),
			"period_start", period.Start, "period_end", period.End,
			"posted_entries", len(jobResult.PostedEntryIDs),
			"already_posted_entries", len(jobResult.AlreadyPosted))
	}
	return result, errors.Join(failures...)
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
				s.logger.Error("cannot determine prior IFRS automation period", "error", err)
				continue
			}
			if _, err := s.RunPeriod(ctx, period); err != nil {
				s.logger.Error("IFRS automation period failed", "period_start", period.Start,
					"period_end", period.End, "error", err)
			}
		}
	}
}

// IsRunning reports whether the daemon is actively executing.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
