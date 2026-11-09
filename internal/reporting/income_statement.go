package reporting

import (
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/shopspring/decimal"
)

type IncomeStatement struct {
	StartDate                        time.Time
	EndDate                          time.Time
	Currency                         string
	Revenue                          decimal.Decimal
	CostOfSales                      decimal.Decimal
	GrossProfit                      decimal.Decimal
	SellingExpenses                  decimal.Decimal
	AdministrativeExpenses           decimal.Decimal
	DepreciationAndProvisionExpenses decimal.Decimal
	OperatingProfit                  decimal.Decimal
	FinanceIncome                    decimal.Decimal
	FinanceCosts                     decimal.Decimal
	ProfitBeforeTax                  decimal.Decimal
	TaxExpense                       decimal.Decimal
	NetProfit                        decimal.Decimal
	OCI                              decimal.Decimal
	TotalComprehensiveIncome         decimal.Decimal
}

type IncomeStatementEngine struct{ repository Repository }

func NewIncomeStatementEngine(repository Repository) *IncomeStatementEngine {
	return &IncomeStatementEngine{repository: repository}
}

func (e *IncomeStatementEngine) Generate(start, end time.Time, currency string) (IncomeStatement, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return IncomeStatement{}, err
	}
	balances, err := accountBalances(e.repository, start, end, currency)
	if err != nil {
		return IncomeStatement{}, err
	}
	report := IncomeStatement{StartDate: start, EndDate: end, Currency: currency}
	for _, account := range accounts {
		amount := accountBalance(balances[account.ID], account)
		classification := normalizeClassification(account.SubClassification)
		switch account.AccountType {
		case masterdata.AccountTypeIncome:
			if contains(classification, "tax") {
				report.TaxExpense = report.TaxExpense.Add(amount)
			} else if contains(classification, "finance") || contains(classification, "interest") {
				report.FinanceIncome = report.FinanceIncome.Add(amount)
			} else if strings.Contains(strings.ToLower(account.IFRSCategory), "other_comprehensive") ||
				strings.Contains(strings.ToLower(classification), "oci") {
				report.OCI = report.OCI.Add(amount)
			} else {
				report.Revenue = report.Revenue.Add(amount)
			}
		case masterdata.AccountTypeExpense:
			if contains(classification, "cost", "sales") {
				report.CostOfSales = report.CostOfSales.Add(amount)
			} else if contains(classification, "selling", "distribution") {
				report.SellingExpenses = report.SellingExpenses.Add(amount)
			} else if contains(classification, "administrative", "general") {
				report.AdministrativeExpenses = report.AdministrativeExpenses.Add(amount)
			} else if contains(classification, "depreciation", "provision") {
				report.DepreciationAndProvisionExpenses = report.DepreciationAndProvisionExpenses.Add(amount)
			} else if contains(classification, "finance", "interest") {
				report.FinanceCosts = report.FinanceCosts.Add(amount)
			} else if contains(classification, "tax", "zakat") {
				report.TaxExpense = report.TaxExpense.Add(amount)
			}
		}
	}
	report.GrossProfit = report.Revenue.Sub(report.CostOfSales)
	report.OperatingProfit = report.GrossProfit.Sub(report.SellingExpenses).Sub(report.AdministrativeExpenses).Sub(report.DepreciationAndProvisionExpenses)
	report.ProfitBeforeTax = report.OperatingProfit.Add(report.FinanceIncome).Sub(report.FinanceCosts)
	report.NetProfit = report.ProfitBeforeTax.Sub(report.TaxExpense)
	report.TotalComprehensiveIncome = report.NetProfit.Add(report.OCI)
	return report, nil
}

func contains(value string, terms ...string) bool {
	for _, term := range terms {
		if term != "" && (value == term || (len(value) >= len(term) && value[:len(value)] != "" && valueContains(value, term))) {
			return true
		}
	}
	return false
}

func valueContains(value, term string) bool {
	return len(term) <= len(value) && (value == term || indexOf(value, term) >= 0)
}

func indexOf(value, term string) int {
	for i := 0; i+len(term) <= len(value); i++ {
		if value[i:i+len(term)] == term {
			return i
		}
	}
	return -1
}
