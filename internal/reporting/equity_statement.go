package reporting

import (
	"context"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type EquityMovement struct {
	AccountID                uuid.UUID
	AccountCode              string
	AccountName              string
	Opening                  decimal.Decimal
	NetIncome                decimal.Decimal
	OtherComprehensiveIncome decimal.Decimal
	Distributions            decimal.Decimal
	TransfersToReserve       decimal.Decimal
	Closing                  decimal.Decimal
}

type EquityStatement struct {
	StartDate          time.Time
	EndDate            time.Time
	Currency           string
	Movements          []EquityMovement
	TranslationReserve decimal.Decimal
	OpeningTotal       decimal.Decimal
	ClosingTotal       decimal.Decimal
}

type EquityStatementEngine struct{ repository Repository }

type equityMovementLine struct {
	accountID uuid.UUID
	debit     decimal.Decimal
	credit    decimal.Decimal
}

func NewEquityStatementEngine(repository Repository) *EquityStatementEngine {
	return &EquityStatementEngine{repository: repository}
}

func (e *EquityStatementEngine) Generate(start, end time.Time, currency string) (EquityStatement, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return EquityStatement{}, err
	}

	open := make(map[uuid.UUID]accountSide)
	if !start.IsZero() {
		open, err = accountBalances(e.repository, time.Time{}, start.Add(-time.Nanosecond), currency)
		if err != nil {
			return EquityStatement{}, err
		}
	}
	close, err := accountBalances(e.repository, time.Time{}, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	openingIncome := IncomeStatement{}
	if !start.IsZero() {
		openingAsOf := start.Add(-time.Nanosecond)
		openingIncome, err = NewIncomeStatementEngine(e.repository).Generate(time.Time{}, openingAsOf, currency)
		if err != nil {
			return EquityStatement{}, err
		}
	}
	closingIncome, err := NewIncomeStatementEngine(e.repository).Generate(time.Time{}, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	transfers, distributions, err := equityMovements(e.repository, accounts, start, end, currency)
	if err != nil {
		return EquityStatement{}, err
	}
	report := EquityStatement{
		StartDate: start, EndDate: end, Currency: currency,
		TranslationReserve: income.TranslationReserve,
	}
	netIncomeAssigned := false
	for _, account := range accounts {
		if account.AccountType != masterdata.AccountTypeEquity {
			continue
		}
		distribution := distributions[account.ID]
		transfer := transfers[account.ID]
		openAmount := signedBalance(open[account.ID], account)
		closeAmount := signedBalance(close[account.ID], account)
		netIncome := decimal.Zero
		oci := decimal.Zero
		movementOpening := openAmount
		movementClosing := closeAmount
		if !netIncomeAssigned && normalizeClassification(account.SubClassification) == masterdata.SubClassRetainedEarnings {
			netIncome = income.NetProfit
			oci = income.OCI
			movementOpening = movementOpening.Add(openingIncome.NetProfit).Add(openingIncome.OCI)
			movementClosing = movementClosing.Add(closingIncome.NetProfit).Add(closingIncome.OCI)
			netIncomeAssigned = true
		}
		movement := EquityMovement{
			AccountID: account.ID, AccountCode: account.Code, AccountName: account.Name,
			Opening: movementOpening, NetIncome: netIncome, OtherComprehensiveIncome: oci,
			Distributions: distribution, TransfersToReserve: transfer,
			Closing: movementClosing,
		}
		report.Movements = append(report.Movements, movement)
		report.OpeningTotal = report.OpeningTotal.Add(movementOpening)
		report.ClosingTotal = report.ClosingTotal.Add(movementClosing)
	}
	if !netIncomeAssigned {
		report.OpeningTotal = report.OpeningTotal.Add(openingIncome.NetProfit).Add(openingIncome.OCI)
		report.ClosingTotal = report.ClosingTotal.Add(closingIncome.NetProfit).Add(closingIncome.OCI)
	}
	return report, nil
}

func equityMovements(
	repository Repository,
	accounts []masterdata.Account,
	start, end time.Time,
	currency string,
) (map[uuid.UUID]decimal.Decimal, map[uuid.UUID]decimal.Decimal, error) {
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	entries, err := listPostedEntries(repository, start, end)
	if err != nil {
		return nil, nil, err
	}
	transfers := make(map[uuid.UUID]decimal.Decimal)
	distributions := make(map[uuid.UUID]decimal.Decimal)
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		var retainedDebits, retainedCredits, reserveDebits, reserveCredits []equityMovementLine
		directDistributions := make(map[uuid.UUID]decimal.Decimal)
		for _, line := range entry.Lines {
			account, ok := accountByID[line.AccountID]
			if !ok {
				return nil, nil, ErrReportingAccountNotFound
			}
			if account.AccountType != masterdata.AccountTypeEquity {
				continue
			}
			debit, credit, err := translateLineAmounts(
				line, account.AccountType, currency, rateProvider(repository), start, end, entryEffectiveDate(entry),
			)
			if err != nil {
				return nil, nil, err
			}
			classification := normalizeClassification(account.SubClassification)
			if classification == masterdata.SubClassRetainedEarnings && debit.IsPositive() {
				retainedDebits = append(retainedDebits, equityMovementLine{accountID: account.ID, debit: debit})
			}
			if classification == masterdata.SubClassRetainedEarnings && credit.IsPositive() {
				retainedCredits = append(retainedCredits, equityMovementLine{accountID: account.ID, credit: credit})
			}
			if isReserveClassification(classification) && debit.IsPositive() {
				reserveDebits = append(reserveDebits, equityMovementLine{accountID: account.ID, debit: debit})
			}
			if isReserveClassification(classification) && credit.IsPositive() {
				reserveCredits = append(reserveCredits, equityMovementLine{accountID: account.ID, credit: credit})
			}
			if classification == masterdata.SubClassDistributions && debit.IsPositive() {
				directDistributions[account.ID] = directDistributions[account.ID].Add(debit)
			}
			if classification == masterdata.SubClassDistributions && credit.IsPositive() &&
				postingClassification(entry) == posting.EntryClassReversing {
				directDistributions[account.ID] = directDistributions[account.ID].Sub(credit)
			}
		}

		outbound := decimal.Min(sumEquityLineAmounts(retainedDebits), sumEquityLineAmounts(reserveCredits))
		allocateEquityTransfers(retainedDebits, outbound, transfers, -1)
		allocateEquityTransfers(reserveCredits, outbound, transfers, 1)
		inbound := decimal.Min(sumEquityLineAmounts(retainedCredits), sumEquityLineAmounts(reserveDebits))
		allocateEquityTransfers(retainedCredits, inbound, transfers, 1)
		allocateEquityTransfers(reserveDebits, inbound, transfers, -1)

		entryClass := postingClassification(entry)
		if entryClass != posting.EntryClassClosing && entryClass != posting.EntryClassOpening {
			for _, line := range retainedDebits {
				transfer := decimal.Min(line.debit, outbound)
				distributions[line.accountID] = distributions[line.accountID].Add(line.debit.Sub(transfer))
				outbound = outbound.Sub(transfer)
			}
		}
		if entryClass == posting.EntryClassReversing {
			for _, line := range retainedCredits {
				transfer := decimal.Min(line.credit, inbound)
				distributions[line.accountID] = distributions[line.accountID].Sub(line.credit.Sub(transfer))
				inbound = inbound.Sub(transfer)
			}
		}
		for accountID, amount := range directDistributions {
			distributions[accountID] = distributions[accountID].Add(amount)
		}
	}
	return transfers, distributions, nil
}

func sumEquityLineAmounts(lines []equityMovementLine) decimal.Decimal {
	total := decimal.Zero
	for _, line := range lines {
		total = total.Add(line.debit).Add(line.credit)
	}
	return total
}

func allocateEquityTransfers(
	lines []equityMovementLine,
	amount decimal.Decimal,
	transfers map[uuid.UUID]decimal.Decimal,
	sign int64,
) {
	remaining := amount
	for _, line := range lines {
		lineAmount := line.debit.Add(line.credit)
		matched := decimal.Min(lineAmount, remaining)
		transfers[line.accountID] = transfers[line.accountID].Add(matched.Mul(decimal.NewFromInt(sign)))
		remaining = remaining.Sub(matched)
	}
}

func isReserveClassification(classification string) bool {
	return classification == masterdata.SubClassStatutoryReserve ||
		classification == masterdata.SubClassOCIReserves || classification == "reserve"
}

func (e *EquityStatementEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (EquityStatement, error) {
	return e.GenerateForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *EquityStatementEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (EquityStatement, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, end)
	if err != nil {
		return EquityStatement{}, err
	}
	return NewEquityStatementEngine(repository).Generate(start, end, currency)
}
