package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
)

func memoryEventEntry(entryID types.JournalEntryID) ledger.JournalEntry {
	return ledger.JournalEntry{
		ID: entryID, Status: ledger.EntryDraft,
		Lines: []ledger.EntryLine{{ID: types.NewJournalEntryLineID()}},
	}
}

func TestPublicEventIdempotencyTransitions(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()
	entryID := uuid.New()
	key := "same-key"

	claimedRecord, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, SourceSystem: "billing", EventType: "sale",
		PayloadHash: "hash-v1", PayloadHashVersion: "v1", Status: publicevents.StatusStarted,
		EntryID: entryID, Entry: memoryEventEntry(types.JournalEntryID(entryID)),
	})
	if err != nil || !claimed {
		t.Fatalf("initial claim: claimed=%t err=%v", claimed, err)
	}
	if claimedRecord.Status != publicevents.StatusStarted || claimedRecord.SourceSystem != "billing" {
		t.Fatalf("claimed record = %+v, want started/billing", claimedRecord)
	}

	// The same caller key under a different source system is independent.
	_, otherClaimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, SourceSystem: "crm", EventType: "sale",
		PayloadHash: "hash-v1", PayloadHashVersion: "v1", Status: publicevents.StatusStarted,
		EntryID: uuid.New(),
	})
	if err != nil || !otherClaimed {
		t.Fatalf("other-source claim: claimed=%t err=%v", otherClaimed, err)
	}

	// Same key and same source with a different payload is not claimed and
	// keeps the stored hash for the caller to compare.
	existing, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, SourceSystem: "billing", EventType: "sale",
		PayloadHash: "different-hash", Status: publicevents.StatusStarted,
	})
	if err != nil {
		t.Fatalf("conflicting claim: %v", err)
	}
	if claimed || existing.PayloadHash != "hash-v1" {
		t.Fatalf("conflicting claim = claimed %t hash %q, want existing hash-v1", claimed, existing.PayloadHash)
	}

	entry := memoryEventEntry(types.JournalEntryID(entryID))
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "wrong-hash", entry); !errors.Is(err, publicevents.ErrStateConflict) {
		t.Fatalf("complete with wrong payload hash error = %v, want state conflict", err)
	}
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "hash-v1", entry); err != nil {
		t.Fatalf("complete with matching payload hash: %v", err)
	}
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "hash-v1", entry); err != nil {
		t.Fatalf("idempotent repeated complete: %v", err)
	}
	conflicting := memoryEventEntry(types.NewJournalEntryID())
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "hash-v1", conflicting); !errors.Is(err, publicevents.ErrStateConflict) {
		t.Fatalf("complete with a different entry error = %v, want state conflict", err)
	}
	if err := driver.FailPublicEventIngestionForSource(ctx, companyID, "billing", key, publicevents.FailureClassTerminal, "nope"); !errors.Is(err, publicevents.ErrStateConflict) {
		t.Fatalf("fail completed record error = %v, want state conflict", err)
	}
	record, found, err := driver.GetPublicEventIngestionForSource(ctx, companyID, "billing", key)
	if err != nil || !found || !publicevents.IsCompleted(record.Status) {
		t.Fatalf("completed record = %+v found=%t err=%v", record, found, err)
	}
}

func TestPublicEventIdempotencyRetryableFailureReclaims(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()
	entryID := uuid.New()
	key := "retryable-key"

	if _, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, EventType: "sale", PayloadHash: "hash-v1",
		Status: publicevents.StatusStarted, EntryID: entryID,
	}); err != nil || !claimed {
		t.Fatalf("claim: claimed=%t err=%v", claimed, err)
	}
	if err := driver.FailPublicEventIngestionClassified(ctx, companyID, publicevents.DefaultSourceSystem, key,
		publicevents.FailureClassTransient, "temporary outage"); err != nil {
		t.Fatalf("record transient failure: %v", err)
	}
	record, found, err := driver.GetPublicEventIngestion(ctx, companyID, key)
	if err != nil || !found {
		t.Fatalf("read failed record: found=%t err=%v", found, err)
	}
	if !publicevents.IsRetryableFailure(record.Status) || record.FailureClass != publicevents.FailureClassTransient {
		t.Fatalf("failed record = %q/%q, want retryable_failure/transient", record.Status, record.FailureClass)
	}
	reclaimed, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, EventType: "sale", PayloadHash: "hash-v1",
		Status: publicevents.StatusStarted, EntryID: entryID,
	})
	if err != nil || !claimed {
		t.Fatalf("reclaim: claimed=%t err=%v", claimed, err)
	}
	if reclaimed.Status != publicevents.StatusStarted || reclaimed.EntryID != entryID {
		t.Fatalf("reclaimed record = %+v, want started with original entry", reclaimed)
	}
}

func TestPublicEventIdempotencyConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()
	entryID := uuid.New()

	const attempts = 16
	var wg sync.WaitGroup
	wg.Add(attempts)
	claimed := make(chan bool, attempts)
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			_, ok, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
				CompanyID: companyID, IdempotencyKey: "concurrent", EventType: "sale",
				PayloadHash: "hash-v1", Status: publicevents.StatusStarted, EntryID: entryID,
			})
			claimed <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
	}
	count := 0
	for ok := range claimed {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent claims succeeded %d times, want exactly once", count)
	}
}

func TestPublicEventIdempotencyReturnsClonedEntry(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()
	entryID := uuid.New()
	draft := memoryEventEntry(types.JournalEntryID(entryID))

	if _, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: "clone", EventType: "sale", PayloadHash: "hash-v1",
		Status: publicevents.StatusStarted, EntryID: entryID, Entry: draft,
	}); err != nil || !claimed {
		t.Fatalf("claim: claimed=%t err=%v", claimed, err)
	}
	record, _, err := driver.GetPublicEventIngestion(ctx, companyID, "clone")
	if err != nil {
		t.Fatal(err)
	}
	record.Entry.Lines[0].ID = types.NewJournalEntryLineID()
	again, _, err := driver.GetPublicEventIngestion(ctx, companyID, "clone")
	if err != nil {
		t.Fatal(err)
	}
	if again.Entry.Lines[0].ID == record.Entry.Lines[0].ID {
		t.Fatal("stored entry was mutated through the returned snapshot")
	}
}
