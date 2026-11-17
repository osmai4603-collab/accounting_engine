package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresPostingLock_SameJournalSerializesAcrossTransactions(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- env.Driver.WithinPostingLock(ctx, env.CompanyID, env.JournalID, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first advisory lock was not acquired")
	}

	go func() {
		secondDone <- env.Driver.WithinPostingLock(ctx, env.CompanyID, env.JournalID, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second transaction acquired the same journal advisory lock concurrently")
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	select {
	case <-secondEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("second lock did not proceed after the first transaction released")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second lock: %v", err)
	}
}

func TestPostgresPostingLock_DifferentJournalsRunConcurrently(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	otherJournalID := uuid.New()

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- env.Driver.WithinPostingLock(ctx, env.CompanyID, env.JournalID, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first journal lock was not acquired")
	}

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- env.Driver.WithinPostingLock(ctx, env.CompanyID, otherJournalID, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("different journal was serialized by the same-journal advisory lock")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second journal lock: %v", err)
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first journal lock: %v", err)
	}
}
