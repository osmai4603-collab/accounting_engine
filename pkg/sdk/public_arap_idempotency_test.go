package sdk_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	publicsubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

// errARAPOpenItemNotFound stands in for store-level not-found errors; the
// AR/AP service treats any error from the store's getters as a miss.
var errARAPOpenItemNotFound = errors.New("AR/AP open item not found")

// arapIdempotencyFixture wires the full SDK stack over the memory adapter with
// injectable AR/AP stores, so tests can prove GL/subledger atomicity and
// idempotent replay end to end.
type arapIdempotencyFixture struct {
	driver      *memory.Driver
	companyID   types.CompanyID
	journalID   types.JournalID
	arControl   types.AccountID
	sales       types.AccountID
	cash        types.AccountID
	badDebt     types.AccountID
	receivables *arapIdempotencyReceivableStore
	payables    *arapIdempotencyPayableStore
	engine      sdk.Engine
}

func newARAPIdempotencyFixture(t *testing.T) *arapIdempotencyFixture {
	t.Helper()
	driver := memory.New()
	ctx := context.Background()
	masterData := driver.MasterDataStore()

	companyID := types.NewCompanyID()
	journalID := types.NewJournalID()
	arControl, sales := types.NewAccountID(), types.NewAccountID()
	cash, badDebt := types.NewAccountID(), types.NewAccountID()
	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "ARAP-IDEM", LegalName: "AR/AP Idempotency",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: publicmasterdata.JournalID(journalID), CompanyID: companyID, Code: "ARAP", Name: "AR/AP",
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []publicmasterdata.Account{
		{ID: arControl, CompanyID: companyID, Code: "1100", Name: "AR Control", Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: sales, CompanyID: companyID, Code: "4000", Name: "Sales", Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit, Currency: "USD", IsActive: true},
		{ID: cash, CompanyID: companyID, Code: "1000", Name: "Cash", Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: badDebt, CompanyID: companyID, Code: "6100", Name: "Bad Debt", Type: publicmasterdata.AccountTypeExpense, NormalBalance: publicmasterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	yearID := types.NewFiscalYearID()
	yearStart := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: yearStart, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: types.NewFiscalPeriodID(), CompanyID: companyID, FiscalYearID: yearID, Number: 1, Name: "January",
		StartDate: yearStart, EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatal(err)
	}

	fixture := &arapIdempotencyFixture{
		driver: driver, companyID: companyID, journalID: journalID,
		arControl: arControl, sales: sales, cash: cash, badDebt: badDebt,
		receivables: &arapIdempotencyReceivableStore{values: map[string]apisubledger.Receivable{}},
		payables:    &arapIdempotencyPayableStore{values: map[string]apisubledger.Payable{}},
	}
	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(driver.Ports()),
		sdk.WithSubledgerARAP(publicsubledger.ARAPStores{
			Receivables: fixture.receivables,
			Payables:    fixture.payables,
			Balance:     &arapIdempotencyBalanceReader{},
		}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	fixture.engine = engine
	return fixture
}

func (f *arapIdempotencyFixture) service(t *testing.T) publicsubledger.ARAPService {
	t.Helper()
	service, err := sdk.SubledgerARAP(f.engine)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (f *arapIdempotencyFixture) ctxWithKey(key string) context.Context {
	ctx := publicapplication.WithRequestMetadata(context.Background(),
		publicapplication.RequestMetadata{IdempotencyKey: key, SourceSystem: "arap-test"})
	return publicapplication.WithPrincipal(ctx, publicapplication.Principal{
		ActorID: "arap-tester", CompanyIDs: []types.CompanyID{f.companyID},
	})
}

func (f *arapIdempotencyFixture) trialBalance(t *testing.T) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	balance, err := f.driver.GetTrialBalance(context.Background(), f.companyID, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatal(err)
	}
	return balance.TotalDebit.Amount(), balance.TotalCredit.Amount()
}

func arapIdempotentMoney(t *testing.T, value string) types.Money {
	t.Helper()
	money, err := types.NewMoneyFromString(value, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

// TestPublicARAPGLSubledgerAtomicRollback proves F-01: when the subledger save
// fails after the balanced entry was posted, the whole operation rolls back —
// no GL posting survives without its subledger record.
func TestPublicARAPGLSubledgerAtomicRollback(t *testing.T) {
	fixture := newARAPIdempotencyFixture(t)
	service := fixture.service(t)
	fixture.receivables.failSave = true

	_, err := service.CreateReceivableInvoice(fixture.ctxWithKey(""), apisubledger.InvoiceCommand{
		CompanyID: fixture.companyID, PartnerID: "customer-1",
		ControlAccountID: fixture.arControl, CounterAccountID: fixture.sales,
		JournalID: fixture.journalID,
		Amount:    arapIdempotentMoney(t, "100"), FunctionalAmount: arapIdempotentMoney(t, "100"),
		DocumentDate: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		Reference:    "INV-ROLLBACK",
	})
	if err == nil || !errors.Is(err, publicsubledger.ErrPostedARAPPersistenceFailed) {
		t.Fatalf("invoice error = %v, want subledger persistence failure", err)
	}

	debit, credit := fixture.trialBalance(t)
	if !debit.IsZero() || !credit.IsZero() {
		t.Fatalf("trial balance after failed invoice = debit %s / credit %s, want zero (GL must roll back)", debit, credit)
	}
}

// TestPublicARAPIdempotentReplay proves F-04 on the AR/AP surface: a retry
// carrying the same idempotency key and payload replays the original result,
// applies the subledger effects exactly once, and a different payload under
// the same key is rejected as a conflict.
func TestPublicARAPIdempotentReplay(t *testing.T) {
	fixture := newARAPIdempotencyFixture(t)
	service := fixture.service(t)

	invoiceDate := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	command := apisubledger.InvoiceCommand{
		CompanyID: fixture.companyID, PartnerID: "customer-1",
		ControlAccountID: fixture.arControl, CounterAccountID: fixture.sales,
		JournalID: fixture.journalID,
		Amount:    arapIdempotentMoney(t, "100"), FunctionalAmount: arapIdempotentMoney(t, "100"),
		DocumentDate: invoiceDate, DueDate: invoiceDate.AddDate(0, 0, 30),
		Reference: "INV-1",
	}
	first, err := service.CreateReceivableInvoice(fixture.ctxWithKey("invoice-key"), command)
	if err != nil {
		t.Fatalf("first invoice: %v", err)
	}
	if first.Receivable == nil || first.EntryID == (types.JournalEntryID{}) {
		t.Fatal("first invoice returned no evidence")
	}

	// Same key and payload: clean replay, one subledger record, one posting.
	retry, err := service.CreateReceivableInvoice(fixture.ctxWithKey("invoice-key"), command)
	if err != nil {
		t.Fatalf("retried invoice: %v", err)
	}
	if retry.EntryID != first.EntryID || retry.Receivable == nil || retry.Receivable.ID != first.Receivable.ID {
		t.Fatalf("replay diverged: first entry %s item %s, retry entry %s item %+v",
			first.EntryID, first.Receivable.ID, retry.EntryID, retry.Receivable)
	}
	if len(fixture.receivables.values) != 1 {
		t.Fatalf("receivable records after replay = %d, want 1", len(fixture.receivables.values))
	}
	debit, credit := fixture.trialBalance(t)
	if !debit.Equal(decimal.NewFromInt(100)) || !credit.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("trial balance after replay = debit %s / credit %s, want one 100 posting", debit, credit)
	}

	// Same key, different payload: conflict, no second posting.
	changed := command
	changed.Amount = arapIdempotentMoney(t, "250")
	changed.FunctionalAmount = arapIdempotentMoney(t, "250")
	if _, err := service.CreateReceivableInvoice(fixture.ctxWithKey("invoice-key"), changed); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "idempotency") {
		t.Fatalf("conflicting payload error = %v, want idempotency conflict", err)
	}
	debit, credit = fixture.trialBalance(t)
	if !debit.Equal(decimal.NewFromInt(100)) || !credit.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("trial balance after conflict = debit %s / credit %s, want unchanged", debit, credit)
	}

	// Idempotent payment: retry replays and never double-reduces the balance.
	paymentCommand := apisubledger.PaymentCommand{
		CompanyID: fixture.companyID, Kind: apisubledger.SubledgerReceivable,
		CashAccountID: fixture.cash, JournalID: fixture.journalID,
		Amount: arapIdempotentMoney(t, "40"), FunctionalAmount: arapIdempotentMoney(t, "40"),
		PaymentDate: invoiceDate.AddDate(0, 0, 5), Reference: "REC-1",
		Allocations: []apisubledger.PaymentAllocation{{
			OpenItemID: first.Receivable.ID, Amount: arapIdempotentMoney(t, "40"),
		}},
	}
	payment, err := service.RecordPayment(fixture.ctxWithKey("payment-key"), paymentCommand)
	if err != nil {
		t.Fatalf("first payment: %v", err)
	}
	if remaining := fixture.receivables.values[first.Receivable.ID].RemainingAmount.Amount(); !remaining.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("remaining after first payment = %s, want 60", remaining)
	}
	paymentRetry, err := service.RecordPayment(fixture.ctxWithKey("payment-key"), paymentCommand)
	if err != nil {
		t.Fatalf("retried payment: %v", err)
	}
	if paymentRetry.EntryID != payment.EntryID || paymentRetry.Payment.ID != payment.Payment.ID {
		t.Fatalf("payment replay diverged: first %s/%s, retry %s/%s",
			payment.EntryID, payment.Payment.ID, paymentRetry.EntryID, paymentRetry.Payment.ID)
	}
	if remaining := fixture.receivables.values[first.Receivable.ID].RemainingAmount.Amount(); !remaining.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("remaining after payment replay = %s, want 60 (effects applied exactly once)", remaining)
	}

	// Idempotent write-off: retry returns the original entry.
	writeOffCommand := apisubledger.WriteOffCommand{
		CompanyID: fixture.companyID, Kind: apisubledger.SubledgerReceivable,
		OpenItemID: first.Receivable.ID, AdjustmentAccountID: fixture.badDebt,
		JournalID: fixture.journalID,
		Amount:    arapIdempotentMoney(t, "10"), FunctionalAmount: arapIdempotentMoney(t, "10"),
		Date: invoiceDate.AddDate(0, 0, 10), Reference: "WO-1",
	}
	writeOffID, err := service.WriteOff(fixture.ctxWithKey("writeoff-key"), writeOffCommand)
	if err != nil {
		t.Fatalf("first write-off: %v", err)
	}
	if remaining := fixture.receivables.values[first.Receivable.ID].RemainingAmount.Amount(); !remaining.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("remaining after write-off = %s, want 50", remaining)
	}
	writeOffRetry, err := service.WriteOff(fixture.ctxWithKey("writeoff-key"), writeOffCommand)
	if err != nil {
		t.Fatalf("retried write-off: %v", err)
	}
	if writeOffRetry != writeOffID {
		t.Fatalf("write-off replay returned entry %s, want %s", writeOffRetry, writeOffID)
	}
	if remaining := fixture.receivables.values[first.Receivable.ID].RemainingAmount.Amount(); !remaining.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("remaining after write-off replay = %s, want 50", remaining)
	}

	debit, credit = fixture.trialBalance(t)
	// 100 (invoice) + 40 (payment) + 10 (write-off)
	if !debit.Equal(decimal.NewFromInt(150)) || !credit.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("final trial balance = debit %s / credit %s, want 150/150", debit, credit)
	}
	if _, found := fixture.receivables.values[uuid.NewSHA1(uuid.NameSpaceOID, []byte("unrelated")).String()]; found {
		t.Fatal("unexpected receivable record")
	}
}

type arapIdempotencyReceivableStore struct {
	values   map[string]apisubledger.Receivable
	failSave bool
}

func (s *arapIdempotencyReceivableStore) SaveReceivable(_ context.Context, companyID types.CompanyID, line apisubledger.Receivable) error {
	if line.CompanyID != companyID {
		return errors.New("company mismatch")
	}
	if s.failSave {
		return errors.New("injected subledger persistence failure")
	}
	s.values[line.ID] = line
	return nil
}

func (s *arapIdempotencyReceivableStore) GetReceivable(_ context.Context, companyID types.CompanyID, id string) (apisubledger.Receivable, error) {
	line, ok := s.values[id]
	if !ok || line.CompanyID != companyID {
		return apisubledger.Receivable{}, errARAPOpenItemNotFound
	}
	return line, nil
}

func (s *arapIdempotencyReceivableStore) ListReceivables(_ context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Receivable, error) {
	var result []apisubledger.Receivable
	for _, line := range s.values {
		if line.CompanyID == companyID && (partnerID == "" || line.PartnerID == partnerID) {
			result = append(result, line)
		}
	}
	return result, nil
}

type arapIdempotencyPayableStore struct {
	values map[string]apisubledger.Payable
}

func (s *arapIdempotencyPayableStore) SavePayable(_ context.Context, companyID types.CompanyID, line apisubledger.Payable) error {
	if line.CompanyID != companyID {
		return errors.New("company mismatch")
	}
	s.values[line.ID] = line
	return nil
}

func (s *arapIdempotencyPayableStore) GetPayable(_ context.Context, companyID types.CompanyID, id string) (apisubledger.Payable, error) {
	line, ok := s.values[id]
	if !ok || line.CompanyID != companyID {
		return apisubledger.Payable{}, errARAPOpenItemNotFound
	}
	return line, nil
}

func (s *arapIdempotencyPayableStore) ListPayables(_ context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Payable, error) {
	var result []apisubledger.Payable
	for _, line := range s.values {
		if line.CompanyID == companyID && (partnerID == "" || line.PartnerID == partnerID) {
			result = append(result, line)
		}
	}
	return result, nil
}

type arapIdempotencyBalanceReader struct{}

func (r *arapIdempotencyBalanceReader) GetAccountBalance(context.Context, types.CompanyID, types.AccountID, time.Time, string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
