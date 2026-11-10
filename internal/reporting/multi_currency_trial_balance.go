package reporting

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrUnbalancedFunctionalTrialBalance = errors.New("functional-currency trial balance is not balanced")

type CurrencyBalance struct {
	Currency     string
	OpenDebit    decimal.Decimal
	OpenCredit   decimal.Decimal
	PeriodDebit  decimal.Decimal
	PeriodCredit decimal.Decimal
	CloseDebit   decimal.Decimal
	CloseCredit  decimal.Decimal
}

type MultiCurrencyTBRow struct {
	AccountID                   uuid.UUID
	AccountName                 string
	TransactionCurrencyBalances map[string]CurrencyBalance
	FunctionalBalance           CurrencyBalance
	UnrealizedFXGainLoss        decimal.Decimal
}

type MultiCurrencyTrialBalance struct {
	StartDate          time.Time
	EndDate            time.Time
	FunctionalCurrency string
	Rows               []MultiCurrencyTBRow
	DebitTotal         decimal.Decimal
	CreditTotal        decimal.Decimal
	Balanced           bool
}

func (e *TrialBalanceEngine) GenerateMultiCurrency(
	startDate, endDate time.Time,
	functionalCurrency string,
) (MultiCurrencyTrialBalance, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return MultiCurrencyTrialBalance{}, err
	}
	entries, err := listPostedEntries(e.repository, time.Time{}, endDate)
	if err != nil {
		return MultiCurrencyTrialBalance{}, err
	}
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	result := MultiCurrencyTrialBalance{
		StartDate: startDate, EndDate: endDate, FunctionalCurrency: functionalCurrency,
		Rows: make([]MultiCurrencyTBRow, 0, len(accounts)),
	}
	transactions := make(map[uuid.UUID]map[string]CurrencyBalance)
	functional := make(map[uuid.UUID]CurrencyBalance)
	functionalByCurrency := make(map[uuid.UUID]map[string]decimal.Decimal)
	valuationDate := endDate
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		date := entryEffectiveDate(entry)
		if valuationDate.IsZero() || date.After(valuationDate) {
			valuationDate = date
		}
		for _, line := range entry.Lines {
			_, ok := accountByID[line.AccountID]
			if !ok {
				return MultiCurrencyTrialBalance{}, ErrReportingAccountNotFound
			}
			debit, credit, transactionCurrency := transactionLineAmounts(line)
			if transactionCurrency != "" {
				balances := transactions[line.AccountID]
				if balances == nil {
					balances = make(map[string]CurrencyBalance)
				}
				balance := balances[transactionCurrency]
				balance.Currency = transactionCurrency
				if date.Before(startDate) {
					balance.OpenDebit = balance.OpenDebit.Add(debit)
					balance.OpenCredit = balance.OpenCredit.Add(credit)
				} else {
					balance.PeriodDebit = balance.PeriodDebit.Add(debit)
					balance.PeriodCredit = balance.PeriodCredit.Add(credit)
				}
				balances[transactionCurrency] = balance
				transactions[line.AccountID] = balances
			}

			functionalDebit, functionalCredit, err := functionalLineAmounts(
				line, functionalCurrency, rateProvider(e.repository), date,
			)
			if err != nil {
				return MultiCurrencyTrialBalance{}, err
			}
			balance := functional[line.AccountID]
			balance.Currency = functionalCurrency
			if date.Before(startDate) {
				balance.OpenDebit = balance.OpenDebit.Add(functionalDebit)
				balance.OpenCredit = balance.OpenCredit.Add(functionalCredit)
			} else {
				balance.PeriodDebit = balance.PeriodDebit.Add(functionalDebit)
				balance.PeriodCredit = balance.PeriodCredit.Add(functionalCredit)
			}
			functional[line.AccountID] = balance
			if transactionCurrency != "" {
				currencies := functionalByCurrency[line.AccountID]
				if currencies == nil {
					currencies = make(map[string]decimal.Decimal)
				}
				currencies[transactionCurrency] = currencies[transactionCurrency].Add(functionalDebit).Sub(functionalCredit)
				functionalByCurrency[line.AccountID] = currencies
			}
		}
	}

	for _, account := range accounts {
		row := MultiCurrencyTBRow{
			AccountID: account.ID, AccountName: account.Name,
			TransactionCurrencyBalances: make(map[string]CurrencyBalance),
			FunctionalBalance:           functional[account.ID],
		}
		row.FunctionalBalance.CloseDebit, row.FunctionalBalance.CloseCredit = endingSides(
			row.FunctionalBalance.OpenDebit, row.FunctionalBalance.OpenCredit,
			row.FunctionalBalance.PeriodDebit, row.FunctionalBalance.PeriodCredit,
		)
		for currency, balance := range transactions[account.ID] {
			balance.CloseDebit, balance.CloseCredit = endingSides(
				balance.OpenDebit, balance.OpenCredit, balance.PeriodDebit, balance.PeriodCredit,
			)
			row.TransactionCurrencyBalances[currency] = balance
			if currency != functionalCurrency && isMonetaryAccount(account) {
				net := balance.CloseDebit.Sub(balance.CloseCredit)
				if !net.IsZero() {
					foreign, err := financialtypes.NewMoney(net, currency)
					if err != nil {
						return MultiCurrencyTrialBalance{}, err
					}
					translated, err := NewCurrencyTranslator(rateProvider(e.repository)).TranslateAmount(
						context.Background(), foreign, functionalCurrency, valuationDate, valuation.ClosingRate,
					)
					if err != nil {
						return MultiCurrencyTrialBalance{}, err
					}
					functionalNet := functionalByCurrency[account.ID][currency]
					row.UnrealizedFXGainLoss = row.UnrealizedFXGainLoss.Add(translated.Amount().Sub(functionalNet))
				}
			}
		}
		result.DebitTotal = result.DebitTotal.Add(row.FunctionalBalance.CloseDebit)
		result.CreditTotal = result.CreditTotal.Add(row.FunctionalBalance.CloseCredit)
		result.Rows = append(result.Rows, row)
	}
	result.DebitTotal = result.DebitTotal.Round(int32(financialtypes.FinancialScale))
	result.CreditTotal = result.CreditTotal.Round(int32(financialtypes.FinancialScale))
	result.Balanced = result.DebitTotal.Equal(result.CreditTotal)
	if !result.Balanced {
		return result, ErrUnbalancedFunctionalTrialBalance
	}
	return result, nil
}

func (e *TrialBalanceEngine) GenerateMultiCurrencyForCompany(
	companyID uuid.UUID,
	startDate, endDate time.Time,
	functionalCurrency string,
) (MultiCurrencyTrialBalance, error) {
	return e.GenerateMultiCurrencyForCompanyContext(context.Background(), companyID, startDate, endDate, functionalCurrency)
}

func (e *TrialBalanceEngine) GenerateMultiCurrencyForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	startDate, endDate time.Time,
	functionalCurrency string,
) (MultiCurrencyTrialBalance, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, endDate)
	if err != nil {
		return MultiCurrencyTrialBalance{}, err
	}
	return NewTrialBalanceEngine(repository).GenerateMultiCurrency(startDate, endDate, functionalCurrency)
}

func transactionLineAmounts(line posting.JournalEntryLine) (decimal.Decimal, decimal.Decimal, string) {
	if line.TransactionAmount.Currency() != "" {
		if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
			return line.TransactionAmount.Amount(), decimal.Zero, line.TransactionAmount.Currency()
		}
		return decimal.Zero, line.TransactionAmount.Amount(), line.TransactionAmount.Currency()
	}
	if line.AmountCurrency.Currency() != "" {
		if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
			return line.AmountCurrency.Amount(), decimal.Zero, line.AmountCurrency.Currency()
		}
		return decimal.Zero, line.AmountCurrency.Amount(), line.AmountCurrency.Currency()
	}
	return sourceLineAmounts(line)
}

func functionalLineAmounts(
	line posting.JournalEntryLine,
	functionalCurrency string,
	provider valuation.RateProviderSPI,
	date time.Time,
) (decimal.Decimal, decimal.Decimal, error) {
	if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
		if line.FunctionalDebit.Currency() != "" && line.FunctionalDebit.Currency() != functionalCurrency {
			return decimal.Zero, decimal.Zero, financialtypes.ErrCurrencyMismatch
		}
		if line.FunctionalCredit.Currency() != "" && line.FunctionalCredit.Currency() != functionalCurrency {
			return decimal.Zero, decimal.Zero, financialtypes.ErrCurrencyMismatch
		}
		return line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount(), nil
	}
	debit, credit, sourceCurrency := sourceLineAmounts(line)
	if sourceCurrency == "" || sourceCurrency == functionalCurrency {
		return debit, credit, nil
	}
	if line.ExchangeRate.IsPositive() {
		return debit.Mul(line.ExchangeRate), credit.Mul(line.ExchangeRate), nil
	}
	debit, credit, err := translateLineAmounts(
		line, masterdata.AccountTypeAsset, functionalCurrency, provider, date, date, date,
	)
	return debit, credit, err
}

func isMonetaryAccount(account masterdata.Account) bool {
	if account.IsReconcilable || account.EffectiveIsCashEquivalent() {
		return true
	}
	classification := normalizeClassification(account.SubClassification)
	switch classification {
	case masterdata.SubClassTradeReceivables,
		masterdata.SubClassAccountsReceivable,
		masterdata.SubClassTradePayables,
		masterdata.SubClassAccountsPayable,
		masterdata.SubClassShortTermBorrowings,
		masterdata.SubClassLongTermBorrowings,
		masterdata.SubClassCurrentLeaseLiabilities,
		masterdata.SubClassNonCurrentLeaseLiabilities,
		masterdata.SubClassAccrualsOtherCredit,
		masterdata.SubClassCurrentTaxLiabilities:
		return true
	default:
		return false
	}
}
