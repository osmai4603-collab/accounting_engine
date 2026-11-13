package subledgers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestSubledgerContextOperationsHonorCancellation(t *testing.T) {
	repository := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	amount, err := financialtypes.NewMoneyFromString("10", "USD")
	if err != nil {
		t.Fatal(err)
	}
	partnerID := uuid.New()
	if _, err := NewReceivableLedger(repository).AddInvoiceContext(ctx, partnerID, amount, time.Time{}, time.Time{}, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("receivable creation error = %v; want context.Canceled", err)
	}
	if _, err := NewTreasuryLedger(repository).RecordItemContext(ctx, uuid.New(), time.Now(), amount, "bank-item", BankItemPending); !errors.Is(err, context.Canceled) {
		t.Fatalf("treasury recording error = %v; want context.Canceled", err)
	}
	if _, _, err := NewMatchingEngine(repository).MatchWithFXContext(ctx, uuid.New(), uuid.New(), amount, FXMatchingParams{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("matching error = %v; want context.Canceled", err)
	}
	expired, expire := context.WithTimeout(context.Background(), 0)
	defer expire()
	if _, err := NewTreasuryLedger(repository).RecordItemContext(expired, uuid.New(), time.Now(), amount, "timed-out", BankItemPending); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out treasury recording error = %v; want context.DeadlineExceeded", err)
	}
	if len(repository.lines) != 0 || len(repository.bank) != 0 {
		t.Fatalf("canceled operations wrote data: %d open lines, %d bank accounts", len(repository.lines), len(repository.bank))
	}
}
