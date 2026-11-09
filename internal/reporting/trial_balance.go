package reporting

import (
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"

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
	entries, err := listPostedEntries(e.repository, startDate, endDate)
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
		if entry.Status != posting.EntryPosted {
			continue
		}
		entryDate := entryEffectiveDate(entry)
		for _, line := range entry.Lines {
			debit, credit := lineAmounts(line, currency)
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
	_ = entries
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

func lineAmounts(line posting.JournalEntryLine, currency string) (decimal.Decimal, decimal.Decimal) {
	if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
		functionalCurrency := line.FunctionalDebit.Currency()
		if functionalCurrency == "" {
			functionalCurrency = line.FunctionalCredit.Currency()
		}
		if currency == "" || functionalCurrency == currency {
			return line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount()
		}
		return decimal.Zero, decimal.Zero
	}
	if line.AmountCurrency.Currency() != "" {
		if currency == "" || line.AmountCurrency.Currency() == currency {
			if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
				return line.AmountCurrency.Amount(), decimal.Zero
			}
			return decimal.Zero, line.AmountCurrency.Amount()
		}
		return decimal.Zero, decimal.Zero
	}
	debit, credit := decimal.Zero, decimal.Zero
	if line.Debit.Currency() == "" || (currency != "" && line.Debit.Currency() != currency) {
		if line.Credit.Currency() != "" && (currency == "" || line.Credit.Currency() == currency) {
			credit = line.Credit.Amount()
		}
	} else {
		debit = line.Debit.Amount()
	}
	if line.Credit.Currency() != "" && (currency == "" || line.Credit.Currency() == currency) {
		credit = line.Credit.Amount()
	}
	return debit, credit
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
	if repo, ok := repository.(LedgerRepository); ok {
		return repo.ListPostedEntries(start, end)
	}
	if repo, ok := repository.(EntryDateRepository); ok {
		return repo.ListEntries(start, end)
	}
	if repo, ok := repository.(interface {
		ListJournalEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
	}); ok {
		return repo.ListJournalEntries(start, end)
	}
	return nil, errors.New("repository does not provide posted ledger entries")
}

func normalizeClassification(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
