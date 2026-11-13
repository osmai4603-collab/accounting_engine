package ifrs15

import (
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func money(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testContract(t *testing.T, price string, obligations ...PerformanceObligation) Contract {
	t.Helper()
	contract, err := NewContract(uuid.New(), uuid.New(), money(t, price, "USD"), obligations)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func obligation(start, end string, pattern RecognitionPattern, ssp string) PerformanceObligation {
	parse := func(value string) time.Time {
		date, _ := time.Parse("2006-01-02", value)
		return date
	}
	return PerformanceObligation{
		ID: uuid.New(), Description: "service", StandalonePrice: mustMoney(ssp, "USD"),
		RecognitionPattern: pattern, StartDate: parse(start), EndDate: parse(end),
	}
}

func mustMoney(amount, currency string) financialtypes.Money {
	value, _ := financialtypes.NewMoneyFromString(amount, currency)
	return value
}

func TestRelativeSSPWeightedAllocation(t *testing.T) {
	first := obligation("2025-01-01", "2025-01-01", PointInTime, "800")
	second := obligation("2025-01-01", "2025-12-31", OverTime, "400")
	contract := testContract(t, "1000", first, second)
	if got := contract.AllocatedPrices[first.ID].Amount(); !got.Equal(decimal.NewFromInt(66667).Shift(-2)) {
		t.Errorf("first allocation = %s, want 666.67", got)
	}
	if got := contract.AllocatedPrices[second.ID].Amount(); !got.Equal(decimal.NewFromInt(33333).Shift(-2)) {
		t.Errorf("second allocation = %s, want 333.33", got)
	}
}

func TestAllocationRoundingSumsExactly(t *testing.T) {
	obligations := []PerformanceObligation{
		obligation("2025-01-01", "2025-01-01", PointInTime, "1"),
		obligation("2025-01-01", "2025-01-01", PointInTime, "1"),
		obligation("2025-01-01", "2025-01-01", PointInTime, "1"),
	}
	contract := testContract(t, "0.01", obligations...)
	total := decimal.Zero
	for _, amount := range contract.AllocatedPrices {
		total = total.Add(amount.Amount())
	}
	if !total.Equal(decimal.NewFromFloat(0.01)) {
		t.Fatalf("allocation total = %s, want 0.01", total)
	}
}

func TestDailyAndMonthlyRecognitionSchedules(t *testing.T) {
	dailyObligation := obligation("2024-01-01", "2024-12-31", OverTime, "366")
	dailyContract := testContract(t, "366", dailyObligation)
	daily, err := dailyContract.Schedule(dailyObligation.ID, Daily)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 366 || !daily[59].RecognitionDate.Equal(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily schedule count/date = %d/%s", len(daily), daily[59].RecognitionDate)
	}
	dailyTotal := decimal.Zero
	for _, entry := range daily {
		dailyTotal = dailyTotal.Add(entry.AmountToRecognize.Amount())
	}
	if !dailyTotal.Equal(decimal.NewFromInt(366)) {
		t.Fatalf("daily schedule totals %s, want 366", dailyTotal)
	}

	monthlyObligation := obligation("2024-01-15", "2024-03-10", OverTime, "100")
	monthlyContract := testContract(t, "100", monthlyObligation)
	monthly, err := monthlyContract.Schedule(monthlyObligation.ID, Monthly)
	if err != nil {
		t.Fatal(err)
	}
	wantDates := []time.Time{
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC),
	}
	sum := decimal.Zero
	for i, entry := range monthly {
		if !entry.RecognitionDate.Equal(wantDates[i]) {
			t.Errorf("monthly date %d = %s, want %s", i, entry.RecognitionDate, wantDates[i])
		}
		sum = sum.Add(entry.AmountToRecognize.Amount())
	}
	if len(monthly) != 3 || !sum.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("monthly schedule = %d periods totaling %s", len(monthly), sum)
	}
	statuses := UpdateScheduleStatus(monthly, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC))
	if statuses[0].Status != ScheduleRecognized || statuses[1].Status != ScheduleRecognized ||
		statuses[2].Status != ScheduleDeferred || monthly[0].Status != SchedulePending {
		t.Fatalf("unexpected schedule statuses: %+v", statuses)
	}
}

func TestContractBalancesAndRecognitionJournals(t *testing.T) {
	po := obligation("2025-01-01", "2025-01-31", OverTime, "100")
	contract := testContract(t, "100", po)
	contract, err := contract.ApplyBilling(money(t, "60", "USD"))
	if err != nil {
		t.Fatal(err)
	}
	asset, liability, err := contract.ContractBalances()
	if err != nil || !liability.Amount().Equal(decimal.NewFromInt(60)) || !asset.Amount().IsZero() {
		t.Fatalf("pre-recognition balances: asset=%s liability=%s err=%v", asset, liability, err)
	}
	journal, err := BuildRecognitionJournal(contract, money(t, "80", "USD"), uuid.New(), time.Now(), uuid.New(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Validate(); err != nil {
		t.Fatalf("liability journal is unbalanced: %v", err)
	}
	contract, err = contract.ApplyRecognition(money(t, "80", "USD"))
	if err != nil {
		t.Fatal(err)
	}
	asset, liability, err = contract.ContractBalances()
	if err != nil || !asset.Amount().Equal(decimal.NewFromInt(20)) || !liability.Amount().IsZero() {
		t.Fatalf("post-recognition balances: asset=%s liability=%s err=%v", asset, liability, err)
	}
	assetJournal, err := BuildRecognitionJournal(contract, money(t, "10", "USD"), uuid.New(), time.Now(), uuid.New(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := assetJournal.Validate(); err != nil {
		t.Fatalf("contract asset journal is unbalanced: %v", err)
	}
	if len(assetJournal.Lines) != 2 || !assetJournal.Lines[0].Debit.Amount().Equal(decimal.NewFromInt(10)) {
		t.Fatalf("asset recognition journal has wrong lines: %+v", assetJournal.Lines)
	}
}

func TestRejectsInvalidContractInputs(t *testing.T) {
	po := obligation("2025-02-02", "2025-02-01", OverTime, "10")
	if _, err := NewContract(uuid.New(), uuid.New(), money(t, "10", "USD"), []PerformanceObligation{po}); err == nil {
		t.Fatal("expected invalid obligation period error")
	}

	po = obligation("2025-01-01", "2025-01-01", PointInTime, "10")
	po.StandalonePrice = money(t, "10", "EUR")
	if _, err := NewContract(uuid.New(), uuid.New(), money(t, "10", "USD"), []PerformanceObligation{po}); err == nil {
		t.Fatal("expected currency mismatch error")
	}
	po.StandalonePrice = money(t, "10", "USD")
	if _, err := NewContract(uuid.New(), uuid.New(), money(t, "0", "USD"), []PerformanceObligation{po}); err == nil {
		t.Fatal("expected invalid transaction price error")
	}
	contract := testContract(t, "10", po)
	if _, err := contract.Schedule(uuid.New(), Daily); err != ErrObligationNotFound {
		t.Fatalf("Schedule error = %v, want obligation not found", err)
	}
	if _, err := contract.ApplyBilling(money(t, "11", "USD")); err == nil {
		t.Fatal("expected billing over transaction price to fail")
	}
	if _, err := contract.ApplyRecognition(money(t, "11", "USD")); err != ErrRecognitionExceeded {
		t.Fatalf("recognition error = %v, want recognition exceeded", err)
	}
	if _, err := contract.Schedule(po.ID, "quarterly"); err != ErrInvalidPeriod {
		t.Fatalf("frequency error = %v, want invalid period", err)
	}
}

func TestUnitCoordinatesAdvanceAndRecognitionJournals(t *testing.T) {
	po := obligation("2025-01-01", "2025-01-01", PointInTime, "1")
	contract := testContract(t, "100", po)
	unit, err := NewUnit(UnitConfig{
		JournalID: uuid.New(), CashAccountID: uuid.New(), ContractAssetAccountID: uuid.New(),
		ReceivableAccountID: uuid.New(), ContractLiabilityAccount: uuid.New(), RevenueAccountID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := unit.AddContract(contract); err != nil {
		t.Fatal(err)
	}
	_, advance, err := unit.ReceiveAdvance(contract.ID, money(t, "60", "USD"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := advance.Validate(); err != nil {
		t.Fatalf("advance journal is unbalanced: %v", err)
	}
	updated, recognition, err := unit.RecognizeObligation(contract.ID, po.ID, money(t, "80", "USD"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := recognition.Validate(); err != nil {
		t.Fatalf("recognition journal is unbalanced: %v", err)
	}
	if len(recognition.Lines) != 3 {
		t.Fatalf("recognition journal lines = %d, want liability, asset, revenue", len(recognition.Lines))
	}
	if !updated.RecognizedAmount.Amount().Equal(decimal.NewFromInt(80)) {
		t.Fatalf("recognized amount = %s, want 80", updated.RecognizedAmount)
	}
	_, invoice, err := unit.BillCustomer(contract.ID, money(t, "40", "USD"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := invoice.Validate(); err != nil {
		t.Fatalf("contract liability invoice is unbalanced: %v", err)
	}
	if _, _, err := unit.RecognizeObligation(contract.ID, po.ID, money(t, "21", "USD"), time.Now()); err != ErrRecognitionExceeded {
		t.Fatalf("recognition beyond the obligation allocation error = %v, want %v", err, ErrRecognitionExceeded)
	}
}
