package subledgers

import (
	"testing"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type draftCreator struct {
	count int
}

func (c *draftCreator) CreateDraft(entry posting.JournalEntry) error {
	c.count++
	return nil
}

func money(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestEventBridgeIdempotency(t *testing.T) {
	repo := NewMemoryRepository()
	creator := &draftCreator{}
	bridge := NewEventBridge(repo, creator)
	debit, credit, journal := uuid.New(), uuid.New(), uuid.New()
	if err := bridge.RegisterMapping(SubledgerEventMapping{EventType: "invoice_issued", DebitAccountID: debit, CreditAccountID: credit, JournalID: journal}); err != nil {
		t.Fatal(err)
	}
	event := EconomicEvent{IdempotencyKey: "invoice-1", EventType: "invoice_issued", Date: time.Now(), Amount: money(t, "100.00"), Reference: "INV-1"}
	first, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || creator.count != 1 {
		t.Fatalf("duplicate event was processed: ids=%s,%s count=%d", first.ID, second.ID, creator.count)
	}
}

func TestReceivableAging(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewReceivableLedger(repo)
	partner := uuid.New()
	asOf := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	dates := []time.Time{
		asOf.AddDate(0, 0, 10),
		asOf.AddDate(0, 0, -15),
		asOf.AddDate(0, 0, -45),
		asOf.AddDate(0, 0, -75),
		asOf.AddDate(0, 0, -120),
	}
	for _, dueDate := range dates {
		if _, err := ledger.AddInvoice(partner, money(t, "100.00"), asOf, dueDate, ""); err != nil {
			t.Fatal(err)
		}
	}
	schedule, err := ledger.Aging(asOf, &partner)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		if !schedule[bucket].Amount().Equal(decimal.NewFromInt(100)) {
			t.Errorf("bucket %s amount = %s", bucket, schedule[bucket].Amount())
		}
	}
}

func TestPartialReconciliation(t *testing.T) {
	repo := NewMemoryRepository()
	partner := uuid.New()
	receivable := OpenLine{ID: uuid.New(), PartnerID: &partner, Kind: LineReceivable, OriginalAmount: money(t, "100.00"), ResidualAmount: money(t, "100.00")}
	payment := OpenLine{ID: uuid.New(), PartnerID: &partner, Kind: LinePayable, OriginalAmount: money(t, "100.00"), ResidualAmount: money(t, "100.00")}
	if err := repo.SaveOpenLine(receivable); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(payment); err != nil {
		t.Fatal(err)
	}
	matcher := NewMatchingEngine(repo)
	matchingNumber, err := matcher.Match(receivable.ID, payment.ID, money(t, "40.00"))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetOpenLine(receivable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if matchingNumber == "" || updated.Closed || !updated.ResidualAmount.Amount().Equal(decimal.NewFromInt(60)) {
		t.Fatalf("unexpected partial match: number=%q residual=%s closed=%v", matchingNumber, updated.ResidualAmount, updated.Closed)
	}
}

func TestStraightLineDepreciation(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewFixedAssetLedger(repo, nil)
	asset := FixedAsset{
		ID: uuid.New(), HistoricalCost: money(t, "12000.00"), SalvageValue: money(t, "0.00"),
		UsefulLifeMonths: 12, Method: StraightLine, InServiceDate: time.Now(),
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	entry, amount, err := ledger.MonthlyDepreciation(asset.ID, uuid.New(), asset.InServiceDate)
	if err != nil {
		t.Fatal(err)
	}
	if amount.Amount().Equal(decimal.NewFromInt(1000)) == false || len(entry.Lines) != 2 {
		t.Fatalf("unexpected depreciation: amount=%s lines=%d", amount, len(entry.Lines))
	}
}

func TestFixedAssetImpairmentIAS36(t *testing.T) {
	repo := NewMemoryRepository()
	creator := &draftCreator{}
	ledger := NewFixedAssetLedger(repo, creator)
	expenseAccount := uuid.New()
	accumulatedAccount := uuid.New()
	journalID := uuid.New()

	asset := FixedAsset{
		ID:                   uuid.New(),
		HistoricalCost:       money(t, "10000.00"),
		SalvageValue:         money(t, "0.00"),
		UsefulLifeMonths:     10,
		Method:               StraightLine,
		InServiceDate:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpenseAccountID:     expenseAccount,
		AccumulatedAccountID: accumulatedAccount,
	}
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}

	loss := money(t, "2500.00")
	asOf := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	entry, err := ledger.ImpairAsset(asset.ID, loss, journalID, asOf)
	if err != nil {
		t.Fatalf("ImpairAsset failed: %v", err)
	}

	if creator.count != 1 {
		t.Fatalf("expected 1 draft journal entry created, got %d", creator.count)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("expected 2 lines in impairment entry, got %d", len(entry.Lines))
	}

	updated, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedBookValue := decimal.NewFromInt(7500)
	if !updated.CurrentBookValue.Amount().Equal(expectedBookValue) {
		t.Fatalf("expected current book value %s, got %s", expectedBookValue, updated.CurrentBookValue.Amount())
	}
}
