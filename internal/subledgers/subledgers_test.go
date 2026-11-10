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

func TestEventBridgeBuildsCompoundEntryFromPersistentRule(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	receivables, revenue, vat, journal := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rule := EventAccountingRule{
		CompanyID: companyID, EventType: "invoice_issued", RuleName: "standard invoice", JournalID: journal,
		Lines: []EventLineTemplate{
			{AccountID: receivables, Side: EventLineDebit, AmountFormula: "amount * 1.15"},
			{AccountID: revenue, Side: EventLineCredit, AmountFormula: "amount"},
			{AccountID: vat, Side: EventLineCredit, AmountFormula: "amount * 0.15"},
		},
	}
	firstBridge := NewEventBridge(repo, &draftCreator{})
	if err := firstBridge.RegisterRule(rule); err != nil {
		t.Fatal(err)
	}
	event := EconomicEvent{
		CompanyID: companyID, IdempotencyKey: "compound-1", EventType: "invoice_issued",
		Amount: money(t, "100.00"), Date: time.Now(), Reference: "INV-C1",
	}
	entry, err := firstBridge.Process(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Lines) != 3 || !entry.Lines[0].Debit.Equal(money(t, "115.00")) ||
		!entry.Lines[1].Credit.Equal(money(t, "100.00")) ||
		!entry.Lines[2].Credit.Equal(money(t, "15.00")) {
		t.Fatalf("unexpected compound journal entry: %+v", entry.Lines)
	}

	secondBridge := NewEventBridge(repo, &draftCreator{})
	event.IdempotencyKey = "compound-2"
	event.Reference = "INV-C2"
	persistedEntry, err := secondBridge.Process(event)
	if err != nil {
		t.Fatalf("new bridge could not load persisted accounting rule: %v", err)
	}
	if len(persistedEntry.Lines) != 3 || persistedEntry.JournalID != journal {
		t.Fatalf("persisted rule produced wrong entry: %+v", persistedEntry)
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
		UsefulLifeMonths: 12, Method: StraightLine, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}

	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	entry, amount, err := ledger.MonthlyDepreciation(asset.ID, uuid.New(), asset.InServiceDate)
	if err != nil {
		t.Fatal(err)
	}
	if !amount.Equal(money(t, "1019.1781")) || len(entry.Lines) != 2 {
		t.Fatalf("unexpected depreciation: amount=%s lines=%d", amount, len(entry.Lines))
	}
}

func TestComponentDepreciationCreatesSeparateJournalLines(t *testing.T) {
	repo := NewMemoryRepository()
	assetID, expenseA, accumulatedA, expenseB, accumulatedB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: assetID, HistoricalCost: money(t, "10000"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 8, Method: StraightLine, InServiceDate: start,
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
		Components: []AssetComponent{
			{ID: uuid.New(), Name: "structure", Cost: money(t, "8000"), SalvageValue: money(t, "0"), UsefulLifeMonths: 8, Method: StraightLine, InServiceDate: start, ExpenseAccountID: expenseA, AccumulatedAccountID: accumulatedA},
			{ID: uuid.New(), Name: "motor", Cost: money(t, "2000"), SalvageValue: money(t, "0"), UsefulLifeMonths: 2, Method: StraightLine, InServiceDate: start, ExpenseAccountID: expenseB, AccumulatedAccountID: accumulatedB},
		},
	}
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	entry, amount, err := ledger.MonthlyDepreciation(assetID, uuid.New(), start)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Lines) != 4 || entry.Lines[0].AccountID != expenseA ||
		entry.Lines[2].AccountID != expenseB || amount.Amount().IsZero() {
		t.Fatalf("component schedules did not produce component journal lines: %+v", entry.Lines)
	}
	repo.mu.Lock()
	posted := repo.drafts[entry.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[entry.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmDepreciation(assetID, entry.ID, amount, start); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetFixedAsset(assetID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Components[0].AccumulatedDepreciation.Amount().IsZero() ||
		updated.Components[1].AccumulatedDepreciation.Amount().IsZero() {
		t.Fatalf("component accumulated depreciation was not updated: %+v", updated.Components)
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
		!updated.AccumulatedDepreciation.Amount().IsZero() || !updated.LastDepreciationDate.IsZero() {
		t.Fatalf("draft creation mutated asset or was not idempotent: successes=%d drafts=%d accumulated=%s",
			successes, drafts, updated.AccumulatedDepreciation)
	}
}

func TestMonthlyDepreciationConfirmationAndRejectedDraftRetry(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: uuid.New(),
		HistoricalCost: money(t, "12000"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 12, Method: StraightLine,
		InServiceDate:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	first, amount, err := ledger.MonthlyDepreciation(asset.ID, uuid.New(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.ConfirmDepreciation(asset.ID, first.ID, amount, asOf); !errors.Is(err, posting.ErrEntryNotPosted) {
		t.Fatalf("ConfirmDepreciation() error = %v, want posted-entry error", err)
	}
	unchanged, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.CurrentBookValue.Equal(money(t, "12000")) || !unchanged.AccumulatedDepreciation.Amount().IsZero() {
		t.Fatal("draft depreciation changed the fixed asset")
	}

	repo.mu.Lock()
	delete(repo.drafts, first.ID)
	repo.mu.Unlock()
	second, amount, err := ledger.MonthlyDepreciation(asset.ID, uuid.New(), asOf)
	if err != nil {
		t.Fatalf("MonthlyDepreciation() after rejected draft = %v", err)
	}
	repo.mu.Lock()
	posted := repo.drafts[second.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[second.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmDepreciation(asset.ID, second.ID, amount, asOf); err != nil {
		t.Fatalf("ConfirmDepreciation() = %v", err)
	}
	updated, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.CurrentBookValue.Equal(money(t, "11079.4521")) ||
		!updated.AccumulatedDepreciation.Equal(amount) || !updated.LastDepreciationDate.Equal(asOf) {
		t.Fatalf("confirmed depreciation was not applied: %+v", updated)
	}
	if err := ledger.ConfirmDepreciation(asset.ID, second.ID, amount, asOf); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate confirmation error = %v, want ErrAlreadyExists", err)
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

func TestTreasuryMultiLineReconciliationPostsBankFeeDraft(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, accountID, feeAccountID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for _, amount := range []string{"60", "40"} {
		if _, err := ledger.RecordItemWithJournal(accountID, uuid.New(), date, money(t, amount), "", BankItemPending); err != nil {
			t.Fatal(err)
		}
	}
	unrelated, err := ledger.RecordItemWithJournal(
		accountID, uuid.New(), date.AddDate(0, 0, 20), money(t, "25"), "", BankItemPending,
	)
	if err != nil {
		t.Fatal(err)
	}
	statement := []BankItem{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountID, Date: date, Amount: money(t, "58")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountID, Date: date, Amount: money(t, "40")},
	}
	match, draft, err := ledger.ReconcileMultiLine(accountID, statement, 0, BankFeeParams{
		JournalID: journalID, FeeAccountID: feeAccountID, BankAccountID: accountID, Date: date,
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft == nil || len(match.StatementLineIDs) != 2 || len(match.LedgerItemIDs) != 2 ||
		len(match.JournalEntryIDs) != 2 || !match.BankFeesAmount.Equal(money(t, "2")) ||
		!match.Difference.IsZero() {
		t.Fatalf("unexpected multi-line reconciliation: match=%+v draft=%+v", match, draft)
	}
	stored, err := repo.ListBankItemsForCompany(companyID, accountID.String())
	if err != nil {
		t.Fatal(err)
	}
	matchedCount := 0
	for _, item := range stored {
		if item.ID == unrelated.ID {
			if item.Status != BankItemPending {
				t.Fatalf("unrelated ledger item was marked matched: %+v", item)
			}
			continue
		}
		if item.Status != BankItemMatched {
			t.Fatalf("selected ledger item was not marked matched: %+v", item)
		}
		matchedCount++
	}
	if len(stored) != 3 || matchedCount != 2 {
		t.Fatalf("multi-line reconciliation status was not persisted: %+v", stored)
	}
}

func TestTreasuryMultiLineReconciliationMatchesOneToFiveAndLeavesOthersPending(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, accountID := uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for _, amount := range []string{"10", "20", "30", "15", "25", "7"} {
		if _, err := ledger.RecordItemWithJournal(accountID, uuid.New(), date, money(t, amount), "", BankItemPending); err != nil {
			t.Fatal(err)
		}
	}
	statement := []BankItem{{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID, Date: date, Amount: money(t, "100"),
	}}
	match, draft, err := ledger.ReconcileMultiLine(accountID, statement, 0, BankFeeParams{})
	if err != nil {
		t.Fatal(err)
	}
	if draft != nil || len(match.LedgerItemIDs) != 5 || len(match.StatementLineIDs) != 1 {
		t.Fatalf("unexpected 1:5 match: match=%+v draft=%+v", match, draft)
	}
	stored, err := repo.ListBankItemsForCompany(companyID, accountID.String())
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, item := range stored {
		if item.Status == BankItemMatched {
			matched++
		} else if !item.Amount.Equal(money(t, "7")) {
			t.Fatalf("eligible transaction was left pending: %+v", item)
		}
	}
	if matched != 5 {
		t.Fatalf("matched ledger item count = %d, want 5", matched)
	}
}

func TestTreasuryMultiLineReconciliationRejectsAmbiguousGroups(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, accountID := uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if _, err := ledger.RecordItemWithJournal(accountID, uuid.New(), date, money(t, "50"), "", BankItemPending); err != nil {
			t.Fatal(err)
		}
	}
	statement := []BankItem{{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID, Date: date, Amount: money(t, "50"),
	}}
	if _, _, err := ledger.ReconcileMultiLine(accountID, statement, 0, BankFeeParams{}); err != ErrAmbiguousBankReconciliation {
		t.Fatalf("ReconcileMultiLine() error = %v, want ambiguity error", err)
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
	if !updated.CurrentBookValue.Amount().Equal(decimal.NewFromInt(10000)) ||
		!updated.AccumulatedImpairment.Amount().IsZero() {
		t.Fatalf("impairment draft changed asset before posting: %+v", updated)
	}
	repo.mu.Lock()
	posted := repo.drafts[entry.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[entry.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmImpairment(asset.ID, entry.ID, loss, asOf); err != nil {
		t.Fatalf("ConfirmImpairment() failed: %v", err)
	}
	updated, err = repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.CurrentBookValue.Amount().Equal(decimal.NewFromInt(7500)) ||
		!updated.AccumulatedImpairment.Equal(loss) {
		t.Fatalf("posted impairment was not applied: %+v", updated)
	}
}

func TestFixedAssetRevaluationUsesOCIThenExpense(t *testing.T) {
	repo := NewMemoryRepository()
	assetID, assetAccount, surplusAccount, lossAccount := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: assetID, HistoricalCost: money(t, "10000"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 10, Method: StraightLine, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		AssetAccountID: assetAccount, RevaluationSurplusAccountID: surplusAccount, RevaluationLossAccountID: lossAccount,
	}
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	journalID := uuid.New()
	firstDate := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	increase, err := ledger.RevalueAsset(assetID, journalID, money(t, "12000"), firstDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(increase.Lines) != 2 || increase.Lines[0].AccountID != assetAccount ||
		increase.Lines[1].AccountID != surplusAccount {
		t.Fatalf("revaluation increase was not recognized in OCI: %+v", increase.Lines)
	}
	repo.mu.Lock()
	posted := repo.drafts[increase.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[increase.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmRevaluation(assetID, increase.ID, money(t, "12000"), firstDate); err != nil {
		t.Fatal(err)
	}

	secondDate := firstDate.AddDate(0, 1, 0)
	decrease, err := ledger.RevalueAsset(assetID, journalID, money(t, "8000"), secondDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(decrease.Lines) != 4 || decrease.Lines[0].AccountID != surplusAccount ||
		decrease.Lines[2].AccountID != lossAccount {
		t.Fatalf("revaluation decrease did not consume surplus before P&L: %+v", decrease.Lines)
	}
	repo.mu.Lock()
	posted = repo.drafts[decrease.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[decrease.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmRevaluation(assetID, decrease.ID, money(t, "8000"), secondDate); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetFixedAsset(assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.CurrentBookValue.Equal(money(t, "8000")) ||
		!updated.RevaluationSurplus.Amount().IsZero() ||
		!updated.RevaluationAdjustment.Equal(money(t, "-2000")) {
		t.Fatalf("revaluation result was not persisted correctly: %+v", updated)
	}
}

func TestDisposeAssetOnlyUpdatesAfterPosting(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewFixedAssetLedger(repo, &draftCreator{})
	disposalDate := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: uuid.New(),
		HistoricalCost: money(t, "10000"), SalvageValue: money(t, "0"),
		AccumulatedDepreciation: money(t, "1000"), CurrentBookValue: money(t, "9000"),
		UsefulLifeMonths: 10, Method: StraightLine, InServiceDate: disposalDate,
		AssetAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
		DisposalCashAccountID: uuid.New(),
	}
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	entry, err := ledger.DisposeAsset(asset.ID, disposalDate, money(t, "9000"), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.DisposedAt != nil {
		t.Fatal("disposal draft marked the asset as disposed")
	}

	repo.mu.Lock()
	posted := repo.drafts[entry.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[entry.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmDisposal(asset.ID, entry.ID, disposalDate, money(t, "9000")); err != nil {
		t.Fatalf("ConfirmDisposal() failed: %v", err)
	}
	updated, err := repo.GetFixedAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisposedAt == nil || !updated.DisposedAt.Equal(disposalDate) {
		t.Fatalf("posted disposal did not mark the asset disposed: %+v", updated)
	}
}

func TestMatchWithRealizedFXGainAndLoss(t *testing.T) {
	repo := NewMemoryRepository()
	engine := NewMatchingEngine(repo)
	companyID := uuid.New()
	journalID := uuid.New()
	arAccountID := uuid.New()
	apAccountID := uuid.New()
	fxGainAccountID := uuid.New()
	fxLossAccountID := uuid.New()

	// 1. Test Receivable FX Gain: Invoice @ 3.75, Payment @ 4.00
	invLine := OpenLine{
		CompanyID:      companyID,
		ID:             uuid.New(),
		Kind:           LineReceivable,
		OriginalAmount: money(t, "100.00"),
		ResidualAmount: money(t, "100.00"),
	}
	payLine := OpenLine{
		CompanyID:      companyID,
		ID:             uuid.New(),
		Kind:           LinePayable,
		OriginalAmount: money(t, "100.00"),
		ResidualAmount: money(t, "100.00"),
	}
	if err := repo.SaveOpenLine(invLine); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(payLine); err != nil {
		t.Fatal(err)
	}

	matchNum, fxEntry, err := engine.MatchWithFX(invLine.ID, payLine.ID, money(t, "100.00"), FXMatchingParams{
		JournalID:          journalID,
		Date:               time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		OriginalRate:       decimal.RequireFromString("3.7500"),
		SettlementRate:     decimal.RequireFromString("4.0000"),
		FunctionalCurrency: "SAR",
		ARAPAccountID:      arAccountID,
		FXGainAccountID:    fxGainAccountID,
		FXLossAccountID:    fxLossAccountID,
		Description:        "FX Gain on Invoice Match",
	})
	if err != nil {
		t.Fatalf("MatchWithFX failed: %v", err)
	}
	if matchNum == "" || fxEntry == nil {
		t.Fatal("expected non-nil match and fx entry")
	}
	if len(fxEntry.Lines) != 2 {
		t.Fatalf("expected 2 lines in fx entry, got %d", len(fxEntry.Lines))
	}
	// Check debit AR 25.0000 SAR, credit FX Gain 25.0000 SAR
	expectedGain := decimal.RequireFromString("25.0000")
	if fxEntry.Lines[0].AccountID != arAccountID || !fxEntry.Lines[0].Debit.Amount().Equal(expectedGain) {
		t.Errorf("line 0 mismatch: got account %v debit %s", fxEntry.Lines[0].AccountID, fxEntry.Lines[0].Debit.Amount())
	}
	if fxEntry.Lines[1].AccountID != fxGainAccountID || !fxEntry.Lines[1].Credit.Amount().Equal(expectedGain) {
		t.Errorf("line 1 mismatch: got account %v credit %s", fxEntry.Lines[1].AccountID, fxEntry.Lines[1].Credit.Amount())
	}

	// Verify atomic draft save
	repo.mu.Lock()
	savedDraft, draftExists := repo.drafts[fxEntry.ID]
	repo.mu.Unlock()
	if !draftExists || savedDraft.ID != fxEntry.ID {
		t.Fatalf("fx entry draft was not saved atomically in repository")
	}

	// 2. Test Payable FX Loss: Invoice @ 3.75, Payment @ 4.00 (paying more than invoice)
	billLine := OpenLine{
		CompanyID:      companyID,
		ID:             uuid.New(),
		Kind:           LinePayable,
		OriginalAmount: money(t, "100.00"),
		ResidualAmount: money(t, "100.00"),
	}
	billPayLine := OpenLine{
		CompanyID:      companyID,
		ID:             uuid.New(),
		Kind:           LineReceivable,
		OriginalAmount: money(t, "100.00"),
		ResidualAmount: money(t, "100.00"),
	}
	if err := repo.SaveOpenLine(billLine); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOpenLine(billPayLine); err != nil {
		t.Fatal(err)
	}

	_, lossEntry, err := engine.MatchWithFX(billLine.ID, billPayLine.ID, money(t, "100.00"), FXMatchingParams{
		JournalID:          journalID,
		Date:               time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		OriginalRate:       decimal.RequireFromString("3.7500"),
		SettlementRate:     decimal.RequireFromString("4.0000"),
		FunctionalCurrency: "SAR",
		ARAPAccountID:      apAccountID,
		FXGainAccountID:    fxGainAccountID,
		FXLossAccountID:    fxLossAccountID,
		Description:        "FX Loss on Bill Match",
	})
	if err != nil {
		t.Fatalf("MatchWithFX for payable failed: %v", err)
	}
	if lossEntry == nil {
		t.Fatal("expected non-nil loss entry")
	}
	// Check debit FX Loss 25.0000 SAR, credit AP 25.0000 SAR
	if lossEntry.Lines[0].AccountID != fxLossAccountID || !lossEntry.Lines[0].Debit.Amount().Equal(expectedGain) {
		t.Errorf("loss line 0 mismatch: got account %v debit %s", lossEntry.Lines[0].AccountID, lossEntry.Lines[0].Debit.Amount())
	}
	if lossEntry.Lines[1].AccountID != apAccountID || !lossEntry.Lines[1].Credit.Amount().Equal(expectedGain) {
		t.Errorf("loss line 1 mismatch: got account %v credit %s", lossEntry.Lines[1].AccountID, lossEntry.Lines[1].Credit.Amount())
	}
}

func TestProcessBankFeeAtomic(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	journalID := uuid.New()
	bankAccountID := uuid.New()
	feeExpenseAccountID := uuid.New()

	item, err := ledger.RecordItem(bankAccountID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), money(t, "25.00"), "BANK-FEE-01", BankItemUnrecorded)
	if err != nil {
		t.Fatal(err)
	}

	updatedItem, draft, err := ledger.ProcessBankFeeAtomic(item, BankFeeParams{
		JournalID:     journalID,
		Date:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		FeeAccountID:  feeExpenseAccountID,
		BankAccountID: bankAccountID,
		Description:   "Monthly Service Fee",
	})
	if err != nil {
		t.Fatalf("ProcessBankFeeAtomic failed: %v", err)
	}
	if updatedItem.Status != BankItemMatched {
		t.Errorf("item status = %s, want %s", updatedItem.Status, BankItemMatched)
	}
	if len(draft.Lines) != 2 {
		t.Fatalf("expected 2 lines in bank fee draft, got %d", len(draft.Lines))
	}
	feeAmount := decimal.RequireFromString("25.0000")
	if draft.Lines[0].AccountID != feeExpenseAccountID || !draft.Lines[0].Debit.Amount().Equal(feeAmount) {
		t.Errorf("fee line debit mismatch: got %s", draft.Lines[0].Debit.Amount())
	}
	if draft.Lines[1].AccountID != bankAccountID || !draft.Lines[1].Credit.Amount().Equal(feeAmount) {
		t.Errorf("bank line credit mismatch: got %s", draft.Lines[1].Credit.Amount())
	}

	repo.mu.Lock()
	savedDraft, exists := repo.drafts[draft.ID]
	repo.mu.Unlock()
	if !exists || savedDraft.ID != draft.ID {
		t.Fatal("bank fee draft was not saved atomically in repository")
	}
}
