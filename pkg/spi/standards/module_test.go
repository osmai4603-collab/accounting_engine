package standards

import (
	"context"
	"errors"
	"testing"
)

type scheduledJobFunc struct {
	name string
	run  func(context.Context) error
}

func (j scheduledJobFunc) Name() string                  { return j.name }
func (j scheduledJobFunc) Run(ctx context.Context) error { return j.run(ctx) }

type scheduledJobRunnerFunc func(context.Context, ScheduledJob) error

func (f scheduledJobRunnerFunc) RunScheduledJob(ctx context.Context, job ScheduledJob) error {
	return f(ctx, job)
}

func TestScheduledJobRunnerContract(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "scheduled")
	wantErr := errors.New("job failed")
	jobRan := false
	job := scheduledJobFunc{
		name: "monthly-close",
		run: func(gotCtx context.Context) error {
			jobRan = true
			if got := gotCtx.Value(struct{}{}); got != "scheduled" {
				t.Fatalf("job context value = %v, want scheduled", got)
			}
			return wantErr
		},
	}

	runner := scheduledJobRunnerFunc(func(gotCtx context.Context, gotJob ScheduledJob) error {
		if gotJob.Name() != "monthly-close" {
			t.Fatalf("job name = %q, want monthly-close", gotJob.Name())
		}
		return gotJob.Run(gotCtx)
	})

	if err := runner.RunScheduledJob(ctx, job); !errors.Is(err, wantErr) {
		t.Fatalf("RunScheduledJob() error = %v, want %v", err, wantErr)
	}
	if !jobRan {
		t.Fatal("runner did not run the scheduled job")
	}
}
