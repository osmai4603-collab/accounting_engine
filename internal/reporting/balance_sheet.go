package reporting

import (
	"context"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/reporting/taxonomy"
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
	TranslationReserve        decimal.Decimal
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
	balances, err := accountBalances(e.repository, time.Time{}, asOf, currency)
	if err != nil {
		return BalanceSheet{}, err
	}
	taxonomyBalances := taxonomy.PeriodBalances{Accounts: make([]taxonomy.AccountBalance, 0, len(accounts))}
	for _, account := range accounts {
		tags := balanceSheetTaxonomyTags(account)
		if len(tags) == 0 {
			continue
		}
		taxonomyBalances.Accounts = append(taxonomyBalances.Accounts, taxonomy.AccountBalance{
			AccountID: account.ID,
			Code:      account.Code,
			Tags:      tags,
			Amount:    adjustedAccountBalance(balances[account.ID], account),
		})
	}
	definition, err := taxonomy.BuiltInTemplate(taxonomy.TemplateIFRSBalanceSheet)
	if err != nil {
		return BalanceSheet{}, err
	}
	evaluated, err := taxonomy.NewTaxonomyEvaluator(staticTaxonomyBalanceReader{balances: taxonomyBalances}).
		EvaluateStatement(context.Background(), uuid.New(), definition, uuid.New(), currency)
	if err != nil {
		return BalanceSheet{}, err
	}
	values := make(map[string]decimal.Decimal, len(evaluated.Lines))
	for _, line := range evaluated.Lines {
		values[line.Code] = line.Amount
	}
	report := BalanceSheet{
		AsOfDate:              asOf,
		Currency:              currency,
		CurrentAssets:         values["CURRENT_ASSETS"],
		NonCurrentAssets:      values["NONCURRENT_ASSETS"],
		TotalAssets:           values["TOTAL_ASSETS"],
		CurrentLiabilities:    values["CURRENT_LIABILITIES"],
		NonCurrentLiabilities: values["NONCURRENT_LIABILITIES"],
		TotalLiabilities:      values["TOTAL_LIABILITIES"],
		Equity:                values["EQUITY"],
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(time.Time{}, asOf, currency)
	if err != nil {
		return BalanceSheet{}, err
	}
	report.Equity = report.Equity.Add(income.NetProfit).Add(income.OCI)
	report.TranslationReserve = income.TranslationReserve
	report.TotalLiabilitiesAndEquity = report.TotalLiabilities.Add(report.Equity)
	report.Balanced = report.TotalAssets.Equal(report.TotalLiabilitiesAndEquity)
	if !report.Balanced {
		return report, ErrUnbalancedBalanceSheet
	}

	return report, nil
}

func balanceSheetTaxonomyTags(account masterdata.Account) []string {
	switch account.AccountType {
	case masterdata.AccountTypeAsset:
		if account.IsCurrentAsset() {
			return []string{"current_asset"}
		}
		return []string{"noncurrent_asset"}
	case masterdata.AccountTypeLiability:
		if account.IsCurrent() {
			return []string{"current_liability"}
		}
		return []string{"noncurrent_liability"}
	case masterdata.AccountTypeEquity:
		return []string{"equity"}
	default:
		return nil
	}
}

func (e *BalanceSheetEngine) GenerateForCompany(companyID uuid.UUID, asOf time.Time, currency string) (BalanceSheet, error) {
	return e.GenerateForCompanyContext(context.Background(), companyID, asOf, currency)
}

func (e *BalanceSheetEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, asOf time.Time, currency string) (BalanceSheet, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, asOf)
	if err != nil {
		return BalanceSheet{}, err
	}
	return NewBalanceSheetEngine(repository).Generate(asOf, currency)
}
