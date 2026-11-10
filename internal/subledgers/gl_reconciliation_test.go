package subledgers

import (
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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

func TestControlAccountBalanceFromEntriesUsesNaturalSide(t *testing.T) {
	companyID, accountID, journalID := uuid.New(), uuid.New(), uuid.New()
	zero := money(t, "0.00")
	debit := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID, Status: posting.EntryPosted,
		Lines: []posting.JournalEntryLine{{CompanyID: companyID, AccountID: accountID, Debit: money(t, "75"), Credit: zero}},
	}
	account := masterdata.Account{CompanyID: companyID, ID: accountID, NormalBalance: masterdata.NormalBalanceDebit}
	balance, err := ControlAccountBalanceFromEntries(account, []posting.JournalEntry{debit}, "USD")
	if err != nil || !balance.Equal(money(t, "75")) {
		t.Fatalf("debit-normal balance = %s, err=%v", balance, err)
	}
	account.NormalBalance = masterdata.NormalBalanceCredit
	credit := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID, Status: posting.EntryPosted,
		Lines: []posting.JournalEntryLine{{CompanyID: companyID, AccountID: accountID, Debit: zero, Credit: money(t, "25")}},
	}
	balance, err = ControlAccountBalanceFromEntries(account, []posting.JournalEntry{credit}, "USD")
	if err != nil || !balance.Equal(money(t, "25")) {
		t.Fatalf("credit-normal balance = %s, err=%v", balance, err)
	}
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

func TestReceivableWriteOffRequiresPostedEntryAndSupportsPartialSettlement(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, partner, controlID := uuid.New(), uuid.New(), uuid.New()
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, ControlAccountID: controlID,
		JournalEntryID: uuid.New(), Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	if err := repo.SaveOpenLine(line); err != nil {
		t.Fatal(err)
	}
	ledger := NewReceivableLedgerForCompany(repo, companyID)
	params := WriteOffParams{JournalID: uuid.New(), AdjustmentAccountID: uuid.New(), Date: time.Now()}
	draft, err := ledger.PrepareWriteOff(line.ID, money(t, "40"), params)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := repo.GetOpenLine(line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.ResidualAmount.Equal(money(t, "100")) || unchanged.PendingWriteOffID != draft.ID {
		t.Fatalf("write-off draft changed residual amount or was not pending: %+v", unchanged)
	}
	if _, err := ledger.PrepareWriteOff(line.ID, money(t, "20"), params); err != ErrWriteOffPending {
		t.Fatalf("second pending write-off error = %v, want %v", err, ErrWriteOffPending)
	}
	if err := ledger.ConfirmWriteOff(line.ID, draft.ID); err != posting.ErrEntryNotPosted {
		t.Fatalf("ConfirmWriteOff() error = %v, want posted-entry error", err)
	}
	repo.mu.Lock()
	posted := repo.drafts[draft.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[draft.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmWriteOff(line.ID, draft.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetOpenLine(line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ResidualAmount.Equal(money(t, "60")) || !updated.WrittenOffAmount.Equal(money(t, "40")) ||
		updated.Closed || updated.PendingWriteOffID != uuid.Nil {
		t.Fatalf("partial write-off was not applied: %+v", updated)
	}
	fullDraft, err := ledger.PrepareWriteOff(line.ID, money(t, "60"), params)
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	posted = repo.drafts[fullDraft.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[fullDraft.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmWriteOff(line.ID, fullDraft.ID); err != nil {
		t.Fatal(err)
	}
	closed, err := repo.GetOpenLine(line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.ResidualAmount.Amount().IsZero() || !closed.WrittenOffAmount.Equal(money(t, "100")) ||
		!closed.Closed || closed.PendingWriteOffID != uuid.Nil {
		t.Fatalf("full write-off did not close the open line: %+v", closed)
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
