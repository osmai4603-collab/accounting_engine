package reporting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrFiscalYearAlreadyClosed            = errors.New("fiscal year is already closed")
	ErrOpeningMoveNotRequired             = errors.New("opening balance moves are not supported by the perpetual general ledger")
	ErrInvalidOpeningYear                 = errors.New("opening balance year is invalid")
	ErrClosingMoveNotPosted               = errors.New("closing move must be posted before the fiscal year can be closed")
	ErrFiscalYearNotClosedForCarryForward = errors.New("fiscal year must be hard closed before balances are carried forward")
	ErrClosingPreCheckFailed              = errors.New("closing pre-check failed: blockers exist")
)

type ClosingEngine struct{ repository Repository }

func NewClosingEngine(repository Repository) *ClosingEngine {
	return &ClosingEngine{repository: repository}
}

func (e *ClosingEngine) GenerateClosingMove(year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
	return e.GenerateClosingMoveContext(context.Background(), year, journalID, retainedEarningsID, currency)
}

func (e *ClosingEngine) GenerateClosingMoveContext(ctx context.Context, year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
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
		repository, err = newCompanyReportingRepository(ctx, repository, year.CompanyID, year.EndDate)
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
	return e.GenerateOpeningMoveContext(context.Background(), year, journalID, currency)
}

func (e *ClosingEngine) GenerateOpeningMoveContext(ctx context.Context, year masterdata.FiscalYear, journalID uuid.UUID, currency string) (posting.JournalEntry, error) {
	if year.StartDate.IsZero() || journalID == uuid.Nil || currency == "" {
		return posting.JournalEntry{}, ErrInvalidOpeningYear
	}
	repository := e.repository
	if year.CompanyID != uuid.Nil {
		var err error
		repository, err = newCompanyReportingRepository(ctx, repository, year.CompanyID, year.StartDate.Add(-time.Nanosecond))
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if history, ok := repository.(FiscalYearHistoryRepository); ok {
		isFirstYear, err := history.IsFirstFiscalYearForCompanyContext(ctx, year.CompanyID, year.ID)
		if err != nil {
			if !errors.Is(err, ErrFiscalYearHistoryRepositoryRequired) {
				return posting.JournalEntry{}, err
			}
			if err := rejectOpeningMoveAfterPriorActivity(repository, year.StartDate); err != nil {
				return posting.JournalEntry{}, err
			}
		} else if !isFirstYear {
			return posting.JournalEntry{}, ErrOpeningMoveNotRequired
		}
	} else if err := rejectOpeningMoveAfterPriorActivity(repository, year.StartDate); err != nil {
		return posting.JournalEntry{}, err
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

func rejectOpeningMoveAfterPriorActivity(repository Repository, yearStart time.Time) error {
	entries, err := listPostedEntries(repository, time.Time{}, yearStart.Add(-time.Nanosecond))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if isLedgerPosted(entry.Status) {
			return ErrOpeningMoveNotRequired
		}
	}
	return nil
}

func (e *ClosingEngine) CarryForwardBalances(
	ctx context.Context,
	closedYear masterdata.FiscalYear,
	newYearFirstPeriodID uuid.UUID,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if closedYear.ID == uuid.Nil || closedYear.CompanyID == uuid.Nil || newYearFirstPeriodID == uuid.Nil {
		return ErrInvalidOpeningYear
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		return ErrFiscalYearNotClosedForCarryForward
	}
	repository, err := newCompanyReportingRepository(
		ctx, e.repository, closedYear.CompanyID, closedYear.EndDate,
	)
	if err != nil {
		return err
	}
	carryForward, ok := repository.(BalanceCarryForwardRepository)
	if !ok {
		return ErrAccountBalanceRepositoryRequired
	}
	return carryForward.CarryForwardBalancesForCompanyContext(
		ctx, closedYear.CompanyID, closedYear.ID, newYearFirstPeriodID,
	)
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
	return e.ConfirmFiscalYearCloseContext(context.Background(), year, postedClosingEntryID)
}

func (e *ClosingEngine) ConfirmFiscalYearCloseContext(ctx context.Context, year masterdata.FiscalYear, postedClosingEntryID uuid.UUID) error {
	if year.ID == uuid.Nil {
		return masterdata.ErrFiscalYearNotFound
	}
	if year.CompanyID == uuid.Nil {
		return ErrCompanyScopeRequired
	}
	storedYear, err := e.repository.GetFiscalYearForCompanyContext(ctx, year.CompanyID, year.ID)
	if err != nil {
		return err
	}
	if storedYear.CompanyID != year.CompanyID {
		return masterdata.ErrFiscalYearNotFound
	}
	year = storedYear
	entry, err := e.repository.GetEntryByIDForCompanyContext(ctx, year.CompanyID, postedClosingEntryID)
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
	if err := e.validateClosingEntryContents(ctx, year, entry); err != nil {
		return err
	}

	changedPeriods := make([]periodStateChange, 0, 2)
	changedYear := false
	setPeriodState := func(periodID uuid.UUID, original masterdata.PeriodState, state masterdata.PeriodState) error {
		if err := e.repository.UpdatePeriodStateForCompanyContext(ctx, year.CompanyID, periodID, state); err != nil {
			return err
		}
		for _, change := range changedPeriods {
			if change.id == periodID {
				return nil
			}
		}
		changedPeriods = append(changedPeriods, periodStateChange{id: periodID, state: original})
		return nil
	}
	rollback := func(cause error) error {
		var rollbackErr error
		for i := len(changedPeriods) - 1; i >= 0; i-- {
			change := changedPeriods[i]
			rollbackErr = errors.Join(rollbackErr, e.repository.UpdatePeriodStateForCompanyContext(
				ctx, year.CompanyID, change.id, change.state,
			))
		}
		if changedYear {
			rollbackErr = errors.Join(rollbackErr, e.repository.UpdateFiscalYearStateForCompanyContext(
				ctx, year.CompanyID, year.ID, year.State,
			))
		}
		return errors.Join(cause, rollbackErr)
	}
	closePeriod := func(target masterdata.FiscalPeriod) error {
		switch target.State {
		case masterdata.PeriodHardClosed:
			return nil
		case masterdata.PeriodOpen:
			if err := setPeriodState(target.ID, target.State, masterdata.PeriodSoftClosed); err != nil {
				return err
			}
			return setPeriodState(target.ID, target.State, masterdata.PeriodHardClosed)
		case masterdata.PeriodSoftClosed:
			return setPeriodState(target.ID, target.State, masterdata.PeriodHardClosed)
		default:
			return ErrPeriodsNotHardClosed
		}
	}
	if err := closePeriod(period); err != nil {
		return rollback(err)
	}
	if auditPeriod, exists := findFiscalPeriod(year, 14); exists {
		if err := closePeriod(auditPeriod); err != nil {
			return rollback(err)
		}
	}
	switch year.State {
	case masterdata.FiscalYearHardClosed:
		return nil
	case masterdata.FiscalYearOpen:
		if err := e.repository.UpdateFiscalYearStateForCompanyContext(ctx, year.CompanyID, year.ID, masterdata.FiscalYearSoftClosed); err != nil {
			return rollback(err)
		}
		changedYear = true
		if err := e.repository.UpdateFiscalYearStateForCompanyContext(ctx, year.CompanyID, year.ID, masterdata.FiscalYearHardClosed); err != nil {
			return rollback(err)
		}
		return nil
	case masterdata.FiscalYearSoftClosed:
		if err := e.repository.UpdateFiscalYearStateForCompanyContext(ctx, year.CompanyID, year.ID, masterdata.FiscalYearHardClosed); err != nil {
			return rollback(err)
		}
		return nil
	default:
		return rollback(ErrPeriodsNotHardClosed)
	}
}

// EvaluateClosingChecklist runs the formal closing checklist and pre-close checks for a fiscal year.
func (e *ClosingEngine) EvaluateClosingChecklist(ctx context.Context, year masterdata.FiscalYear) (ClosingChecklistReport, error) {
	return EvaluateFiscalYearChecklist(ctx, e.repository, year)
}

// CloseFiscalYearWithChecklist validates pre-close checklist blockers before generating the closing move.
func (e *ClosingEngine) CloseFiscalYearWithChecklist(
	ctx context.Context,
	year masterdata.FiscalYear,
	journalID, retainedEarningsID, actorID uuid.UUID,
	currency string,
	reason string,
) (posting.JournalEntry, ClosingChecklistReport, error) {
	if actorID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return posting.JournalEntry{}, ClosingChecklistReport{}, masterdata.ErrPeriodTransitionNeedsAudit
	}

	report, err := e.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		return posting.JournalEntry{}, report, err
	}
	if !report.CanClose {
		return posting.JournalEntry{}, report, fmt.Errorf("%w: %s", ErrClosingPreCheckFailed, report.Summary())
	}

	closingEntry, _, err := e.GenerateClosingMoveContext(ctx, year, journalID, retainedEarningsID, currency)
	if err != nil {
		return posting.JournalEntry{}, report, err
	}

	return closingEntry, report, nil
}

// ReopenFiscalYear performs an audited reopening of a closed fiscal year.
// It requires an explicit actor UUID and non-empty business justification.
func (e *ClosingEngine) ReopenFiscalYear(
	ctx context.Context,
	companyID, yearID, actorID uuid.UUID,
	reason string,
) error {
	if companyID == uuid.Nil || yearID == uuid.Nil {
		return masterdata.ErrFiscalYearNotFound
	}
	if actorID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return masterdata.ErrPeriodReopenRequiresAudit
	}

	year, err := e.repository.GetFiscalYearForCompanyContext(ctx, companyID, yearID)
	if err != nil {
		return err
	}
	if year.State == masterdata.FiscalYearOpen {
		return errors.New("fiscal year is already open")
	}

	if err := masterdata.ValidateFiscalYearReopen(year.State, masterdata.FiscalYearOpen, actorID, reason); err != nil {
		return err
	}

	// Reopen the fiscal year state to Open using audited reopen repository if available
	if reopenRepo, ok := e.repository.(FiscalYearReopenRepository); ok {
		if err := reopenRepo.ReopenFiscalYearForCompanyContext(ctx, companyID, yearID, actorID, reason); err != nil {
			return err
		}
	} else if err := e.repository.UpdateFiscalYearStateForCompanyContext(ctx, companyID, yearID, masterdata.FiscalYearOpen); err != nil {
		return err
	}

	// Reopen adjustment period 13 if present
	if p13, ok := findFiscalPeriod(year, 13); ok && p13.State != masterdata.PeriodOpen {
		if periodReopenRepo, ok := e.repository.(PeriodReopenRepository); ok {
			_ = periodReopenRepo.ReopenPeriodContext(ctx, companyID, p13.ID, actorID, reason)
		} else {
			_ = e.repository.UpdatePeriodStateForCompanyContext(ctx, companyID, p13.ID, masterdata.PeriodOpen)
		}
	}
	// Reopen adjustment period 14 if present
	if p14, ok := findFiscalPeriod(year, 14); ok && p14.State != masterdata.PeriodOpen {
		if periodReopenRepo, ok := e.repository.(PeriodReopenRepository); ok {
			_ = periodReopenRepo.ReopenPeriodContext(ctx, companyID, p14.ID, actorID, reason)
		} else {
			_ = e.repository.UpdatePeriodStateForCompanyContext(ctx, companyID, p14.ID, masterdata.PeriodOpen)
		}
	}

	return nil
}

type periodStateChange struct {
	id    uuid.UUID
	state masterdata.PeriodState
}

func (e *ClosingEngine) validateClosingEntryContents(
	ctx context.Context,
	year masterdata.FiscalYear,
	entry posting.JournalEntry,
) error {
	currency := entry.TransactionCurrency
	if currency == "" && len(entry.Lines) > 0 {
		_, _, currency = posting.LineFunctionalAmounts(entry.Lines[0])
	}
	if currency == "" {
		return ErrClosingMoveNotPosted
	}
	for _, line := range entry.Lines {
		_, _, lineCurrency := posting.LineFunctionalAmounts(line)
		if lineCurrency != currency {
			return ErrClosingMoveNotPosted
		}
	}

	repository, err := newCompanyReportingRepository(ctx, e.repository, year.CompanyID, year.EndDate)
	if err != nil {
		return err
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return err
	}
	entries, err := listPostedEntries(repository, year.StartDate, year.EndDate)
	if err != nil {
		return err
	}
	yearEntries := make([]posting.JournalEntry, 0, len(entries))
	for _, candidate := range entries {
		if candidate.ID != entry.ID {
			yearEntries = append(yearEntries, candidate)
		}
	}
	balances, err := accountBalancesFromEntries(
		repository, accounts, yearEntries, year.StartDate, year.EndDate, currency,
	)
	if err != nil {
		return err
	}
	actual := closingEntryAmounts(entry)
	for _, account := range accounts {
		if account.AccountType != masterdata.AccountTypeEquity ||
			account.SubClassification != masterdata.SubClassRetainedEarnings {
			continue
		}
		expectedEntry, _, err := e.generateYearMove(
			year.EndDate, entry.JournalID, account.ID, currency, accounts, balances,
		)
		if err != nil {
			return err
		}
		if closingEntriesMatch(actual, closingEntryAmounts(expectedEntry)) {
			return nil
		}
	}
	return ErrClosingMoveNotPosted
}

type closingAmounts struct {
	debit    decimal.Decimal
	credit   decimal.Decimal
	currency string
}

func closingEntryAmounts(entry posting.JournalEntry) map[uuid.UUID]closingAmounts {
	amounts := make(map[uuid.UUID]closingAmounts, len(entry.Lines))
	for _, line := range entry.Lines {
		debit, credit, currency := posting.LineFunctionalAmounts(line)
		current := amounts[line.AccountID]
		current.debit = current.debit.Add(debit)
		current.credit = current.credit.Add(credit)
		if current.currency == "" {
			current.currency = currency
		} else if currency != "" && currency != current.currency {
			current.currency = ""
		}
		amounts[line.AccountID] = current
	}
	return amounts
}

func closingEntriesMatch(actual, expected map[uuid.UUID]closingAmounts) bool {
	if len(actual) != len(expected) {
		return false
	}
	for accountID, want := range expected {
		got, ok := actual[accountID]
		if !ok || got.currency != want.currency ||
			!got.debit.Equal(want.debit) || !got.credit.Equal(want.credit) {
			return false
		}
	}
	return true
}

func FindFiscalPeriod(year masterdata.FiscalYear, number int) (masterdata.FiscalPeriod, bool) {
	return findFiscalPeriod(year, number)
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
