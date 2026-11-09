package subledgers

import (
	financialtypes "accounting_engine/internal/types"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type stubGLReader struct {
	balances map[string]financialtypes.Money
}

func (s *stubGLReader) GetAccountBalance(_ context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	balance, ok := s.balances[companyID.String()+"|"+accountID.String()]
	if !ok {
		return financialtypes.Money{}, ErrNotFound
	}
	return balance, nil
}

func TestLedgerReconciliationEngineDetectsMismatch(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	controlID := uuid.New()
	partner := uuid.New()
	ledger := NewReceivableLedgerForCompany(repo, companyID)
	if _, err := ledger.AddInvoiceWithControl(partner, controlID, uuid.New(), money(t, "120.00"), time.Now(), time.Now().Add(24*time.Hour), "INV-1"); err != nil {
		t.Fatal(err)
	}
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "100.00"),
	}})
	discrepancy, err := engine.ReconcileARControl(context.Background(), companyID, controlID)
	if err == nil || discrepancy == nil {
		t.Fatal("expected mismatch to be reported")
	}
	if !discrepancy.Difference.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("unexpected difference: got %s want 20", discrepancy.Difference)
	}
}

func TestLedgerReconciliationEngineRejectsMixedCurrencies(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, controlID, partner := uuid.New(), uuid.New(), uuid.New()
	ledger := NewPayableLedgerForCompany(repo, companyID)
	for _, amount := range []financialtypes.Money{money(t, "10.00"), mustMoney(t, "10.00", "EUR")} {
		if _, err := ledger.AddInvoiceForCompanyWithControl(companyID, partner, controlID, uuid.New(), amount, time.Now(), time.Now(), "BILL"); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "20.00"),
	}})
	_, err := engine.ReconcileAPControl(context.Background(), companyID, controlID)
	if err != ErrMixedCurrencyReconciliation {
		t.Fatalf("ReconcileAPControl() error = %v, want %v", err, ErrMixedCurrencyReconciliation)
	}
}

func TestLedgerReconciliationEngineUsesGLCurrencyForEmptySubledger(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, controlID := uuid.New(), uuid.New()
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "0.00"),
	}})
	discrepancy, err := engine.ReconcileARControl(context.Background(), companyID, controlID)
	if err != nil || discrepancy != nil {
		t.Fatalf("empty balanced control account: discrepancy=%+v err=%v", discrepancy, err)
	}
}

func TestAddInvoiceWithControlRequiresAccount(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewReceivableLedgerForCompany(repo, uuid.New())
	_, err := ledger.AddInvoiceWithControl(uuid.New(), uuid.Nil, uuid.New(), money(t, "50.00"), time.Now(), time.Now().Add(24*time.Hour), "INV-2")
	if err != ErrInvalidOpenLine {
		t.Fatalf("AddInvoiceWithControl() error = %v, want %v", err, ErrInvalidOpenLine)
	}
}

func mustMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
