package reporting

import (
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrCashFlowClassificationRequired = errors.New("explicit cash-flow activity is required")

type CashFlow struct {
	StartDate                        time.Time
	EndDate                          time.Time
	Currency                         string
	NetProfitBeforeTax               decimal.Decimal
	InvestingAndFinancingAdjustments decimal.Decimal
	NonCashAdjustments               decimal.Decimal
	WorkingCapitalChange             decimal.Decimal
	OperatingCashFlow                decimal.Decimal
	InvestingCashFlow                decimal.Decimal
	FinancingCashFlow                decimal.Decimal
	NetChangeInCash                  decimal.Decimal
	EffectOfExchangeRatesOnCash      decimal.Decimal
	OpeningCash                      decimal.Decimal
	ClosingCash                      decimal.Decimal
	Reconciled                       bool
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
	report.InvestingAndFinancingAdjustments = income.InvestingIncome.Add(income.FinancingIncome).
		Sub(income.FinanceCosts).Neg()
	report.InvestingCashFlow = income.InvestingIncome
	report.FinancingCashFlow = income.FinancingIncome.Sub(income.FinanceCosts)
	for _, account := range accounts {
		change := signedBalance(periodBalances[account.ID], account)
		if isCashAccount(account) {
			continue
		}
		activity, err := cashFlowActivity(account)
		if err != nil {
			return CashFlow{}, err
		}
		if account.IsNonCashAdjustment || activity == masterdata.CashFlowNonCashAdjustment {
			report.NonCashAdjustments = report.NonCashAdjustments.Add(change)
			continue
		}
		open := signedBalance(openBalances[account.ID], account)
		close := signedBalance(closeBalances[account.ID], account)
		balanceChange := close.Sub(open)
		switch activity {
		case masterdata.CashFlowOperating:
			switch account.AccountType {
			case masterdata.AccountTypeAsset:
				report.WorkingCapitalChange = report.WorkingCapitalChange.Sub(balanceChange)
			case masterdata.AccountTypeLiability:
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(balanceChange)
			}
		case masterdata.CashFlowInvesting:
			switch account.AccountType {
			case masterdata.AccountTypeAsset:
				report.InvestingCashFlow = report.InvestingCashFlow.Sub(balanceChange)
			case masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
				report.InvestingCashFlow = report.InvestingCashFlow.Add(balanceChange)
			}
		case masterdata.CashFlowFinancing:
			report.FinancingCashFlow = report.FinancingCashFlow.Add(balanceChange)
		}
	}
	report.OperatingCashFlow = report.NetProfitBeforeTax.Add(report.InvestingAndFinancingAdjustments).
		Add(report.NonCashAdjustments).Add(report.WorkingCapitalChange)
	report.NetChangeInCash = report.OperatingCashFlow.Add(report.InvestingCashFlow).Add(report.FinancingCashFlow)
	for _, account := range accounts {
		if isCashAccount(account) {
			report.OpeningCash = report.OpeningCash.Add(signedBalance(openBalances[account.ID], account))
			report.ClosingCash = report.ClosingCash.Add(signedBalance(closeBalances[account.ID], account))
		}
	}
	if translated, err := entriesRequireTranslation(e.repository, end, currency); err != nil {
		return CashFlow{}, err
	} else if translated {
		report.EffectOfExchangeRatesOnCash = report.ClosingCash.Sub(report.OpeningCash).Sub(report.NetChangeInCash)
	}
	report.Reconciled = report.NetChangeInCash.Add(report.EffectOfExchangeRatesOnCash).
		Equal(report.ClosingCash.Sub(report.OpeningCash))
	if !report.Reconciled {
		return report, ErrUnbalancedCashFlow
	}
	return report, nil
}

func (e *CashFlowEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return CashFlow{}, err
	}
	return NewCashFlowEngine(repository).Generate(start, end, currency)
}

func (e *CashFlowEngine) GenerateDirect(start, end time.Time, currency string) (CashFlow, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return CashFlow{}, err
	}
	entries, err := listPostedEntries(e.repository, start, end)
	if err != nil {
		return CashFlow{}, err
	}
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	report := CashFlow{StartDate: start, EndDate: end, Currency: currency}
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		entryDate := entryEffectiveDate(entry)
		for i, cashLine := range entry.Lines {
			cashAccount, ok := accountByID[cashLine.AccountID]
			if !ok {
				return CashFlow{}, ErrReportingAccountNotFound
			}
			if !isCashAccount(cashAccount) {
				continue
			}
			cashDebit, cashCredit, err := translateLineAmounts(
				cashLine, cashAccount.AccountType, currency, rateProvider(e.repository), start, end, entryDate,
			)
			if err != nil {
				return CashFlow{}, err
			}
			cashChange := cashDebit.Sub(cashCredit)
			if cashChange.IsZero() {
				continue
			}
			activityAmounts := make(map[string]decimal.Decimal)
			total := decimal.Zero
			for j, other := range entry.Lines {
				if i == j {
					continue
				}
				account, ok := accountByID[other.AccountID]
				if !ok {
					return CashFlow{}, ErrReportingAccountNotFound
				}
				debit, credit, err := translateLineAmounts(
					other, account.AccountType, currency, rateProvider(e.repository), start, end, entryDate,
				)
				if err != nil {
					return CashFlow{}, err
				}
				amount := debit.Add(credit).Abs()
				if amount.IsZero() {
					continue
				}
				activity, err := cashFlowActivity(account)
				if err != nil {
					return CashFlow{}, err
				}
				activityAmounts[activity] = activityAmounts[activity].Add(amount)
				total = total.Add(amount)
			}
			if total.IsZero() {
				report.OperatingCashFlow = report.OperatingCashFlow.Add(cashChange)
				continue
			}
			for activity, amount := range activityAmounts {
				allocated := cashChange.Mul(amount).Div(total)
				switch activity {
				case masterdata.CashFlowInvesting:
					report.InvestingCashFlow = report.InvestingCashFlow.Add(allocated)
				case masterdata.CashFlowFinancing:
					report.FinancingCashFlow = report.FinancingCashFlow.Add(allocated)
				default:
					report.OperatingCashFlow = report.OperatingCashFlow.Add(allocated)
				}
			}
		}
	}
	open, err := accountBalances(e.repository, time.Time{}, start, currency)
	if err != nil {
		return CashFlow{}, err
	}
	close, err := accountBalances(e.repository, time.Time{}, end, currency)
	if err != nil {
		return CashFlow{}, err
	}
	for _, account := range accounts {
		if isCashAccount(account) {
			report.OpeningCash = report.OpeningCash.Add(signedBalance(open[account.ID], account))
			report.ClosingCash = report.ClosingCash.Add(signedBalance(close[account.ID], account))
		}
	}
	report.NetChangeInCash = report.OperatingCashFlow.Add(report.InvestingCashFlow).Add(report.FinancingCashFlow)
	if translated, err := entriesRequireTranslation(e.repository, end, currency); err != nil {
		return CashFlow{}, err
	} else if translated {
		report.EffectOfExchangeRatesOnCash = report.ClosingCash.Sub(report.OpeningCash).Sub(report.NetChangeInCash)
	}
	report.Reconciled = report.NetChangeInCash.Add(report.EffectOfExchangeRatesOnCash).
		Equal(report.ClosingCash.Sub(report.OpeningCash))
	if !report.Reconciled {
		return report, ErrUnbalancedCashFlow
	}
	return report, nil
}

func (e *CashFlowEngine) GenerateDirectForCompany(companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return CashFlow{}, err
	}
	return NewCashFlowEngine(repository).GenerateDirect(start, end, currency)
}

func isCashAccount(account masterdata.Account) bool {
	return account.AccountType == masterdata.AccountTypeAsset &&
		(account.CashFlowActivity == masterdata.CashFlowCashEquivalent ||
			account.IsCashEquivalent || account.EffectiveIsCashEquivalent())
}

func cashFlowActivity(account masterdata.Account) (string, error) {
	switch account.CashFlowActivity {
	case masterdata.CashFlowOperating,
		masterdata.CashFlowInvesting,
		masterdata.CashFlowFinancing,
		masterdata.CashFlowNonCashAdjustment:
		return account.CashFlowActivity, nil
	case masterdata.CashFlowCashEquivalent:
		if isCashAccount(account) {
			return account.CashFlowActivity, nil
		}
	}
	return "", ErrCashFlowClassificationRequired
}

func signedBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	value := side.debit.Sub(side.credit)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		return value.Neg()
	}
	return value
}
