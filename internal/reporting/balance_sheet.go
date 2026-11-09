package reporting

import (
	"accounting_engine/internal/masterdata"
	"github.com/shopspring/decimal"
	"time"
)

type ReportAmount struct {
	Classification string
	Amount         decimal.Decimal
}

type BalanceSheet struct {
	AsOfDate                  time.Time
	Currency                  string
	CurrentAssets             decimal.Decimal
	NonCurrentAssets          decimal.Decimal
	TotalAssets               decimal.Decimal
	CurrentLiabilities        decimal.Decimal
	NonCurrentLiabilities     decimal.Decimal
	TotalLiabilities          decimal.Decimal
	Equity                    decimal.Decimal
	TotalLiabilitiesAndEquity decimal.Decimal
	Balanced                  bool
}

type BalanceSheetEngine struct{ repository Repository }

func NewBalanceSheetEngine(repository Repository) *BalanceSheetEngine {
	return &BalanceSheetEngine{repository: repository}
}

func (e *BalanceSheetEngine) Generate(asOf time.Time, currency string) (BalanceSheet, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return BalanceSheet{}, err
	}
	report := BalanceSheet{AsOfDate: asOf, Currency: currency}
	balances, err := accountBalances(e.repository, time.Time{}, asOf, currency)
	if err != nil {
		return BalanceSheet{}, err
	}
	for _, account := range accounts {
		amount := accountBalance(balances[account.ID], account)
		if account.IsContraAccount {
			amount = amount.Neg()
		}
		classification := normalizeClassification(account.SubClassification)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			if isCurrent(classification) {
				report.CurrentAssets = report.CurrentAssets.Add(amount)
			} else {
				report.NonCurrentAssets = report.NonCurrentAssets.Add(amount)
			}
		case masterdata.AccountTypeLiability:
			if isCurrent(classification) {
				report.CurrentLiabilities = report.CurrentLiabilities.Add(amount)
			} else {
				report.NonCurrentLiabilities = report.NonCurrentLiabilities.Add(amount)
			}
		case masterdata.AccountTypeEquity:
			report.Equity = report.Equity.Add(amount)
		}
	}
	currentResult := decimal.Zero
	for _, account := range accounts {
		if account.AccountType == masterdata.AccountTypeIncome {
			currentResult = currentResult.Add(accountBalance(balances[account.ID], account))
		}
		if account.AccountType == masterdata.AccountTypeExpense {
			currentResult = currentResult.Sub(accountBalance(balances[account.ID], account))
		}
	}
	report.Equity = report.Equity.Add(currentResult)
	report.TotalAssets = report.CurrentAssets.Add(report.NonCurrentAssets)
	report.TotalLiabilities = report.CurrentLiabilities.Add(report.NonCurrentLiabilities)
	report.TotalLiabilitiesAndEquity = report.TotalLiabilities.Add(report.Equity)
	report.Balanced = report.TotalAssets.Equal(report.TotalLiabilitiesAndEquity)
	if !report.Balanced {
		return report, ErrUnbalancedBalanceSheet
	}
	return report, nil
}

func isCurrent(classification string) bool {
	return classification == "" || classification == "current" ||
		classification == "current_asset" || classification == "current_assets" ||
		classification == "current_liability" || classification == "current_liabilities" ||
		classification == "trade_receivable" || classification == "accounts_receivable" ||
		classification == "inventory" || classification == "inventories" ||
		classification == "prepaid_expense" || classification == "prepayments" ||
		classification == "trade_payable" || classification == "accounts_payable" ||
		classification == "accrued_expense" || classification == "short_term_borrowing"
}
