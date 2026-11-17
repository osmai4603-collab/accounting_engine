package ifrs16

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestOperatingLeaseStraightLineSchedule(t *testing.T) {
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 4, 1), Amount: money(t, "200", "USD")},
			{DueDate: date(2026, 10, 1), Amount: money(t, "400", "USD")},
		},
		money(t, "1000", "USD"), money(t, "100", "USD"), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-operating-1")

	assertAmount(t, contract.TotalConsideration, "600")
	assertAmount(t, contract.DepreciableAssetBase, "1100")

	schedule, err := contract.StraightLineSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 2 {
		t.Fatalf("schedule length = %d, want 2", len(schedule))
	}
	// Straight-line income is 300 per period regardless of the uneven cash.
	assertAmount(t, schedule[0].LeaseIncome, "300")
	assertAmount(t, schedule[1].LeaseIncome, "300")
	// Period 1: income 300 exceeds cash 200, so rent receivable accrues.
	assertAmount(t, schedule[0].DeferredMovement, "100")
	assertAmount(t, schedule[0].CumulativeDeferredIncome, "100")
	// Period 2: cash 400 exceeds income 300, so the accrual clears to zero.
	assertAmount(t, schedule[1].DeferredMovement, "-100")
	assertAmount(t, schedule[1].CumulativeDeferredIncome, "0")
	// The retained asset depreciates over the lease term (default periods).
	assertAmount(t, schedule[0].AssetDepreciation, "550")
	assertAmount(t, schedule[1].AssetDepreciation, "550")
	assertAmount(t, schedule[1].AccumulatedDepreciation, "1100")
	assertAmount(t, schedule[1].ClosingAssetCarrying, "0")
}

func TestOperatingLeaseRetainedCarryingWhenUsefulLifeExceedsTerm(t *testing.T) {
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2027, 1, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2028, 1, 1), Amount: money(t, "100", "USD")},
		},
		money(t, "900", "USD"), money(t, "0", "USD"), 4,
	)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := contract.StraightLineSchedule()
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, schedule[1].AssetDepreciation, "225")
	assertAmount(t, schedule[1].AccumulatedDepreciation, "450")
	assertAmount(t, schedule[1].ClosingAssetCarrying, "450")
}

func TestOperatingLeaseEntriesBalance(t *testing.T) {
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 4, 1), Amount: money(t, "200", "USD")},
			{DueDate: date(2026, 10, 1), Amount: money(t, "400", "USD")},
		},
		money(t, "1000", "USD"), money(t, "100", "USD"), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-operating-entry")
	builder := testLessorEntryBuilder(t, testLessorAccounts())
	companyID, journalID := uuid.New(), uuid.New()
	ctx := context.Background()

	idc, err := builder.BuildOperatingInitialDirectCostsEntry(ctx, contract, companyID, journalID, "lessor-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, idc)

	schedule, err := contract.StraightLineSchedule()
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range schedule {
		income, err := builder.BuildOperatingIncomeEntry(ctx, period, companyID, journalID, "lessor-leases", "tester")
		if err != nil {
			t.Fatal(err)
		}
		assertBalanced(t, income)
		depreciation, err := builder.BuildOperatingDepreciationEntry(ctx, period, companyID, journalID, "lessor-leases", "tester")
		if err != nil {
			t.Fatal(err)
		}
		assertBalanced(t, depreciation)
	}
}

func TestOperatingLeaseValidation(t *testing.T) {
	cases := []struct {
		name       string
		payments   []LeasePayment
		assetCost  string
		directCost string
		periods    int
	}{
		{"zero asset cost", []LeasePayment{{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")}}, "0", "0", 0},
		{"negative direct costs", []LeasePayment{{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")}}, "100", "-5", 0},
		{"unordered payments", []LeasePayment{
			{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")},
		}, "100", "0", 0},
		{"negative depreciation periods", []LeasePayment{{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")}}, "100", "0", -1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewOperatingLeaseContract(
				date(2026, 1, 1), test.payments,
				money(t, test.assetCost, "USD"), money(t, test.directCost, "USD"), test.periods,
			); err == nil {
				t.Fatal("expected invalid operating lease error")
			}
		})
	}
}

func TestFinanceLeaseZeroRateExactBalances(t *testing.T) {
	// A zero implicit rate with no initial direct costs produces exact balances:
	// the net investment amortizes by receipts alone down to the residual.
	contract, err := NewFinanceLeaseContract(
		date(2026, 1, 1), decimal.Zero,
		[]LeasePayment{
			{DueDate: date(2026, 12, 31), Amount: money(t, "5000", "USD")},
			{DueDate: date(2027, 12, 31), Amount: money(t, "5000", "USD")},
		},
		money(t, "1000", "USD"), money(t, "0", "USD"), money(t, "8000", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-finance-0")

	assertAmount(t, contract.NetInvestment, "11000")
	assertAmount(t, contract.FairValueEquivalent, "11000")
	assertAmount(t, contract.DerecognitionGainLoss, "3000")

	schedule, err := contract.FinanceIncomeSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 2 {
		t.Fatalf("schedule length = %d, want 2", len(schedule))
	}
	assertAmount(t, schedule[0].FinanceIncome, "0")
	assertAmount(t, schedule[0].PrincipalRecovery, "5000")
	assertAmount(t, schedule[0].ClosingNetInvestment, "6000")
	assertAmount(t, schedule[1].ClosingNetInvestment, "1000")
	assertAmount(t, schedule[1].ClosingNetInvestment, contract.UnguaranteedResidual.Amount().String())
}

func TestFinanceLeaseNetInvestmentAndFinanceIncome(t *testing.T) {
	contract, err := NewFinanceLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.10"),
		[]LeasePayment{
			{DueDate: date(2026, 12, 31), Amount: money(t, "5000", "USD")},
			{DueDate: date(2027, 12, 31), Amount: money(t, "5000", "USD")},
		},
		money(t, "1000", "USD"), money(t, "200", "USD"), money(t, "8000", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-finance-1")

	// Net investment = PV(receipts) + PV(residual) + initial direct costs.
	receiptsPV, err := presentValue(contract.Payments, contract.CommencementDate, contract.ImplicitRate, contract.Currency)
	if err != nil {
		t.Fatal(err)
	}
	residualPV, err := presentValue(
		[]LeasePayment{{DueDate: date(2027, 12, 31), Amount: money(t, "1000", "USD")}},
		contract.CommencementDate, contract.ImplicitRate, contract.Currency)
	if err != nil {
		t.Fatal(err)
	}
	expectedNI := receiptsPV.Amount().Add(residualPV.Amount()).Add(decimal.RequireFromString("200"))
	if !contract.NetInvestment.Amount().Equal(expectedNI) {
		t.Fatalf("net investment = %s, want %s", contract.NetInvestment.Amount(), expectedNI)
	}

	schedule, err := contract.FinanceIncomeSchedule()
	if err != nil {
		t.Fatal(err)
	}
	totalIncome := decimal.Zero
	for _, period := range schedule {
		if period.FinanceIncome.Amount().IsNegative() || period.ClosingNetInvestment.Amount().IsNegative() {
			t.Fatalf("negative finance income or net investment in period %d", period.Period)
		}
		totalIncome = totalIncome.Add(period.FinanceIncome.Amount())
	}
	// Total finance income = total receipts + residual - net investment.
	expectedIncome := decimal.RequireFromString("10000").Add(decimal.RequireFromString("1000")).Sub(contract.NetInvestment.Amount())
	if !totalIncome.Round(scale).Equal(expectedIncome.Round(scale)) {
		t.Fatalf("total finance income = %s, want %s", totalIncome, expectedIncome)
	}
	// The final closing balance equals the unguaranteed residual value.
	assertAmount(t, schedule[len(schedule)-1].ClosingNetInvestment, "1000")
}

func TestFinanceLeaseEntriesBalanceAndIdempotency(t *testing.T) {
	contract, err := NewFinanceLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.10"),
		[]LeasePayment{
			{DueDate: date(2026, 12, 31), Amount: money(t, "5000", "USD")},
			{DueDate: date(2027, 12, 31), Amount: money(t, "5000", "USD")},
		},
		money(t, "1000", "USD"), money(t, "200", "USD"), money(t, "8000", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-finance-entry")
	accounts := testLessorAccounts()
	builder := testLessorEntryBuilder(t, accounts)
	companyID, journalID := uuid.New(), uuid.New()
	ctx := context.Background()

	commencement, err := builder.BuildFinanceCommencementEntry(ctx, contract, companyID, journalID, "lessor-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, commencement)
	retry, err := builder.BuildFinanceCommencementEntry(ctx, contract, companyID, journalID, "lessor-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if commencement.ID != retry.ID || commencement.IdempotencyKey != retry.IdempotencyKey {
		t.Fatal("retry did not reproduce the same finance commencement identity")
	}
	if commencement.SourceModule != "ifrs16" || commencement.Reference != contract.Reference {
		t.Fatalf("commencement traceability fields are incomplete: %+v", commencement)
	}

	schedule, err := contract.FinanceIncomeSchedule()
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range schedule {
		receipt, err := builder.BuildFinanceReceiptEntry(ctx, period, companyID, journalID, "lessor-leases", "tester")
		if err != nil {
			t.Fatal(err)
		}
		assertBalanced(t, receipt)
	}
}

func TestFinanceLeaseValidation(t *testing.T) {
	valid := []LeasePayment{{DueDate: date(2026, 12, 31), Amount: money(t, "5000", "USD")}}
	cases := []struct {
		name  string
		rate  decimal.Decimal
		carry string
	}{
		{"negative implicit rate", decimal.NewFromInt(-1), "8000"},
		{"negative asset carrying", decimal.Zero, "-10"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewFinanceLeaseContract(
				date(2026, 1, 1), test.rate, valid,
				money(t, "1000", "USD"), money(t, "200", "USD"), money(t, test.carry, "USD"),
			); err == nil {
				t.Fatal("expected invalid finance lease error")
			}
		})
	}
}

func TestLessorEntryBuilderRequiresCapabilityAndMapping(t *testing.T) {
	if _, err := NewLessorEntryBuilder(testLessorPostingCapability{accounts: testLessorAccounts()}); err != ErrPostingCapabilityUnavailable {
		t.Fatalf("NewLessorEntryBuilder without posting capability error = %v", err)
	}
	builder := testLessorEntryBuilder(t, testLessorAccounts())
	contract := testOperatingContract(t)
	if _, err := builder.BuildOperatingIncomeEntry(
		context.Background(), StraightLineLeasePeriod{LeaseReference: contract.Reference}, uuid.New(), uuid.New(), "", "tester",
	); err != ErrJournalMappingUnavailable {
		t.Fatalf("BuildOperatingIncomeEntry without mapping error = %v", err)
	}
}

func TestFinanceLeaseDerecognitionLossEntry(t *testing.T) {
	// Carrying amount above the fair-value equivalent produces a derecognition loss.
	contract, err := NewFinanceLeaseContract(
		date(2026, 1, 1), decimal.Zero,
		[]LeasePayment{{DueDate: date(2026, 12, 31), Amount: money(t, "11000", "USD")}},
		money(t, "0", "USD"), money(t, "0", "USD"), money(t, "12000", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-finance-loss")

	assertAmount(t, contract.DerecognitionGainLoss, "-1000")
	entry, err := testLessorEntryBuilder(t, testLessorAccounts()).BuildFinanceCommencementEntry(
		context.Background(), contract, uuid.New(), uuid.New(), "lessor-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
}

func TestFinanceLeaseCommencementRequiresAccounts(t *testing.T) {
	contract, err := NewFinanceLeaseContract(
		date(2026, 1, 1), decimal.Zero,
		[]LeasePayment{{DueDate: date(2026, 12, 31), Amount: money(t, "1000", "USD")}},
		money(t, "0", "USD"), money(t, "50", "USD"), money(t, "800", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-finance-accounts")
	accounts := testLessorAccounts()
	builder := testLessorEntryBuilder(t, accounts)
	companyID, journalID := uuid.New(), uuid.New()

	for name, mutate := range map[string]func(*LessorAccounts){
		"missing net investment account": func(a *LessorAccounts) { a.NetInvestmentInLease = uuid.Nil },
		"missing derecognition gain":     func(a *LessorAccounts) { a.DerecognitionGain = uuid.Nil },
		"missing cash for direct costs":  func(a *LessorAccounts) { a.Cash = uuid.Nil },
	} {
		t.Run(name, func(t *testing.T) {
			broken := accounts
			mutate(&broken)
			brokenBuilder := testLessorEntryBuilder(t, broken)
			if _, err := brokenBuilder.BuildFinanceCommencementEntry(
				context.Background(), contract, companyID, journalID, "lessor-leases", "tester",
			); err == nil {
				t.Fatal("expected invalid lessor accounts error")
			}
		})
	}
	_ = builder
}

func TestOperatingDepreciationRejectsZeroPeriod(t *testing.T) {
	// A depreciation horizon shorter than the lease term leaves later schedule
	// periods with no depreciation due; building a draft for them must fail.
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 7, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2027, 1, 1), Amount: money(t, "100", "USD")},
		},
		money(t, "1000", "USD"), money(t, "0", "USD"), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-operating-zero")
	schedule, err := contract.StraightLineSchedule()
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, schedule[1].AssetDepreciation, "0")
	if _, err := testLessorEntryBuilder(t, testLessorAccounts()).BuildOperatingDepreciationEntry(
		context.Background(), schedule[1], uuid.New(), uuid.New(), "lessor-leases", "tester",
	); err != ErrAssetDepreciationUnav {
		t.Fatalf("BuildOperatingDepreciationEntry error = %v, want %v", err, ErrAssetDepreciationUnav)
	}
}

func TestOperatingIncomeEntryRequiresContraAccounts(t *testing.T) {
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 7, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2027, 1, 1), Amount: money(t, "300", "USD")},
		},
		money(t, "1000", "USD"), money(t, "0", "USD"), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("lessor-operating-contra")
	schedule, err := contract.StraightLineSchedule()
	if err != nil {
		t.Fatal(err)
	}
	// Period 1: income 200 exceeds cash 100 (positive movement, accrued rent).
	// Period 2: cash 300 exceeds income 200 (negative movement, deferred income).
	companyID, journalID := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		period  StraightLineLeasePeriod
		missing func(*LessorAccounts)
	}{
		{"missing accrued rent receivable", schedule[0], func(a *LessorAccounts) { a.AccruedRentReceivable = uuid.Nil }},
		{"missing deferred lease income", schedule[1], func(a *LessorAccounts) { a.DeferredLeaseIncome = uuid.Nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			broken := testLessorAccounts()
			test.missing(&broken)
			builder := testLessorEntryBuilder(t, broken)
			if _, err := builder.BuildOperatingIncomeEntry(
				context.Background(), test.period, companyID, journalID, "lessor-leases", "tester",
			); err == nil {
				t.Fatal("expected invalid lessor accounts error")
			}
		})
	}
}

type testLessorPostingCapability struct {
	accounts  LessorAccounts
	available bool
}

func (c testLessorPostingCapability) LedgerPostingAvailable() bool { return c.available }

func (c testLessorPostingCapability) ResolveLessorAccounts(
	_ context.Context, _ uuid.UUID, mappingID string,
) (LessorAccounts, error) {
	if mappingID != "lessor-leases" {
		return LessorAccounts{}, ErrJournalMappingUnavailable
	}
	return c.accounts, nil
}

func testLessorEntryBuilder(t *testing.T, accounts LessorAccounts) *LessorEntryBuilder {
	t.Helper()
	builder, err := NewLessorEntryBuilder(testLessorPostingCapability{accounts: accounts, available: true})
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func testLessorAccounts() LessorAccounts {
	return LessorAccounts{
		LeasedAsset: uuid.New(), AccumulatedDepreciation: uuid.New(), NetInvestmentInLease: uuid.New(),
		Cash: uuid.New(), OperatingLeaseIncome: uuid.New(), FinanceIncome: uuid.New(),
		DepreciationExpense: uuid.New(), DeferredLeaseIncome: uuid.New(), AccruedRentReceivable: uuid.New(),
		DerecognitionGain: uuid.New(), DerecognitionLoss: uuid.New(),
	}
}

func testOperatingContract(t *testing.T) OperatingLeaseContract {
	t.Helper()
	contract, err := NewOperatingLeaseContract(
		date(2026, 1, 1),
		[]LeasePayment{{DueDate: date(2026, 4, 1), Amount: money(t, "200", "USD")}},
		money(t, "1000", "USD"), money(t, "0", "USD"), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	return contract.WithReference("lessor-operating-helper")
}
