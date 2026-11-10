package reporting

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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
	TranslationReserve               decimal.Decimal
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
		amount := adjustedAccountBalance(balances[account.ID], account)
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
	translated, err := entriesRequireTranslation(e.repository, end, currency)
	if err != nil {
		return IncomeStatement{}, err
	}
	if translated {
		closingReserve, err := translationReserveAt(e.repository, accounts, end, currency)
		if err != nil {
			return IncomeStatement{}, err
		}
		openingReserve := decimal.Zero
		if !start.IsZero() {
			openingReserve, err = translationReserveAt(e.repository, accounts, start.Add(-time.Nanosecond), currency)
			if err != nil {
				return IncomeStatement{}, err
			}
		}
		report.TranslationReserve = closingReserve.Sub(openingReserve)
		report.OCI = report.OCI.Add(report.TranslationReserve)
	}
	report.TotalComprehensiveIncome = report.NetProfit.Add(report.OCI)
	return report, nil
}

func translationReserveAt(
	repository Repository,
	accounts []masterdata.Account,
	asOf time.Time,
	currency string,
) (decimal.Decimal, error) {
	balances, err := accountBalances(repository, time.Time{}, asOf, currency)
	if err != nil {
		return decimal.Zero, err
	}
	var assets, liabilities, equity decimal.Decimal
	for _, account := range accounts {
		amount := adjustedAccountBalance(balances[account.ID], account)
		switch account.AccountType {
		case masterdata.AccountTypeAsset:
			assets = assets.Add(amount)
		case masterdata.AccountTypeLiability:
			liabilities = liabilities.Add(amount)
		case masterdata.AccountTypeEquity:
			equity = equity.Add(amount)
		}
	}
	netIncome, otherOCI, err := cumulativeIncomeAndOCI(accounts, balances)
	if err != nil {
		return decimal.Zero, err
	}
	return assets.Sub(liabilities).Sub(equity).Sub(netIncome).Sub(otherOCI), nil
}

func cumulativeIncomeAndOCI(
	accounts []masterdata.Account,
	balances map[uuid.UUID]accountSide,
) (decimal.Decimal, decimal.Decimal, error) {
	netIncome, oci := decimal.Zero, decimal.Zero
	for _, account := range accounts {
		if account.AccountType != masterdata.AccountTypeIncome &&
			account.AccountType != masterdata.AccountTypeExpense {
			continue
		}
		category := normalizeClassification(account.IFRSCategory)
		if !validIFRSCategory(category) {
			return decimal.Zero, decimal.Zero, ErrIFRSClassificationRequired
		}
		if category == masterdata.IFRSCategoryBalanceSheetPerpetual {
			continue
		}
		amount := adjustedAccountBalance(balances[account.ID], account)
		sign := decimal.NewFromInt(1)
		if account.AccountType == masterdata.AccountTypeExpense {
			sign = sign.Neg()
		}
		if category == masterdata.IFRSCategoryEquityMovements {
			oci = oci.Add(amount.Mul(sign))
		} else if category == masterdata.IFRSCategoryTax || category == masterdata.IFRSCategoryIncomeTaxes {
			netIncome = netIncome.Sub(amount)
		} else {
			netIncome = netIncome.Add(amount.Mul(sign))
		}
	}
	return netIncome, oci, nil
}

func entriesRequireTranslation(repository Repository, asOf time.Time, currency string) (bool, error) {
	entries, err := listPostedEntries(repository, time.Time{}, asOf)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		for _, line := range entry.Lines {
			_, _, sourceCurrency := sourceLineAmounts(line)
			if sourceCurrency != "" && currency != "" && sourceCurrency != currency {
				return true, nil
			}
		}
	}
	return false, nil
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
	return e.GenerateForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *IncomeStatementEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (IncomeStatement, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, end)
	if err != nil {
		return IncomeStatement{}, err
	}
	return NewIncomeStatementEngine(repository).Generate(start, end, currency)
}
