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
		if isCashAccount(account) {
			continue
		}
		if account.IsNonCashAdjustment {
			report.NonCashAdjustments = report.NonCashAdjustments.Add(change.Abs())
			continue
		}
		if account.AccountType == masterdata.AccountTypeEquity {
			report.FinancingCashFlow = report.FinancingCashFlow.Add(change)
			continue
		}
		classification := normalizeClassification(account.SubClassification)
		if account.CashFlowActivity == "" || account.CashFlowActivity == "unclassified" {
			if account.AccountType == masterdata.AccountTypeAsset &&
				(strings.Contains(classification, "receiv") || strings.Contains(classification, "inventory") ||
					strings.Contains(classification, "prepaid")) {
				report.WorkingCapitalChange = report.WorkingCapitalChange.Sub(change)
				continue
			}
			if account.AccountType == masterdata.AccountTypeLiability &&
				(strings.Contains(classification, "payable") || strings.Contains(classification, "accrued")) {
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(change)
				continue
			}
		}
		if account.AccountType == masterdata.AccountTypeAsset &&
			(strings.Contains(classification, "fixed") || strings.Contains(classification, "ppe") ||
				strings.Contains(classification, "plant") || strings.Contains(classification, "equipment")) {
			report.InvestingCashFlow = report.InvestingCashFlow.Sub(change.Abs())
			continue
		}
		switch account.CashFlowActivity {
		case "operating":
			switch account.AccountType {
			case masterdata.AccountTypeAsset:
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Sub(close.Sub(open))
			case masterdata.AccountTypeLiability:
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(close.Sub(open))
			}
		case "investing":
			if account.AccountType == masterdata.AccountTypeAsset {
				report.InvestingCashFlow = report.InvestingCashFlow.Sub(change.Abs())
			} else {
				report.InvestingCashFlow = report.InvestingCashFlow.Add(change)
			}
		case "financing":
			report.FinancingCashFlow = report.FinancingCashFlow.Add(change)
		case "none", "unclassified":
			if strings.Contains(classification, "receiv") || strings.Contains(classification, "inventory") || strings.Contains(classification, "prepaid") {
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Sub(close.Sub(open))
			} else if strings.Contains(classification, "payable") || strings.Contains(classification, "accrued") {
				open := signedBalance(openBalances[account.ID], account)
				close := signedBalance(closeBalances[account.ID], account)
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(close.Sub(open))
			} else if strings.Contains(classification, "fixed") || strings.Contains(classification, "ppe") || strings.Contains(classification, "equipment") || strings.Contains(classification, "plant") || strings.Contains(classification, "property") || strings.Contains(classification, "investment") {
				report.InvestingCashFlow = report.InvestingCashFlow.Sub(change.Abs())
			} else if strings.Contains(classification, "loan") || strings.Contains(classification, "lease") {
				report.FinancingCashFlow = report.FinancingCashFlow.Add(change)
			}
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
	return account.AccountType == masterdata.AccountTypeAsset &&
		(account.IsCashEquivalent || account.EffectiveIsCashEquivalent())
}

func signedBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	value := side.debit.Sub(side.credit)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		return value.Neg()
	}
	return value
}
