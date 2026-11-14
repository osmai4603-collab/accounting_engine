package valuation

import (
	"context"
	"errors"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

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
	IsLiability            bool
}

type FXResult struct {
	AccountID      uuid.UUID
	Currency       string
	Difference     financialtypes.Money
	IsGain         bool
	ClosingRate    decimal.Decimal
	RateDate       time.Time
	RateType       RateType
	RevaluedAmount financialtypes.Money
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
	isGain := difference.Amount().IsPositive()
	if position.IsLiability {
		isGain = difference.Amount().IsNegative()
	}
	return FXResult{
		AccountID:      position.AccountID,
		Currency:       position.TransactionCurrency,
		Difference:     difference,
		IsGain:         isGain,
		ClosingRate:    rate,
		RateDate:       asOf,
		RateType:       ClosingRate,
		RevaluedAmount: revaluedAmount,
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
	CompanyID        uuid.UUID
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
		ID:        uuid.New(),
		CompanyID: accounts.CompanyID,
		JournalID: accounts.JournalID,
		EntryDate: date,
		Date:      date,
		Status:    posting.EntryDraft,
		CreatedAt: time.Now().UTC(),
		Reference: "FX-REVAL-" + result.Currency + "-" + date.Format("2006-01-02"),
	}
	lineForSide := func(accountID uuid.UUID, debit bool) (posting.JournalEntryLine, error) {
		zero, err := financialtypes.Zero(amount.Currency())
		if err != nil {
			return posting.JournalEntryLine{}, err
		}
		line := posting.JournalEntryLine{
			ID:                  uuid.New(),
			CompanyID:           accounts.CompanyID,
			AccountID:           accountID,
			TransactionAmount:   amount,
			TransactionCurrency: amount.Currency(),
			ExchangeRate:        decimal.NewFromInt(1),
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

// RealizedFXSettlementMove represents inputs to record realized exchange differences
// upon settling a foreign currency receivable or payable (IAS 21.28).
type RealizedFXSettlementMove struct {
	CompanyID             uuid.UUID
	JournalID             uuid.UUID
	MonetaryAccountID     uuid.UUID // AR or AP control account
	SettlementAccountID   uuid.UUID // Bank or Cash account
	RealizedGainAccountID uuid.UUID
	RealizedLossAccountID uuid.UUID
	ForeignAmount         decimal.Decimal
	TransactionCurrency   string
	FunctionalCurrency    string
	OriginalRate          decimal.Decimal
	SettlementRate        decimal.Decimal
	SettlementDate        time.Time
	IsReceivable          bool // true for settling receivable (AR), false for payable (AP)
	Reference             string
}

// GenerateRealizedFXSettlementEntry generates a balanced journal entry reflecting settlement
// and the realized FX difference in profit or loss under IAS 21.28.
func GenerateRealizedFXSettlementEntry(move RealizedFXSettlementMove) (posting.JournalEntry, financialtypes.Money, error) {
	if move.OriginalRate.IsZero() || move.OriginalRate.IsNegative() ||
		move.SettlementRate.IsZero() || move.SettlementRate.IsNegative() {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidRate
	}
	if move.ForeignAmount.IsZero() || move.ForeignAmount.IsNegative() {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidPosition
	}
	if move.MonetaryAccountID == uuid.Nil || move.SettlementAccountID == uuid.Nil ||
		move.RealizedGainAccountID == uuid.Nil || move.RealizedLossAccountID == uuid.Nil {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidPosition
	}

	diff, err := RealizedFXDifference(move.ForeignAmount, move.OriginalRate, move.SettlementRate, move.FunctionalCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}

	originalFunctionalAmt, err := financialtypes.NewMoney(move.ForeignAmount.Mul(move.OriginalRate), move.FunctionalCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	settlementFunctionalAmt, err := financialtypes.NewMoney(move.ForeignAmount.Mul(move.SettlementRate), move.FunctionalCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	foreignMoney, err := financialtypes.NewMoney(move.ForeignAmount, move.TransactionCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	zeroFunctional, err := financialtypes.Zero(move.FunctionalCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}

	entry := posting.JournalEntry{
		ID:        uuid.New(),
		CompanyID: move.CompanyID,
		JournalID: move.JournalID,
		EntryDate: move.SettlementDate,
		Date:      move.SettlementDate,
		Status:    posting.EntryDraft,
		CreatedAt: time.Now().UTC(),
		Reference: move.Reference,
	}
	if entry.Reference == "" {
		entry.Reference = "FX-SETTLE-" + move.TransactionCurrency + "-" + move.SettlementDate.Format("2006-01-02")
	}

	var lines []posting.JournalEntryLine
	if move.IsReceivable {
		// AR settlement:
		// Bank debit (settlement functional amount)
		// AR credit (original functional amount)
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.SettlementAccountID,
			Debit: settlementFunctionalAmt, Credit: zeroFunctional,
			FunctionalDebit: settlementFunctionalAmt, FunctionalCredit: zeroFunctional,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.SettlementRate,
		})
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.MonetaryAccountID,
			Debit: zeroFunctional, Credit: originalFunctionalAmt,
			FunctionalDebit: zeroFunctional, FunctionalCredit: originalFunctionalAmt,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.OriginalRate,
		})
		if !diff.Amount().IsZero() {
			diffAbs := diff
			if diff.IsNegative() {
				diffAbs, _ = financialtypes.NewMoney(diff.Amount().Abs(), move.FunctionalCurrency)
			}
			if diff.Amount().IsPositive() {
				// Realized Gain (Credit)
				lines = append(lines, posting.JournalEntryLine{
					ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.RealizedGainAccountID,
					Debit: zeroFunctional, Credit: diffAbs,
					FunctionalDebit: zeroFunctional, FunctionalCredit: diffAbs,
					TransactionAmount: diffAbs, TransactionCurrency: move.FunctionalCurrency,
					ExchangeRate: decimal.NewFromInt(1),
				})
			} else {
				// Realized Loss (Debit)
				lines = append(lines, posting.JournalEntryLine{
					ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.RealizedLossAccountID,
					Debit: diffAbs, Credit: zeroFunctional,
					FunctionalDebit: diffAbs, FunctionalCredit: zeroFunctional,
					TransactionAmount: diffAbs, TransactionCurrency: move.FunctionalCurrency,
					ExchangeRate: decimal.NewFromInt(1),
				})
			}
		}
	} else {
		// AP settlement:
		// AP debit (original functional amount)
		// Bank credit (settlement functional amount)
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.MonetaryAccountID,
			Debit: originalFunctionalAmt, Credit: zeroFunctional,
			FunctionalDebit: originalFunctionalAmt, FunctionalCredit: zeroFunctional,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.OriginalRate,
		})
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.SettlementAccountID,
			Debit: zeroFunctional, Credit: settlementFunctionalAmt,
			FunctionalDebit: zeroFunctional, FunctionalCredit: settlementFunctionalAmt,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.SettlementRate,
		})
		if !diff.Amount().IsZero() {
			diffAbs := diff
			if diff.IsNegative() {
				diffAbs, _ = financialtypes.NewMoney(diff.Amount().Abs(), move.FunctionalCurrency)
			}
			if move.SettlementRate.GreaterThan(move.OriginalRate) {
				// Paid more functional currency -> Loss (Debit)
				lines = append(lines, posting.JournalEntryLine{
					ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.RealizedLossAccountID,
					Debit: diffAbs, Credit: zeroFunctional,
					FunctionalDebit: diffAbs, FunctionalCredit: zeroFunctional,
					TransactionAmount: diffAbs, TransactionCurrency: move.FunctionalCurrency,
					ExchangeRate: decimal.NewFromInt(1),
				})
			} else {
				// Paid less functional currency -> Gain (Credit)
				lines = append(lines, posting.JournalEntryLine{
					ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.RealizedGainAccountID,
					Debit: zeroFunctional, Credit: diffAbs,
					FunctionalDebit: zeroFunctional, FunctionalCredit: diffAbs,
					TransactionAmount: diffAbs, TransactionCurrency: move.FunctionalCurrency,
					ExchangeRate: decimal.NewFromInt(1),
				})
			}
		}
	}

	entry.Lines = lines
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	return entry, diff, nil
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
