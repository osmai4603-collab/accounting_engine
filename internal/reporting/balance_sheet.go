package reporting

import (
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
		amount := adjustedAccountBalance(balances[account.ID], account)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			if account.IsCurrentAsset() {
				report.CurrentAssets = report.CurrentAssets.Add(amount)
			} else {
				report.NonCurrentAssets = report.NonCurrentAssets.Add(amount)
			}
		case masterdata.AccountTypeLiability:
			if account.IsCurrent() {
				report.CurrentLiabilities = report.CurrentLiabilities.Add(amount)
			} else {
				report.NonCurrentLiabilities = report.NonCurrentLiabilities.Add(amount)
			}
		case masterdata.AccountTypeEquity:
			report.Equity = report.Equity.Add(amount)
		}
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(time.Time{}, asOf, currency)
	if err != nil {
		return BalanceSheet{}, err
	}
	report.Equity = report.Equity.Add(income.NetProfit).Add(income.OCI)
	report.TotalAssets = report.CurrentAssets.Add(report.NonCurrentAssets)
	report.TotalLiabilities = report.CurrentLiabilities.Add(report.NonCurrentLiabilities)
	report.TotalLiabilitiesAndEquity = report.TotalLiabilities.Add(report.Equity)
	report.Balanced = report.TotalAssets.Equal(report.TotalLiabilitiesAndEquity)
	if !report.Balanced {
		return report, ErrUnbalancedBalanceSheet
	}
	return report, nil
}

func (e *BalanceSheetEngine) GenerateForCompany(companyID uuid.UUID, asOf time.Time, currency string) (BalanceSheet, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return BalanceSheet{}, err
	}
	return NewBalanceSheetEngine(repository).Generate(asOf, currency)
}
