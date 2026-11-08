package reporting

import (
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrFiscalYearAlreadyClosed = errors.New("fiscal year is already closed")

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
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	balances, err := accountBalances(e.repository, year.StartDate, year.EndDate, currency)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	return e.generateYearMove(year.EndDate, journalID, retainedEarningsID, currency, accounts, balances, true)
}

func (e *ClosingEngine) GenerateOpeningMove(previousYear masterdata.FiscalYear, journalID uuid.UUID, currency string) (posting.JournalEntry, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	balances, err := accountBalances(e.repository, time.Time{}, previousYear.EndDate, currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines := make([]posting.JournalEntryLine, 0)
	retainedID, retainedErr := FindRetainedEarningsAccount(accounts)
	currentNetIncome := decimal.Zero
	for _, account := range accounts {
		switch account.AccountType {
		case masterdata.AccountTypeIncome:
			currentNetIncome = currentNetIncome.Add(closingBalance(balances[account.ID], account))
		case masterdata.AccountTypeExpense:
			currentNetIncome = currentNetIncome.Sub(closingBalance(balances[account.ID], account))
		}
	}
	for _, account := range accounts {
		if account.AccountType != masterdata.AccountTypeAsset &&
			account.AccountType != masterdata.AccountTypeLiability &&
			account.AccountType != masterdata.AccountTypeEquity {
			continue
		}
		balance := balanceAfterClosing(balances[account.ID], account)
		if balance.IsZero() {
			continue
		}
		line, err := openingBalanceLine(account, balance, currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, line)
	}
	if retainedErr == nil && !currentNetIncome.IsZero() {
		line, err := openingBalanceLine(masterdata.Account{
			ID: retainedID, AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
		}, currentNetIncome, currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, line)
	}
	entry, err := posting.NewJournalEntry(journalID, nextYearStart(previousYear.EndDate), "closing-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.DocumentReference = "OPENING BALANCE: " + previousYear.Name
	return entry, nil
}

func (e *ClosingEngine) generateYearMove(date time.Time, journalID, retainedEarningsID uuid.UUID, currency string, accounts []masterdata.Account, balances map[uuid.UUID]accountSide, closing bool) (posting.JournalEntry, decimal.Decimal, error) {
	lines := make([]posting.JournalEntryLine, 0)
	netIncome := decimal.Zero
	for _, account := range accounts {
		balance := closingBalance(balances[account.ID], account)
		if balance.IsZero() {
			continue
		}
		switch account.AccountType {
		case masterdata.AccountTypeIncome:
			netIncome = netIncome.Add(balance)
			line, err := singleAmountLine(account.ID, balance, currency, true)
			if err != nil {
				return posting.JournalEntry{}, decimal.Zero, err
			}
			lines = append(lines, line)
		case masterdata.AccountTypeExpense:
			netIncome = netIncome.Sub(balance)
			line, err := singleAmountLine(account.ID, balance, currency, false)
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

func closingBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	if account.AccountType == masterdata.AccountTypeIncome {
		return signedBalance(side, account).Abs()
	}
	if account.AccountType == masterdata.AccountTypeExpense {
		return signedBalance(side, account).Abs()
	}
	return decimal.Zero
}

func balanceAfterClosing(side accountSide, account masterdata.Account) decimal.Decimal {
	return signedBalance(side, account)
}

func nextYearStart(end time.Time) time.Time {
	return time.Date(end.UTC().Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
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

func balanceLine(accountID uuid.UUID, balance decimal.Decimal, currency string) (posting.JournalEntryLine, error) {
	return singleAmountLine(accountID, balance.Abs(), currency, balance.GreaterThan(decimal.Zero))
}

func openingBalanceLine(account masterdata.Account, balance decimal.Decimal, currency string) (posting.JournalEntryLine, error) {
	debit := balance.GreaterThan(decimal.Zero)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		debit = balance.LessThan(decimal.Zero)
	}
	return singleAmountLine(account.ID, balance.Abs(), currency, debit)
}

func FindRetainedEarningsAccount(accounts []masterdata.Account) (uuid.UUID, error) {
	for _, account := range accounts {
		classification := strings.ToLower(account.SubClassification)
		if strings.Contains(classification, "retained") && account.AccountType == masterdata.AccountTypeEquity {
			return account.ID, nil
		}
	}
	for _, account := range accounts {
		if strings.Contains(strings.ToLower(account.Name), "retained earnings") {
			return account.ID, nil
		}
	}
	return uuid.Nil, ErrRetainedEarningsNotSet
}
