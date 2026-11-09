package reporting

import (
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/shopspring/decimal"
)

type LiquidityRatios struct {
	CurrentRatio decimal.Decimal
	QuickRatio   decimal.Decimal
	CashRatio    decimal.Decimal
}

type ProfitabilityRatios struct {
	GrossMargin     decimal.Decimal
	OperatingMargin decimal.Decimal
	NetMargin       decimal.Decimal
	ROA             decimal.Decimal
	ROE             decimal.Decimal
}

type ActivityRatios struct {
	AssetTurnover       decimal.Decimal
	ReceivablesTurnover decimal.Decimal
	InventoryTurnover   decimal.Decimal
}

type SolvencyRatios struct {
	DebtToEquity     decimal.Decimal
	DebtToAssets     decimal.Decimal
	InterestCoverage decimal.Decimal
}

type FinancialRatios struct {
	Liquidity     LiquidityRatios
	Profitability ProfitabilityRatios
	Activity      ActivityRatios
	Solvency      SolvencyRatios
}

type RatiosEngine struct{ repository Repository }

func NewRatiosEngine(repository Repository) *RatiosEngine {
	return &RatiosEngine{repository: repository}
}

func (e *RatiosEngine) Generate(start, end time.Time, currency string) (FinancialRatios, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return FinancialRatios{}, err
	}
	balances, err := accountBalances(e.repository, start, end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}
	sheet, err := NewBalanceSheetEngine(e.repository).Generate(end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}

	var cash, receivables, inventory, currentAssets, currentLiabilities decimal.Decimal
	for _, account := range accounts {
		amount := accountBalance(balances[account.ID], account).Abs()
		classification := normalizeClassification(account.SubClassification)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			if account.IsCashEquivalent() {
				cash = cash.Add(amount)
			}
			if strings.Contains(classification, "receivable") {
				receivables = receivables.Add(amount)
			}
			if strings.Contains(classification, "invent") {
				inventory = inventory.Add(amount)
			}
			if strings.Contains(classification, "current") || strings.Contains(classification, "cash") ||
				account.IsCashEquivalent() || strings.Contains(classification, "receivable") || strings.Contains(classification, "invent") {
				currentAssets = currentAssets.Add(amount)
			}
		case masterdata.AccountTypeLiability:
			if account.IsCurrent() {
				currentLiabilities = currentLiabilities.Add(amount)
			}
		}
	}
	if sheet.CurrentAssets.Sign() != 0 {
		currentAssets = sheet.CurrentAssets
	}
	if sheet.CurrentLiabilities.Sign() != 0 {
		currentLiabilities = sheet.CurrentLiabilities
	}

	return FinancialRatios{
		Liquidity: LiquidityRatios{
			CurrentRatio: safeDiv(currentAssets, currentLiabilities),
			QuickRatio:   safeDiv(cash.Add(receivables), currentLiabilities),
			CashRatio:    safeDiv(cash, currentLiabilities),
		},
		Profitability: ProfitabilityRatios{
			GrossMargin:     safePct(income.GrossProfit, income.Revenue),
			OperatingMargin: safePct(income.OperatingProfit, income.Revenue),
			NetMargin:       safePct(income.NetProfit, income.Revenue),
			ROA:             safePct(income.NetProfit, sheet.TotalAssets),
			ROE:             safePct(income.NetProfit, sheet.Equity),
		},
		Activity: ActivityRatios{
			AssetTurnover:       safeDiv(income.Revenue, sheet.TotalAssets),
			ReceivablesTurnover: safeDiv(income.Revenue, receivables),
			InventoryTurnover:   safeDiv(income.CostOfSales, inventory),
		},
		Solvency: SolvencyRatios{
			DebtToEquity:     safeDiv(sheet.TotalLiabilities, sheet.Equity),
			DebtToAssets:     safeDiv(sheet.TotalLiabilities, sheet.TotalAssets),
			InterestCoverage: safeDiv(income.OperatingProfit, income.FinanceCosts.Abs()),
		},
	}, nil
}

func safeDiv(numerator, denominator decimal.Decimal) decimal.Decimal {
	if denominator.IsZero() {
		return decimal.Zero
	}
	return numerator.Div(denominator).Round(4)
}

func safePct(numerator, denominator decimal.Decimal) decimal.Decimal {
	return safeDiv(numerator, denominator).Mul(decimal.NewFromInt(100)).Round(4)
}
