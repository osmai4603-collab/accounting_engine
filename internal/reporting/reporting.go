package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

var ErrReportingAccountNotFound = errors.New("posted journal line references an unknown account")

type accountSide struct{ debit, credit decimal.Decimal }

func isLedgerPosted(status posting.EntryStatus) bool {
	return status == posting.EntryPosted || status == posting.EntryReversed
}

func accountBalances(repository Repository, start, end time.Time, currency string) (map[uuid.UUID]accountSide, error) {
	balances, available, err := readPeriodBalances(repository, start, end, currency, true)
	if err != nil {
		return nil, err
	}
	if available {
		result := make(map[uuid.UUID]accountSide, len(balances))
		for accountID, balance := range balances {
			side := accountSide{debit: balance.PeriodDebit, credit: balance.PeriodCredit}
			if start.IsZero() {
				side = accountSide{debit: balance.EndingDebit, credit: balance.EndingCredit}
			}
			result[accountID] = side
		}
		return result, nil
	}
	accounts, err := listAccounts(repository)
	if err != nil {
		return nil, err
	}
	entries, err := listPostedEntries(repository, start, end)
	if err != nil {
		return nil, err
	}
	return accountBalancesFromEntries(repository, accounts, entries, start, end, currency)
}

func readPeriodBalances(
	repository Repository,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	reader, ok := repository.(MaterializedBalanceReader)
	if !ok {
		return nil, false, nil
	}
	return reader.ReadAccountPeriodBalancesContext(
		context.Background(), start, end, currency, includeAdjusting,
	)
}

func accountBalancesFromEntries(
	repository Repository,
	accounts []masterdata.Account,
	entries []posting.JournalEntry,
	start, end time.Time,
	currency string,
) (map[uuid.UUID]accountSide, error) {
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	balances := make(map[uuid.UUID]accountSide)
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) {
			continue
		}
		for _, line := range entry.Lines {
			account, ok := accountByID[line.AccountID]
			if !ok {
				return nil, ErrReportingAccountNotFound
			}
			debit, credit, err := translateLineAmounts(
				line, account.AccountType, currency, rateProvider(repository), start, end, entryEffectiveDate(entry),
			)
			if err != nil {
				return nil, err
			}
			side := balances[line.AccountID]
			side.debit = side.debit.Add(debit)
			side.credit = side.credit.Add(credit)
			balances[line.AccountID] = side
		}
	}
	return balances, nil
}

func accountMovementBalances(repository Repository, start, end time.Time, currency string) (map[uuid.UUID]accountSide, error) {
	accounts, err := listAccounts(repository)
	if err != nil {
		return nil, err
	}
	entries, err := listPostedEntries(repository, start, end)
	if err != nil {
		return nil, err
	}
	movements := make([]posting.JournalEntry, 0, len(entries))
	for _, entry := range entries {
		if postingClassification(entry) != posting.EntryClassOpening {
			movements = append(movements, entry)
		}
	}
	return accountBalancesFromEntries(repository, accounts, movements, start, end, currency)
}

func translateLineAmounts(
	line posting.JournalEntryLine,
	accountType masterdata.AccountType,
	currency string,
	provider valuation.RateProviderSPI,
	periodStart, periodEnd, entryDate time.Time,
) (decimal.Decimal, decimal.Decimal, error) {
	_ = periodStart
	rateType, rateDate := valuation.ClosingRate, periodEnd
	switch accountType {
	case masterdata.AccountTypeEquity:
		rateType, rateDate = valuation.SpotRate, entryDate
	case masterdata.AccountTypeIncome, masterdata.AccountTypeExpense:
		rateType = valuation.AverageRate
		if rateDate.IsZero() {
			rateDate = entryDate
		}
	default:
		if rateDate.IsZero() {
			rateDate = entryDate
		}
	}
	return translateLineAmountsUsingRate(line, currency, provider, rateType, rateDate)
}

func translateLineAmountsUsingRate(
	line posting.JournalEntryLine,
	currency string,
	provider valuation.RateProviderSPI,
	rateType valuation.RateType,
	rateDate time.Time,
) (decimal.Decimal, decimal.Decimal, error) {
	debit, credit, sourceCurrency := sourceLineAmounts(line)
	if currency == "" || sourceCurrency == "" || sourceCurrency == currency {
		return debit, credit, nil
	}
	translator := NewCurrencyTranslator(provider)
	if !debit.IsZero() {
		amount, err := financialtypes.NewMoney(debit, sourceCurrency)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		translated, err := translator.TranslateAmount(
			context.Background(), amount, currency, rateDate, rateType,
		)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		debit = translated.Amount()
	}
	if !credit.IsZero() {
		amount, err := financialtypes.NewMoney(credit, sourceCurrency)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		translated, err := translator.TranslateAmount(
			context.Background(), amount, currency, rateDate, rateType,
		)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		credit = translated.Amount()
	}
	return debit, credit, nil
}

func sourceLineAmounts(line posting.JournalEntryLine) (decimal.Decimal, decimal.Decimal, string) {
	if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
		currency := line.FunctionalDebit.Currency()
		if currency == "" {
			currency = line.FunctionalCredit.Currency()
		}
		return line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount(), currency
	}
	if line.AmountCurrency.Currency() != "" {
		if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
			return line.AmountCurrency.Amount(), decimal.Zero, line.AmountCurrency.Currency()
		}
		return decimal.Zero, line.AmountCurrency.Amount(), line.AmountCurrency.Currency()
	}
	if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
		return line.Debit.Amount(), decimal.Zero, line.Debit.Currency()
	}
	if line.Credit.Currency() != "" && !line.Credit.Amount().IsZero() {
		return decimal.Zero, line.Credit.Amount(), line.Credit.Currency()
	}
	if line.Debit.Currency() != "" {
		return decimal.Zero, decimal.Zero, line.Debit.Currency()
	}
	return decimal.Zero, decimal.Zero, line.Credit.Currency()
}

func rateProvider(repository Repository) valuation.RateProviderSPI {
	provider, _ := repository.(valuation.RateProviderSPI)
	return provider
}

func accountBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	amount := side.debit.Sub(side.credit)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		return amount.Neg()
	}
	return amount
}

func adjustedAccountBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	amount := accountBalance(side, account)
	if account.IsContraAccount {
		amount = amount.Neg()
	}
	return amount
}

func postedEntriesInRange(entries []posting.JournalEntry, start, end time.Time) []posting.JournalEntry {
	result := make([]posting.JournalEntry, 0, len(entries))
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) {
			continue
		}
		date := entryEffectiveDate(entry)
		if (start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			result = append(result, entry)
		}
	}
	return result
}
