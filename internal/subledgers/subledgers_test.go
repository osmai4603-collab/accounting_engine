package subledgers

import (
	"errors"
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
	companyID := uuid.New()
	if err := bridge.RegisterMapping(SubledgerEventMapping{
		CompanyID: companyID, EventType: "invoice_issued",
		DebitAccountID: debit, CreditAccountID: credit, JournalID: journal,
	}); err != nil {
		t.Fatal(err)
	}
	event := EconomicEvent{
		CompanyID: companyID, IdempotencyKey: "invoice-1", EventType: "invoice_issued",
		Date: time.Now(), Amount: money(t, "100.00"), Reference: "INV-1",
	}
	first, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	draftCount := len(repo.drafts)
	repo.mu.Unlock()
	if first.ID != second.ID || draftCount != 1 || creator.count != 0 {
		t.Fatalf("duplicate event was processed: ids=%s,%s drafts=%d legacy_creator_calls=%d", first.ID, second.ID, draftCount, creator.count)
	}
}

func TestEventIdempotencyIsCompanyScoped(t *testing.T) {
	repo := NewMemoryRepository()
	bridge := NewEventBridge(repo, &draftCreator{})
	companyA, companyB := uuid.New(), uuid.New()
	for _, companyID := range []uuid.UUID{companyA, companyB} {
		if err := bridge.RegisterMapping(SubledgerEventMapping{
			CompanyID: companyID, EventType: "treasury_transfer",
			DebitAccountID: uuid.New(), CreditAccountID: uuid.New(), JournalID: uuid.New(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	event := EconomicEvent{CompanyID: companyA, IdempotencyKey: "event-1", EventType: "treasury_transfer", Amount: money(t, "10")}
	first, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	event.CompanyID = companyB
	second, err := bridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("idempotency keys should not collide across companies")
	}
}

func TestEventBridgeRejectsStoredEventWithoutResult(t *testing.T) {
	repo := NewMemoryRepository()
	event := EconomicEvent{
		CompanyID: uuid.New(), IdempotencyKey: "incomplete-event", EventType: "treasury_transfer",
		Amount: money(t, "10"),
	}
	if err := repo.SaveEvent(event); err != nil {
		t.Fatal(err)
	}
	_, err := NewEventBridge(repo, &draftCreator{}).Process(event)
	if !errors.Is(err, ErrEventResultMissing) {
		t.Fatalf("Process() error = %v, want ErrEventResultMissing", err)
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

func TestCompanyScopedReceivableAging(t *testing.T) {
	repo := NewMemoryRepository()
	companyA, companyB := uuid.New(), uuid.New()
	partner := uuid.New()
	asOf := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	ledgerA := NewReceivableLedgerForCompany(repo, companyA)
	ledgerB := NewReceivableLedgerForCompany(repo, companyB)
	if _, err := ledgerA.AddInvoice(partner, money(t, "25"), asOf, asOf, "A-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerB.AddInvoice(partner, money(t, "75"), asOf, asOf, "B-1"); err != nil {
		t.Fatal(err)
	}
	schedule, err := ledgerA.Aging(asOf, &partner)
	if err != nil {
		t.Fatal(err)
	}
	if !schedule[AgingCurrent].Amount().Equal(decimal.NewFromInt(25)) {
		t.Fatalf("company A receivable aging includes another company: %s", schedule[AgingCurrent])
	}
}

func TestPartialReconciliation(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	partner := uuid.New()
	receivable := OpenLine{CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, Kind: LineReceivable, OriginalAmount: money(t, "100.00"), ResidualAmount: money(t, "100.00")}
	payment := OpenLine{CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, Kind: LinePayable, OriginalAmount: money(t, "100.00"), ResidualAmount: money(t, "100.00")}
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

func TestMultiplePartialMatchesCanBeReversed(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	receivable := OpenLine{
		CompanyID: companyID, ID: uuid.New(), Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	payable := OpenLine{
		CompanyID: companyID, ID: uuid.New(), Kind: LinePayable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	if err := repo.SaveOpenLine(receivable); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(payable); err != nil {
		t.Fatal(err)
	}
	matcher := NewMatchingEngine(repo)
	for _, amount := range []string{"30", "20"} {
		if _, err := matcher.Match(receivable.ID, payable.ID, money(t, amount)); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := repo.GetOpenLine(receivable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Matches) != 2 || !updated.ResidualAmount.Amount().Equal(decimal.NewFromInt(50)) {
		t.Fatalf("unexpected partial matches: matches=%d residual=%s", len(updated.Matches), updated.ResidualAmount)
	}
	if err := matcher.Unreconcile(receivable.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{receivable.ID, payable.ID} {
		line, err := repo.GetOpenLine(id)
		if err != nil {
			t.Fatal(err)
		}
		if !line.ResidualAmount.Equal(money(t, "100")) || len(line.Matches) != 0 || line.Closed {
			t.Fatalf("line was not fully unreconciled: %+v", line)
		}
	}
}

func TestConcurrentReconciliationDoesNotOvermatch(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	receivable := OpenLine{
		CompanyID: companyID, ID: uuid.New(), Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	payable := OpenLine{
		CompanyID: companyID, ID: uuid.New(), Kind: LinePayable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	if err := repo.SaveOpenLine(receivable); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(payable); err != nil {
		t.Fatal(err)
	}
	matcher := NewMatchingEngine(repo)
	results := make(chan error, 2)
	amount := money(t, "60")
	for i := 0; i < 2; i++ {
		go func() {
			_, err := matcher.Match(receivable.ID, payable.ID, amount)
			results <- err
		}()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalidAmount) && !errors.Is(err, ErrConcurrentMatch) {
			t.Fatalf("Match() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful matches = %d, want 1", successes)
	}
	line, err := repo.GetOpenLine(receivable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !line.ResidualAmount.Amount().Equal(decimal.NewFromInt(40)) {
		t.Fatalf("concurrent matches over-applied: residual=%s", line.ResidualAmount)
	}
}

func TestMatchingRejectsCrossCompanyLines(t *testing.T) {
	repo := NewMemoryRepository()
	partner := uuid.New()
	receivable := OpenLine{
		CompanyID: uuid.New(), ID: uuid.New(), PartnerID: &partner, Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	payable := OpenLine{
		CompanyID: uuid.New(), ID: uuid.New(), PartnerID: &partner, Kind: LinePayable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	if err := repo.SaveOpenLine(receivable); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(payable); err != nil {
		t.Fatal(err)
	}
	_, err := NewMatchingEngine(repo).Match(receivable.ID, payable.ID, money(t, "10"))
	if !errors.Is(err, ErrCompanyMismatch) {
		t.Fatalf("Match() error = %v, want company mismatch", err)
	}
}

func TestStraightLineDepreciation(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	asset := FixedAsset{
		CompanyID: uuid.New(),
		ID:        uuid.New(), HistoricalCost: money(t, "12000.00"), SalvageValue: money(t, "0.00"),
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

func TestConcurrentMonthlyDepreciationIsAppliedOnce(t *testing.T) {
	repo := NewMemoryRepository()
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: uuid.New(),
		HistoricalCost: money(t, "12000"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 12, Method: StraightLine,
		InServiceDate:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, err := ledger.MonthlyDepreciation(asset.ID, uuid.New(), asOf)
			results <- err
		}()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrAlreadyExists) && !errors.Is(err, ErrConcurrentAssetUpdate) {
			t.Fatalf("MonthlyDepreciation() error = %v", err)
		}
	}
	updated, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	drafts := len(repo.drafts)
	repo.mu.Unlock()
	if successes != 1 || drafts != 1 ||
		!updated.AccumulatedDepreciation.Amount().Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("depreciation was not applied exactly once: successes=%d drafts=%d accumulated=%s",
			successes, drafts, updated.AccumulatedDepreciation)
	}
}

func TestTreasuryReconciliationDateTolerance(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewTreasuryLedger(repo)
	accountID := uuid.New()
	ledgerDate := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	statementDate := ledgerDate.AddDate(0, 0, 2)
	ledgerItem, err := ledger.RecordItem(accountID, ledgerDate, money(t, "50"), "REF-1", BankItemPending)
	if err != nil {
		t.Fatal(err)
	}
	statementItem := BankItem{ID: uuid.New(), AccountID: accountID, Date: statementDate, Amount: money(t, "50"), Reference: "REF-1"}
	matched, pending, _, err := ledger.ReconcileWithTolerance(accountID, []BankItem{statementItem}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || len(pending) != 0 || matched[0].ID != ledgerItem.ID {
		t.Fatalf("unexpected tolerant reconciliation: matched=%d pending=%d", len(matched), len(pending))
	}
	stored, err := repo.ListBankItems(accountID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Status != BankItemMatched {
		t.Fatalf("bank reconciliation status was not persisted: %+v", stored)
	}
}

func TestCompanyScopedTreasuryReconciliation(t *testing.T) {
	repo := NewMemoryRepository()
	companyA, companyB := uuid.New(), uuid.New()
	accountID := uuid.New()
	date := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	ledgerA := NewTreasuryLedgerForCompany(repo, companyA)
	ledgerB := NewTreasuryLedgerForCompany(repo, companyB)
	itemA, err := ledgerA.RecordItem(accountID, date, money(t, "50"), "REF-A", BankItemPending)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerB.RecordItem(accountID, date, money(t, "75"), "REF-B", BankItemPending); err != nil {
		t.Fatal(err)
	}
	statement := BankItem{ID: uuid.New(), CompanyID: companyA, AccountID: accountID, Date: date, Amount: money(t, "50"), Reference: "REF-A"}
	matched, pending, unrecorded, err := ledgerA.Reconcile(accountID, []BankItem{statement})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != itemA.ID || len(pending) != 0 || len(unrecorded) != 0 {
		t.Fatalf("company-scoped reconciliation returned unexpected rows: matched=%d pending=%d unrecorded=%d",
			len(matched), len(pending), len(unrecorded))
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
		CompanyID:            uuid.New(),
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

	repo.mu.Lock()
	draftCount := len(repo.drafts)
	repo.mu.Unlock()
	if draftCount != 1 || creator.count != 0 {
		t.Fatalf("expected atomic journal draft save: drafts=%d external_creator_calls=%d", draftCount, creator.count)
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
