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
	ErrInvalidOpeningYear      = errors.New("opening balance year is invalid")
	ErrClosingMoveNotPosted    = errors.New("closing move must be posted before the fiscal year can be closed")
)

type ClosingEngine struct{ repository Repository }

func NewClosingEngine(repository Repository) *ClosingEngine {
	return &ClosingEngine{repository: repository}
}

func (e *ClosingEngine) GenerateClosingMove(year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
	if year.State == masterdata.FiscalYearHardClosed {
		return posting.JournalEntry{}, decimal.Zero, ErrFiscalYearAlreadyClosed
	}
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

func (e *ClosingEngine) GenerateOpeningMove(year masterdata.FiscalYear, journalID uuid.UUID, currency string) (posting.JournalEntry, error) {
	if year.StartDate.IsZero() || journalID == uuid.Nil || currency == "" {
		return posting.JournalEntry{}, ErrInvalidOpeningYear
	}
	repository := e.repository
	if year.CompanyID != uuid.Nil {
		var err error
		repository, err = newCompanyReportingRepository(repository, year.CompanyID)
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	balances, err := openingAccountBalances(repository, year.StartDate.Add(-time.Nanosecond), currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines := make([]posting.JournalEntryLine, 0)
	netIncome := decimal.Zero
	var retainedEarningsID uuid.UUID
	for _, account := range accounts {
		side := balances[account.ID]
		netBalance := side.debit.Sub(side.credit)
		if netBalance.IsZero() {
			continue
		}
		switch account.AccountType {
		case masterdata.AccountTypeAsset, masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
			line, err := singleAmountLine(account.ID, netBalance.Abs(), currency, netBalance.IsPositive())
			if err != nil {
				return posting.JournalEntry{}, err
			}
			lines = append(lines, line)
			if account.SubClassification == masterdata.SubClassRetainedEarnings {
				retainedEarningsID = account.ID
			}
		case masterdata.AccountTypeIncome, masterdata.AccountTypeExpense:
			netIncome = netIncome.Sub(netBalance)
			if account.SubClassification == masterdata.SubClassRetainedEarnings {
				retainedEarningsID = account.ID
			}
		}
	}
	if !netIncome.IsZero() {
		if retainedEarningsID == uuid.Nil {
			retainedEarningsID, err = FindRetainedEarningsAccount(accounts)
			if err != nil {
				return posting.JournalEntry{}, err
			}
		}
		line, err := retainedEarningsLine(retainedEarningsID, netIncome, currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, line)
	}
	if len(lines) < 2 {
		return posting.JournalEntry{}, nil
	}
	entry, err := posting.NewJournalEntry(journalID, year.StartDate, "opening-balance-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.DocumentReference = "YEAR-OPENING BALANCES"
	entry.Classification = posting.EntryClassOpening
	entry.TargetPeriodNumber = 1
	entry.CompanyID = year.CompanyID
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = year.CompanyID
	}
	return entry, nil
}

func openingAccountBalances(repository Repository, end time.Time, currency string) (map[uuid.UUID]accountSide, error) {
	accounts, err := listAccounts(repository)
	if err != nil {
		return nil, err
	}
	entries, err := listPostedEntries(repository, time.Time{}, end)
	if err != nil {
		return nil, err
	}
	return accountBalancesFromEntries(repository, accounts, entries, time.Time{}, end, currency)
}

func (e *ClosingEngine) ConfirmFiscalYearClose(year masterdata.FiscalYear, postedClosingEntryID uuid.UUID) error {
	repository, ok := e.repository.(FiscalYearRepository)
	if !ok {
		return masterdata.ErrFiscalYearNotFound
	}
	if year.ID == uuid.Nil {
		return masterdata.ErrFiscalYearNotFound
	}
	storedYear, err := repository.GetFiscalYear(year.ID)
	if err != nil {
		return err
	}
	if year.CompanyID != uuid.Nil && storedYear.CompanyID != year.CompanyID {
		return masterdata.ErrFiscalYearNotFound
	}
	year = storedYear
	entryRepository, ok := e.repository.(interface {
		GetEntryByID(uuid.UUID) (posting.JournalEntry, error)
	})
	if !ok {
		return ErrClosingMoveNotPosted
	}
	entry, err := entryRepository.GetEntryByID(postedClosingEntryID)
	if err != nil {
		return err
	}
	if entry.Status != posting.EntryPosted || entry.Classification != posting.EntryClassClosing ||
		entry.DocumentReference != "YEAR-END CLOSING" || entry.TargetPeriodNumber != 13 ||
		!entry.AdjustmentAuthorized ||
		(year.CompanyID != uuid.Nil && entry.CompanyID != year.CompanyID) {
		return ErrClosingMoveNotPosted
	}
	entryDate := entry.EntryDate
	if entryDate.IsZero() {
		entryDate = entry.Date
	}
	if entryDate.Before(year.StartDate) || entryDate.After(year.EndDate) {
		return ErrClosingMoveNotPosted
	}
	if err := requireMonthlyPeriodsClosed(year); err != nil {
		return err
	}
	period, ok := findFiscalPeriod(year, 13)
	if !ok {
		return ErrPeriodsNotHardClosed
	}
	if entry.PeriodID != period.ID {
		return ErrClosingMoveNotPosted
	}
	if err := hardCloseFiscalPeriod(repository, period); err != nil {
		return err
	}
	if auditPeriod, exists := findFiscalPeriod(year, 14); exists {
		if err := hardCloseFiscalPeriod(repository, auditPeriod); err != nil {
			return err
		}
	}
	switch year.State {
	case masterdata.FiscalYearHardClosed:
		return nil
	case masterdata.FiscalYearOpen:
		if err := repository.UpdateFiscalYearState(year.ID, masterdata.FiscalYearSoftClosed); err != nil {
			return err
		}
		return repository.UpdateFiscalYearState(year.ID, masterdata.FiscalYearHardClosed)
	case masterdata.FiscalYearSoftClosed:
		return repository.UpdateFiscalYearState(year.ID, masterdata.FiscalYearHardClosed)
	default:
		return ErrPeriodsNotHardClosed
	}
}

func hardCloseFiscalPeriod(repository FiscalYearRepository, period masterdata.FiscalPeriod) error {
	switch period.State {
	case masterdata.PeriodHardClosed:
		return nil
	case masterdata.PeriodOpen:
		if err := repository.UpdatePeriodState(period.ID, masterdata.PeriodSoftClosed); err != nil {
			return err
		}
		return repository.UpdatePeriodState(period.ID, masterdata.PeriodHardClosed)
	case masterdata.PeriodSoftClosed:
		return repository.UpdatePeriodState(period.ID, masterdata.PeriodHardClosed)
	default:
		return ErrPeriodsNotHardClosed
	}
}

func findFiscalPeriod(year masterdata.FiscalYear, number int) (masterdata.FiscalPeriod, bool) {
	for _, period := range year.Periods {
		if period.Number == number {
			return period, true
		}
	}
	return masterdata.FiscalPeriod{}, false
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
	entry.Classification = posting.EntryClassClosing
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
