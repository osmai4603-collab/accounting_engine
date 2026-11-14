package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting/taxonomy"
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
	taxonomyBalances := taxonomy.PeriodBalances{Accounts: make([]taxonomy.AccountBalance, 0, len(accounts))}
	for _, account := range accounts {
		tags, include, err := incomeStatementTaxonomyTags(account)
		if err != nil {
			return IncomeStatement{}, err
		}
		if !include {
			continue
		}
		taxonomyBalances.Accounts = append(taxonomyBalances.Accounts, taxonomy.AccountBalance{
			AccountID: account.ID,
			Code:      account.Code,
			Tags:      tags,
			Amount:    adjustedAccountBalance(balances[account.ID], account),
		})
	}
	definition, err := taxonomy.BuiltInTemplate(taxonomy.TemplateIFRSFunctionIncome)
	if err != nil {
		return IncomeStatement{}, err
	}
	evaluated, err := taxonomy.NewTaxonomyEvaluator(staticTaxonomyBalanceReader{balances: taxonomyBalances}).
		EvaluateStatement(context.Background(), uuid.New(), definition, uuid.New(), currency)
	if err != nil {
		return IncomeStatement{}, err
	}
	values := make(map[string]decimal.Decimal, len(evaluated.Lines))
	for _, line := range evaluated.Lines {
		values[line.Code] = line.Amount
	}
	report := IncomeStatement{
		StartDate:                        start,
		EndDate:                          end,
		Currency:                         currency,
		Revenue:                          values["REVENUE"],
		InvestingIncome:                  values["NET_INVESTING_INCOME"],
		CostOfSales:                      values["COST_OF_SALES"],
		GrossProfit:                      values["GROSS_PROFIT"],
		SellingExpenses:                  values["SELLING_EXPENSES"],
		AdministrativeExpenses:           values["ADMIN_EXPENSES"],
		DepreciationAndProvisionExpenses: values["DEPRECIATION_EXPENSES"],
		OtherOperatingExpenses:           values["OTHER_OPERATING_EXPENSES"],
		OperatingProfit:                  values["OPERATING_PROFIT"],
		ProfitBeforeFinancingAndTax:      values["PROFIT_BEFORE_FINANCING_AND_TAX"],
		FinanceIncome:                    values["FINANCE_INCOME"],
		FinancingIncome:                  values["FINANCING_REVENUE"],
		FinanceCosts:                     values["FINANCE_COSTS"],
		ProfitBeforeTax:                  values["PROFIT_BEFORE_TAX"],
		TaxExpense:                       values["INCOME_TAX_ZAKAT"],
		DiscontinuedOperations:           values["DISCONTINUED_OPERATIONS"],
		NetProfit:                        values["NET_PROFIT"],
		OCI:                              values["OCI"],
		TotalComprehensiveIncome:         values["TOTAL_COMPREHENSIVE_INCOME"],
	}
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

func incomeStatementTaxonomyTags(account masterdata.Account) ([]string, bool, error) {
	if account.AccountType != masterdata.AccountTypeIncome && account.AccountType != masterdata.AccountTypeExpense {
		return nil, false, nil
	}
	category := normalizeClassification(account.IFRSCategory)
	if !validIFRSCategory(category) {
		return nil, false, ErrIFRSClassificationRequired
	}
	if category == masterdata.IFRSCategoryBalanceSheetPerpetual {
		return nil, false, nil
	}
	categoryTags := map[string]map[masterdata.AccountType]string{
		masterdata.IFRSCategoryOperating: {
			masterdata.AccountTypeIncome: "revenue",
		},
		masterdata.IFRSCategoryInvesting: {
			masterdata.AccountTypeIncome:  "investing_revenue",
			masterdata.AccountTypeExpense: "investing_expense",
		},
		masterdata.IFRSCategoryFinancing: {
			masterdata.AccountTypeIncome:  "financing_revenue",
			masterdata.AccountTypeExpense: "finance_cost",
		},
		masterdata.IFRSCategoryTax: {
			masterdata.AccountTypeIncome:  "income_tax",
			masterdata.AccountTypeExpense: "income_tax",
		},
		masterdata.IFRSCategoryIncomeTaxes: {
			masterdata.AccountTypeIncome:  "income_tax",
			masterdata.AccountTypeExpense: "income_tax",
		},
		masterdata.IFRSCategoryEquityMovements: {
			masterdata.AccountTypeIncome:  "oci_income",
			masterdata.AccountTypeExpense: "oci_expense",
		},
		masterdata.IFRSCategoryDiscontinued: {
			masterdata.AccountTypeIncome:  "discontinued_income",
			masterdata.AccountTypeExpense: "discontinued_expense",
		},
		masterdata.IFRSCategoryDiscontinuedOperations: {
			masterdata.AccountTypeIncome:  "discontinued_income",
			masterdata.AccountTypeExpense: "discontinued_expense",
		},
	}
	tag := categoryTags[category][account.AccountType]
	if account.AccountType == masterdata.AccountTypeExpense && category == masterdata.IFRSCategoryOperating {
		expenseTags := map[string]string{
			masterdata.SubClassCostOfSales:                "cost_of_sales",
			masterdata.SubClassSellingDistributionExpense: "selling_expense",
			masterdata.SubClassGeneralAdminExpense:        "administrative_expense",
			masterdata.SubClassDepreciationAmortExpense:   "depreciation_expense",
			masterdata.SubClassExpectedCreditLossExpense:  "depreciation_expense",
			masterdata.SubClassOtherOperatingExpense:      "other_operating_expense",
		}
		tag = expenseTags[normalizeClassification(account.SubClassification)]
		if tag == "" {
			tag = "other_operating_expense"
		}
	}
	if tag == "" {
		return nil, false, ErrIFRSClassificationRequired
	}
	return []string{tag}, true, nil
}

type staticTaxonomyBalanceReader struct {
	balances taxonomy.PeriodBalances
}

func (r staticTaxonomyBalanceReader) GetPeriodBalances(
	ctx context.Context,
	_, _ uuid.UUID,
	_ string,
) (taxonomy.PeriodBalances, error) {
	if err := ctx.Err(); err != nil {
		return taxonomy.PeriodBalances{}, err
	}
	return r.balances, nil
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
		if !isLedgerPosted(entry.Status) {
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
