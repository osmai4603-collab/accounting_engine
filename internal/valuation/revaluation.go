package valuation

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrInvalidPosition = errors.New("invalid monetary position")

type PositionType string

const (
	Monetary             PositionType = "MONETARY"
	NonMonetary          PositionType = "NON_MONETARY"
	MonetaryPositionKind PositionType = Monetary
)

type MonetaryPosition struct {
	AccountID              uuid.UUID
	ForeignCurrencyBalance decimal.Decimal
	TransactionCurrency    string
	FunctionalCurrency     string
	CarryingAmount         financialtypes.Money
	Type                   PositionType
}

type FXResult struct {
	AccountID  uuid.UUID
	Currency   string
	Difference financialtypes.Money
	IsGain     bool
}

type RevaluationEngine struct {
	provider RateProviderSPI
}

func NewRevaluationEngine(provider RateProviderSPI) *RevaluationEngine {
	return &RevaluationEngine{provider: provider}
}

func (e *RevaluationEngine) Calculate(ctx context.Context, position MonetaryPosition, asOf time.Time) (FXResult, error) {
	if position.Type != "" && position.Type != Monetary {
		return FXResult{}, ErrInvalidPosition
	}
	if position.AccountID == uuid.Nil || position.FunctionalCurrency == "" ||
		position.CarryingAmount.Currency() != position.FunctionalCurrency ||
		position.ForeignCurrencyBalance.IsZero() && position.CarryingAmount.Amount().IsZero() {
		return FXResult{}, ErrInvalidPosition
	}
	if e.provider == nil {
		return FXResult{}, ErrRateNotFound
	}
	rate, err := getClosingRate(ctx, e.provider, position.TransactionCurrency, position.FunctionalCurrency, asOf)
	if err != nil {
		return FXResult{}, err
	}

	revaluedAmount, err := financialtypes.NewMoney(position.ForeignCurrencyBalance.Mul(rate), position.FunctionalCurrency)
	if err != nil {
		return FXResult{}, err
	}
	difference, err := revaluedAmount.Subtract(position.CarryingAmount)
	if err != nil {
		return FXResult{}, err
	}
	return FXResult{
		AccountID:  position.AccountID,
		Currency:   position.TransactionCurrency,
		Difference: difference,
		IsGain:     difference.Amount().IsPositive(),
	}, nil
}

func getClosingRate(ctx context.Context, provider RateProviderSPI, from, to string, date time.Time) (decimal.Decimal, error) {
	return provider.GetRateOfType(ctx, ClosingRate, from, to, date)
}

func (e *RevaluationEngine) Revalue(ctx context.Context, positions []MonetaryPosition, asOf time.Time) ([]FXResult, error) {
	results := make([]FXResult, 0, len(positions))
	for _, position := range positions {
		if position.Type == NonMonetary {
			continue
		}
		result, err := e.Calculate(ctx, position, asOf)
		if err != nil {
			return nil, err
		}
		if !result.Difference.Amount().IsZero() {
			results = append(results, result)
		}
	}
	return results, nil
}

func (e *RevaluationEngine) CalculateRevaluation(ctx context.Context, position MonetaryPosition, asOf time.Time) (FXResult, error) {
	return e.Calculate(ctx, position, asOf)
}

type FXJournalAccounts struct {
	JournalID        uuid.UUID
	UnrealizedGainID uuid.UUID
	UnrealizedLossID uuid.UUID
}

func GenerateRevaluationEntry(result FXResult, accounts FXJournalAccounts, date time.Time) (posting.JournalEntry, error) {
	if result.Difference.Amount().IsZero() {
		return posting.JournalEntry{}, ErrInvalidPosition
	}
	amount := result.Difference
	if amount.IsNegative() {
		var err error
		amount, err = financialtypes.NewMoney(amount.Amount().Abs(), amount.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	entry := posting.JournalEntry{
		ID: uuid.New(), JournalID: accounts.JournalID, EntryDate: date, Date: date,
		Status: posting.EntryDraft, CreatedAt: time.Now().UTC(),
	}
	lineForSide := func(accountID uuid.UUID, debit bool) (posting.JournalEntryLine, error) {
		zero, err := financialtypes.Zero(amount.Currency())
		if err != nil {
			return posting.JournalEntryLine{}, err
		}
		line := posting.JournalEntryLine{
			ID: uuid.New(), AccountID: accountID,
			TransactionAmount: amount, TransactionCurrency: amount.Currency(),
			ExchangeRate: decimal.NewFromInt(1),
		}
		if debit {
			line.Debit, line.FunctionalDebit = amount, amount
			line.Credit, line.FunctionalCredit = zero, zero
		} else {
			line.Debit, line.FunctionalDebit = zero, zero
			line.Credit, line.FunctionalCredit = amount, amount
		}
		return line, nil
	}
	var debitLine, creditLine posting.JournalEntryLine
	var err error
	if result.IsGain {
		debitLine, err = lineForSide(result.AccountID, true)
		if err == nil {
			creditLine, err = lineForSide(accounts.UnrealizedGainID, false)
		}
	} else {
		debitLine, err = lineForSide(accounts.UnrealizedLossID, true)
		if err == nil {
			creditLine, err = lineForSide(result.AccountID, false)
		}
	}
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.Lines = []posting.JournalEntryLine{debitLine, creditLine}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func RealizedFXDifference(foreignAmount decimal.Decimal, originalRate, settlementRate decimal.Decimal, functionalCurrency string) (financialtypes.Money, error) {
	if originalRate.IsZero() || settlementRate.IsZero() || originalRate.IsNegative() || settlementRate.IsNegative() {
		return financialtypes.Money{}, ErrInvalidRate
	}
	return financialtypes.NewMoney(foreignAmount.Mul(settlementRate.Sub(originalRate)), functionalCurrency)
}

func RealizedFXGainLoss(foreignAmount decimal.Decimal, originalRate, settlementRate decimal.Decimal, functionalCurrency string) (financialtypes.Money, error) {
	return RealizedFXDifference(foreignAmount, originalRate, settlementRate, functionalCurrency)
}
