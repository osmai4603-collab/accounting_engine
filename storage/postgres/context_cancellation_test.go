package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresQueryHonorsContextCancellation(t *testing.T) {
	driver, err := newIsolatedPostgresDriver(t, context.Background())
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = driver.Pool().Exec(ctx, "SELECT pg_sleep(5)")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled PostgreSQL query error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("PostgreSQL query took %s to honor cancellation, want under one second", elapsed)
	}
}
