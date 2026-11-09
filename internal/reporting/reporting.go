package reporting

import (
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type accountSide struct{ debit, credit decimal.Decimal }

func accountBalances(repository Repository, start, end time.Time, currency string) (map[uuid.UUID]accountSide, error) {
	entries, err := listPostedEntries(repository, start, end)
	if err != nil {
		return nil, err
	}
	balances := make(map[uuid.UUID]accountSide)
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		for _, line := range entry.Lines {
			debit, credit := lineAmounts(line, currency)
			side := balances[line.AccountID]
			side.debit = side.debit.Add(debit)
			side.credit = side.credit.Add(credit)
			balances[line.AccountID] = side
		}
	}
	return balances, nil
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
		if entry.Status != posting.EntryPosted {
			continue
		}
		date := entryEffectiveDate(entry)
		if (start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			result = append(result, entry)
		}
	}
	return result
}
