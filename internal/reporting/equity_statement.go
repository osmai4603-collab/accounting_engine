package reporting

import (
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type EquityMovement struct {
	AccountID          uuid.UUID
	AccountCode        string
	AccountName        string
	Opening            decimal.Decimal
	NetIncome          decimal.Decimal
	Distributions      decimal.Decimal
	TransfersToReserve decimal.Decimal
	Closing            decimal.Decimal
}

type EquityStatement struct {
	StartDate    time.Time
	EndDate      time.Time
	Currency     string
	Movements    []EquityMovement
	OpeningTotal decimal.Decimal
	ClosingTotal decimal.Decimal
}

type EquityStatementEngine struct{ repository Repository }

func NewEquityStatementEngine(repository Repository) *EquityStatementEngine {
	return &EquityStatementEngine{repository: repository}
}

func (e *EquityStatementEngine) Generate(start, end time.Time, currency string) (EquityStatement, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return EquityStatement{}, err
	}

	open, err := accountBalances(e.repository, time.Time{}, start, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	period, err := accountBalances(e.repository, start, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	close, err := accountBalances(e.repository, time.Time{}, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	report := EquityStatement{StartDate: start, EndDate: end, Currency: currency}
	for _, account := range accounts {
		if account.AccountType != masterdata.AccountTypeEquity {
			continue
		}
		distributions := period[account.ID].debit
		transfer := decimal.Zero
		netIncome := decimal.Zero
		if normalizeClassification(account.SubClassification) == masterdata.SubClassRetainedEarnings {
			netIncome = income.NetProfit
		}
		openAmount := signedBalance(open[account.ID], account)
		closeAmount := signedBalance(close[account.ID], account)
		movement := EquityMovement{
			AccountID: account.ID, AccountCode: account.Code, AccountName: account.Name,
			Opening: openAmount, NetIncome: netIncome, Distributions: distributions,
			TransfersToReserve: transfer, Closing: closeAmount,
		}
		report.Movements = append(report.Movements, movement)
		report.OpeningTotal = report.OpeningTotal.Add(openAmount)
		report.ClosingTotal = report.ClosingTotal.Add(closeAmount)
	}
	return report, nil
}

func (e *EquityStatementEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (EquityStatement, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return EquityStatement{}, err
	}
	return NewEquityStatementEngine(repository).Generate(start, end, currency)
}
