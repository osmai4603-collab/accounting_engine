package consolidation

import (
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

const (
	parentCompanyID = "00000000-0000-4000-8000-000000000001"
	subCompanyID    = "00000000-0000-4000-8000-000000000002"
)

func TestConsolidateAggregatesAndSortsAccounts(t *testing.T) {
	group, tbs := basicFixture()
	result, err := NewEngine(nil).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Totals.Balanced || !result.Totals.Assets.Equal(dec("140")) ||
		!result.Totals.Equity.Equal(dec("140")) {
		t.Fatalf("unexpected consolidated totals: %+v", result.Totals)
	}
	if !sort.SliceIsSorted(result.Accounts, func(i, j int) bool {
		return result.Accounts[i].AccountID < result.Accounts[j].AccountID
	}) {
		t.Fatal("account balances are not deterministically ordered")
	}
}

func TestConsolidateTranslatesByExplicitRateClass(t *testing.T) {
	group, tbs := basicFixture()
	group.ReportingCurrency = "USD"
	group.Entities[1].FunctionalCurrency = "GBP"
	tbs[1].Currency = "GBP"
	group.Mappings = replaceMapping(group.Mappings, AccountMapping{
		EntityID: subCompanyID, SourceAccountID: "cash", GroupAccountID: "cash", RateClass: RateClosing,
	})
	group.Mappings = replaceMapping(group.Mappings, AccountMapping{
		EntityID: subCompanyID, SourceAccountID: "equity", GroupAccountID: "sub-equity",
		RateClass: RateHistorical, HistoricalDate: dt(2024, 1, 1),
	})
	group.Accounts = append(group.Accounts,
		GroupAccount{ID: "revenue", Type: AccountRevenue},
		GroupAccount{ID: "expense", Type: AccountExpense})
	group.Mappings = append(group.Mappings,
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "revenue", GroupAccountID: "revenue", RateClass: RateAverage},
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "expense", GroupAccountID: "expense", RateClass: RateAverage})
	tbs[1].Lines[1].EndingCredit = dec("30")
	tbs[1].Lines = append(tbs[1].Lines,
		TrialBalanceLine{AccountID: "revenue", PeriodCredit: dec("20"), EndingCredit: dec("20")},
		TrialBalanceLine{AccountID: "expense", PeriodDebit: dec("10"), EndingDebit: dec("10")})
	group.TranslationReserveAccountID = "cta"
	group.Accounts = append(group.Accounts, GroupAccount{ID: "cta", Type: AccountEquity})
	rates := &testRates{values: map[RateClass]decimal.Decimal{
		RateClosing: dec("2"), RateAverage: dec("2"), RateHistorical: dec("1.5"),
	}}
	result, err := NewEngine(rates).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Account("cash").Amount.Equal(dec("180")) {
		t.Fatalf("translated cash = %s, want 180", result.Account("cash").Amount)
	}
	if len(result.TranslationAdjustments) != 1 ||
		!result.TranslationAdjustments[0].Amount.Equal(dec("-15")) {
		t.Fatalf("unexpected translation adjustments: %+v", result.TranslationAdjustments)
	}
	if !result.Totals.Balanced {
		t.Fatalf("translation did not balance statements: %+v", result.Totals)
	}
	if len(rates.requests) == 0 || rates.requests[0].Class != RateClosing {
		t.Fatalf("rate classes were not requested: %+v", rates.requests)
	}
	foundHistorical := false
	for _, request := range rates.requests {
		if request.Class == RateHistorical && sameDate(request.AsOfDate, dt(2024, 1, 1)) {
			foundHistorical = true
		}
	}
	if !foundHistorical {
		t.Fatalf("historical rate request missing: %+v", rates.requests)
	}
	foundAverage := false
	for _, request := range rates.requests {
		if request.Class == RateAverage {
			foundAverage = true
		}
	}
	if !foundAverage {
		t.Fatalf("average rate request missing: %+v", rates.requests)
	}
}

func TestConsolidateDoesNotGuessMissingRates(t *testing.T) {
	group, tbs := basicFixture()
	group.Entities[1].FunctionalCurrency = "EUR"
	tbs[1].Currency = "EUR"
	_, err := NewEngine(nil).Consolidate(group, tbs)
	if !errors.Is(err, ErrMissingExchangeRate) {
		t.Fatalf("error = %v, want ErrMissingExchangeRate", err)
	}
}

func TestConsolidateEliminatesIntercompanyBalancesTradingInventoryAndInvestment(t *testing.T) {
	group, tbs := eliminationFixture()
	result, err := NewEngine(nil).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]bool)
	for _, entry := range result.Entries {
		kinds[entry.Kind] = true
		if !entry.Debit.Equal(entry.Credit) {
			t.Errorf("entry %s is unbalanced: %+v", entry.ID, entry)
		}
	}
	for _, kind := range []string{
		"intercompany-ar-ap", "intercompany-sales-expense", "unrealized-inventory-profit",
		"investment-equity-goodwill-nci",
	} {
		if !kinds[kind] {
			t.Errorf("missing %s elimination", kind)
		}
	}
	if !result.Account("receivable").Amount.IsZero() || !result.Account("payable").Amount.IsZero() ||
		!result.Account("sales").Amount.IsZero() || !result.Account("expense").Amount.Equal(dec("5")) {
		t.Fatalf("unexpected post-elimination balances: %+v", result.Accounts)
	}
	if !result.Account("goodwill").Amount.Equal(dec("10")) {
		t.Fatalf("goodwill = %s, want 10", result.Account("goodwill").Amount)
	}
	if !result.Totals.Balanced || !result.Totals.Assets.Equal(dec("1155")) {
		t.Fatalf("consolidated totals did not balance: %+v", result.Totals)
	}
}

func TestConsolidateRecognizesGoodwillAndAllocatesNCI(t *testing.T) {
	group, tbs := basicFixture()
	group.Ownerships[0].Percentage = dec("0.6")
	group.Ownerships[0].Consideration = dec("60")
	group.Ownerships[0].FairValueNetAssets = dec("100")
	group.Ownerships[0].NCIAccountID = "nci"
	group.Accounts = append(group.Accounts, GroupAccount{ID: "nci", Type: AccountEquity})
	group.Ownerships[0].GoodwillAccountID = "goodwill"
	tbs[0].Lines[0].EndingDebit = dec("100")
	tbs[0].Lines[1].EndingDebit = dec("60")
	tbs[0].Lines[2].EndingCredit = dec("160")
	tbs[1].Lines[0].EndingDebit = dec("110")
	tbs[1].Lines[1].EndingCredit = dec("100")
	tbs[1].Lines = append(tbs[1].Lines,
		TrialBalanceLine{AccountID: "revenue", PeriodCredit: dec("20"), EndingCredit: dec("20")},
		TrialBalanceLine{AccountID: "expense", PeriodDebit: dec("10"), EndingDebit: dec("10")})
	group.Accounts = append(group.Accounts,
		GroupAccount{ID: "revenue", Type: AccountRevenue},
		GroupAccount{ID: "expense", Type: AccountExpense})
	group.Mappings = append(group.Mappings,
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "revenue", GroupAccountID: "revenue", RateClass: RateAverage},
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "expense", GroupAccountID: "expense", RateClass: RateAverage})
	group.Ownerships[0].Consideration = dec("60")
	group.Ownerships[0].FairValueNetAssets = dec("100")
	result, err := NewEngine(nil).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Account("goodwill").Amount.IsZero() {
		t.Fatalf("goodwill = %s, want zero under partial-goodwill inputs", result.Account("goodwill").Amount)
	}
	if !result.Account("nci").Amount.Equal(dec("-44")) {
		t.Fatalf("NCI equity balance = %s, want -44", result.Account("nci").Amount)
	}
	if !result.Totals.NCIIncome.Equal(dec("4")) || !result.Totals.ParentNetIncome.Equal(dec("6")) {
		t.Fatalf("incorrect income attribution: %+v", result.Totals)
	}
	if !result.Totals.Balanced {
		t.Fatalf("NCI allocation did not balance: %+v", result.Totals)
	}
	group.Ownerships[0].GoodwillMethod = FullGoodwill
	group.Ownerships[0].NCIAtFairValue = dec("50")
	fullGoodwill, err := NewEngine(nil).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	if !fullGoodwill.Account("goodwill").Amount.Equal(dec("10")) ||
		!fullGoodwill.Account("nci").Amount.Equal(dec("-54")) {
		t.Fatalf("full-goodwill result has goodwill %s and NCI %s, want 10 and -54",
			fullGoodwill.Account("goodwill").Amount, fullGoodwill.Account("nci").Amount)
	}
}

func TestConsolidateSupportsNestedOwnershipChain(t *testing.T) {
	group, tbs := basicFixture()
	grandchildID := "00000000-0000-4000-8000-000000000003"
	group.Entities = append(group.Entities, Entity{ID: grandchildID, FunctionalCurrency: "USD"})
	group.Mappings = append(group.Mappings,
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "investment", GroupAccountID: "investment", RateClass: RateClosing},
		AccountMapping{EntityID: grandchildID, SourceAccountID: "cash", GroupAccountID: "cash", RateClass: RateClosing},
		AccountMapping{EntityID: grandchildID, SourceAccountID: "equity", GroupAccountID: "sub-equity", RateClass: RateHistorical, HistoricalDate: dt(2024, 1, 1)})
	group.Ownerships = append(group.Ownerships, Ownership{
		ParentEntityID: subCompanyID, SubsidiaryEntityID: grandchildID, Percentage: dec("1"),
		ControlBasis: "majority voting rights", ControlEvidenceReference: "evidence-grandchild-control",
		AcquisitionDate: dt(2024, 1, 1), Consideration: dec("10"), FairValueNetAssets: dec("10"),
		InvestmentAccountID: "investment", GoodwillAccountID: "goodwill",
		NCIAccountID: "parent-equity", ParentRetainedEarningsAccountID: "retained",
	})
	tbs[1].Lines = []TrialBalanceLine{
		{AccountID: "cash", EndingDebit: dec("40")},
		{AccountID: "investment", EndingDebit: dec("10")},
		{AccountID: "equity", EndingCredit: dec("50")},
	}
	tbs = append(tbs, TrialBalance{
		EntityID: grandchildID, StartDate: group.Period.StartDate, EndDate: group.Period.EndDate, Currency: "USD",
		Lines: []TrialBalanceLine{{AccountID: "cash", EndingDebit: dec("10")}, {AccountID: "equity", EndingCredit: dec("10")}},
	})
	result, err := NewEngine(nil).Consolidate(group, tbs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Totals.Balanced || !result.Account("investment").Amount.IsZero() {
		t.Fatalf("nested investments were not eliminated in a balanced consolidation: %+v", result.Totals)
	}
}

func TestConsolidateRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Group, []TrialBalance)
		want error
	}{
		{
			name: "invalid currency",
			edit: func(g *Group, _ []TrialBalance) { g.ReportingCurrency = "ZZZ" },
			want: ErrInvalidInput,
		},
		{
			name: "invalid company ID",
			edit: func(g *Group, _ []TrialBalance) { g.Entities[0].ID = "parent" },
			want: ErrInvalidInput,
		},
		{
			name: "missing account mapping",
			edit: func(_ *Group, tbs []TrialBalance) { tbs[0].Lines[0].AccountID = "missing" },
			want: ErrMissingAccountMapping,
		},
		{
			name: "unbalanced trial balance",
			edit: func(_ *Group, tbs []TrialBalance) { tbs[0].Lines[0].EndingDebit = dec("101") },
			want: ErrUnbalancedTrialBalance,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group, tbs := basicFixture()
			test.edit(&group, tbs)
			_, err := NewEngine(nil).Consolidate(group, tbs)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestConsolidateRequiresExplicitPolicyAndControlEvidence(t *testing.T) {
	group, tbs := basicFixture()
	group.Policies.IntercompanyPolicyID = ""
	if _, err := NewEngine(nil).Consolidate(group, tbs); !errors.Is(err, ErrMissingConsolidationPolicy) {
		t.Fatalf("missing policy error = %v", err)
	}

	group, tbs = basicFixture()
	group.Policies.IntercompanyScope = PolicyScopeConfigured
	if _, err := NewEngine(nil).Consolidate(group, tbs); !errors.Is(err, ErrMissingConsolidationPolicy) {
		t.Fatalf("scope without elimination rules error = %v", err)
	}

	group, tbs = basicFixture()
	group.Ownerships[0].ControlEvidenceReference = ""
	if _, err := NewEngine(nil).Consolidate(group, tbs); !errors.Is(err, ErrMissingConsolidationPolicy) {
		t.Fatalf("missing control evidence error = %v", err)
	}
}

func TestConsolidateRejectsUnbalancedJournalAndMissingTranslationReserve(t *testing.T) {
	group, tbs := eliminationFixture()
	group.Rules.IntercompanyBalances[0].PayableAccountID = "cash"
	_, err := NewEngine(nil).Consolidate(group, tbs)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid reciprocal account error = %v", err)
	}

	group, tbs = basicFixture()
	group.Entities[1].FunctionalCurrency = "EUR"
	tbs[1].Currency = "EUR"
	rates := &testRates{values: map[RateClass]decimal.Decimal{
		RateClosing: dec("2"), RateHistorical: dec("1.5"),
	}}
	_, err = NewEngine(rates).Consolidate(group, tbs)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing translation reserve error = %v", err)
	}
}

func basicFixture() (Group, []TrialBalance) {
	group := Group{
		ID: "group", ParentEntityID: parentCompanyID, ReportingCurrency: "USD",
		Period: ReportingPeriod{StartDate: dt(2025, 1, 1), EndDate: dt(2025, 12, 31)},
		Policies: Policies{
			AccountingPolicyID: "accounting-v1", ControlPolicyID: "control-v1",
			CurrencyTranslationPolicyID: "translation-v1", EliminationPolicyID: "elimination-v1",
			IntercompanyPolicyID: "intercompany-v1", EliminationScope: PolicyScopeNone,
			IntercompanyScope: PolicyScopeNone,
		},
		Entities: []Entity{
			{ID: parentCompanyID, FunctionalCurrency: "USD"},
			{ID: subCompanyID, FunctionalCurrency: "USD"},
		},
		Accounts: []GroupAccount{
			{ID: "cash", Type: AccountAsset}, {ID: "investment", Type: AccountAsset},
			{ID: "parent-equity", Type: AccountEquity}, {ID: "sub-equity", Type: AccountEquity},
			{ID: "retained", Type: AccountEquity}, {ID: "goodwill", Type: AccountAsset},
		},
		Mappings: []AccountMapping{
			{EntityID: parentCompanyID, SourceAccountID: "cash", GroupAccountID: "cash", RateClass: RateClosing},
			{EntityID: parentCompanyID, SourceAccountID: "investment", GroupAccountID: "investment", RateClass: RateClosing},
			{EntityID: parentCompanyID, SourceAccountID: "equity", GroupAccountID: "parent-equity", RateClass: RateHistorical, HistoricalDate: dt(2024, 1, 1)},
			{EntityID: subCompanyID, SourceAccountID: "cash", GroupAccountID: "cash", RateClass: RateClosing},
			{EntityID: subCompanyID, SourceAccountID: "equity", GroupAccountID: "sub-equity", RateClass: RateHistorical, HistoricalDate: dt(2024, 1, 1)},
		},
		Ownerships: []Ownership{{
			ParentEntityID: parentCompanyID, SubsidiaryEntityID: subCompanyID, Percentage: dec("1"),
			ControlBasis: "majority voting rights", ControlEvidenceReference: "evidence-sub-ownership",
			AcquisitionDate: dt(2024, 1, 1), Consideration: dec("40"), FairValueNetAssets: dec("40"),
			InvestmentAccountID: "investment", GoodwillAccountID: "goodwill",
			NCIAccountID: "parent-equity", ParentRetainedEarningsAccountID: "retained",
		}},
	}
	tbs := []TrialBalance{
		{
			EntityID: parentCompanyID, StartDate: group.Period.StartDate, EndDate: group.Period.EndDate, Currency: "USD",
			Lines: []TrialBalanceLine{
				{AccountID: "cash", EndingDebit: dec("100")},
				{AccountID: "investment", EndingDebit: dec("40")},
				{AccountID: "equity", EndingCredit: dec("140")},
			},
		},
		{
			EntityID: subCompanyID, StartDate: group.Period.StartDate, EndDate: group.Period.EndDate, Currency: "USD",
			Lines: []TrialBalanceLine{
				{AccountID: "cash", EndingDebit: dec("40")},
				{AccountID: "equity", EndingCredit: dec("40")},
			},
		},
	}
	return group, tbs
}

func eliminationFixture() (Group, []TrialBalance) {
	group, tbs := basicFixture()
	group.Accounts = append(group.Accounts,
		GroupAccount{ID: "receivable", Type: AccountAsset},
		GroupAccount{ID: "inventory", Type: AccountAsset},
		GroupAccount{ID: "payable", Type: AccountLiability},
		GroupAccount{ID: "sales", Type: AccountRevenue},
		GroupAccount{ID: "expense", Type: AccountExpense})
	group.Mappings = append(group.Mappings,
		AccountMapping{EntityID: parentCompanyID, SourceAccountID: "ar", GroupAccountID: "receivable", RateClass: RateClosing},
		AccountMapping{EntityID: parentCompanyID, SourceAccountID: "inventory", GroupAccountID: "inventory", RateClass: RateClosing},
		AccountMapping{EntityID: parentCompanyID, SourceAccountID: "sales", GroupAccountID: "sales", RateClass: RateAverage},
		AccountMapping{EntityID: parentCompanyID, SourceAccountID: "expense", GroupAccountID: "expense", RateClass: RateAverage},
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "ap", GroupAccountID: "payable", RateClass: RateClosing},
		AccountMapping{EntityID: subCompanyID, SourceAccountID: "expense", GroupAccountID: "expense", RateClass: RateAverage})
	tbs[0].Lines = []TrialBalanceLine{
		{AccountID: "cash", EndingDebit: dec("1000")},
		{AccountID: "investment", EndingDebit: dec("100")},
		{AccountID: "ar", EndingDebit: dec("20")},
		{AccountID: "inventory", EndingDebit: dec("50")},
		{AccountID: "sales", EndingCredit: dec("30")},
		{AccountID: "expense"},
		{AccountID: "equity", EndingCredit: dec("1140")},
	}
	tbs[1].Lines = []TrialBalanceLine{
		{AccountID: "cash", EndingDebit: dec("100")},
		{AccountID: "ap", EndingCredit: dec("20")},
		{AccountID: "expense", EndingDebit: dec("30")},
		{AccountID: "equity", EndingCredit: dec("110")},
	}
	group.Ownerships[0].Consideration = dec("120")
	group.Ownerships[0].FairValueNetAssets = dec("110")
	group.BargainPurchaseGainAccountID = "sales"
	group.Rules = EliminationRules{
		IntercompanyBalances: []IntercompanyBalanceRule{{
			ID: "ar-ap", ReceivableEntityID: parentCompanyID, ReceivableAccountID: "ar",
			PayableEntityID: subCompanyID, PayableAccountID: "ap",
		}},
		IntercompanyTrading: []IntercompanyTradingRule{{
			ID: "sales-cost", SellerEntityID: parentCompanyID, SalesAccountID: "sales",
			BuyerEntityID: subCompanyID, ExpenseAccountID: "expense",
		}},
		UnrealizedInventory: []UnrealizedInventoryRule{{
			ID: "inventory-profit", SellerEntityID: parentCompanyID, InventoryEntityID: parentCompanyID, InventoryAccountID: "inventory",
			ExpenseGroupAccountID: "expense", UnrealizedProfit: dec("5"),
		}},
	}
	group.Policies.EliminationScope = PolicyScopeConfigured
	group.Policies.IntercompanyScope = PolicyScopeConfigured
	return group, tbs
}

type testRates struct {
	values   map[RateClass]decimal.Decimal
	requests []RateRequest
}

func (r *testRates) Rate(request RateRequest) (decimal.Decimal, error) {
	r.requests = append(r.requests, request)
	rate, ok := r.values[request.Class]
	if !ok {
		return decimal.Zero, fmt.Errorf("no test rate")
	}
	return rate, nil
}

func (r Result) Account(id string) AccountBalance {
	for _, account := range r.Accounts {
		if account.AccountID == id {
			return account
		}
	}
	return AccountBalance{AccountID: id}
}

func replaceMapping(mappings []AccountMapping, replacement AccountMapping) []AccountMapping {
	for i := range mappings {
		if mappings[i].EntityID == replacement.EntityID && mappings[i].SourceAccountID == replacement.SourceAccountID {
			mappings[i] = replacement
			return mappings
		}
	}
	return append(mappings, replacement)
}

func dec(value string) decimal.Decimal { return decimal.RequireFromString(value) }
func dt(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
