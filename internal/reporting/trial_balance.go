package reporting

import (
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrUnbalancedTrialBalance = errors.New("trial balance is not balanced")
	ErrUnbalancedBalanceSheet = errors.New("balance sheet is not balanced")
	ErrUnbalancedCashFlow     = errors.New("cash flow does not reconcile to the cash balance")
	ErrUnbalancedClosing      = errors.New("closing entry is not balanced")
	ErrPeriodsNotHardClosed   = errors.New("all monthly fiscal periods must be hard closed")
	ErrRetainedEarningsNotSet = errors.New("retained earnings account is required")
)

type TrialBalanceLine struct {
	AccountID     uuid.UUID
	AccountCode   string
	AccountName   string
	AccountType   masterdata.AccountType
	OpeningDebit  decimal.Decimal
	OpeningCredit decimal.Decimal
	PeriodDebit   decimal.Decimal
	PeriodCredit  decimal.Decimal
	EndingDebit   decimal.Decimal
	EndingCredit  decimal.Decimal
}

type TrialBalance struct {
	StartDate   time.Time
	EndDate     time.Time
	Currency    string
	Lines       []TrialBalanceLine
	IsAdjusted  bool
	DebitTotal  decimal.Decimal
	CreditTotal decimal.Decimal
	Balanced    bool
}

type TrialBalanceEngine struct {
	repository Repository
}

func NewTrialBalanceEngine(repository Repository) *TrialBalanceEngine {
	return &TrialBalanceEngine{repository: repository}
}

func (e *TrialBalanceEngine) Generate(startDate, endDate time.Time, currency string) (TrialBalance, error) {
	return e.GenerateAdjusted(startDate, endDate, currency, false)
}

func (e *TrialBalanceEngine) GenerateForCompany(companyID uuid.UUID, startDate, endDate time.Time, currency string) (TrialBalance, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return TrialBalance{}, err
	}
	return NewTrialBalanceEngine(repository).Generate(startDate, endDate, currency)
}

func (e *TrialBalanceEngine) GenerateAdjustedForCompany(companyID uuid.UUID, startDate, endDate time.Time, currency string, adjusted bool) (TrialBalance, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return TrialBalance{}, err
	}
	return NewTrialBalanceEngine(repository).GenerateAdjusted(startDate, endDate, currency, adjusted)
}

func (e *TrialBalanceEngine) GenerateAdjusted(startDate, endDate time.Time, currency string, adjusted bool) (TrialBalance, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return TrialBalance{}, err
	}

	started := make(map[uuid.UUID][2]decimal.Decimal)
	period := make(map[uuid.UUID][2]decimal.Decimal)
	accountMap := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountMap[account.ID] = account
	}

	allEntries, err := listPostedEntries(e.repository, time.Time{}, endDate)
	if err != nil {
		return TrialBalance{}, err
	}
	for _, entry := range allEntries {
		if entry.Status != posting.EntryPosted ||
			(!adjusted && postingClassification(entry) == posting.EntryClassAdjusting) {
			continue
		}
		entryDate := entryEffectiveDate(entry)
		for _, line := range entry.Lines {
			_, ok := accountMap[line.AccountID]
			if !ok {
				return TrialBalance{}, ErrReportingAccountNotFound
			}
			debit, credit, err := translateLineAmountsUsingRate(
				line, currency, rateProvider(e.repository), valuation.SpotRate, entryDate,
			)
			if err != nil {
				return TrialBalance{}, err
			}
			if entryDate.Before(startDate) {
				values := started[line.AccountID]
				values[0] = values[0].Add(debit)
				values[1] = values[1].Add(credit)
				started[line.AccountID] = values
			} else if !entryDate.After(endDate) {
				values := period[line.AccountID]
				values[0] = values[0].Add(debit)
				values[1] = values[1].Add(credit)
				period[line.AccountID] = values
			}
		}
	}

	result := TrialBalance{StartDate: startDate, EndDate: endDate, Currency: currency, IsAdjusted: adjusted,
		DebitTotal: decimal.Zero, CreditTotal: decimal.Zero}
	for _, account := range accounts {
		open := started[account.ID]
		move := period[account.ID]
		openingDebit, openingCredit := balancedSides(open[0], open[1], account.NormalBalance)
		endingDebit, endingCredit := endingSides(openingDebit, openingCredit, move[0], move[1])
		result.Lines = append(result.Lines, TrialBalanceLine{
			AccountID: account.ID, AccountCode: account.Code, AccountName: account.Name, AccountType: account.AccountType,
			OpeningDebit: openingDebit, OpeningCredit: openingCredit, PeriodDebit: move[0], PeriodCredit: move[1],
			EndingDebit: endingDebit, EndingCredit: endingCredit,
		})
		result.DebitTotal = result.DebitTotal.Add(endingDebit)
		result.CreditTotal = result.CreditTotal.Add(endingCredit)
	}
	result.DebitTotal = result.DebitTotal.Round(int32(4))
	result.CreditTotal = result.CreditTotal.Round(int32(4))
	result.Balanced = result.DebitTotal.Equal(result.CreditTotal)
	if !result.Balanced {
		return result, ErrUnbalancedTrialBalance
	}
	return result, nil
}

func endingSides(openDebit, openCredit, periodDebit, periodCredit decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	net := openDebit.Sub(openCredit).Add(periodDebit).Sub(periodCredit)
	if net.GreaterThanOrEqual(decimal.Zero) {
		return net, decimal.Zero
	}
	return decimal.Zero, net.Abs()
}

func balancedSides(debit, credit decimal.Decimal, normal masterdata.NormalBalance) (decimal.Decimal, decimal.Decimal) {
	net := debit.Sub(credit)
	if net.IsZero() {
		return decimal.Zero, decimal.Zero
	}
	if net.GreaterThan(decimal.Zero) {
		return net, decimal.Zero
	}
	return decimal.Zero, net.Abs()
}

func entryEffectiveDate(entry posting.JournalEntry) time.Time {
	if !entry.EntryDate.IsZero() {
		return entry.EntryDate
	}
	return entry.Date
}

func postingClassification(entry posting.JournalEntry) posting.EntryClassification {
	if entry.Classification == "" {
		return posting.EntryClassRegular
	}
	return entry.Classification
}

func listAccounts(repository Repository) ([]masterdata.Account, error) {
	if repo, ok := repository.(AccountRepository); ok {
		return repo.ListAccounts()
	}
	if repo, ok := repository.(interface {
		GetAccounts() ([]masterdata.Account, error)
	}); ok {
		return repo.GetAccounts()
	}
	return nil, errors.New("repository does not provide accounts")
}

func listPostedEntries(repository Repository, start, end time.Time) ([]posting.JournalEntry, error) {
	var (
		entries []posting.JournalEntry
		err     error
	)
	if repo, ok := repository.(LedgerRepository); ok {
		entries, err = repo.ListPostedEntries(start, end)
	} else if repo, ok := repository.(EntryDateRepository); ok {
		entries, err = repo.ListEntries(start, end)
	} else if repo, ok := repository.(interface {
		ListJournalEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
	}); ok {
		entries, err = repo.ListJournalEntries(start, end)
	} else {
		return nil, errors.New("repository does not provide posted ledger entries")
	}
	if err != nil {
		return nil, err
	}
	var latestOpening time.Time
	for _, entry := range entries {
		if entry.Status == posting.EntryPosted && postingClassification(entry) == posting.EntryClassOpening {
			date := entryEffectiveDate(entry)
			if latestOpening.IsZero() || date.After(latestOpening) {
				latestOpening = date
			}
		}
	}
	if latestOpening.IsZero() {
		return entries, nil
	}
	currentPeriod := make([]posting.JournalEntry, 0, len(entries))
	for _, entry := range entries {
		if !entryEffectiveDate(entry).Before(latestOpening) {
			currentPeriod = append(currentPeriod, entry)
		}
	}
	return currentPeriod, nil
}

func normalizeClassification(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
