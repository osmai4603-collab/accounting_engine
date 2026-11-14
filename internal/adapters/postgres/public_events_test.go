package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
)

func setupPublicEventCompany(t *testing.T) (context.Context, *postgres.Driver, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("EVT-%s", companyID.String()[:8]),
		LegalName: "Public Event Idempotency Co", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	return ctx, driver, companyID
}

func postgresEventEntry(entryID types.JournalEntryID) ledger.JournalEntry {
	return ledger.JournalEntry{
		ID: entryID, Status: ledger.EntryDraft,
		Lines: []ledger.EntryLine{{ID: types.NewJournalEntryLineID()}},
	}
}

func TestPostgresPublicEventIdempotencyTransitions(t *testing.T) {
	ctx, driver, companyID := setupPublicEventCompany(t)
	entryID := uuid.New()
	key := "same-key"

	claimedRecord, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, SourceSystem: "billing", EventType: "sale",
		PayloadHash: "hash-v1", PayloadHashVersion: "v1", Status: publicevents.StatusStarted,
		EntryID: entryID, Entry: postgresEventEntry(types.JournalEntryID(entryID)),
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

	// Same key and source with a different payload is not claimed and keeps
	// the stored hash for the caller to compare.
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

	entry := postgresEventEntry(types.JournalEntryID(entryID))
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "wrong-hash", entry); !errors.Is(err, publicevents.ErrStateConflict) {
		t.Fatalf("complete with wrong payload hash error = %v, want state conflict", err)
	}
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "hash-v1", entry); err != nil {
		t.Fatalf("complete with matching payload hash: %v", err)
	}
	if err := driver.CompletePublicEventIngestionForSource(ctx, companyID, "billing", key, "hash-v1", entry); err != nil {
		t.Fatalf("idempotent repeated complete: %v", err)
	}
	conflicting := postgresEventEntry(types.NewJournalEntryID())
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
	if record.EntryID != entryID {
		t.Fatalf("completed entry id = %s, want %s", record.EntryID, entryID)
	}
}

func TestPostgresPublicEventIdempotencyRetryableFailureReclaims(t *testing.T) {
	ctx, driver, companyID := setupPublicEventCompany(t)
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

func TestPostgresPublicEventIdempotencyTerminalFailureIsNotReclaimed(t *testing.T) {
	ctx, driver, companyID := setupPublicEventCompany(t)
	key := "terminal-key"

	if _, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, EventType: "sale", PayloadHash: "hash-v1",
		Status: publicevents.StatusStarted, EntryID: uuid.New(),
	}); err != nil || !claimed {
		t.Fatalf("claim: claimed=%t err=%v", claimed, err)
	}
	if err := driver.FailPublicEventIngestionClassified(ctx, companyID, publicevents.DefaultSourceSystem, key,
		publicevents.FailureClassTerminal, "invalid account"); err != nil {
		t.Fatalf("record terminal failure: %v", err)
	}
	record, found, err := driver.GetPublicEventIngestion(ctx, companyID, key)
	if err != nil || !found || !publicevents.IsTerminalFailure(record.Status) {
		t.Fatalf("terminal record = %+v found=%t err=%v", record, found, err)
	}
	if _, claimed, err := driver.ClaimPublicEventIngestion(ctx, publicevents.Ingestion{
		CompanyID: companyID, IdempotencyKey: key, EventType: "sale", PayloadHash: "hash-v1",
		Status: publicevents.StatusStarted, EntryID: uuid.New(),
	}); err != nil || claimed {
		t.Fatalf("terminal reclaim: claimed=%t err=%v, want claimed=false", claimed, err)
	}
}

func TestPostgresPublicEventIdempotencyConcurrentClaim(t *testing.T) {
	ctx, driver, companyID := setupPublicEventCompany(t)
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

func TestPostgresPublicEventIdempotencyMissingRecords(t *testing.T) {
	ctx, driver, companyID := setupPublicEventCompany(t)

	if _, found, err := driver.GetPublicEventIngestion(ctx, companyID, "absent"); err != nil || found {
		t.Fatalf("get absent = found %t err %v, want not found", found, err)
	}
	if err := driver.FailPublicEventIngestion(ctx, companyID, "absent", "nope"); !errors.Is(err, publicevents.ErrRecordNotFound) {
		t.Fatalf("fail absent error = %v, want record not found", err)
	}
}
