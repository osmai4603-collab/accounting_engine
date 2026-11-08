package reporting

import (
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/shopspring/decimal"
)

type CashFlow struct {
	StartDate            time.Time
	EndDate              time.Time
	Currency             string
	NetProfitBeforeTax   decimal.Decimal
	NonCashAdjustments   decimal.Decimal
	WorkingCapitalChange decimal.Decimal
	OperatingCashFlow    decimal.Decimal
	InvestingCashFlow    decimal.Decimal
	FinancingCashFlow    decimal.Decimal
	NetChangeInCash      decimal.Decimal
	OpeningCash          decimal.Decimal
	ClosingCash          decimal.Decimal
	Reconciled           bool
}

type CashFlowEngine struct{ repository Repository }

func NewCashFlowEngine(repository Repository) *CashFlowEngine {
	return &CashFlowEngine{repository: repository}
}

func (e *CashFlowEngine) Generate(start, end time.Time, currency string) (CashFlow, error) {
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return CashFlow{}, err
	}
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return CashFlow{}, err
	}
	periodBalances, err := accountBalances(e.repository, start, end, currency)
	if err != nil {
		return CashFlow{}, err
	}
	openBalances, err := accountBalances(e.repository, time.Time{}, start, currency)
	if err != nil {
		return CashFlow{}, err
	}
	closeBalances, err := accountBalances(e.repository, time.Time{}, end, currency)
	if err != nil {
		return CashFlow{}, err
	}

	report := CashFlow{
		StartDate:          start,
		EndDate:            end,
		Currency:           currency,
		NetProfitBeforeTax: income.ProfitBeforeTax,
	}
	for _, account := range accounts {
		change := signedBalance(periodBalances[account.ID], account)
		classification := normalizeClassification(account.SubClassification)
		if isCashAccount(account) {
			continue
		}
		switch account.AccountType {
		case masterdata.AccountTypeExpense:
			if strings.Contains(classification, "depreciation") || strings.Contains(classification, "provision") {
				report.NonCashAdjustments = report.NonCashAdjustments.Add(change.Abs())
			}
		case masterdata.AccountTypeAsset:
			if strings.Contains(classification, "receivable") || strings.Contains(classification, "inventory") || strings.Contains(classification, "prepaid") {
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Sub(close.Sub(open))
			} else if strings.Contains(classification, "fixed") || strings.Contains(classification, "ppe") || strings.Contains(classification, "investment") {
				report.InvestingCashFlow = report.InvestingCashFlow.Sub(change.Abs())
			}
		case masterdata.AccountTypeLiability:
			if strings.Contains(classification, "payable") || strings.Contains(classification, "accrued") {
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(close.Sub(open))
			} else if strings.Contains(classification, "loan") || strings.Contains(classification, "lease") {
				report.FinancingCashFlow = report.FinancingCashFlow.Add(change)
			}
		case masterdata.AccountTypeEquity:
			report.FinancingCashFlow = report.FinancingCashFlow.Add(change)
		}
	}
	report.OperatingCashFlow = report.NetProfitBeforeTax.Add(report.NonCashAdjustments).Add(report.WorkingCapitalChange)
	report.NetChangeInCash = report.OperatingCashFlow.Add(report.InvestingCashFlow).Add(report.FinancingCashFlow)
	for _, account := range accounts {
		if isCashAccount(account) {
			report.OpeningCash = report.OpeningCash.Add(signedBalance(openBalances[account.ID], account))
			report.ClosingCash = report.ClosingCash.Add(signedBalance(closeBalances[account.ID], account))
		}
	}
	report.Reconciled = report.NetChangeInCash.Equal(report.ClosingCash.Sub(report.OpeningCash))
	if !report.Reconciled {
		return report, ErrUnbalancedCashFlow
	}
	return report, nil
}

func isCashAccount(account masterdata.Account) bool {
	classification := normalizeClassification(account.SubClassification)
	return account.AccountType == masterdata.AccountTypeAsset &&
		(classification == "cash" || strings.Contains(classification, "cash") || strings.Contains(classification, "bank"))
}

func signedBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	value := side.debit.Sub(side.credit)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		return value.Neg()
	}
	return value
}
