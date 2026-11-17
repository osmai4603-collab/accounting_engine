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
	ErrFiscalYearAlreadyClosed               = errors.New("fiscal year is already closed")
	ErrFiscalYearNotOpen                     = errors.New("fiscal year is not open")
	ErrOpeningMoveNotRequired                = errors.New("opening balance moves are not supported by the perpetual general ledger")
	ErrInvalidOpeningYear                    = errors.New("opening balance year is invalid")
	ErrClosingMoveNotPosted                  = errors.New("closing move must be posted before the fiscal year can be closed")
	ErrFiscalYearNotClosedForCarryForward    = errors.New("fiscal year must be hard closed before balances are carried forward")
	ErrClosingPreCheckFailed                 = errors.New("closing pre-check failed: blockers exist")
	ErrInvalidClosingPolicy                  = errors.New("closing policy or income summary account is invalid")
	ErrClosingFunctionalCurrencyMismatch     = errors.New("closing entries require one company functional currency")
	ErrClosingAdjustmentPeriodUnavailable    = errors.New("period 13 must exist and be open for annual closing")
	ErrOpeningCurrencyAggregationUnsupported = errors.New("opening balance cannot preserve source currency and functional balance together")
	ErrAtomicYearEndReopenRequired           = errors.New("atomic year-end reversal and reopen capability is required")
)

type ClosingPolicy string

const (
	ClosingPolicyDirectToRetainedEarnings ClosingPolicy = "direct_to_retained_earnings"
	ClosingPolicyIncomeSummary            ClosingPolicy = "income_summary"
)

type ClosingOptions struct {
	Policy                 ClosingPolicy
	IncomeSummaryAccountID uuid.UUID
}

type ClosingEngine struct{ repository Repository }

func NewClosingEngine(repository Repository) *ClosingEngine {
	return &ClosingEngine{repository: repository}
}

func (e *ClosingEngine) GenerateClosingMove(year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
	return e.GenerateClosingMoveContext(context.Background(), year, journalID, retainedEarningsID, currency)
}

func (e *ClosingEngine) GenerateClosingMoveContext(ctx context.Context, year masterdata.FiscalYear, journalID, retainedEarningsID uuid.UUID, currency string) (posting.JournalEntry, decimal.Decimal, error) {
	entries, netIncome, err := e.GenerateClosingMovesContext(
		ctx, year, journalID, retainedEarningsID, currency,
		ClosingOptions{Policy: ClosingPolicyDirectToRetainedEarnings},
	)
	if err != nil {
		return posting.JournalEntry{}, decimal.Zero, err
	}
	if len(entries) == 0 {
		return posting.JournalEntry{}, netIncome, nil
	}
	return entries[0], netIncome, nil
}

// GenerateClosingMovesContext generates one or two annual closing entries.
// Income-summary closing returns the nominal-account close first and the
// income-summary transfer to retained earnings second.
func (e *ClosingEngine) GenerateClosingMovesContext(
	ctx context.Context,
	year masterdata.FiscalYear,
	journalID, retainedEarningsID uuid.UUID,
	currency string,
	options ClosingOptions,
) ([]posting.JournalEntry, decimal.Decimal, error) {
	if year.State != masterdata.FiscalYearOpen {
		return nil, decimal.Zero, ErrFiscalYearNotOpen
	}
	if retainedEarningsID == uuid.Nil {
		return nil, decimal.Zero, ErrRetainedEarningsNotSet
	}
	if options.Policy == "" {
		options.Policy = ClosingPolicyDirectToRetainedEarnings
	}
	if options.Policy != ClosingPolicyDirectToRetainedEarnings &&
		options.Policy != ClosingPolicyIncomeSummary {
		return nil, decimal.Zero, ErrInvalidClosingPolicy
	}
	if options.Policy == ClosingPolicyIncomeSummary &&
		(options.IncomeSummaryAccountID == uuid.Nil || options.IncomeSummaryAccountID == retainedEarningsID) {
		return nil, decimal.Zero, ErrInvalidClosingPolicy
	}
	if err := requireMonthlyPeriodsClosed(year); err != nil {
		return nil, decimal.Zero, err
	}
	if period13, ok := findFiscalPeriod(year, 13); !ok || period13.State != masterdata.PeriodOpen {
		return nil, decimal.Zero, ErrClosingAdjustmentPeriodUnavailable
	}
	repository := e.repository
	if year.CompanyID != uuid.Nil {
		var err error
		repository, err = newCompanyReportingRepository(ctx, repository, year.CompanyID, year.EndDate)
		if err != nil {
			return nil, decimal.Zero, err
		}
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return nil, decimal.Zero, err
	}
	retainedEarningsFound := false
	for _, account := range accounts {
		if account.ID == retainedEarningsID &&
			account.AccountType == masterdata.AccountTypeEquity &&
			account.SubClassification == masterdata.SubClassRetainedEarnings {
			retainedEarningsFound = true
			break
		}
	}
	if !retainedEarningsFound {
		return nil, decimal.Zero, ErrRetainedEarningsNotSet
	}
	if options.Policy == ClosingPolicyIncomeSummary {
		found := false
		for _, account := range accounts {
			if account.ID == options.IncomeSummaryAccountID &&
				account.AccountType == masterdata.AccountTypeEquity &&
				account.SubClassification != masterdata.SubClassRetainedEarnings {
				found = true
				break
			}
		}
		if !found {
			return nil, decimal.Zero, ErrInvalidClosingPolicy
		}
	}
	entries, err := listPostedEntries(repository, year.StartDate, year.EndDate)
	if err != nil {
		return nil, decimal.Zero, err
	}
	closingEntries, netIncome, err := generateYearEndEntries(
		year, journalID, retainedEarningsID, currency, options, accounts, entries,
	)
	if err != nil {
		return nil, decimal.Zero, err
	}
	return closingEntries, netIncome, nil
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
	firstPeriod, ok := findFiscalPeriod(year, 1)
	if !ok {
		return posting.JournalEntry{}, ErrInvalidOpeningYear
	}
	entry, err := posting.NewJournalEntry(journalID, year.StartDate, "opening-balance-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.DocumentReference = "YEAR-OPENING BALANCES"
	entry.Classification = posting.EntryClassOpening
	entry.TargetPeriodNumber = 1
	entry.PeriodID = firstPeriod.ID
	entry.CompanyID = year.CompanyID
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = year.CompanyID
	}
	return entry, nil
}

// GenerateYearEndOpeningMoveContext generates the next year's opening from
// the closed year's posted ledger. Source currency, rate, and dimensions are
// carried per unambiguous account balance; unsupported aggregation is rejected.
func (e *ClosingEngine) GenerateYearEndOpeningMoveContext(
	ctx context.Context,
	closedYear, nextYear masterdata.FiscalYear,
	journalID uuid.UUID,
	functionalCurrency string,
) (posting.JournalEntry, error) {
	if closedYear.ID == uuid.Nil || nextYear.ID == uuid.Nil ||
		closedYear.CompanyID == uuid.Nil || closedYear.CompanyID != nextYear.CompanyID ||
		journalID == uuid.Nil || functionalCurrency == "" ||
		!closedYear.EndDate.Add(time.Nanosecond).Equal(nextYear.StartDate) ||
		nextYear.State != masterdata.FiscalYearOpen {
		return posting.JournalEntry{}, ErrInvalidOpeningYear
	}
	firstPeriod, ok := findFiscalPeriod(nextYear, 1)
	if !ok || firstPeriod.State != masterdata.PeriodOpen ||
		firstPeriod.Type != masterdata.FiscalPeriodTypeNormal {
		return posting.JournalEntry{}, ErrInvalidOpeningYear
	}
	repository, err := newCompanyReportingRepository(ctx, e.repository, closedYear.CompanyID, closedYear.EndDate)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entries, err := listPostedEntries(repository, time.Time{}, closedYear.EndDate)
	if err != nil {
		return posting.JournalEntry{}, err
	}

	accountTypes := make(map[uuid.UUID]masterdata.AccountType, len(accounts))
	for _, account := range accounts {
		accountTypes[account.ID] = account.AccountType
	}
	balances := make(map[openingBalanceKey]openingBalance)
	balanceKeys := make([]openingBalanceKey, 0)
	profitByDimension := make(map[openingDimensionKey]decimal.Decimal)
	profitDimensions := make([]openingDimensionKey, 0)
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) {
			continue
		}
		for _, line := range entry.Lines {
			accountType, ok := accountTypes[line.AccountID]
			if !ok {
				return posting.JournalEntry{}, ErrReportingAccountNotFound
			}
			normalized, err := posting.NormalizeLineCurrencies(line)
			if err != nil {
				return posting.JournalEntry{}, err
			}
			if normalized.FunctionalDebit.Currency() != functionalCurrency ||
				normalized.FunctionalCredit.Currency() != functionalCurrency {
				return posting.JournalEntry{}, ErrClosingFunctionalCurrencyMismatch
			}
			dimensions := openingDimensions(normalized)
			debit := normalized.FunctionalDebit.Amount()
			credit := normalized.FunctionalCredit.Amount()
			switch accountType {
			case masterdata.AccountTypeIncome, masterdata.AccountTypeExpense:
				if _, exists := profitByDimension[dimensions]; !exists {
					profitDimensions = append(profitDimensions, dimensions)
				}
				profitByDimension[dimensions] = profitByDimension[dimensions].Add(credit.Sub(debit))
			case masterdata.AccountTypeAsset, masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
				key := openingBalanceKey{
					AccountID: line.AccountID, Currency: normalized.TransactionCurrency,
					Rate: normalized.ExchangeRate.String(), Dimensions: dimensions,
				}
				balance, exists := balances[key]
				if !exists {
					balanceKeys = append(balanceKeys, key)
				}
				balance.FunctionalNet = balance.FunctionalNet.Add(debit.Sub(credit))
				switch {
				case debit.IsPositive():
					balance.TransactionNet = balance.TransactionNet.Add(normalized.TransactionAmount.Amount())
				case credit.IsPositive():
					balance.TransactionNet = balance.TransactionNet.Sub(normalized.TransactionAmount.Amount())
				}
				balances[key] = balance
			}
		}
	}

	lines := make([]posting.JournalEntryLine, 0, len(balanceKeys)+len(profitDimensions))
	for _, key := range balanceKeys {
		balance := balances[key]
		if balance.FunctionalNet.IsZero() {
			continue
		}
		if balance.TransactionNet.IsZero() ||
			balance.FunctionalNet.Sign() != balance.TransactionNet.Sign() {
			return posting.JournalEntry{}, ErrOpeningCurrencyAggregationUnsupported
		}
		rate, err := decimal.NewFromString(key.Rate)
		if err != nil || !balance.TransactionNet.Abs().Mul(rate).Equal(balance.FunctionalNet.Abs()) {
			return posting.JournalEntry{}, ErrOpeningCurrencyAggregationUnsupported
		}
		line, err := openingBalanceLine(key, balance, functionalCurrency, rate)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		line.CompanyID = closedYear.CompanyID
		lines = append(lines, line)
	}

	retainedEarningsID, err := FindRetainedEarningsAccount(accounts)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	for _, dimensions := range profitDimensions {
		netIncome := profitByDimension[dimensions]
		if netIncome.IsZero() {
			continue
		}
		line, err := retainedEarningsLine(retainedEarningsID, netIncome, functionalCurrency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		line.CompanyID = closedYear.CompanyID
		setOpeningLineDimensions(&line, dimensions)
		lines = append(lines, line)
	}
	if len(lines) < 2 {
		return posting.JournalEntry{}, nil
	}
	entry, err := posting.NewJournalEntry(journalID, nextYear.StartDate, "year-end-opening-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.CompanyID = nextYear.CompanyID
	entry.DocumentReference = "YEAR-OPENING BALANCES"
	entry.Classification = posting.EntryClassOpening
	entry.TargetPeriodNumber = 1
	entry.PeriodID = firstPeriod.ID
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = nextYear.CompanyID
	}
	return entry, nil
}

type openingDimensionKey struct {
	partnerID, costCenterID, projectID, segmentID, businessUnitID      uuid.UUID
	hasPartner, hasCostCenter, hasProject, hasSegment, hasBusinessUnit bool
}

type openingBalanceKey struct {
	AccountID  uuid.UUID
	Currency   string
	Rate       string
	Dimensions openingDimensionKey
}

type openingBalance struct {
	FunctionalNet  decimal.Decimal
	TransactionNet decimal.Decimal
}

func openingDimensions(line posting.JournalEntryLine) openingDimensionKey {
	result := openingDimensionKey{}
	if line.PartnerID != nil {
		result.partnerID, result.hasPartner = *line.PartnerID, true
	}
	if line.CostCenterID != nil {
		result.costCenterID, result.hasCostCenter = *line.CostCenterID, true
	}
	if line.ProjectID != nil {
		result.projectID, result.hasProject = *line.ProjectID, true
	}
	if line.SegmentID != nil {
		result.segmentID, result.hasSegment = *line.SegmentID, true
	}
	if line.BusinessUnitID != nil {
		result.businessUnitID, result.hasBusinessUnit = *line.BusinessUnitID, true
	}
	return result
}

func setOpeningLineDimensions(line *posting.JournalEntryLine, dimensions openingDimensionKey) {
	if dimensions.hasPartner {
		id := dimensions.partnerID
		line.PartnerID = &id
	}
	if dimensions.hasCostCenter {
		id := dimensions.costCenterID
		line.CostCenterID = &id
	}
	if dimensions.hasProject {
		id := dimensions.projectID
		line.ProjectID = &id
	}
	if dimensions.hasSegment {
		id := dimensions.segmentID
		line.SegmentID = &id
	}
	if dimensions.hasBusinessUnit {
		id := dimensions.businessUnitID
		line.BusinessUnitID = &id
	}
}

func openingBalanceLine(
	key openingBalanceKey,
	balance openingBalance,
	functionalCurrency string,
	rate decimal.Decimal,
) (posting.JournalEntryLine, error) {
	transactionAmount, err := financialtypes.NewMoney(balance.TransactionNet.Abs(), key.Currency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	functionalAmount, err := financialtypes.NewMoney(balance.FunctionalNet.Abs(), functionalCurrency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	zero, err := financialtypes.Zero(functionalCurrency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	line := posting.JournalEntryLine{
		ID: uuid.New(), AccountID: key.AccountID, TransactionAmount: transactionAmount,
		TransactionCurrency: key.Currency, ExchangeRate: rate,
		Debit: zero, Credit: zero, FunctionalDebit: zero, FunctionalCredit: zero,
	}
	if balance.FunctionalNet.IsPositive() {
		line.Debit, line.FunctionalDebit = functionalAmount, functionalAmount
		line.FunctionalCredit = zero
	} else {
		line.Credit, line.FunctionalCredit = functionalAmount, functionalAmount
		line.FunctionalDebit = zero
	}
	setOpeningLineDimensions(&line, key.Dimensions)
	return line, nil
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

// ConfirmFiscalYearCloseContext is a legacy finalization operation for an
// already-posted entry. It does not atomically commit that entry, generated
// opening entries, audit evidence, and fiscal-period state.
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
		(entry.DocumentReference != "YEAR-END CLOSING" &&
			entry.DocumentReference != "YEAR-END CLOSING - INCOME SUMMARY") ||
		entry.TargetPeriodNumber != 13 ||
		!entry.AdjustmentAuthorized ||
		(year.CompanyID != uuid.Nil && entry.CompanyID != year.CompanyID) {
		return fmt.Errorf("%w: invalid status, classification, reference, target period, authorization, or company",
			ErrClosingMoveNotPosted)
	}
	entryDate := entry.EntryDate
	if entryDate.IsZero() {
		entryDate = entry.Date
	}
	if entryDate.Before(year.StartDate) || entryDate.After(year.EndDate) {
		return fmt.Errorf("%w: entry date is outside fiscal year", ErrClosingMoveNotPosted)
	}
	if err := requireMonthlyPeriodsClosed(year); err != nil {
		return err
	}
	period, ok := findFiscalPeriod(year, 13)
	if !ok {
		return ErrPeriodsNotHardClosed
	}
	if entry.PeriodID != period.ID {
		return fmt.Errorf("%w: entry is not assigned to period 13", ErrClosingMoveNotPosted)
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

func (e *ClosingEngine) ConfirmFiscalYearCloseWithoutEntriesContext(
	ctx context.Context,
	year masterdata.FiscalYear,
	journalID, retainedEarningsID uuid.UUID,
	currency string,
	options ClosingOptions,
) error {
	storedYear, err := e.repository.GetFiscalYearForCompanyContext(ctx, year.CompanyID, year.ID)
	if err != nil {
		return err
	}
	if storedYear.State != masterdata.FiscalYearOpen {
		return ErrFiscalYearNotOpen
	}
	entries, _, err := e.GenerateClosingMovesContext(
		ctx, storedYear, journalID, retainedEarningsID, currency, options,
	)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return ErrClosingMoveNotPosted
	}
	closePeriod := func(number int) error {
		period, ok := findFiscalPeriod(storedYear, number)
		if !ok {
			return ErrPeriodsNotHardClosed
		}
		switch period.State {
		case masterdata.PeriodHardClosed:
			return nil
		case masterdata.PeriodOpen:
			if err := e.repository.UpdatePeriodStateForCompanyContext(ctx, storedYear.CompanyID, period.ID, masterdata.PeriodSoftClosed); err != nil {
				return err
			}
			return e.repository.UpdatePeriodStateForCompanyContext(ctx, storedYear.CompanyID, period.ID, masterdata.PeriodHardClosed)
		case masterdata.PeriodSoftClosed:
			return e.repository.UpdatePeriodStateForCompanyContext(ctx, storedYear.CompanyID, period.ID, masterdata.PeriodHardClosed)
		default:
			return ErrPeriodsNotHardClosed
		}
	}
	if err := closePeriod(13); err != nil {
		return err
	}
	if _, ok := findFiscalPeriod(storedYear, 14); ok {
		if err := closePeriod(14); err != nil {
			return err
		}
	}
	if err := e.repository.UpdateFiscalYearStateForCompanyContext(
		ctx, storedYear.CompanyID, storedYear.ID, masterdata.FiscalYearSoftClosed,
	); err != nil {
		return err
	}
	return e.repository.UpdateFiscalYearStateForCompanyContext(
		ctx, storedYear.CompanyID, storedYear.ID, masterdata.FiscalYearHardClosed,
	)
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
	entries, report, err := e.CloseFiscalYearWithChecklistPolicy(
		ctx, year, journalID, retainedEarningsID, actorID, currency, reason,
		ClosingOptions{Policy: ClosingPolicyDirectToRetainedEarnings},
	)
	if err != nil {
		return posting.JournalEntry{}, report, err
	}
	if len(entries) == 0 {
		return posting.JournalEntry{}, report, nil
	}
	return entries[0], report, nil
}

// CloseFiscalYearWithChecklistPolicy runs the pre-close checks and generates
// entries according to the requested accounting policy. Posting and the
// hard-close transition remain separate operations on the legacy API.
func (e *ClosingEngine) CloseFiscalYearWithChecklistPolicy(
	ctx context.Context,
	year masterdata.FiscalYear,
	journalID, retainedEarningsID, actorID uuid.UUID,
	currency string,
	reason string,
	options ClosingOptions,
) ([]posting.JournalEntry, ClosingChecklistReport, error) {
	if actorID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return nil, ClosingChecklistReport{}, masterdata.ErrPeriodTransitionNeedsAudit
	}

	report, err := e.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		return nil, report, err
	}
	if !report.CanClose {
		return nil, report, fmt.Errorf("%w: %s", ErrClosingPreCheckFailed, report.Summary())
	}

	closingEntries, _, err := e.GenerateClosingMovesContext(
		ctx, year, journalID, retainedEarningsID, currency, options,
	)
	if err != nil {
		return nil, report, err
	}

	return closingEntries, report, nil
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

	reopenRepo, ok := e.repository.(YearEndCloseReopenRepository)
	if !ok {
		return ErrAtomicYearEndReopenRequired
	}
	return reopenRepo.ReopenYearEndCloseForCompanyContext(ctx, companyID, yearID, actorID, reason)
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
		return fmt.Errorf("%w: closing entry has no currency", ErrClosingMoveNotPosted)
	}
	for _, line := range entry.Lines {
		_, _, lineCurrency := posting.LineFunctionalAmounts(line)
		if lineCurrency != currency {
			return fmt.Errorf("%w: closing line currency %q differs from %q",
				ErrClosingMoveNotPosted, lineCurrency, currency)
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
	retainedID, err := FindRetainedEarningsAccount(accounts)
	if err != nil {
		return err
	}
	directEntries, _, err := generateYearEndEntries(
		year, entry.JournalID, retainedID, currency,
		ClosingOptions{Policy: ClosingPolicyDirectToRetainedEarnings}, accounts, yearEntries,
	)
	if err != nil {
		return err
	}
	if len(directEntries) == 1 && closingLinesMatch(entry.Lines, directEntries[0].Lines) {
		return nil
	}
	for _, summaryAccount := range accounts {
		if summaryAccount.AccountType != masterdata.AccountTypeEquity ||
			summaryAccount.SubClassification == masterdata.SubClassRetainedEarnings {
			continue
		}
		expectedEntries, _, err := generateYearEndEntries(
			year, entry.JournalID, retainedID, currency,
			ClosingOptions{
				Policy: ClosingPolicyIncomeSummary, IncomeSummaryAccountID: summaryAccount.ID,
			}, accounts, yearEntries,
		)
		if err != nil {
			return err
		}
		if len(expectedEntries) != 2 || !closingLinesMatch(entry.Lines, expectedEntries[0].Lines) {
			continue
		}
		if err := validateIncomeSummaryTransfer(
			repository, year, expectedEntries[1],
		); err == nil {
			return nil
		} else if !errors.Is(err, ErrClosingMoveNotPosted) {
			return err
		}
	}
	return fmt.Errorf("%w: posted closing lines do not match any valid closing policy",
		ErrClosingMoveNotPosted)
}

func validateIncomeSummaryTransfer(
	repository Repository,
	year masterdata.FiscalYear,
	expected posting.JournalEntry,
) error {
	entries, err := listPostedEntries(repository, year.StartDate, year.EndDate)
	if err != nil {
		return err
	}
	period, ok := findFiscalPeriod(year, 13)
	if !ok {
		return ErrClosingMoveNotPosted
	}
	for _, candidate := range entries {
		if candidate.Status != posting.EntryPosted ||
			candidate.Classification != posting.EntryClassClosing ||
			candidate.DocumentReference != "YEAR-END CLOSING - INCOME SUMMARY TRANSFER" ||
			candidate.JournalID != expected.JournalID ||
			candidate.TargetPeriodNumber != 13 ||
			candidate.PeriodID != period.ID ||
			(candidate.CompanyID != uuid.Nil && candidate.CompanyID != year.CompanyID) {
			continue
		}
		if closingLinesMatch(candidate.Lines, expected.Lines) {
			return nil
		}
	}
	return ErrClosingMoveNotPosted
}

func closingLinesMatch(actual, expected []posting.JournalEntryLine) bool {
	if len(actual) != len(expected) {
		return false
	}
	matched := make([]bool, len(expected))
	for _, actualLine := range actual {
		found := false
		for i, expectedLine := range expected {
			if matched[i] {
				continue
			}
			expectedLine.ID = actualLine.ID
			expectedLine.Debit = actualLine.Debit
			expectedLine.Credit = actualLine.Credit
			if actualLine.Equal(expectedLine) {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
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

func generateYearEndEntries(
	year masterdata.FiscalYear,
	journalID, retainedEarningsID uuid.UUID,
	functionalCurrency string,
	options ClosingOptions,
	accounts []masterdata.Account,
	entries []posting.JournalEntry,
) ([]posting.JournalEntry, decimal.Decimal, error) {
	accountTypes := make(map[uuid.UUID]masterdata.AccountType, len(accounts))
	for _, account := range accounts {
		accountTypes[account.ID] = account.AccountType
	}
	nominalLines := make([]posting.JournalEntryLine, 0)
	offsetLines := make([]posting.JournalEntryLine, 0)
	netIncome := decimal.Zero
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) {
			continue
		}
		for _, source := range entry.Lines {
			accountType, known := accountTypes[source.AccountID]
			if !known {
				return nil, decimal.Zero, ErrReportingAccountNotFound
			}
			if accountType != masterdata.AccountTypeIncome && accountType != masterdata.AccountTypeExpense {
				continue
			}
			normalized, err := posting.NormalizeLineCurrencies(source)
			if err != nil {
				return nil, decimal.Zero, err
			}
			sourceDebit := normalized.FunctionalDebit.Amount()
			sourceCredit := normalized.FunctionalCredit.Amount()
			if normalized.FunctionalDebit.Currency() != functionalCurrency ||
				normalized.FunctionalCredit.Currency() != functionalCurrency {
				return nil, decimal.Zero, ErrClosingFunctionalCurrencyMismatch
			}
			if normalized.TransactionAmount.Amount().IsZero() {
				continue
			}
			netIncome = netIncome.Add(sourceCredit.Sub(sourceDebit))
			reversal, err := closingLineFromSource(normalized, source.AccountID, sourceCredit, sourceDebit)
			if err != nil {
				return nil, decimal.Zero, err
			}
			nominalLines = append(nominalLines, reversal)

			offsetAccountID := retainedEarningsID
			if options.Policy == ClosingPolicyIncomeSummary {
				offsetAccountID = options.IncomeSummaryAccountID
			}
			offset, err := functionalClosingLine(
				normalized, offsetAccountID, sourceDebit, sourceCredit,
			)
			if err != nil {
				return nil, decimal.Zero, err
			}
			offsetLines = append(offsetLines, offset)
		}
	}
	if len(nominalLines) == 0 {
		return nil, netIncome, nil
	}

	firstLines := append(append([]posting.JournalEntryLine(nil), nominalLines...), offsetLines...)
	firstReference := "YEAR-END CLOSING"
	if options.Policy == ClosingPolicyIncomeSummary {
		firstReference += " - INCOME SUMMARY"
	}
	first, err := newClosingEntry(year, journalID, firstReference, firstLines)
	if err != nil {
		return nil, decimal.Zero, err
	}
	result := []posting.JournalEntry{first}
	if options.Policy != ClosingPolicyIncomeSummary {
		return result, netIncome, nil
	}

	transferLines := make([]posting.JournalEntryLine, 0, len(offsetLines)*2)
	for _, offset := range offsetLines {
		summaryReversal, err := reverseClosingLine(offset)
		if err != nil {
			return nil, decimal.Zero, err
		}
		retainedOffset, err := functionalClosingLine(
			offset, retainedEarningsID,
			offset.FunctionalDebit.Amount(), offset.FunctionalCredit.Amount(),
		)
		if err != nil {
			return nil, decimal.Zero, err
		}
		transferLines = append(transferLines, summaryReversal, retainedOffset)
	}
	transfer, err := newClosingEntry(
		year, journalID, "YEAR-END CLOSING - INCOME SUMMARY TRANSFER", transferLines,
	)
	if err != nil {
		return nil, decimal.Zero, err
	}
	result = append(result, transfer)
	return result, netIncome, nil
}

func newClosingEntry(
	year masterdata.FiscalYear,
	journalID uuid.UUID,
	reference string,
	lines []posting.JournalEntryLine,
) (posting.JournalEntry, error) {
	entry, err := posting.NewJournalEntry(journalID, year.EndDate, "closing-engine", lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.CompanyID = year.CompanyID
	entry.DocumentReference = reference
	entry.Classification = posting.EntryClassClosing
	entry.TargetPeriodNumber = 13
	entry.AdjustmentAuthorized = true
	if period13, ok := findFiscalPeriod(year, 13); ok {
		entry.PeriodID = period13.ID
	}
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = year.CompanyID
	}
	return entry, nil
}

func closingLineFromSource(
	source posting.JournalEntryLine,
	accountID uuid.UUID,
	debit, credit decimal.Decimal,
) (posting.JournalEntryLine, error) {
	debitMoney, err := financialtypes.NewMoney(debit, source.FunctionalDebit.Currency())
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	creditMoney, err := financialtypes.NewMoney(credit, source.FunctionalCredit.Currency())
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	return posting.JournalEntryLine{
		CompanyID:           source.CompanyID,
		ID:                  uuid.New(),
		AccountID:           accountID,
		PartnerID:           source.PartnerID,
		CostCenterID:        source.CostCenterID,
		ProjectID:           source.ProjectID,
		SegmentID:           source.SegmentID,
		BusinessUnitID:      source.BusinessUnitID,
		MatchingNumber:      source.MatchingNumber,
		ReconciledAmount:    source.ReconciledAmount,
		Debit:               debitMoney,
		Credit:              creditMoney,
		TransactionAmount:   source.TransactionAmount,
		TransactionCurrency: source.TransactionCurrency,
		ExchangeRate:        source.ExchangeRate,
		FunctionalDebit:     debitMoney,
		FunctionalCredit:    creditMoney,
	}, nil
}

func functionalClosingLine(
	source posting.JournalEntryLine,
	accountID uuid.UUID,
	debit, credit decimal.Decimal,
) (posting.JournalEntryLine, error) {
	currency := source.FunctionalDebit.Currency()
	if currency == "" {
		currency = source.FunctionalCredit.Currency()
	}
	debitMoney, err := financialtypes.NewMoney(debit, currency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	creditMoney, err := financialtypes.NewMoney(credit, currency)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	amount := debit
	if amount.IsZero() {
		amount = credit
	}
	transactionAmount := source.TransactionAmount
	transactionCurrency := source.TransactionCurrency
	exchangeRate := source.ExchangeRate
	if transactionCurrency == "" || transactionAmount.Currency() == "" || exchangeRate.IsZero() {
		transactionAmount, err = financialtypes.NewMoney(amount, currency)
		if err != nil {
			return posting.JournalEntryLine{}, err
		}
		transactionCurrency = currency
		exchangeRate = decimal.NewFromInt(1)
	}
	return posting.JournalEntryLine{
		CompanyID:           source.CompanyID,
		ID:                  uuid.New(),
		AccountID:           accountID,
		PartnerID:           source.PartnerID,
		CostCenterID:        source.CostCenterID,
		ProjectID:           source.ProjectID,
		SegmentID:           source.SegmentID,
		BusinessUnitID:      source.BusinessUnitID,
		MatchingNumber:      source.MatchingNumber,
		ReconciledAmount:    decimal.Zero,
		Debit:               debitMoney,
		Credit:              creditMoney,
		TransactionAmount:   transactionAmount,
		TransactionCurrency: transactionCurrency,
		ExchangeRate:        exchangeRate,
		FunctionalDebit:     debitMoney,
		FunctionalCredit:    creditMoney,
	}, nil
}

func reverseClosingLine(source posting.JournalEntryLine) (posting.JournalEntryLine, error) {
	normalized, err := posting.NormalizeLineCurrencies(source)
	if err != nil {
		return posting.JournalEntryLine{}, err
	}
	return closingLineFromSource(
		normalized, source.AccountID,
		normalized.FunctionalCredit.Amount(), normalized.FunctionalDebit.Amount(),
	)
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
	line := posting.JournalEntryLine{ID: uuid.New(), AccountID: accountID, Debit: zero, Credit: zero}
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
