package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestComparativeStatementLineVariance(t *testing.T) {
	line := reporting.ComparativeLine("TOTAL_ASSETS", "Total assets", decimal.NewFromInt(125), decimal.NewFromInt(100))
	if !line.Variance.Equal(decimal.NewFromInt(25)) ||
		!line.VariancePct.Equal(decimal.NewFromInt(25)) ||
		!line.VariancePctAvailable {
		t.Fatalf("comparative line = %+v, want 25 variance and 25 percent", line)
	}

	line = reporting.ComparativeLine("REVENUE", "Revenue", decimal.NewFromInt(7), decimal.Zero)
	if !line.Variance.Equal(decimal.NewFromInt(7)) || line.VariancePctAvailable || !line.VariancePct.IsZero() {
		t.Fatalf("zero-base comparative line = %+v, want unavailable percentage", line)
	}

	segmentID := uuid.New()
	segments := reporting.CompareSegmentDisclosures(
		[]reporting.SegmentDisclosure{{SegmentID: segmentID, Revenue: decimal.NewFromInt(120), Profit: decimal.NewFromInt(30)}},
		[]reporting.SegmentDisclosure{{SegmentID: segmentID, Revenue: decimal.NewFromInt(100), Profit: decimal.NewFromInt(20)}},
	)
	if len(segments) != 1 || !segments[0].Revenue.Variance.Equal(decimal.NewFromInt(20)) ||
		!segments[0].Profit.Variance.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("comparative segment disclosure = %+v", segments)
	}
}

func TestComparativeCashFlowIncludesReconciledClosingCash(t *testing.T) {
	current := reporting.CashFlow{
		OperatingCashFlow: decimal.NewFromInt(40), InvestingCashFlow: decimal.NewFromInt(-10),
		FinancingCashFlow: decimal.NewFromInt(5), NetChangeInCash: decimal.NewFromInt(35),
		OpeningCash: decimal.NewFromInt(15), ClosingCash: decimal.NewFromInt(50), Reconciled: true,
	}
	prior := reporting.CashFlow{
		OperatingCashFlow: decimal.NewFromInt(20), InvestingCashFlow: decimal.NewFromInt(-4),
		FinancingCashFlow: decimal.NewFromInt(2), NetChangeInCash: decimal.NewFromInt(18),
		OpeningCash: decimal.NewFromInt(12), ClosingCash: decimal.NewFromInt(30), Reconciled: true,
	}
	lines := reporting.ComparativeCashFlowLines(current, prior)
	byCode := make(map[string]reporting.ComparativeStatementLine, len(lines))
	for _, line := range lines {
		byCode[line.LineCode] = line
	}
	if !byCode["CLOSING_CASH"].CurrentAmount.Equal(current.ClosingCash) ||
		!byCode["CLOSING_CASH"].PriorAmount.Equal(prior.ClosingCash) {
		t.Fatalf("closing cash comparative line = %+v", byCode["CLOSING_CASH"])
	}
	if !current.NetChangeInCash.Add(current.OpeningCash).Equal(current.ClosingCash) ||
		!prior.NetChangeInCash.Add(prior.OpeningCash).Equal(prior.ClosingCash) {
		t.Fatal("test cash flows must reconcile to their closing balances")
	}
}

func TestComparativePeriodValidation(t *testing.T) {
	now := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	if err := reporting.ValidateComparativePeriods(now, now, now.AddDate(-1, 0, 0), now); err != nil {
		t.Fatalf("valid periods rejected: %v", err)
	}
	if err := reporting.ValidateComparativePeriods(now, time.Time{}, now, now); !errors.Is(err, reporting.ErrInvalidComparativePeriods) {
		t.Fatalf("invalid periods error = %v", err)
	}
}

func TestGenerateComparativeStatementsFor2025And2024(t *testing.T) {
	cash := reportAccount(uuid.New(), "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	equity := reportAccount(uuid.New(), "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital)
	equity.CashFlowActivity = masterdata.CashFlowFinancing
	revenue := reportAccount(uuid.New(), "4000", "Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue)
	revenue.IFRSCategory = masterdata.IFRSCategoryOperating
	repository := &memoryRepository{accounts: []masterdata.Account{cash, equity, revenue}}

	repository.entries = []posting.JournalEntry{
		reportEntry(t, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "1000", "USD")},
			posting.JournalEntryLine{AccountID: equity.ID, Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "200", "USD")},
			posting.JournalEntryLine{AccountID: revenue.ID, Credit: reportMoney(t, "200", "USD")},
		),
		reportEntry(t, time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "300", "USD")},
			posting.JournalEntryLine{AccountID: revenue.ID, Credit: reportMoney(t, "300", "USD")},
		),
	}

	currentStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	currentEnd := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	priorStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	priorEnd := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	bundle, err := reporting.NewBundleEngine(repository).GenerateComparative(
		currentStart, currentEnd, priorStart, priorEnd, "USD",
		[]reporting.NoteDisclosure{{Code: "N1", Title: "Cash", Amount: decimal.NewFromInt(1500)}},
	)
	if err != nil {
		t.Fatalf("GenerateComparative() error = %v", err)
	}
	balanceLines := make(map[string]reporting.ComparativeStatementLine, len(bundle.BalanceSheet.Lines))
	for _, line := range bundle.BalanceSheet.Lines {
		balanceLines[line.LineCode] = line
	}
	totalAssets := balanceLines["TOTAL_ASSETS"]
	if !totalAssets.CurrentAmount.Equal(decimal.NewFromInt(1500)) ||
		!totalAssets.PriorAmount.Equal(decimal.NewFromInt(1200)) ||
		!totalAssets.Variance.Equal(decimal.NewFromInt(300)) ||
		!totalAssets.VariancePct.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("2025/2024 total assets comparison = %+v", totalAssets)
	}
	if !bundle.CashFlow.Current.Reconciled || !bundle.CashFlow.Prior.Reconciled ||
		!bundle.CashFlow.Current.ClosingCash.Equal(totalAssets.CurrentAmount) ||
		!bundle.CashFlow.Prior.ClosingCash.Equal(totalAssets.PriorAmount) {
		t.Fatalf("comparative cash flow does not reconcile to balance sheets: %+v", bundle.CashFlow)
	}
	if !bundle.EquityStatement.Current.ClosingTotal.Equal(totalAssets.CurrentAmount) ||
		!bundle.EquityStatement.Prior.ClosingTotal.Equal(totalAssets.PriorAmount) ||
		len(bundle.EquityStatement.Movements) != 1 {
		t.Fatalf("comparative equity statement = %+v", bundle.EquityStatement)
	}
	if len(bundle.Notes) != 1 || bundle.Notes[0].Currency != "USD" {
		t.Fatalf("note attachments = %+v", bundle.Notes)
	}
}

type traceRepositoryStub struct {
	result reporting.TraceResult
	err    error
}

func (r traceRepositoryStub) TraceFinancialStatementItem(
	ctx context.Context,
	companyID uuid.UUID,
	reportID, lineItem string,
	start, end time.Time,
	currency string,
) (reporting.TraceResult, error) {
	if err := ctx.Err(); err != nil {
		return reporting.TraceResult{}, err
	}
	if companyID == uuid.Nil || reportID != "BALANCE_SHEET" || lineItem != "TOTAL_ASSETS" ||
		currency != "USD" || !start.IsZero() || end.IsZero() {
		return reporting.TraceResult{}, errors.New("unexpected trace scope")
	}
	return r.result, r.err
}

func (r traceRepositoryStub) TraceEconomicEvent(
	ctx context.Context,
	companyID, eventID uuid.UUID,
) (reporting.TraceResult, error) {
	if err := ctx.Err(); err != nil {
		return reporting.TraceResult{}, err
	}
	if companyID == uuid.Nil || eventID == uuid.Nil {
		return reporting.TraceResult{}, errors.New("invalid reverse trace scope")
	}
	return r.result, r.err
}

func TestFinancialTraceabilityUsesCompanyScopedRequestAndContext(t *testing.T) {
	companyID := uuid.New()
	end := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	expected := reporting.TraceResult{
		StatementItem: "TOTAL_ASSETS", AccountIDs: []uuid.UUID{uuid.New()},
		Reconciled: true, LedgerAmount: decimal.NewFromInt(125),
		StatementAmount: decimal.NewFromInt(125),
	}
	unit := reporting.NewFinancialTraceabilityUnit(traceRepositoryStub{result: expected}, companyID, time.Time{}, end, "USD")
	result, err := unit.TraceStatementItem(context.Background(), "BALANCE_SHEET", "TOTAL_ASSETS")
	if err != nil {
		t.Fatalf("TraceStatementItem() error = %v", err)
	}
	if !result.Reconciled || !result.StatementAmount.Equal(decimal.NewFromInt(125)) {
		t.Fatalf("trace result = %+v", result)
	}
	reverse, err := unit.TraceEconomicEvent(context.Background(), uuid.New())
	if err != nil || len(reverse.AccountIDs) != 1 {
		t.Fatalf("TraceEconomicEvent() result = %+v, error %v", reverse, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := unit.TraceStatementItem(ctx, "BALANCE_SHEET", "TOTAL_ASSETS"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled trace error = %v", err)
	}
}

func TestEconomicEventPayloadDecodeKeepsOriginalBytesAvailable(t *testing.T) {
	payload := []byte("{ \"Reference\" : \"INV-1\" }")
	event := reporting.TracedEconomicEvent{EventID: uuid.New(), Payload: payload}
	var decoded struct {
		Reference string
	}
	if err := event.DecodeSourceEventPayload(&decoded); err != nil {
		t.Fatalf("DecodeSourceEventPayload() error = %v", err)
	}
	if decoded.Reference != "INV-1" || string(event.Payload) != string(payload) {
		t.Fatalf("decoded event = %+v, payload %q", decoded, event.Payload)
	}
}
