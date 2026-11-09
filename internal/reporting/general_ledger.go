package reporting

import (
	"errors"
	"sort"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type GeneralLedgerLine struct {
	Date           time.Time
	EntryID        uuid.UUID
	SequenceNumber int64
	Reference      string
	Debit          decimal.Decimal
	Credit         decimal.Decimal
	RunningBalance decimal.Decimal
}

type GeneralLedgerAccountReport struct {
	AccountID      uuid.UUID
	Code           string
	Name           string
	OpeningBalance decimal.Decimal
	Lines          []GeneralLedgerLine
	EndingBalance  decimal.Decimal
	NetMovement    decimal.Decimal
}

type GeneralLedgerEngine struct{ repository Repository }

func NewGeneralLedgerEngine(repository Repository) *GeneralLedgerEngine {
	return &GeneralLedgerEngine{repository: repository}
}

func (e *GeneralLedgerEngine) Generate(accountID uuid.UUID, start, end time.Time, currency string) (GeneralLedgerAccountReport, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return GeneralLedgerAccountReport{}, err
	}
	var account masterdata.Account
	found := false
	for _, candidate := range accounts {
		if candidate.ID == accountID {
			account = candidate
			found = true
			break
		}
	}
	if !found {
		return GeneralLedgerAccountReport{}, errors.New("account not found")
	}

	entries, err := listPostedEntries(e.repository, time.Time{}, end)
	if err != nil {
		return GeneralLedgerAccountReport{}, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entryEffectiveDate(entries[i]), entryEffectiveDate(entries[j])
		if left.Equal(right) {
			return entries[i].SequenceNumber < entries[j].SequenceNumber
		}
		return left.Before(right)
	})

	openingDebit, openingCredit := decimal.Zero, decimal.Zero
	for _, entry := range entries {
		date := entryEffectiveDate(entry)
		if !date.Before(start) {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != accountID {
				continue
			}
			debit, credit := lineAmounts(line, currency)
			openingDebit = openingDebit.Add(debit)
			openingCredit = openingCredit.Add(credit)
		}
	}
	opening := signedAccountBalance(openingDebit.Sub(openingCredit), account.NormalBalance)

	report := GeneralLedgerAccountReport{
		AccountID: accountID, Code: account.Code, Name: account.Name,
		OpeningBalance: opening, Lines: make([]GeneralLedgerLine, 0),
		EndingBalance: opening,
	}
	for _, entry := range entries {
		date := entryEffectiveDate(entry)
		if date.Before(start) || (!end.IsZero() && date.After(end)) {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != accountID {
				continue
			}
			debit, credit := lineAmounts(line, currency)
			movement := signedAccountBalance(debit.Sub(credit), account.NormalBalance)
			report.EndingBalance = report.EndingBalance.Add(movement)
			report.Lines = append(report.Lines, GeneralLedgerLine{
				Date: date, EntryID: entry.ID, SequenceNumber: entry.SequenceNumber,
				Reference: ledgerReference(entry), Debit: debit, Credit: credit, RunningBalance: report.EndingBalance,
			})
		}
	}
	report.NetMovement = report.EndingBalance.Sub(report.OpeningBalance).Round(4)
	report.OpeningBalance = report.OpeningBalance.Round(4)
	report.EndingBalance = report.EndingBalance.Round(4)
	return report, nil
}

func (e *GeneralLedgerEngine) GenerateForCompany(companyID, accountID uuid.UUID, start, end time.Time, currency string) (GeneralLedgerAccountReport, error) {
	repository, err := newCompanyReportingRepository(e.repository, companyID)
	if err != nil {
		return GeneralLedgerAccountReport{}, err
	}
	return NewGeneralLedgerEngine(repository).Generate(accountID, start, end, currency)
}

func signedAccountBalance(net decimal.Decimal, normal masterdata.NormalBalance) decimal.Decimal {
	if normal == masterdata.NormalBalanceCredit {
		return net.Neg()
	}
	return net
}

func ledgerReference(entry posting.JournalEntry) string {
	if entry.DocumentReference != "" {
		return entry.DocumentReference
	}
	return entry.Reference
}
