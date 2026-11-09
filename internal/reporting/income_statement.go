package reporting

import (
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrIFRSClassificationRequired = errors.New("explicit IFRS statement category is required")

type IncomeStatement struct {
	StartDate                        time.Time
	EndDate                          time.Time
	Currency                         string
	Revenue                          decimal.Decimal
	InvestingIncome                  decimal.Decimal
	FinancingIncome                  decimal.Decimal
	CostOfSales                      decimal.Decimal
	GrossProfit                      decimal.Decimal
	SellingExpenses                  decimal.Decimal
	AdministrativeExpenses           decimal.Decimal
	DepreciationAndProvisionExpenses decimal.Decimal
	OtherOperatingExpenses           decimal.Decimal
	OperatingProfit                  decimal.Decimal
	ProfitBeforeFinancingAndTax      decimal.Decimal
	FinanceIncome                    decimal.Decimal
	FinanceCosts                     decimal.Decimal
	ProfitBeforeTax                  decimal.Decimal
	TaxExpense                       decimal.Decimal
	DiscontinuedOperations           decimal.Decimal
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
		category := normalizeClassification(account.IFRSCategory)
		switch account.AccountType {
		case masterdata.AccountTypeIncome:
			if !validIFRSCategory(category) {
				return IncomeStatement{}, ErrIFRSClassificationRequired
			}
			switch category {
			case masterdata.IFRSCategoryBalanceSheetPerpetual:
				continue
			case masterdata.IFRSCategoryTax, masterdata.IFRSCategoryIncomeTaxes:
				report.TaxExpense = report.TaxExpense.Add(amount)
			case masterdata.IFRSCategoryInvesting:
				report.InvestingIncome = report.InvestingIncome.Add(amount)
			case masterdata.IFRSCategoryFinancing:
				report.FinancingIncome = report.FinancingIncome.Add(amount)
			case masterdata.IFRSCategoryEquityMovements:
				report.OCI = report.OCI.Add(amount)
			case masterdata.IFRSCategoryDiscontinued, masterdata.IFRSCategoryDiscontinuedOperations:
				report.DiscontinuedOperations = report.DiscontinuedOperations.Add(amount)
			case masterdata.IFRSCategoryOperating:
				report.Revenue = report.Revenue.Add(amount)
			default:
				return IncomeStatement{}, ErrIFRSClassificationRequired
			}
		case masterdata.AccountTypeExpense:
			if !validIFRSCategory(category) {
				return IncomeStatement{}, ErrIFRSClassificationRequired
			}
			switch category {
			case masterdata.IFRSCategoryBalanceSheetPerpetual:
				continue
			case masterdata.IFRSCategoryTax, masterdata.IFRSCategoryIncomeTaxes:
				report.TaxExpense = report.TaxExpense.Add(amount)
				continue
			case masterdata.IFRSCategoryInvesting:
				report.InvestingIncome = report.InvestingIncome.Sub(amount)
				continue
			case masterdata.IFRSCategoryFinancing:
				report.FinanceCosts = report.FinanceCosts.Add(amount)
				continue
			case masterdata.IFRSCategoryEquityMovements:
				report.OCI = report.OCI.Sub(amount)
				continue
			case masterdata.IFRSCategoryDiscontinued, masterdata.IFRSCategoryDiscontinuedOperations:
				report.DiscontinuedOperations = report.DiscontinuedOperations.Sub(amount)
				continue
			case masterdata.IFRSCategoryOperating:
			default:
				return IncomeStatement{}, ErrIFRSClassificationRequired
			}
			switch classification {
			case masterdata.SubClassCostOfSales:
				report.CostOfSales = report.CostOfSales.Add(amount)
			case masterdata.SubClassSellingDistributionExpense:
				report.SellingExpenses = report.SellingExpenses.Add(amount)
			case masterdata.SubClassGeneralAdminExpense:
				report.AdministrativeExpenses = report.AdministrativeExpenses.Add(amount)
			case masterdata.SubClassDepreciationAmortExpense, masterdata.SubClassExpectedCreditLossExpense:
				report.DepreciationAndProvisionExpenses = report.DepreciationAndProvisionExpenses.Add(amount)
			default:
				report.OtherOperatingExpenses = report.OtherOperatingExpenses.Add(amount)
			}
		}
	}
	report.GrossProfit = report.Revenue.Sub(report.CostOfSales)
	report.OperatingProfit = report.GrossProfit.Sub(report.SellingExpenses).Sub(report.AdministrativeExpenses).
		Sub(report.DepreciationAndProvisionExpenses).Sub(report.OtherOperatingExpenses)
	report.ProfitBeforeFinancingAndTax = report.OperatingProfit.Add(report.InvestingIncome)
	report.FinanceIncome = report.InvestingIncome.Add(report.FinancingIncome)
	report.ProfitBeforeTax = report.ProfitBeforeFinancingAndTax.Add(report.FinancingIncome).Sub(report.FinanceCosts)
	report.NetProfit = report.ProfitBeforeTax.Sub(report.TaxExpense)
	report.NetProfit = report.NetProfit.Add(report.DiscontinuedOperations)
	report.TotalComprehensiveIncome = report.NetProfit.Add(report.OCI)
	return report, nil
}

func validIFRSCategory(category string) bool {
	switch category {
	case masterdata.IFRSCategoryOperating,
		masterdata.IFRSCategoryInvesting,
		masterdata.IFRSCategoryFinancing,
		masterdata.IFRSCategoryTax,
		masterdata.IFRSCategoryIncomeTaxes,
		masterdata.IFRSCategoryEquityMovements,
		masterdata.IFRSCategoryDiscontinued,
		masterdata.IFRSCategoryDiscontinuedOperations,
		masterdata.IFRSCategoryBalanceSheetPerpetual:
		return true
	default:
		return false
	}
}

func (e *IncomeStatementEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (IncomeStatement, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return IncomeStatement{}, err
	}
	return NewIncomeStatementEngine(repository).Generate(start, end, currency)
}
