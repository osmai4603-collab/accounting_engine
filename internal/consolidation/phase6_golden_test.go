package consolidation

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
)

// TestPhase6GoldenConsolidationHoldingThreeSubsidiaries is the Phase 6 golden
// consolidation benchmark: one USD parent and three subsidiaries reporting in
// EUR, GBP and JPY, with reciprocal balances, intercompany trading, unrealized
// inventory profit and acquisition eliminations. Every expected amount below
// was derived independently (outside the engine) from the published closing,
// average and historical rates, then frozen as the audit reference.
func TestPhase6GoldenConsolidationHoldingThreeSubsidiaries(t *testing.T) {
	const (
		parentID = "10000000-0000-4000-8000-000000000001"
		eurID    = "10000000-0000-4000-8000-000000000002"
		gbpID    = "10000000-0000-4000-8000-000000000003"
		jpyID    = "10000000-0000-4000-8000-000000000004"
	)

	periodStart := dt(2026, 1, 1)
	periodEnd := dt(2026, 12, 31)
	historicalDate := dt(2024, 1, 1)

	group := Group{
		ID:                "phase6-holding-group",
		ParentEntityID:    parentID,
		ReportingCurrency: "USD",
		Period:            ReportingPeriod{StartDate: periodStart, EndDate: periodEnd},
		Policies: Policies{
			AccountingPolicyID: "group-accounting-v1", ControlPolicyID: "group-control-v1",
			CurrencyTranslationPolicyID: "ias21-group-v1", EliminationPolicyID: "group-eliminations-v1",
			IntercompanyPolicyID: "group-intercompany-v1", EliminationScope: PolicyScopeConfigured,
			IntercompanyScope: PolicyScopeConfigured,
		},
		Entities: []Entity{
			{ID: parentID, FunctionalCurrency: "USD"},
			{ID: eurID, FunctionalCurrency: "EUR"},
			{ID: gbpID, FunctionalCurrency: "GBP"},
			{ID: jpyID, FunctionalCurrency: "JPY"},
		},
		Accounts: []GroupAccount{
			{ID: "cash", Name: "Cash", Type: AccountAsset},
			{ID: "receivable", Name: "Trade Receivables", Type: AccountAsset},
			{ID: "inventory", Name: "Inventory", Type: AccountAsset},
			{ID: "ppe", Name: "Property, Plant and Equipment", Type: AccountAsset},
			{ID: "investment", Name: "Investments in Subsidiaries", Type: AccountAsset},
			{ID: "goodwill", Name: "Goodwill", Type: AccountAsset},
			{ID: "payable", Name: "Trade Payables", Type: AccountLiability},
			{ID: "lease", Name: "Lease Liabilities", Type: AccountLiability},
			{ID: "share", Name: "Share Capital", Type: AccountEquity},
			{ID: "retained", Name: "Retained Earnings", Type: AccountEquity},
			{ID: "nci", Name: "Non-Controlling Interests", Type: AccountEquity},
			{ID: "cta", Name: "Translation Reserve", Type: AccountEquity},
			{ID: "sales", Name: "Revenue", Type: AccountRevenue},
			{ID: "cogs", Name: "Cost of Sales", Type: AccountExpense},
			{ID: "opex", Name: "Operating Expenses", Type: AccountExpense},
		},
		TranslationReserveAccountID: "cta",
	}

	// Every entity uses the same source chart, so the mappings are generated
	// from one policy table. Balance-sheet accounts translate at closing,
	// equity at historical and profit or loss at average rates.
	type mappingPolicy struct {
		source    string
		group     string
		rateClass RateClass
	}
	policy := []mappingPolicy{
		{"cash", "cash", RateClosing},
		{"ar", "receivable", RateClosing},
		{"inventory", "inventory", RateClosing},
		{"ppe", "ppe", RateClosing},
		{"ap", "payable", RateClosing},
		{"lease", "lease", RateClosing},
		{"share", "share", RateHistorical},
		{"retained", "retained", RateHistorical},
		{"sales", "sales", RateAverage},
		{"cogs", "cogs", RateAverage},
		{"opex", "opex", RateAverage},
	}
	for _, entity := range group.Entities {
		for _, item := range policy {
			group.Mappings = append(group.Mappings, AccountMapping{
				EntityID:        entity.ID,
				SourceAccountID: item.source,
				GroupAccountID:  item.group,
				RateClass:       item.rateClass,
				HistoricalDate:  historicalDate,
			})
		}
	}
	for _, investment := range []string{"investment-eur", "investment-gbp", "investment-jpy"} {
		group.Mappings = append(group.Mappings, AccountMapping{
			EntityID: parentID, SourceAccountID: investment,
			GroupAccountID: "investment", RateClass: RateClosing,
		})
	}

	group.Ownerships = []Ownership{
		{
			ParentEntityID: parentID, SubsidiaryEntityID: eurID, Percentage: dec("1"),
			ControlBasis: "majority voting rights", ControlEvidenceReference: "evidence-eur-control",
			AcquisitionDate: historicalDate, Consideration: dec("300"), FairValueNetAssets: dec("250"),
			InvestmentAccountID: "investment-eur", GoodwillAccountID: "goodwill",
			NCIAccountID: "nci", ParentRetainedEarningsAccountID: "retained",
		},
		{
			ParentEntityID: parentID, SubsidiaryEntityID: gbpID, Percentage: dec("0.8"),
			ControlBasis: "majority voting rights", ControlEvidenceReference: "evidence-gbp-control",
			AcquisitionDate: historicalDate, Consideration: dec("100"), FairValueNetAssets: dec("100"),
			InvestmentAccountID: "investment-gbp", GoodwillAccountID: "goodwill",
			NCIAccountID: "nci", ParentRetainedEarningsAccountID: "retained",
		},
		{
			ParentEntityID: parentID, SubsidiaryEntityID: jpyID, Percentage: dec("0.6"),
			ControlBasis: "majority voting rights", ControlEvidenceReference: "evidence-jpy-control",
			AcquisitionDate: historicalDate, Consideration: dec("50"), FairValueNetAssets: dec("60"),
			InvestmentAccountID: "investment-jpy", GoodwillAccountID: "goodwill",
			NCIAccountID: "nci", ParentRetainedEarningsAccountID: "retained",
		},
	}

	group.Rules = EliminationRules{
		IntercompanyBalances: []IntercompanyBalanceRule{{
			ID: "eur-ar-gbp-ap", ReceivableEntityID: eurID, ReceivableAccountID: "ar",
			PayableEntityID: gbpID, PayableAccountID: "ap",
		}},
		IntercompanyTrading: []IntercompanyTradingRule{{
			ID: "gbp-sales-jpy-cogs", SellerEntityID: gbpID, SalesAccountID: "sales",
			BuyerEntityID: jpyID, ExpenseAccountID: "cogs",
		}},
		UnrealizedInventory: []UnrealizedInventoryRule{{
			ID: "gbp-profit-in-jpy-inventory", SellerEntityID: gbpID,
			InventoryEntityID: jpyID, InventoryAccountID: "inventory",
			ExpenseGroupAccountID: "cogs", UnrealizedProfit: dec("20"),
		}},
	}

	trialBalance := func(entityID, currency string, lines []TrialBalanceLine) TrialBalance {
		return TrialBalance{
			EntityID: entityID, StartDate: periodStart, EndDate: periodEnd,
			Currency: currency, Lines: lines,
		}
	}
	balance := func(account, debit, credit string) TrialBalanceLine {
		line := TrialBalanceLine{AccountID: account}
		if debit != "" {
			line.EndingDebit = dec(debit)
		}
		if credit != "" {
			line.EndingCredit = dec(credit)
		}
		return line
	}
	income := func(account, debit, credit string) TrialBalanceLine {
		line := balance(account, debit, credit)
		line.PeriodDebit, line.PeriodCredit = line.EndingDebit, line.EndingCredit
		return line
	}

	tbs := []TrialBalance{
		trialBalance(parentID, "USD", []TrialBalanceLine{
			balance("cash", "500", ""),
			balance("ar", "220", ""),
			balance("inventory", "100", ""),
			balance("ppe", "900", ""),
			balance("investment-eur", "300", ""),
			balance("investment-gbp", "100", ""),
			balance("investment-jpy", "50", ""),
			balance("ap", "", "150"),
			balance("lease", "", "300"),
			balance("share", "", "1000"),
			balance("retained", "", "600"),
			income("sales", "", "400"),
			income("cogs", "150", ""),
			income("opex", "130", ""),
		}),
		trialBalance(eurID, "EUR", []TrialBalanceLine{
			balance("cash", "300", ""),
			balance("ar", "100", ""),
			balance("inventory", "80", ""),
			balance("ppe", "500", ""),
			balance("ap", "", "60"),
			balance("lease", "", "120"),
			balance("share", "", "400"),
			balance("retained", "", "280"),
			income("sales", "", "300"),
			income("cogs", "100", ""),
			income("opex", "80", ""),
		}),
		trialBalance(gbpID, "GBP", []TrialBalanceLine{
			balance("cash", "200", ""),
			balance("ar", "80", ""),
			balance("inventory", "100", ""),
			balance("ppe", "400", ""),
			balance("ap", "", "88"),
			balance("lease", "", "80"),
			balance("share", "", "300"),
			balance("retained", "", "212"),
			income("sales", "", "250"),
			income("cogs", "80", ""),
			income("opex", "70", ""),
		}),
		trialBalance(jpyID, "JPY", []TrialBalanceLine{
			balance("cash", "100000", ""),
			balance("ar", "50000", ""),
			balance("inventory", "30000", ""),
			balance("ppe", "200000", ""),
			balance("ap", "", "15000"),
			balance("lease", "", "40000"),
			balance("share", "", "150000"),
			balance("retained", "", "130000"),
			income("sales", "", "120000"),
			income("cogs", "40000", ""),
			income("opex", "35000", ""),
		}),
	}

	rates := phase6Rates{values: map[string]decimal.Decimal{
		"EUR:closing": dec("1.10"), "EUR:average": dec("1.08"), "EUR:historical": dec("1.05"),
		"GBP:closing": dec("1.25"), "GBP:average": dec("1.22"), "GBP:historical": dec("1.20"),
		"JPY:closing": dec("0.0067"), "JPY:average": dec("0.0066"), "JPY:historical": dec("0.0065"),
	}}

	result, err := NewEngine(rates).Consolidate(group, tbs)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	wantAccounts := map[string]string{
		"cash": "1750", "receivable": "655", "inventory": "494", "ppe": "3290",
		"investment": "0", "goodwill": "84", "payable": "-316.5", "lease": "-800",
		"share": "-1000", "retained": "-2344.4", "nci": "-1038", "cta": "-125.5",
		"sales": "-1557", "cogs": "375.6", "opex": "532.8",
	}
	for accountID, want := range wantAccounts {
		if got := result.Account(accountID).Amount; !got.Equal(dec(want)) {
			t.Errorf("account %s = %s, want %s", accountID, got, want)
		}
	}

	wantTotals := FinancialTotals{
		Assets: dec("6273"), Liabilities: dec("1116.5"), Equity: dec("4507.9"),
		Revenue: dec("1557"), Expenses: dec("908.4"), NetIncome: dec("648.6"),
		NCIIncome: dec("192"), ParentNetIncome: dec("456.6"),
		BalanceDifference: decimal.Zero, Balanced: true,
	}
	gotTotals := result.Totals
	for _, check := range []struct {
		name      string
		got, want decimal.Decimal
	}{
		{"assets", gotTotals.Assets, wantTotals.Assets},
		{"liabilities", gotTotals.Liabilities, wantTotals.Liabilities},
		{"equity", gotTotals.Equity, wantTotals.Equity},
		{"revenue", gotTotals.Revenue, wantTotals.Revenue},
		{"expenses", gotTotals.Expenses, wantTotals.Expenses},
		{"net income", gotTotals.NetIncome, wantTotals.NetIncome},
		{"NCI income", gotTotals.NCIIncome, wantTotals.NCIIncome},
		{"parent net income", gotTotals.ParentNetIncome, wantTotals.ParentNetIncome},
		{"balance difference", gotTotals.BalanceDifference, wantTotals.BalanceDifference},
	} {
		if !check.got.Equal(check.want) {
			t.Errorf("%s = %s, want %s", check.name, check.got, check.want)
		}
	}
	if gotTotals.Balanced != wantTotals.Balanced {
		t.Errorf("balanced = %v, want %v", gotTotals.Balanced, wantTotals.Balanced)
	}

	wantTranslation := map[string]string{eurID: "-36.4", gbpID: "-28.6", jpyID: "-60.5"}
	if len(result.TranslationAdjustments) != len(wantTranslation) {
		t.Fatalf("translation adjustments = %d, want %d", len(result.TranslationAdjustments), len(wantTranslation))
	}
	for _, adjustment := range result.TranslationAdjustments {
		if !adjustment.Amount.Equal(dec(wantTranslation[adjustment.EntityID])) {
			t.Errorf("translation adjustment for %s = %s, want %s",
				adjustment.EntityID, adjustment.Amount, wantTranslation[adjustment.EntityID])
		}
	}

	if len(result.Entries) != 6 {
		t.Fatalf("elimination entries = %d, want 6", len(result.Entries))
	}
	for _, entry := range result.Entries {
		if !entry.Debit.Equal(entry.Credit) {
			t.Errorf("elimination entry %s is unbalanced: debit %s, credit %s", entry.ID, entry.Debit, entry.Credit)
		}
	}
}

type phase6Rates struct {
	values map[string]decimal.Decimal
}

func (r phase6Rates) Rate(request RateRequest) (decimal.Decimal, error) {
	key := fmt.Sprintf("%s:%s", request.FromCurrency, request.Class)
	rate, ok := r.values[key]
	if !ok {
		return decimal.Zero, fmt.Errorf("missing golden rate %s", key)
	}
	return rate, nil
}
