package reporting

import (
	"context"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
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
	endingBalances, err := accountBalances(e.repository, time.Time{}, end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}
	var openingBalances map[uuid.UUID]accountSide
	if !start.IsZero() {
		openingBalances, err = accountBalances(e.repository, time.Time{}, start.Add(-time.Nanosecond), currency)
		if err != nil {
			return FinancialRatios{}, err
		}
	} else {
		openingBalances = make(map[uuid.UUID]accountSide)
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}
	sheet, err := NewBalanceSheetEngine(e.repository).Generate(end, currency)
	if err != nil {
		return FinancialRatios{}, err
	}

	openingTotals, err := ratioBalanceTotals(e.repository, accounts, openingBalances, start, currency)
	if err != nil {
		return FinancialRatios{}, err
	}
	endingTotals := ratioTotals{
		assets: sheet.TotalAssets,
		equity: sheet.Equity,
	}
	var cash, endingReceivables, endingInventory decimal.Decimal
	for _, account := range accounts {
		amount := adjustedAccountBalance(endingBalances[account.ID], account)
		classification := normalizeClassification(account.SubClassification)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			if account.EffectiveIsCashEquivalent() {
				cash = cash.Add(amount)
			}
			if classification == masterdata.SubClassTradeReceivables ||
				classification == masterdata.SubClassAccountsReceivable {
				endingReceivables = endingReceivables.Add(amount)
			}
			if classification == masterdata.SubClassInventories {
				endingInventory = endingInventory.Add(amount)
			}
		}
	}
	averageAssets := openingTotals.assets.Add(endingTotals.assets).Div(decimal.NewFromInt(2))
	averageEquity := openingTotals.equity.Add(endingTotals.equity).Div(decimal.NewFromInt(2))
	averageReceivables := openingTotals.receivables.Add(endingReceivables).Div(decimal.NewFromInt(2))
	averageInventory := openingTotals.inventory.Add(endingInventory).Div(decimal.NewFromInt(2))

	return FinancialRatios{
		Liquidity: LiquidityRatios{
			CurrentRatio: safeDiv(sheet.CurrentAssets, sheet.CurrentLiabilities),
			QuickRatio:   safeDiv(cash.Add(endingReceivables), sheet.CurrentLiabilities),
			CashRatio:    safeDiv(cash, sheet.CurrentLiabilities),
		},
		Profitability: ProfitabilityRatios{
			GrossMargin:     safePct(income.GrossProfit, income.Revenue),
			OperatingMargin: safePct(income.OperatingProfit, income.Revenue),
			NetMargin:       safePct(income.NetProfit, income.Revenue),
			ROA:             safePct(income.NetProfit, averageAssets),
			ROE:             safePct(income.NetProfit, averageEquity),
		},
		Activity: ActivityRatios{
			AssetTurnover:       safeDiv(income.Revenue, averageAssets),
			ReceivablesTurnover: safeDiv(income.Revenue, averageReceivables),
			InventoryTurnover:   safeDiv(income.CostOfSales, averageInventory),
		},
		Solvency: SolvencyRatios{
			DebtToEquity:     safeDiv(sheet.TotalLiabilities, sheet.Equity),
			DebtToAssets:     safeDiv(sheet.TotalLiabilities, sheet.TotalAssets),
			InterestCoverage: safeDiv(income.OperatingProfit, income.FinanceCosts.Abs()),
		},
	}, nil
}

type ratioTotals struct {
	assets      decimal.Decimal
	equity      decimal.Decimal
	receivables decimal.Decimal
	inventory   decimal.Decimal
}

func ratioBalanceTotals(
	repository Repository,
	accounts []masterdata.Account,
	balances map[uuid.UUID]accountSide,
	asOf time.Time,
	currency string,
) (ratioTotals, error) {
	totals := ratioTotals{}
	for _, account := range accounts {
		amount := adjustedAccountBalance(balances[account.ID], account)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			totals.assets = totals.assets.Add(amount)
			classification := normalizeClassification(account.SubClassification)
			if classification == masterdata.SubClassTradeReceivables ||
				classification == masterdata.SubClassAccountsReceivable {
				totals.receivables = totals.receivables.Add(amount)
			}
			if classification == masterdata.SubClassInventories {
				totals.inventory = totals.inventory.Add(amount)
			}
		case masterdata.AccountTypeEquity:
			totals.equity = totals.equity.Add(amount)
		}
	}
	if !asOf.IsZero() {
		income, err := NewIncomeStatementEngine(repository).Generate(time.Time{}, asOf, currency)
		if err != nil {
			return ratioTotals{}, err
		}
		totals.equity = totals.equity.Add(income.NetProfit).Add(income.OCI)
	}
	return totals, nil
}

func (e *RatiosEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (FinancialRatios, error) {
	return e.GenerateForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *RatiosEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (FinancialRatios, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, end)
	if err != nil {
		return FinancialRatios{}, err
	}
	return NewRatiosEngine(repository).Generate(start, end, currency)
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
