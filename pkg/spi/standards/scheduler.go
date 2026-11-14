package standards

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrSchedulerNotReady is returned when attempting to run a scheduled job before core dependencies are initialized.
	ErrSchedulerNotReady = errors.New("scheduler: engine dependencies not ready")

	// ErrJobNotFound is returned when attempting to run an unregistered job.
	ErrJobNotFound = errors.New("scheduler: job not found")

	// ErrDuplicateJobExecution is returned when a job is already running or previously completed under an idempotent policy.
	ErrDuplicateJobExecution = errors.New("scheduler: duplicate job execution in progress or already completed")

	// ErrNilScheduledJob is returned when attempting to register a nil scheduled job.
	ErrNilScheduledJob = errors.New("scheduler: cannot register nil job")
)

type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// JobIdempotencyPolicy defines execution concurrency and idempotency rules for a job.
type JobIdempotencyPolicy struct {
	Key         string        `json:"key"`
	LockTimeout time.Duration `json:"lock_timeout"`
	Idempotent  bool          `json:"idempotent"`
}

// JobExecutionRecord logs details of a single scheduled job run.
type JobExecutionRecord struct {
	JobID       string    `json:"job_id"`
	ExecutionID string    `json:"execution_id"`
	Status      JobStatus `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// SchedulerPort defines the operational interface for scheduled jobs management.
type SchedulerPort interface {
	ScheduledJobRunner
	RegisterJob(job ScheduledJob, policy JobIdempotencyPolicy) error
	RunJob(ctx context.Context, jobName string) (JobExecutionRecord, error)
	RunAllScheduledJobs(ctx context.Context) map[string]error
	GetExecutionHistory(jobName string) []JobExecutionRecord
	SetReady(ready bool)
	IsReady() bool
}

// DefaultScheduler is a concurrency-safe, in-memory implementation of SchedulerPort.
type DefaultScheduler struct {
	mu          sync.RWMutex
	ready       bool
	jobs        map[string]ScheduledJob
	policies    map[string]JobIdempotencyPolicy
	records     map[string][]JobExecutionRecord
	activeLocks map[string]time.Time
}

// NewDefaultScheduler initializes a new DefaultScheduler.
func NewDefaultScheduler() *DefaultScheduler {
	return &DefaultScheduler{
		jobs:        make(map[string]ScheduledJob),
		policies:    make(map[string]JobIdempotencyPolicy),
		records:     make(map[string][]JobExecutionRecord),
		activeLocks: make(map[string]time.Time),
	}
}

func (s *DefaultScheduler) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

func (s *DefaultScheduler) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *DefaultScheduler) RegisterJob(job ScheduledJob, policy JobIdempotencyPolicy) error {
	if job == nil {
		return ErrNilScheduledJob
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	name := job.Name()
	if name == "" {
		return errors.New("scheduler: job name cannot be empty")
	}

	if policy.Key == "" {
		policy.Key = name
	}
	if policy.LockTimeout == 0 {
		policy.LockTimeout = 5 * time.Minute
	}

	s.jobs[name] = job
	s.policies[name] = policy
	return nil
}

func (s *DefaultScheduler) RunScheduledJob(ctx context.Context, job ScheduledJob) error {
	if job == nil {
		return ErrNilScheduledJob
	}
	if err := s.RegisterJob(job, JobIdempotencyPolicy{Key: job.Name()}); err != nil && !errors.Is(err, ErrNilScheduledJob) {
		// job may already be registered
	}
	_, err := s.RunJob(ctx, job.Name())
	return err
}

func (s *DefaultScheduler) RunJob(ctx context.Context, jobName string) (JobExecutionRecord, error) {
	s.mu.Lock()
	if !s.ready {
		s.mu.Unlock()
		return JobExecutionRecord{}, ErrSchedulerNotReady
	}

	job, exists := s.jobs[jobName]
	if !exists {
		s.mu.Unlock()
		return JobExecutionRecord{}, fmt.Errorf("%w: %s", ErrJobNotFound, jobName)
	}

	policy := s.policies[jobName]
	lockKey := policy.Key

	// Check lock / idempotency
	now := time.Now()
	if lockTime, locked := s.activeLocks[lockKey]; locked {
		if now.Before(lockTime.Add(policy.LockTimeout)) {
			s.mu.Unlock()
			return JobExecutionRecord{}, fmt.Errorf("%w: job %q is currently locked", ErrDuplicateJobExecution, jobName)
		}
	}

	// If job is idempotent and previously completed successfully
	if policy.Idempotent {
		records := s.records[jobName]
		for _, rec := range records {
			if rec.Status == JobStatusCompleted {
				s.mu.Unlock()
				return rec, fmt.Errorf("%w: job %q already completed", ErrDuplicateJobExecution, jobName)
			}
		}
	}

	// Acquire lock and record start
	s.activeLocks[lockKey] = now
	execID := fmt.Sprintf("%s-%d", jobName, now.UnixNano())
	record := JobExecutionRecord{
		JobID:       jobName,
		ExecutionID: execID,
		Status:      JobStatusRunning,
		StartedAt:   now,
	}
	s.records[jobName] = append(s.records[jobName], record)
	s.mu.Unlock()

	// Execute job
	jobErr := job.Run(ctx)
	completedAt := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeLocks, lockKey)

	// Update record status
	records := s.records[jobName]
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].ExecutionID == execID {
			records[i].CompletedAt = completedAt
			if jobErr != nil {
				records[i].Status = JobStatusFailed
				records[i].Error = jobErr.Error()
			} else {
				records[i].Status = JobStatusCompleted
			}
			record = records[i]
			break
		}
	}

	if jobErr != nil {
		return record, fmt.Errorf("scheduled job %q failed: %w", jobName, jobErr)
	}
	return record, nil
}

func (s *DefaultScheduler) RunAllScheduledJobs(ctx context.Context) map[string]error {
	s.mu.RLock()
	var jobNames []string
	for name := range s.jobs {
		jobNames = append(jobNames, name)
	}
	s.mu.RUnlock()

	results := make(map[string]error)
	for _, name := range jobNames {
		if ctx.Err() != nil {
			results[name] = ctx.Err()
			continue
		}
		_, err := s.RunJob(ctx, name)
		results[name] = err
	}
	return results
}

func (s *DefaultScheduler) GetExecutionHistory(jobName string) []JobExecutionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	recs := s.records[jobName]
	out := make([]JobExecutionRecord, len(recs))
	copy(out, recs)
	return out
}
