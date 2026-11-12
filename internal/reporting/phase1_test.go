package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type rateMemoryRepository struct {
	*memoryRepository
	rates *valuation.RateRegistry
}

func (r rateMemoryRepository) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return r.rates.GetRate(ctx, from, to, date)
}

func (r rateMemoryRepository) GetRateOfType(
	ctx context.Context,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	return r.rates.GetRateOfType(ctx, rateType, from, to, date)
}

func addReportingRate(t *testing.T, rates *valuation.RateRegistry, from, to string, value string, date time.Time, kind valuation.RateType) {
	t.Helper()
	if err := rates.Register(valuation.ExchangeRate{
		FromCurrency: from, ToCurrency: to, Rate: decimal.RequireFromString(value),
		EffectiveDate: date, Type: kind,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCurrencyTranslatorUsesIAS21RatesAndReportsReserve(t *testing.T) {
	date := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	rates := valuation.NewRateRegistry()
	addReportingRate(t, rates, "SAR", "USD", "0.25", date, valuation.ClosingRate)
	addReportingRate(t, rates, "SAR", "USD", "0.20", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), valuation.SpotRate)
	addReportingRate(t, rates, "SAR", "USD", "0.30", date, valuation.AverageRate)
	translator := reporting.NewCurrencyTranslator(rates)
	items, difference, err := translator.TranslateBalanceSheet([]reporting.BalanceSheetItem{
		{Amount: decimal.NewFromInt(100), Classification: reporting.TranslationAsset},
		{Amount: decimal.NewFromInt(20), Classification: reporting.TranslationLiability},
		{Amount: decimal.NewFromInt(60), Classification: reporting.TranslationEquity, HistoricalDate: date},
	}, "SAR", "USD", date)
	if err != nil {
		t.Fatal(err)
	}
	if !items[0].Amount.Equal(decimal.NewFromInt(25)) ||
		!items[1].Amount.Equal(decimal.NewFromInt(5)) ||
		!items[2].Amount.Equal(decimal.NewFromInt(12)) ||
		!difference.Amount().Equal(decimal.NewFromInt(8)) {
		t.Fatalf("unexpected IAS 21 translation: items=%+v reserve=%s", items, difference.Amount())
	}

	cashID, capitalID, revenueID := uuid.New(), uuid.New(), uuid.New()
	repository := &rateMemoryRepository{
		memoryRepository: &memoryRepository{accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
			reportAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		}},
		rates: rates,
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "SAR")},
			posting.JournalEntryLine{AccountID: capitalID, Credit: reportMoney(t, "100", "SAR")},
		),
		reportEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "SAR")},
			posting.JournalEntryLine{AccountID: revenueID, Credit: reportMoney(t, "100", "SAR")},
		),
	}
	income, err := reporting.NewIncomeStatementEngine(repository).Generate(start, date, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !income.Revenue.Equal(decimal.NewFromInt(30)) {
		t.Fatalf("translated revenue = %s, want 30 at average rate", income.Revenue)
	}
	sheet, err := reporting.NewBalanceSheetEngine(repository).Generate(date, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !sheet.Balanced || !sheet.TotalAssets.Equal(decimal.NewFromInt(50)) ||
		!sheet.Equity.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("translated balance sheet did not balance: %+v", sheet)
	}
	trial, err := reporting.NewTrialBalanceEngine(repository).Generate(start, date, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !trial.Balanced {
		t.Fatal("presentation-currency trial balance should balance at transaction-date rates")
	}
}

func TestCurrencyTranslatorRejectsMissingRates(t *testing.T) {
	translator := reporting.NewCurrencyTranslator(nil)
	_, err := translator.TranslateAmount(
		context.Background(), reportMoney(t, "1", "SAR"), "USD", time.Now(), valuation.SpotRate,
	)
	if !errors.Is(err, reporting.ErrCurrencyTranslatorRequired) {
		t.Fatalf("missing rate provider error = %v, want %v", err, reporting.ErrCurrencyTranslatorRequired)
	}
}

func TestPresentationCurrencyDifferenceIsReportedInOCI(t *testing.T) {
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rates := valuation.NewRateRegistry()
	addReportingRate(t, rates, "SAR", "USD", "0.25", end, valuation.ClosingRate)
	addReportingRate(t, rates, "SAR", "USD", "0.20", start.AddDate(0, 0, -1), valuation.SpotRate)
	repository := &rateMemoryRepository{
		memoryRepository: &memoryRepository{accounts: []masterdata.Account{
			reportAccount(uuid.New(), "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(uuid.New(), "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		}},
		rates: rates,
	}
	cashID, capitalID := repository.accounts[0].ID, repository.accounts[1].ID
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "SAR")},
			posting.JournalEntryLine{AccountID: capitalID, Credit: reportMoney(t, "100", "SAR")},
		),
	}

	income, err := reporting.NewIncomeStatementEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !income.TranslationReserve.Equal(decimal.NewFromInt(5)) ||
		!income.OCI.Equal(decimal.NewFromInt(5)) ||
		!income.TotalComprehensiveIncome.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("translation difference not included in OCI: %+v", income)
	}
	sheet, err := reporting.NewBalanceSheetEngine(repository).Generate(end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !sheet.Balanced || !sheet.TranslationReserve.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("balance sheet translation reserve is incorrect: %+v", sheet)
	}
}

func TestCashFlowReportsExchangeEffectOnTranslatedCash(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	rates := valuation.NewRateRegistry()
	addReportingRate(t, rates, "SAR", "USD", "0.20", start.AddDate(0, 0, -1), valuation.ClosingRate)
	addReportingRate(t, rates, "SAR", "USD", "0.20", start, valuation.ClosingRate)
	addReportingRate(t, rates, "SAR", "USD", "0.25", end, valuation.ClosingRate)
	addReportingRate(t, rates, "SAR", "USD", "0.30", end, valuation.AverageRate)
	addReportingRate(t, rates, "SAR", "USD", "0.20", start.AddDate(0, 0, -1), valuation.SpotRate)
	cashID, capitalID := uuid.New(), uuid.New()
	repository := &rateMemoryRepository{
		memoryRepository: &memoryRepository{accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		}},
		rates: rates,
	}
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.Add(-time.Hour),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "SAR")},
			posting.JournalEntryLine{AccountID: capitalID, Credit: reportMoney(t, "100", "SAR")},
		),
	}
	report, err := reporting.NewCashFlowEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Reconciled || !report.EffectOfExchangeRatesOnCash.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("exchange effect not included in cash-flow reconciliation: %+v", report)
	}
}

func TestAdjustedTrialBalanceFiltersAdjustingEntries(t *testing.T) {
	cashID, capitalID, expenseID := uuid.New(), uuid.New(), uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		reportAccount(expenseID, "5000", "Expense", masterdata.AccountTypeExpense, masterdata.SubClassOtherOperatingExpense),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 31)
	regular := reportEntry(t, start.Add(-time.Hour),
		posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "USD")},
		posting.JournalEntryLine{AccountID: capitalID, Credit: reportMoney(t, "100", "USD")},
	)
	adjusting := reportEntry(t, start.Add(time.Hour),
		posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "10", "USD")},
		posting.JournalEntryLine{AccountID: cashID, Credit: reportMoney(t, "10", "USD")},
	)
	adjusting.Classification = posting.EntryClassAdjusting
	repository.entries = []posting.JournalEntry{regular, adjusting}

	unadjusted, err := reporting.NewTrialBalanceEngine(repository).GenerateAdjusted(start, end, "USD", false)
	if err != nil {
		t.Fatal(err)
	}
	adjusted, err := reporting.NewTrialBalanceEngine(repository).GenerateAdjusted(start, end, "USD", true)
	if err != nil {
		t.Fatal(err)
	}
	if !unadjusted.Balanced || !adjusted.Balanced {
		t.Fatal("both trial balances must remain balanced")
	}
	var unadjustedCash, adjustedCash decimal.Decimal
	for _, line := range unadjusted.Lines {
		if line.AccountID == expenseID && !line.EndingDebit.IsZero() {
			t.Fatalf("unadjusted trial balance contains adjusting expense: %+v", line)
		}
		if line.AccountID == cashID {
			unadjustedCash = line.EndingDebit.Sub(line.EndingCredit)
		}
	}
	for _, line := range adjusted.Lines {
		if line.AccountID == expenseID && !line.EndingDebit.Equal(decimal.NewFromInt(10)) {
			t.Fatalf("adjusted expense = %s, want 10", line.EndingDebit)
		}
		if line.AccountID == cashID {
			adjustedCash = line.EndingDebit.Sub(line.EndingCredit)
		}
	}
	if !unadjustedCash.Sub(adjustedCash).Equal(decimal.NewFromInt(10)) {
		t.Fatalf("adjustment changed cash by %s, want 10", unadjustedCash.Sub(adjustedCash))
	}
}

func TestMultiCurrencyTrialBalanceShowsTransactionAndFunctionalAmounts(t *testing.T) {
	cashID, capitalID := uuid.New(), uuid.New()
	repository := &rateMemoryRepository{
		memoryRepository: &memoryRepository{accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Foreign cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		}},
		rates: valuation.NewRateRegistry(),
	}
	date := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	addReportingRate(t, repository.rates, "USD", "SAR", "3.5", date, valuation.ClosingRate)
	foreign, err := posting.NewJournalEntry(uuid.New(), date, "tester", []posting.JournalEntryLine{
		{
			AccountID: cashID, Debit: reportMoney(t, "30", "USD"),
			TransactionAmount: reportMoney(t, "30", "USD"), TransactionCurrency: "USD",
			ExchangeRate:     decimal.RequireFromString("3.3333333333"),
			FunctionalDebit:  reportMoney(t, "100", "SAR"),
			FunctionalCredit: reportMoney(t, "0", "SAR"),
		},
		{
			AccountID: capitalID, Credit: reportMoney(t, "100", "SAR"),
			TransactionAmount: reportMoney(t, "100", "SAR"), TransactionCurrency: "SAR",
			ExchangeRate:     decimal.NewFromInt(1),
			FunctionalDebit:  reportMoney(t, "0", "SAR"),
			FunctionalCredit: reportMoney(t, "100", "SAR"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign.Status = posting.EntryPosted
	repository.entries = []posting.JournalEntry{foreign}

	report, err := reporting.NewTrialBalanceEngine(repository).GenerateMultiCurrency(date, date, "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Balanced || len(report.Rows) != 2 {
		t.Fatalf("unexpected multi-currency trial balance: %+v", report)
	}
	for _, row := range report.Rows {
		if row.AccountID == cashID {
			balance := row.TransactionCurrencyBalances["USD"]
			if !balance.PeriodDebit.Equal(decimal.NewFromInt(30)) ||
				!row.FunctionalBalance.CloseDebit.Equal(decimal.NewFromInt(100)) ||
				!row.UnrealizedFXGainLoss.Equal(decimal.NewFromInt(5)) {
				t.Fatalf("unexpected foreign-currency row: %+v", row)
			}
		}
	}
}

func TestEquityStatementReportsReserveTransfersAndDistributions(t *testing.T) {
	cashID, retainedID, reserveID, payableID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings),
		reportAccount(reserveID, "3200", "Statutory reserve", masterdata.AccountTypeEquity, masterdata.SubClassStatutoryReserve),
		reportAccount(payableID, "2100", "Dividend payable", masterdata.AccountTypeLiability, masterdata.SubClassTradePayables),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 31)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.Add(-time.Hour),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "200", "USD")},
			posting.JournalEntryLine{AccountID: retainedID, Credit: reportMoney(t, "200", "USD")},
		),
		reportEntry(t, start.Add(time.Hour),
			posting.JournalEntryLine{AccountID: retainedID, Debit: reportMoney(t, "50", "USD")},
			posting.JournalEntryLine{AccountID: reserveID, Credit: reportMoney(t, "50", "USD")},
		),
		reportEntry(t, start.Add(2*time.Hour),
			posting.JournalEntryLine{AccountID: retainedID, Debit: reportMoney(t, "20", "USD")},
			posting.JournalEntryLine{AccountID: payableID, Credit: reportMoney(t, "20", "USD")},
		),
	}
	report, err := reporting.NewEquityStatementEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !report.OpeningTotal.Equal(decimal.NewFromInt(200)) ||
		!report.ClosingTotal.Equal(decimal.NewFromInt(180)) {
		t.Fatalf("equity totals = opening %s, closing %s", report.OpeningTotal, report.ClosingTotal)
	}
	for _, movement := range report.Movements {
		switch movement.AccountID {
		case retainedID:
			if !movement.Distributions.Equal(decimal.NewFromInt(20)) ||
				!movement.TransfersToReserve.Equal(decimal.NewFromInt(-50)) {
				t.Fatalf("retained earnings movement = %+v", movement)
			}
		case reserveID:
			if !movement.TransfersToReserve.Equal(decimal.NewFromInt(50)) {
				t.Fatalf("reserve movement = %+v", movement)
			}
		}
	}
}

func TestRatiosUseAverageBalances(t *testing.T) {
	cashID, receivableID, inventoryID := uuid.New(), uuid.New(), uuid.New()
	equityID, revenueID, cogsID := uuid.New(), uuid.New(), uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(receivableID, "1100", "Trade receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables),
		reportAccount(inventoryID, "1200", "Inventory", masterdata.AccountTypeAsset, masterdata.SubClassInventories),
		reportAccount(equityID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		reportAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		reportAccount(cogsID, "5000", "Cost of sales", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.Add(-time.Hour),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "2000", "USD")},
			posting.JournalEntryLine{AccountID: receivableID, Debit: reportMoney(t, "100", "USD")},
			posting.JournalEntryLine{AccountID: inventoryID, Debit: reportMoney(t, "1000", "USD")},
			posting.JournalEntryLine{AccountID: equityID, Credit: reportMoney(t, "3100", "USD")},
		),
		reportEntry(t, start.Add(time.Hour),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "1000", "USD")},
			posting.JournalEntryLine{AccountID: revenueID, Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, start.Add(2*time.Hour),
			posting.JournalEntryLine{AccountID: receivableID, Debit: reportMoney(t, "500", "USD")},
			posting.JournalEntryLine{AccountID: revenueID, Credit: reportMoney(t, "500", "USD")},
		),
		reportEntry(t, start.Add(3*time.Hour),
			posting.JournalEntryLine{AccountID: cogsID, Debit: reportMoney(t, "400", "USD")},
			posting.JournalEntryLine{AccountID: inventoryID, Credit: reportMoney(t, "400", "USD")},
		),
	}
	ratios, err := reporting.NewRatiosEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !ratios.Profitability.ROA.Equal(decimal.RequireFromString("30.14")) ||
		!ratios.Profitability.ROE.Equal(decimal.RequireFromString("30.14")) ||
		!ratios.Activity.ReceivablesTurnover.Equal(decimal.RequireFromString("4.2857")) ||
		!ratios.Activity.InventoryTurnover.Equal(decimal.RequireFromString("0.5")) {
		t.Fatalf("ratios did not use average balances: %+v", ratios)
	}
}
