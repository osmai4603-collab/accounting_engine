package reporting

import (
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrFiscalYearAlreadyClosed = errors.New("fiscal year is already closed")
	ErrOpeningMoveNotRequired  = errors.New("opening balance moves are not supported by the perpetual general ledger")
)

type ClosingEngine struct{ repository Repository }

func NewClosingEngine(repository Repository) *ClosingEngine {
	return &ClosingEngine{repository: repository}
}

func (e *ClosingEngine) GenerateClosingMove(year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
	if retainedEarningsID == uuid.Nil {
		return posting.JournalEntry{}, decimal.Zero, ErrRetainedEarningsNotSet
	}
	if err := requireMonthlyPeriodsClosed(year); err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	repository := e.repository
	if year.CompanyID != uuid.Nil {
		var err error
		repository, err = newCompanyReportingRepository(repository, year.CompanyID)
		if err != nil {
			return posting.JournalEntry{}, decimal.Zero, err
		}
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	balances, err := accountBalances(repository, year.StartDate, year.EndDate, currency)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	entry, netIncome, err := e.generateYearMove(year.EndDate, journalID, retainedEarningsID, currency, accounts, balances)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	if entry.ID != uuid.Nil {
		entry.CompanyID = year.CompanyID
		for i := range entry.Lines {
			entry.Lines[i].CompanyID = year.CompanyID
		}
		entry.TargetPeriodNumber = 13
		entry.AdjustmentAuthorized = true
	}
	return entry, netIncome, nil
}

func (e *ClosingEngine) GenerateOpeningMove(_ masterdata.FiscalYear, _ uuid.UUID, _ string) (posting.JournalEntry, error) {
	return posting.JournalEntry{}, ErrOpeningMoveNotRequired
}

func (e *ClosingEngine) generateYearMove(date time.Time, journalID, retainedEarningsID uuid.UUID, currency string, accounts []masterdata.Account, balances map[uuid.UUID]accountSide) (posting.JournalEntry, decimal.Decimal, error) {
	lines := make([]posting.JournalEntryLine, 0)
	netIncome := decimal.Zero
	for _, account := range accounts {
		side := balances[account.ID]
		netBalance := side.debit.Sub(side.credit)
		if netBalance.IsZero() {
			continue
		}
		switch account.AccountType {
		case masterdata.AccountTypeIncome:
			netIncome = netIncome.Sub(netBalance)
			line, err := singleAmountLine(account.ID, netBalance.Abs(), currency, netBalance.IsNegative())
			if err != nil {
				return posting.JournalEntry{}, decimal.Zero, err
			}
			lines = append(lines, line)
		case masterdata.AccountTypeExpense:
			netIncome = netIncome.Sub(netBalance)
			line, err := singleAmountLine(account.ID, netBalance.Abs(), currency, netBalance.IsNegative())
			if err != nil {
				return posting.JournalEntry{}, decimal.Zero, err
			}
			lines = append(lines, line)
		}
	}
	if !netIncome.IsZero() {
		line, err := retainedEarningsLine(retainedEarningsID, netIncome, currency)
		if err != nil {
			return posting.JournalEntry{}, decimal.Zero, err
		}
		lines = append(lines, line)
	}
	if len(lines) < 2 {
		return posting.JournalEntry{}, netIncome, nil
	}
	entry, err := posting.NewJournalEntry(journalID, date, "closing-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	entry.DocumentReference = "YEAR-END CLOSING"
	return entry, netIncome, nil
}

func requireMonthlyPeriodsClosed(year masterdata.FiscalYear) error {
	monthlyClosed := make(map[int]bool)
	for _, period := range year.Periods {
		if period.Number >= 1 && period.Number <= 12 && period.State == masterdata.PeriodHardClosed {
			monthlyClosed[period.Number] = true
		}
	}
	for number := 1; number <= 12; number++ {
		if !monthlyClosed[number] {
			return ErrPeriodsNotHardClosed
		}
	}
	return nil
}

func singleAmountLine(accountID uuid.UUID, amount decimal.Decimal, currency string, debit bool) (posting.JournalEntryLine, error) {
	money, err := financialtypes.NewMoney(amount, currency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	line := posting.JournalEntryLine{AccountID: accountID, Debit: zero, Credit: zero}
	if debit {
		line.Debit, line.Credit = money, zero
	} else {
		line.Debit, line.Credit = zero, money
	}
	return line, nil
}

func retainedEarningsLine(accountID uuid.UUID, netIncome decimal.Decimal, currency string) (posting.JournalEntryLine, error) {
	return singleAmountLine(accountID, netIncome.Abs(), currency, netIncome.LessThan(decimal.Zero))
}

func FindRetainedEarningsAccount(accounts []masterdata.Account) (uuid.UUID, error) {
	for _, account := range accounts {
		if account.SubClassification == masterdata.SubClassRetainedEarnings &&
			account.AccountType == masterdata.AccountTypeEquity {
			return account.ID, nil
		}
	}
	return uuid.Nil, ErrRetainedEarningsNotSet
}
