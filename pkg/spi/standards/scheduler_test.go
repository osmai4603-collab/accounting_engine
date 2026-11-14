package standards

import (
	"context"
	"errors"
	"testing"
)

type dummyJob struct {
	name string
	err  error
	runs int
}

func (j *dummyJob) Name() string { return j.name }

func (j *dummyJob) Run(ctx context.Context) error {
	j.runs++
	return j.err
}

func TestScheduler_NotReady(t *testing.T) {
	scheduler := NewDefaultScheduler()
	job := &dummyJob{name: "test_job"}

	if err := scheduler.RegisterJob(job, JobIdempotencyPolicy{Key: "test_key"}); err != nil {
		t.Fatalf("unexpected registration error: %v", err)
	}

	_, err := scheduler.RunJob(context.Background(), "test_job")
	if !errors.Is(err, ErrSchedulerNotReady) {
		t.Fatalf("expected ErrSchedulerNotReady, got %v", err)
	}
}

func TestScheduler_ExecutionAndIdempotency(t *testing.T) {
	scheduler := NewDefaultScheduler()
	scheduler.SetReady(true)

	job := &dummyJob{name: "idempotent_job"}
	policy := JobIdempotencyPolicy{
		Key:        "idempotent_key",
		Idempotent: true,
	}

	if err := scheduler.RegisterJob(job, policy); err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	// First execution should succeed
	record, err := scheduler.RunJob(context.Background(), "idempotent_job")
	if err != nil {
		t.Fatalf("first execution failed: %v", err)
	}
	if record.Status != JobStatusCompleted {
		t.Fatalf("expected status %s, got %s", JobStatusCompleted, record.Status)
	}
	if job.runs != 1 {
		t.Fatalf("expected 1 run, got %d", job.runs)
	}

	// Second execution should fail due to idempotency policy
	_, err = scheduler.RunJob(context.Background(), "idempotent_job")
	if !errors.Is(err, ErrDuplicateJobExecution) {
		t.Fatalf("expected ErrDuplicateJobExecution, got %v", err)
	}
	if job.runs != 1 {
		t.Fatalf("job ran again despite idempotency policy: %d runs", job.runs)
	}
}

func TestScheduler_RunAllJobs(t *testing.T) {
	scheduler := NewDefaultScheduler()
	scheduler.SetReady(true)

	job1 := &dummyJob{name: "job1"}
	job2 := &dummyJob{name: "job2", err: errors.New("job2 failed")}

	_ = scheduler.RegisterJob(job1, JobIdempotencyPolicy{})
	_ = scheduler.RegisterJob(job2, JobIdempotencyPolicy{})

	results := scheduler.RunAllScheduledJobs(context.Background())
	if len(results) != 2 {
		t.Fatalf("expected 2 job results, got %d", len(results))
	}
	if results["job1"] != nil {
		t.Fatalf("job1 error: %v", results["job1"])
	}
	if results["job2"] == nil {
		t.Fatalf("expected job2 to fail")
	}

	history1 := scheduler.GetExecutionHistory("job1")
	if len(history1) != 1 || history1[0].Status != JobStatusCompleted {
		t.Fatalf("invalid history for job1: %v", history1)
	}

	history2 := scheduler.GetExecutionHistory("job2")
	if len(history2) != 1 || history2[0].Status != JobStatusFailed {
		t.Fatalf("invalid history for job2: %v", history2)
	}
}
