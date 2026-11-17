package valuation

import (
	"context"
	"errors"
	"strings"
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
	AccountID              uuid.UUID
	Currency               string
	Difference             financialtypes.Money
	IsGain                 bool
	ClosingRate            decimal.Decimal
	RateDate               time.Time
	RateType               RateType
	RevaluedAmount         financialtypes.Money
	RateReference          ExchangeRate
	RateReferenceAvailable bool
	RateInverted           bool
}

type RevaluationEngine struct {
	provider RateProviderSPI
}

func NewRevaluationEngine(provider RateProviderSPI) *RevaluationEngine {
	return &RevaluationEngine{provider: provider}
}

func (e *RevaluationEngine) Calculate(ctx context.Context, position MonetaryPosition, asOf time.Time) (FXResult, error) {
	if position.Type != Monetary {
		return FXResult{}, ErrInvalidPosition
	}
	if position.AccountID == uuid.Nil || position.TransactionCurrency == "" ||
		position.FunctionalCurrency == "" ||
		position.CarryingAmount.Currency() != position.FunctionalCurrency ||
		!position.ForeignCurrencyBalance.IsPositive() ||
		!position.CarryingAmount.Amount().IsPositive() {
		return FXResult{}, ErrInvalidPosition
	}
	if e.provider == nil {
		return FXResult{}, ErrRateNotFound
	}
	rate, rateReference, rateReferenceAvailable, rateInverted, err := getClosingRateReference(
		ctx, e.provider, position.TransactionCurrency, position.FunctionalCurrency, asOf,
	)
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
		AccountID:              position.AccountID,
		Currency:               position.TransactionCurrency,
		Difference:             difference,
		IsGain:                 isGain,
		ClosingRate:            rate,
		RateDate:               asOf,
		RateType:               ClosingRate,
		RevaluedAmount:         revaluedAmount,
		RateReference:          rateReference,
		RateReferenceAvailable: rateReferenceAvailable,
		RateInverted:           rateInverted,
	}, nil
}

func getClosingRate(ctx context.Context, provider RateProviderSPI, from, to string, date time.Time) (decimal.Decimal, error) {
	return provider.GetRateOfType(ctx, ClosingRate, from, to, date)
}

type rateRecordProvider interface {
	GetRateRecord(context.Context, RateType, string, string, time.Time) (ExchangeRate, bool, decimal.Decimal, error)
}

func getClosingRateReference(ctx context.Context, provider RateProviderSPI, from, to string, date time.Time) (decimal.Decimal, ExchangeRate, bool, bool, error) {
	if records, ok := provider.(rateRecordProvider); ok {
		record, inverted, rate, err := records.GetRateRecord(ctx, ClosingRate, from, to, date)
		return rate, record, true, inverted, err
	}
	rate, err := getClosingRate(ctx, provider, from, to, date)
	if err != nil {
		return decimal.Zero, ExchangeRate{}, false, false, err
	}
	// Providers implementing only RateProviderSPI cannot expose their source record.
	// Preserve the requested quote and applied rate without implying that the request
	// date was the effective date of the provider's source rate.
	return rate, ExchangeRate{
		FromCurrency: strings.ToUpper(strings.TrimSpace(from)),
		ToCurrency:   strings.ToUpper(strings.TrimSpace(to)),
		Rate:         rate,
		Type:         ClosingRate,
	}, false, false, nil
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
		ID:                uuid.New(),
		CompanyID:         accounts.CompanyID,
		JournalID:         accounts.JournalID,
		EntryDate:         date,
		Date:              date,
		Status:            posting.EntryDraft,
		CreatedAt:         time.Now().UTC(),
		Reference:         "FX-REVAL-" + result.Currency + "-" + date.Format("2006-01-02"),
		DocumentReference: revaluationRateReference(result),
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

func revaluationRateReference(result FXResult) string {
	record := result.RateReference
	if !result.RateReferenceAvailable {
		return "requested=" + record.FromCurrency + "/" + record.ToCurrency +
			" type=" + string(record.Type) +
			" date=" + dateOnly(result.RateDate).Format("2006-01-02") +
			" applied=" + result.ClosingRate.String() + " provider_record=unavailable"
	}
	reference := "rate=" + record.FromCurrency + "/" + record.ToCurrency +
		" type=" + string(record.Type) +
		" effective=" + dateOnly(record.EffectiveDate).Format("2006-01-02") +
		" applied=" + result.ClosingRate.String()
	if record.Source != "" {
		reference += " source=" + record.Source
	}
	if result.RateInverted {
		reference += " inverted=true"
	}
	return reference
}

// RealizedFXSettlementMove represents inputs to record realized exchange differences
// upon settling a foreign currency receivable or payable (IAS 21.28).
type RealizedFXSettlementMove struct {
	CompanyID              uuid.UUID
	JournalID              uuid.UUID
	MonetaryAccountID      uuid.UUID // AR or AP control account
	SettlementAccountID    uuid.UUID // Bank or Cash account
	RealizedGainAccountID  uuid.UUID
	RealizedLossAccountID  uuid.UUID
	ForeignAmount          decimal.Decimal
	TransactionCurrency    string
	FunctionalCurrency     string
	OriginalRate           decimal.Decimal
	SettlementRate         decimal.Decimal
	OriginalRateRecord     ExchangeRate
	SettlementRateRecord   ExchangeRate
	OriginalRateInverted   bool
	SettlementRateInverted bool
	CarryingAmount         financialtypes.Money
	SettlementDate         time.Time
	IsReceivable           bool // true for settling receivable (AR), false for payable (AP)
	Reference              string
}

// GenerateRealizedFXSettlementEntry generates a balanced draft entry for settlement.
// CarryingAmount, when provided, is the functional-currency amount currently in the
// ledger (including any prior period-end remeasurement); otherwise the original rate
// is used to derive the carrying amount. The returned difference is settlement value
// minus carrying value; gain or loss classification depends on whether this is AR or AP.
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

	settlementFunctionalAmt, err := financialtypes.NewMoney(move.ForeignAmount.Mul(move.SettlementRate), move.FunctionalCurrency)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	carryingFunctionalAmt := move.CarryingAmount
	if carryingFunctionalAmt.Currency() == "" {
		carryingFunctionalAmt, err = financialtypes.NewMoney(move.ForeignAmount.Mul(move.OriginalRate), move.FunctionalCurrency)
		if err != nil {
			return posting.JournalEntry{}, financialtypes.Money{}, err
		}
	} else if carryingFunctionalAmt.Currency() != move.FunctionalCurrency ||
		!carryingFunctionalAmt.Amount().IsPositive() {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidPosition
	}
	diff, err := settlementFunctionalAmt.Subtract(carryingFunctionalAmt)
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
		ID:                uuid.New(),
		CompanyID:         move.CompanyID,
		JournalID:         move.JournalID,
		EntryDate:         move.SettlementDate,
		Date:              move.SettlementDate,
		Status:            posting.EntryDraft,
		CreatedAt:         time.Now().UTC(),
		Reference:         move.Reference,
		DocumentReference: settlementRateReference(move),
	}
	if entry.Reference == "" {
		entry.Reference = "FX-SETTLE-" + move.TransactionCurrency + "-" + move.SettlementDate.Format("2006-01-02")
	}

	var lines []posting.JournalEntryLine
	if move.IsReceivable {
		// AR settlement:
		// Bank debit (settlement functional amount)
		// AR credit (current carrying amount)
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.SettlementAccountID,
			Debit: settlementFunctionalAmt, Credit: zeroFunctional,
			FunctionalDebit: settlementFunctionalAmt, FunctionalCredit: zeroFunctional,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.SettlementRate,
		})
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.MonetaryAccountID,
			Debit: zeroFunctional, Credit: carryingFunctionalAmt,
			FunctionalDebit: zeroFunctional, FunctionalCredit: carryingFunctionalAmt,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: carryingFunctionalAmt.Amount().DivRound(move.ForeignAmount, financialtypes.InternalScale),
		})
		if !diff.Amount().IsZero() {
			diffAbs, err := financialtypes.NewMoney(diff.Amount().Abs(), move.FunctionalCurrency)
			if err != nil {
				return posting.JournalEntry{}, financialtypes.Money{}, err
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
		// AP debit (current carrying amount)
		// Bank credit (settlement functional amount)
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.MonetaryAccountID,
			Debit: carryingFunctionalAmt, Credit: zeroFunctional,
			FunctionalDebit: carryingFunctionalAmt, FunctionalCredit: zeroFunctional,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: carryingFunctionalAmt.Amount().DivRound(move.ForeignAmount, financialtypes.InternalScale),
		})
		lines = append(lines, posting.JournalEntryLine{
			ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.SettlementAccountID,
			Debit: zeroFunctional, Credit: settlementFunctionalAmt,
			FunctionalDebit: zeroFunctional, FunctionalCredit: settlementFunctionalAmt,
			TransactionAmount: foreignMoney, TransactionCurrency: move.TransactionCurrency,
			ExchangeRate: move.SettlementRate,
		})
		if !diff.Amount().IsZero() {
			diffAbs, err := financialtypes.NewMoney(diff.Amount().Abs(), move.FunctionalCurrency)
			if err != nil {
				return posting.JournalEntry{}, financialtypes.Money{}, err
			}
			if diff.Amount().IsPositive() {
				// Paid more functional currency than the carrying amount -> loss.
				lines = append(lines, posting.JournalEntryLine{
					ID: uuid.New(), CompanyID: move.CompanyID, AccountID: move.RealizedLossAccountID,
					Debit: diffAbs, Credit: zeroFunctional,
					FunctionalDebit: diffAbs, FunctionalCredit: zeroFunctional,
					TransactionAmount: diffAbs, TransactionCurrency: move.FunctionalCurrency,
					ExchangeRate: decimal.NewFromInt(1),
				})
			} else {
				// Paid less functional currency than the carrying amount -> gain.
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

func settlementRateReference(move RealizedFXSettlementMove) string {
	format := func(label string, record ExchangeRate, rate decimal.Decimal, inverted bool) string {
		if record.Type == "" && record.FromCurrency == "" && record.ToCurrency == "" {
			return label + "-rate=" + rate.String()
		}
		reference := label + "-rate=" + record.FromCurrency + "/" + record.ToCurrency +
			" applied=" + rate.String()
		if record.Type != "" {
			reference += " type=" + string(record.Type)
		}
		if !record.EffectiveDate.IsZero() {
			reference += " effective=" + dateOnly(record.EffectiveDate).Format("2006-01-02")
		}
		if record.Source != "" {
			reference += " source=" + record.Source
		}
		if inverted {
			reference += " inverted=true"
		}
		return reference
	}
	return format("original", move.OriginalRateRecord, move.OriginalRate, move.OriginalRateInverted) +
		"; " + format("settlement", move.SettlementRateRecord, move.SettlementRate, move.SettlementRateInverted)
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
