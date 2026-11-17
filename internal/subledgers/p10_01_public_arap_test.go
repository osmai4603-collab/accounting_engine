package subledgers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicsubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

func TestP1001PublicARAPAccountingLifecycleAndInquirySeparation(t *testing.T) {
	ctx := context.Background()
	companyID, arControl, apControl := types.NewCompanyID(), types.NewAccountID(), types.NewAccountID()
	sales, expense, cash, badDebt := types.NewAccountID(), types.NewAccountID(), types.NewAccountID(), types.NewAccountID()
	app := &p1001UseCaseRecorder{}
	receivables := &p1001ReceivableStore{values: make(map[string]apisubledger.Receivable)}
	payables := &p1001PayableStore{values: make(map[string]apisubledger.Payable)}
	balance := &p1001BalanceReader{amount: decimal.NewFromInt(0)}
	service, err := NewPublicARAPService(app, publicsubledger.ARAPStores{
		Receivables: receivables, Payables: payables, Balance: balance,
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	invoiceDate := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ar, err := service.CreateReceivableInvoice(ctx, apisubledger.InvoiceCommand{
		CompanyID: companyID, PartnerID: "customer-1", ControlAccountID: arControl,
		CounterAccountID: sales, JournalID: types.NewJournalID(),
		Amount: p1001Money(t, "100", "USD"), FunctionalAmount: p1001Money(t, "100", "USD"),
		DocumentDate: invoiceDate, DueDate: invoiceDate.AddDate(0, 0, 30), Reference: "AR-1",
	})
	if err != nil {
		t.Fatalf("create receivable invoice: %v", err)
	}
	if ar.Receivable == nil || ar.EntryID == (types.JournalEntryID{}) {
		t.Fatal("receivable invoice did not return its posted accounting evidence")
	}
	assertP1001BalancedEntry(t, app.entries[ar.EntryID], arControl, sales)
	assertP1001AccountSide(t, app.entries[ar.EntryID], arControl, true)
	assertP1001AccountSide(t, app.entries[ar.EntryID], sales, false)

	ap, err := service.CreatePayableInvoice(ctx, apisubledger.InvoiceCommand{
		CompanyID: companyID, PartnerID: "supplier-1", ControlAccountID: apControl,
		CounterAccountID: expense, JournalID: types.NewJournalID(),
		Amount: p1001Money(t, "80", "USD"), FunctionalAmount: p1001Money(t, "80", "USD"),
		DocumentDate: invoiceDate, DueDate: invoiceDate.AddDate(0, 0, 30), Reference: "AP-1",
	})
	if err != nil {
		t.Fatalf("create payable invoice: %v", err)
	}
	if ap.Payable == nil {
		t.Fatal("payable invoice missing")
	}
	assertP1001BalancedEntry(t, app.entries[ap.EntryID], apControl, expense)
	assertP1001AccountSide(t, app.entries[ap.EntryID], apControl, false)
	assertP1001AccountSide(t, app.entries[ap.EntryID], expense, true)

	payment, err := service.RecordPayment(ctx, apisubledger.PaymentCommand{
		CompanyID: companyID, Kind: apisubledger.SubledgerReceivable, CashAccountID: cash,
		JournalID: types.NewJournalID(), Amount: p1001Money(t, "40", "USD"),
		FunctionalAmount: p1001Money(t, "40", "USD"), PaymentDate: invoiceDate.AddDate(0, 0, 5),
		Reference: "REC-1", Allocations: []apisubledger.PaymentAllocation{{
			OpenItemID: ar.Receivable.ID, Amount: p1001Money(t, "40", "USD"),
		}},
	})
	if err != nil {
		t.Fatalf("record collection: %v", err)
	}
	if payment.Payment.Amount.Amount().String() != "40" ||
		receivables.values[ar.Receivable.ID].RemainingAmount.Amount().String() != "60" {
		t.Fatalf("collection balance not updated: payment=%+v receivable=%+v", payment, receivables.values[ar.Receivable.ID])
	}
	assertP1001BalancedEntry(t, app.entries[payment.EntryID], arControl, cash)
	assertP1001AccountSide(t, app.entries[payment.EntryID], arControl, false)
	assertP1001AccountSide(t, app.entries[payment.EntryID], cash, true)

	writeOffID, err := service.WriteOff(ctx, apisubledger.WriteOffCommand{
		CompanyID: companyID, Kind: apisubledger.SubledgerReceivable, OpenItemID: ar.Receivable.ID,
		AdjustmentAccountID: badDebt, JournalID: types.NewJournalID(),
		Amount: p1001Money(t, "10", "USD"), FunctionalAmount: p1001Money(t, "10", "USD"),
		Date: invoiceDate.AddDate(0, 0, 10), Reference: "WO-1",
	})
	if err != nil {
		t.Fatalf("write off: %v", err)
	}
	if receivables.values[ar.Receivable.ID].RemainingAmount.Amount().String() != "50" {
		t.Fatal("write-off did not reduce the receivable balance")
	}
	assertP1001BalancedEntry(t, app.entries[writeOffID], arControl, badDebt)
	assertP1001AccountSide(t, app.entries[writeOffID], arControl, false)
	assertP1001AccountSide(t, app.entries[writeOffID], badDebt, true)

	payablePayment, err := service.RecordPayment(ctx, apisubledger.PaymentCommand{
		CompanyID: companyID, Kind: apisubledger.SubledgerPayable, CashAccountID: cash,
		JournalID: types.NewJournalID(), Amount: p1001Money(t, "30", "USD"),
		FunctionalAmount: p1001Money(t, "30", "USD"), PaymentDate: invoiceDate.AddDate(0, 0, 5),
		Reference: "PAY-1", Allocations: []apisubledger.PaymentAllocation{{
			OpenItemID: ap.Payable.ID, Amount: p1001Money(t, "30", "USD"),
		}},
	})
	if err != nil {
		t.Fatalf("record payable payment: %v", err)
	}
	if payables.values[ap.Payable.ID].RemainingAmount.Amount().String() != "50" {
		t.Fatal("payable payment did not reduce the balance")
	}
	assertP1001BalancedEntry(t, app.entries[payablePayment.EntryID], apControl, cash)
	assertP1001AccountSide(t, app.entries[payablePayment.EntryID], apControl, true)
	assertP1001AccountSide(t, app.entries[payablePayment.EntryID], cash, false)

	payableWriteOffID, err := service.WriteOff(ctx, apisubledger.WriteOffCommand{
		CompanyID: companyID, Kind: apisubledger.SubledgerPayable, OpenItemID: ap.Payable.ID,
		AdjustmentAccountID: badDebt, JournalID: types.NewJournalID(),
		Amount: p1001Money(t, "10", "USD"), FunctionalAmount: p1001Money(t, "10", "USD"),
		Date: invoiceDate.AddDate(0, 0, 10), Reference: "AP-WO-1",
	})
	if err != nil {
		t.Fatalf("payable write-off: %v", err)
	}
	if payables.values[ap.Payable.ID].RemainingAmount.Amount().String() != "40" {
		t.Fatal("payable write-off did not reduce the balance")
	}
	assertP1001BalancedEntry(t, app.entries[payableWriteOffID], apControl, badDebt)
	assertP1001AccountSide(t, app.entries[payableWriteOffID], apControl, true)
	assertP1001AccountSide(t, app.entries[payableWriteOffID], badDebt, false)

	beforeInquiry := app.posted
	if _, err := service.GetReceivable(ctx, companyID, ar.Receivable.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aging(ctx, companyID, apisubledger.SubledgerReceivable, "", invoiceDate.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	balance.amount = decimal.NewFromInt(50)
	report, err := service.Reconcile(ctx, companyID, apisubledger.SubledgerReceivable, arControl, "USD", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Reconciled || report.SubledgerBalance.String() != "50" || app.posted != beforeInquiry {
		t.Fatalf("inquiry/reconciliation changed accounting state or mismatched: %+v", report)
	}
	balance.amount = decimal.NewFromInt(-40)
	apReport, err := service.Reconcile(ctx, companyID, apisubledger.SubledgerPayable, apControl, "USD", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !apReport.Reconciled || apReport.SubledgerBalance.String() != "40" {
		t.Fatalf("AP credit-normal balance was not normalized: %+v", apReport)
	}
}

func TestP1001PublicARAPRequiresAllBuilderStores(t *testing.T) {
	if _, err := NewPublicARAPService(&p1001UseCaseRecorder{}, publicsubledger.ARAPStores{}, false); !errors.Is(err, ErrARAPStoresRequired) {
		t.Fatalf("missing stores error = %v", err)
	}
}

func p1001Money(t *testing.T, value, currency string) types.Money {
	t.Helper()
	money, err := types.NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func assertP1001BalancedEntry(t *testing.T, entry ledger.JournalEntry, controlID, counterpartID types.AccountID) {
	t.Helper()
	if entry.Status != ledger.EntryPosted || len(entry.Lines) != 2 {
		t.Fatalf("entry not posted or not two-line: %+v", entry)
	}
	debits, credits := decimal.Zero, decimal.Zero
	accounts := map[types.AccountID]bool{}
	for _, line := range entry.Lines {
		debits = debits.Add(line.FunctionalDebit.Amount())
		credits = credits.Add(line.FunctionalCredit.Amount())
		accounts[line.AccountID] = true
	}
	if !debits.Equal(credits) || !accounts[controlID] || !accounts[counterpartID] {
		t.Fatalf("entry is not balanced through the expected accounts: %+v", entry.Lines)
	}
}

func assertP1001AccountSide(t *testing.T, entry ledger.JournalEntry, accountID types.AccountID, debit bool) {
	t.Helper()
	for _, line := range entry.Lines {
		if line.AccountID != accountID {
			continue
		}
		if debit && line.FunctionalDebit.IsZero() || !debit && line.FunctionalCredit.IsZero() {
			t.Fatalf("account %s is on the wrong side in entry %s", accountID, entry.ID)
		}
		return
	}
	t.Fatalf("account %s missing from entry %s", accountID, entry.ID)
}

type p1001UseCaseRecorder struct {
	entries map[types.JournalEntryID]ledger.JournalEntry
	posted  int
}

func (s *p1001UseCaseRecorder) CreateDraft(_ context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	if s.entries == nil {
		s.entries = make(map[types.JournalEntryID]ledger.JournalEntry)
	}
	entry := ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: command.CompanyID, JournalID: command.JournalID,
		Status: ledger.EntryDraft, EffectiveDate: command.EffectiveDate, PostingDate: command.PostingDate,
		Lines: command.Lines,
	}
	s.entries[entry.ID] = entry
	return publicapplication.EntryResult{Entry: entry}, nil
}

func (s *p1001UseCaseRecorder) Validate(context.Context, publicapplication.ValidateEntryCommand) error {
	return nil
}

func (s *p1001UseCaseRecorder) Post(_ context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	entry, ok := s.entries[command.EntryID]
	if !ok {
		return publicapplication.EntryResult{}, ErrNotFound
	}
	entry.Status = ledger.EntryPosted
	s.entries[entry.ID] = entry
	s.posted++
	return publicapplication.EntryResult{Entry: entry}, nil
}

func (s *p1001UseCaseRecorder) Reverse(context.Context, publicapplication.ReverseEntryCommand) (publicapplication.EntryResult, error) {
	return publicapplication.EntryResult{}, nil
}

func (s *p1001UseCaseRecorder) GetEntry(context.Context, publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type p1001ReceivableStore struct {
	values map[string]apisubledger.Receivable
}

func (s *p1001ReceivableStore) SaveReceivable(_ context.Context, companyID types.CompanyID, line apisubledger.Receivable) error {
	if line.CompanyID != companyID {
		return ErrCompanyMismatch
	}
	s.values[line.ID] = line
	return nil
}
func (s *p1001ReceivableStore) GetReceivable(_ context.Context, companyID types.CompanyID, id string) (apisubledger.Receivable, error) {
	line, ok := s.values[id]
	if !ok || line.CompanyID != companyID {
		return apisubledger.Receivable{}, ErrNotFound
	}
	return line, nil
}
func (s *p1001ReceivableStore) ListReceivables(_ context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Receivable, error) {
	var result []apisubledger.Receivable
	for _, line := range s.values {
		if line.CompanyID == companyID && (partnerID == "" || line.PartnerID == partnerID) {
			result = append(result, line)
		}
	}
	return result, nil
}

type p1001PayableStore struct {
	values map[string]apisubledger.Payable
}

func (s *p1001PayableStore) SavePayable(_ context.Context, companyID types.CompanyID, line apisubledger.Payable) error {
	if line.CompanyID != companyID {
		return ErrCompanyMismatch
	}
	s.values[line.ID] = line
	return nil
}
func (s *p1001PayableStore) GetPayable(_ context.Context, companyID types.CompanyID, id string) (apisubledger.Payable, error) {
	line, ok := s.values[id]
	if !ok || line.CompanyID != companyID {
		return apisubledger.Payable{}, ErrNotFound
	}
	return line, nil
}
func (s *p1001PayableStore) ListPayables(_ context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Payable, error) {
	var result []apisubledger.Payable
	for _, line := range s.values {
		if line.CompanyID == companyID && (partnerID == "" || line.PartnerID == partnerID) {
			result = append(result, line)
		}
	}
	return result, nil
}

type p1001BalanceReader struct{ amount decimal.Decimal }

func (r *p1001BalanceReader) GetAccountBalance(context.Context, types.CompanyID, types.AccountID, time.Time, string) (decimal.Decimal, error) {
	return r.amount, nil
}

var _ publicsubledger.ARAPService = (*PublicARAPService)(nil)
var _ publicapplication.Service = (*p1001UseCaseRecorder)(nil)
var _ publicsubledger.ReceivableStore = (*p1001ReceivableStore)(nil)
var _ publicsubledger.PayableStore = (*p1001PayableStore)(nil)
