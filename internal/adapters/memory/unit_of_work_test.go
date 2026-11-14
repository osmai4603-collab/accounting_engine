package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func TestMemoryUnitOfWorkRollsBackSequenceAuditAndIdempotency(t *testing.T) {
	driver := New()
	companyID, journalID := types.NewCompanyID(), types.NewJournalID()
	failure := errors.New("callback failed")

	err := driver.Execute(context.Background(), func(ctx context.Context, uow storage.UnitOfWork) error {
		if _, err := uow.Sequences().NextSequence(ctx, companyID, journalID); err != nil {
			return err
		}
		if err := uow.Audit().AppendAudit(ctx, storage.AuditRecord{
			CompanyID: companyID, Action: "POST",
		}); err != nil {
			return err
		}
		if _, claimed, err := uow.Idempotency().Claim(ctx, storage.IdempotencyRecord{
			CompanyID: companyID, Key: "request-1", Operation: "post", PayloadHash: "hash-1",
		}); err != nil || !claimed {
			t.Fatalf("claim = (%v, %v), want (true, nil)", claimed, err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Execute error = %v, want callback error", err)
	}

	err = driver.Execute(context.Background(), func(ctx context.Context, uow storage.UnitOfWork) error {
		sequence, err := uow.Sequences().NextSequence(ctx, companyID, journalID)
		if err != nil {
			return err
		}
		if sequence != 1 {
			t.Fatalf("sequence after rollback = %d, want 1", sequence)
		}
		_, claimed, err := uow.Idempotency().Claim(ctx, storage.IdempotencyRecord{
			CompanyID: companyID, Key: "request-1", Operation: "post", PayloadHash: "hash-1",
		})
		if err != nil || !claimed {
			t.Fatalf("claim after rollback = (%v, %v), want (true, nil)", claimed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("commit after rollback: %v", err)
	}
}

func TestMemoryIdempotencyIsCompanyScopedAndValidatesPayload(t *testing.T) {
	driver := New()
	firstCompany, secondCompany := types.NewCompanyID(), types.NewCompanyID()
	ports := driver.Ports()
	record := storage.IdempotencyRecord{
		CompanyID: firstCompany, Key: "same-key", Operation: "post", PayloadHash: "hash-a",
	}
	if _, claimed, err := ports.Idempotency.Claim(context.Background(), record); err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", claimed, err)
	}
	if existing, claimed, err := ports.Idempotency.Claim(context.Background(), record); err != nil || claimed || existing.PayloadHash != "hash-a" {
		t.Fatalf("repeated claim = (%+v, %v, %v), want existing hash-a and not claimed", existing, claimed, err)
	}
	otherCompanyRecord := record
	otherCompanyRecord.CompanyID = secondCompany
	if _, claimed, err := ports.Idempotency.Claim(context.Background(), otherCompanyRecord); err != nil || !claimed {
		t.Fatalf("other company claim = (%v, %v), want (true, nil)", claimed, err)
	}
	record.PayloadHash = "hash-b"
	if _, _, err := ports.Idempotency.Claim(context.Background(), record); !errors.Is(err, storage.ErrUniqueConstraint) {
		t.Fatalf("payload mismatch error = %v, want ErrUniqueConstraint", err)
	}
}

func TestMemoryTransactionRunnerRollsBackContextBoundPorts(t *testing.T) {
	driver := New()
	ports := driver.Ports()
	companyID, journalID := types.NewCompanyID(), types.NewJournalID()
	failure := errors.New("abort transaction")

	err := ports.TxRunner.WithinTransaction(context.Background(), func(ctx context.Context) error {
		sequence, err := ports.Sequences.NextSequence(ctx, companyID, journalID)
		if err != nil {
			return err
		}
		if sequence != 1 {
			t.Fatalf("transaction sequence = %d, want 1", sequence)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("transaction error = %v, want callback error", err)
	}
	sequence, err := ports.Sequences.NextSequence(context.Background(), companyID, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 1 {
		t.Fatalf("sequence after rollback = %d, want 1", sequence)
	}
}

func TestMemoryReadOnlyTransactionRejectsWrites(t *testing.T) {
	driver := New()
	companyID, journalID := types.NewCompanyID(), types.NewJournalID()
	err := driver.ExecuteWithOptions(context.Background(), storage.TxOptions{ReadOnly: true}, func(_ context.Context, uow storage.UnitOfWork) error {
		_, err := uow.Sequences().NextSequence(context.Background(), companyID, journalID)
		return err
	})
	if !errors.Is(err, ErrReadOnlyTransaction) {
		t.Fatalf("read-only write error = %v, want ErrReadOnlyTransaction", err)
	}
	sequence, err := driver.Ports().Sequences.NextSequence(context.Background(), companyID, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 1 {
		t.Fatalf("sequence after rejected write = %d, want 1", sequence)
	}
}
