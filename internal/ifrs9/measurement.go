package ifrs9

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type MeasurementCategory string

const (
	AmortisedCost MeasurementCategory = "amortised_cost"
	FVOCI         MeasurementCategory = "fvoci"
	FVTPL         MeasurementCategory = "fvtpl"
)

var (
	ErrInvalidClassification       = errors.New("invalid IFRS 9 host classification")
	ErrInvalidMeasurement          = errors.New("invalid IFRS 9 subsequent measurement input")
	ErrInvalidContractualCashFlows = errors.New("invalid IFRS 9 contractual cash flows")
	ErrECLNotApplicable            = errors.New("ECL is not applicable to FVTPL assets")
	ErrMeasurementNoEntry          = errors.New("IFRS 9 measurement has no journal movement")
	ErrMeasurementAccounts         = errors.New("IFRS 9 measurement accounts are invalid")
	ErrMultipleECLCategories       = errors.New("separate ECL account mappings are required by measurement category")
)

// HostClassification records the host's final business-model and SPPI
// conclusions. The accounting engine validates the supplied decision but does
// not make either assessment.
type HostClassification struct {
	MeasurementCategory     MeasurementCategory
	BusinessModelReference  string
	SPPIAssessmentReference string
}

func (c HostClassification) Validate() error {
	if !validMeasurementCategory(c.MeasurementCategory) ||
		strings.TrimSpace(c.BusinessModelReference) == "" ||
		strings.TrimSpace(c.SPPIAssessmentReference) == "" {
		return ErrInvalidClassification
	}
	return nil
}

func validMeasurementCategory(category MeasurementCategory) bool {
	return category == AmortisedCost || category == FVOCI || category == FVTPL
}

// CalculateEAD adds the ledger exposure and undrawn commitments. The caller
// supplies both amounts in the same currency; no credit-conversion factor is
// inferred by the engine.
func CalculateEAD(ledgerBalance, undrawnCommitments financialtypes.Money) (financialtypes.Money, error) {
	if ledgerBalance.Currency() == "" || ledgerBalance.Currency() != undrawnCommitments.Currency() ||
		ledgerBalance.IsNegative() || undrawnCommitments.IsNegative() {
		return financialtypes.Money{}, ErrInvalidGeneralExposure
	}
	return ledgerBalance.Add(undrawnCommitments)
}

type ContractualCashFlow struct {
	Date   time.Time
	Amount financialtypes.Money
}

// CalculateEffectiveInterestRate derives an annual effective rate from the
// initial carrying amount and dated, nonnegative contractual cash flows.
func CalculateEffectiveInterestRate(
	initialCarryingAmount financialtypes.Money,
	initialRecognitionDate time.Time,
	cashFlows []ContractualCashFlow,
) (decimal.Decimal, error) {
	if initialCarryingAmount.Currency() == "" || !initialCarryingAmount.Amount().IsPositive() ||
		initialRecognitionDate.IsZero() || len(cashFlows) == 0 {
		return decimal.Zero, ErrInvalidContractualCashFlows
	}
	currency := initialCarryingAmount.Currency()
	flows := append([]ContractualCashFlow(nil), cashFlows...)
	for _, cashFlow := range flows {
		if cashFlow.Date.IsZero() || !cashFlow.Date.After(initialRecognitionDate) ||
			cashFlow.Amount.Currency() != currency || !cashFlow.Amount.Amount().IsPositive() {
			return decimal.Zero, ErrInvalidContractualCashFlows
		}
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date.Before(flows[j].Date) })
	const lowerBound = "-0.999999"
	low := decimal.RequireFromString(lowerBound)
	high := decimal.NewFromInt(1)
	lowPV, err := contractualCashFlowsPresentValue(initialRecognitionDate, flows, low)
	if err != nil || lowPV.LessThan(initialCarryingAmount.Amount()) {
		return decimal.Zero, ErrInvalidContractualCashFlows
	}
	highPV, err := contractualCashFlowsPresentValue(initialRecognitionDate, flows, high)
	for attempts := 0; err == nil && highPV.GreaterThan(initialCarryingAmount.Amount()) && attempts < 64; attempts++ {
		high = high.Mul(decimal.NewFromInt(2))
		highPV, err = contractualCashFlowsPresentValue(initialRecognitionDate, flows, high)
	}
	if err != nil || highPV.GreaterThan(initialCarryingAmount.Amount()) {
		return decimal.Zero, ErrInvalidContractualCashFlows
	}
	for iteration := 0; iteration < 128; iteration++ {
		midpoint := low.Add(high).Div(decimal.NewFromInt(2))
		presentValue, pvErr := contractualCashFlowsPresentValue(initialRecognitionDate, flows, midpoint)
		if pvErr != nil {
			return decimal.Zero, ErrInvalidContractualCashFlows
		}
		if presentValue.GreaterThan(initialCarryingAmount.Amount()) {
			low = midpoint
		} else {
			high = midpoint
		}
	}
	return low.Add(high).Div(decimal.NewFromInt(2)), nil
}

func contractualCashFlowsPresentValue(
	initialRecognitionDate time.Time,
	cashFlows []ContractualCashFlow,
	rate decimal.Decimal,
) (decimal.Decimal, error) {
	base := decimal.NewFromInt(1).Add(rate)
	if !base.IsPositive() {
		return decimal.Zero, ErrInvalidContractualCashFlows
	}
	yearNanos := decimal.NewFromInt(int64(365 * 24 * time.Hour))
	presentValue := decimal.Zero
	for _, cashFlow := range cashFlows {
		horizon := cashFlow.Date.Sub(initialRecognitionDate)
		if horizon <= 0 {
			return decimal.Zero, ErrInvalidContractualCashFlows
		}
		years := decimal.NewFromInt(int64(horizon)).Div(yearNanos)
		presentValue = presentValue.Add(cashFlow.Amount.Amount().Div(base.Pow(years)))
	}
	return presentValue, nil
}

// MeasurementInput contains a host-approved classification and the balances
// required to measure one reporting period. Cash principal repayments are
// separately posted with the returned measurement entry.
type MeasurementInput struct {
	InstrumentID          uuid.UUID
	AsOf                  time.Time
	Classification        HostClassification
	Stage                 Stage
	OpeningGrossCarrying  financialtypes.Money
	OpeningCarryingAmount financialtypes.Money
	ExistingLossAllowance financialtypes.Money
	PrincipalRepayments   financialtypes.Money
	FairValue             financialtypes.Money
	EffectiveInterestRate decimal.Decimal
	Period                time.Duration
}

type MeasurementResult struct {
	InstrumentID          uuid.UUID
	AsOf                  time.Time
	Classification        HostClassification
	InputFingerprint      string
	Category              MeasurementCategory
	Stage                 Stage
	Currency              string
	GrossInterestAccrued  financialtypes.Money
	InterestIncome        financialtypes.Money
	AllowanceAccretion    financialtypes.Money
	PrincipalRepayments   financialtypes.Money
	AmortisedCostBalance  financialtypes.Money
	ClosingCarryingAmount financialtypes.Money
	FairValueAdjustment   financialtypes.Money
}

// MeasureSubsequentPeriod applies the host-provided category to effective
// interest and fair-value movements. Stage 3 interest uses the net carrying
// amount; stages 1 and 2 use the gross carrying amount.
func MeasureSubsequentPeriod(input MeasurementInput) (MeasurementResult, error) {
	if input.InstrumentID == uuid.Nil || input.AsOf.IsZero() || input.Period <= 0 ||
		input.Stage != Stage1 && input.Stage != Stage2 && input.Stage != Stage3 ||
		input.Classification.Validate() != nil {
		return MeasurementResult{}, ErrInvalidMeasurement
	}
	if input.Classification.MeasurementCategory != FVTPL &&
		!input.EffectiveInterestRate.GreaterThan(decimal.NewFromInt(-1)) {
		return MeasurementResult{}, ErrInvalidMeasurement
	}
	currency := input.OpeningGrossCarrying.Currency()
	if currency == "" || input.ExistingLossAllowance.Currency() != currency ||
		input.PrincipalRepayments.Currency() != currency ||
		input.OpeningGrossCarrying.IsNegative() || input.ExistingLossAllowance.IsNegative() ||
		input.PrincipalRepayments.IsNegative() ||
		input.ExistingLossAllowance.GreaterThan(input.OpeningGrossCarrying) ||
		input.PrincipalRepayments.GreaterThan(input.OpeningGrossCarrying) {
		return MeasurementResult{}, ErrInvalidMeasurement
	}
	if input.Classification.MeasurementCategory == FVTPL && !input.ExistingLossAllowance.Amount().IsZero() {
		return MeasurementResult{}, ErrInvalidMeasurement
	}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return MeasurementResult{}, err
	}
	openingCarrying := input.OpeningCarryingAmount
	fairValue := input.FairValue
	if input.Classification.MeasurementCategory == AmortisedCost {
		if openingCarrying.Currency() != "" || fairValue.Currency() != "" {
			return MeasurementResult{}, ErrInvalidMeasurement
		}
	} else if openingCarrying.Currency() != currency || fairValue.Currency() != currency ||
		openingCarrying.IsNegative() || fairValue.IsNegative() {
		return MeasurementResult{}, ErrInvalidMeasurement
	}

	effectivePeriodRate := decimal.Zero
	if input.Classification.MeasurementCategory != FVTPL {
		periodYears := decimal.NewFromInt(int64(input.Period)).Div(
			decimal.NewFromInt(int64(365 * 24 * time.Hour)),
		)
		effectivePeriodRate = decimal.NewFromInt(1).
			Add(input.EffectiveInterestRate).
			Pow(periodYears).
			Sub(decimal.NewFromInt(1))
	}
	grossInterest := input.OpeningGrossCarrying.Amount().Mul(effectivePeriodRate)
	interestBase := input.OpeningGrossCarrying.Amount()
	if input.Stage == Stage3 {
		interestBase = interestBase.Sub(input.ExistingLossAllowance.Amount())
	}
	interestIncomeAmount := interestBase.Mul(effectivePeriodRate)
	allowanceAccretionAmount := grossInterest.Sub(interestIncomeAmount)
	grossInterestMoney, err := financialtypes.NewMoney(grossInterest, currency)
	if err != nil {
		return MeasurementResult{}, err
	}
	interestIncome, err := financialtypes.NewMoney(interestIncomeAmount, currency)
	if err != nil {
		return MeasurementResult{}, err
	}
	allowanceAccretion, err := financialtypes.NewMoney(allowanceAccretionAmount, currency)
	if err != nil {
		return MeasurementResult{}, err
	}
	amortisedCost, err := input.OpeningGrossCarrying.Add(grossInterestMoney)
	if err != nil {
		return MeasurementResult{}, err
	}
	amortisedCost, err = amortisedCost.Subtract(input.PrincipalRepayments)
	if err != nil {
		return MeasurementResult{}, err
	}
	if amortisedCost.IsNegative() {
		return MeasurementResult{}, ErrInvalidMeasurement
	}
	fairValueAdjustment := zero
	closingCarrying := amortisedCost
	switch input.Classification.MeasurementCategory {
	case AmortisedCost:
		if input.Stage == Stage3 {
			allowanceAtClose, addErr := input.ExistingLossAllowance.Add(allowanceAccretion)
			if addErr != nil {
				return MeasurementResult{}, addErr
			}
			if allowanceAtClose.IsNegative() {
				return MeasurementResult{}, ErrInvalidMeasurement
			}
			closingCarrying, err = amortisedCost.Subtract(allowanceAtClose)
			if err != nil || closingCarrying.IsNegative() {
				return MeasurementResult{}, ErrInvalidMeasurement
			}
		}
	case FVOCI:
		preRemeasurement, addErr := openingCarrying.Add(interestIncome)
		if addErr != nil {
			return MeasurementResult{}, addErr
		}
		preRemeasurement, err = preRemeasurement.Subtract(input.PrincipalRepayments)
		if err != nil {
			return MeasurementResult{}, err
		}
		fairValueAdjustment, err = fairValue.Subtract(preRemeasurement)
		if err != nil {
			return MeasurementResult{}, err
		}
		closingCarrying = fairValue
	case FVTPL:
		preRemeasurement, subErr := openingCarrying.Subtract(input.PrincipalRepayments)
		if subErr != nil {
			return MeasurementResult{}, subErr
		}
		fairValueAdjustment, err = fairValue.Subtract(preRemeasurement)
		if err != nil {
			return MeasurementResult{}, err
		}
		closingCarrying = fairValue
		interestIncome = zero
		grossInterestMoney = zero
		allowanceAccretion = zero
	}
	return MeasurementResult{
		InstrumentID: input.InstrumentID, AsOf: input.AsOf.UTC(),
		Classification: input.Classification, InputFingerprint: fingerprintMeasurementInput(input),
		Category: input.Classification.MeasurementCategory, Stage: input.Stage, Currency: currency,
		GrossInterestAccrued: grossInterestMoney, InterestIncome: interestIncome,
		AllowanceAccretion: allowanceAccretion, PrincipalRepayments: input.PrincipalRepayments,
		AmortisedCostBalance: amortisedCost, ClosingCarryingAmount: closingCarrying,
		FairValueAdjustment: fairValueAdjustment,
	}, nil
}

func fingerprintMeasurementInput(input MeasurementInput) string {
	var canonical bytes.Buffer
	values := []string{
		"ifrs9-measurement-v1",
		input.InstrumentID.String(),
		input.AsOf.UTC().Format(time.RFC3339Nano),
		string(input.Classification.MeasurementCategory),
		input.Classification.BusinessModelReference,
		input.Classification.SPPIAssessmentReference,
		string(input.Stage),
		input.OpeningGrossCarrying.Amount().String(),
		input.OpeningGrossCarrying.Currency(),
		input.OpeningCarryingAmount.Amount().String(),
		input.OpeningCarryingAmount.Currency(),
		input.ExistingLossAllowance.Amount().String(),
		input.ExistingLossAllowance.Currency(),
		input.PrincipalRepayments.Amount().String(),
		input.PrincipalRepayments.Currency(),
		input.FairValue.Amount().String(),
		input.FairValue.Currency(),
		input.EffectiveInterestRate.String(),
		fmt.Sprint(int64(input.Period)),
	}
	for _, value := range values {
		fmt.Fprintf(&canonical, "%d:", len(value))
		canonical.WriteString(value)
	}
	digest := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(digest[:])
}

type MeasurementAccounts struct {
	Asset             uuid.UUID
	Cash              uuid.UUID
	InterestIncome    uuid.UUID
	OCIReserve        uuid.UUID
	FairValueGainLoss uuid.UUID
}

// BuildMeasurementEntry creates a balanced draft for interest, principal
// repayments, and subsequent fair-value movements. ECL movements are posted
// separately through the ECL workflow.
func BuildMeasurementEntry(
	result MeasurementResult,
	accounts MeasurementAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	if companyID == uuid.Nil || journalID == uuid.Nil || result.InstrumentID == uuid.Nil ||
		result.AsOf.IsZero() || !validMeasurementCategory(result.Category) ||
		result.Classification.Validate() != nil ||
		result.Classification.MeasurementCategory != result.Category ||
		len(result.InputFingerprint) != sha256.Size*2 || result.Currency == "" {
		return posting.JournalEntry{}, ErrInvalidMeasurement
	}
	zero, err := financialtypes.Zero(result.Currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	seed := fmt.Sprintf("ifrs9-measurement|%s|%s|%s|%s|%s",
		companyID, journalID, result.InstrumentID, result.AsOf.Format(time.RFC3339Nano), result.Category)
	entryID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed))
	lines := make([]posting.JournalEntryLine, 0, 6)
	type accountRole struct {
		role string
		id   uuid.UUID
	}
	var requiredAccounts []accountRole
	if !result.PrincipalRepayments.RoundForPosting().Amount().IsZero() {
		requiredAccounts = append(requiredAccounts,
			accountRole{role: "asset", id: accounts.Asset},
			accountRole{role: "cash", id: accounts.Cash},
		)
	}
	if result.Category != FVTPL && !result.InterestIncome.RoundForPosting().Amount().IsZero() {
		requiredAccounts = append(requiredAccounts,
			accountRole{role: "asset", id: accounts.Asset},
			accountRole{role: "interest_income", id: accounts.InterestIncome},
		)
	}
	if !result.FairValueAdjustment.RoundForPosting().Amount().IsZero() {
		requiredAccounts = append(requiredAccounts, accountRole{role: "asset", id: accounts.Asset})
		if result.Category == FVOCI {
			requiredAccounts = append(requiredAccounts, accountRole{role: "oci_reserve", id: accounts.OCIReserve})
		} else if result.Category == FVTPL {
			requiredAccounts = append(requiredAccounts, accountRole{role: "fair_value_gain_loss", id: accounts.FairValueGainLoss})
		} else {
			return posting.JournalEntry{}, ErrInvalidMeasurement
		}
	}
	seenAccounts := make(map[uuid.UUID]string, len(requiredAccounts))
	for _, role := range requiredAccounts {
		if role.id == uuid.Nil {
			return posting.JournalEntry{}, ErrMeasurementAccounts
		}
		if previousRole, exists := seenAccounts[role.id]; exists && previousRole != role.role {
			return posting.JournalEntry{}, fmt.Errorf("%w: %s and %s", ErrMeasurementAccounts, previousRole, role.role)
		}
		seenAccounts[role.id] = role.role
	}
	appendMovement := func(debitAccount, creditAccount uuid.UUID, amount financialtypes.Money) error {
		if amount.Currency() != result.Currency {
			return ErrInvalidMeasurement
		}
		amount = amount.RoundForPosting()
		if amount.Amount().IsZero() {
			return nil
		}
		if debitAccount == uuid.Nil || creditAccount == uuid.Nil || debitAccount == creditAccount {
			return ErrMeasurementAccounts
		}
		debit, credit := amount, zero
		if amount.IsNegative() {
			debit, err = financialtypes.NewMoney(amount.Amount().Abs(), result.Currency)
			if err != nil {
				return err
			}
			debit, credit = zero, debit
			debitAccount, creditAccount = creditAccount, debitAccount
		}
		amountAbs := debit
		if amountAbs.Amount().IsZero() {
			amountAbs = credit
		}
		movementIndex := len(lines) / 2
		lines = append(lines,
			posting.JournalEntryLine{
				ID:        uuid.NewSHA1(entryID, []byte(fmt.Sprintf("debit:%d", movementIndex))),
				CompanyID: companyID, AccountID: debitAccount,
				Debit: amountAbs, Credit: zero, TransactionAmount: amountAbs,
				TransactionCurrency: result.Currency, ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: amountAbs, FunctionalCredit: zero,
			},
			posting.JournalEntryLine{
				ID:        uuid.NewSHA1(entryID, []byte(fmt.Sprintf("credit:%d", movementIndex))),
				CompanyID: companyID, AccountID: creditAccount,
				Debit: zero, Credit: amountAbs, TransactionAmount: amountAbs,
				TransactionCurrency: result.Currency, ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: zero, FunctionalCredit: amountAbs,
			},
		)
		return nil
	}
	if !result.PrincipalRepayments.Amount().IsZero() {
		if err := appendMovement(accounts.Cash, accounts.Asset, result.PrincipalRepayments); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if result.Category != FVTPL && !result.InterestIncome.Amount().IsZero() {
		if err := appendMovement(accounts.Asset, accounts.InterestIncome, result.InterestIncome); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if !result.FairValueAdjustment.Amount().IsZero() {
		counterAccount := accounts.FairValueGainLoss
		if result.Category == FVOCI {
			counterAccount = accounts.OCIReserve
		}
		if result.Category == AmortisedCost {
			return posting.JournalEntry{}, ErrInvalidMeasurement
		}
		if err := appendMovement(accounts.Asset, counterAccount, result.FairValueAdjustment); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if len(lines) == 0 {
		return posting.JournalEntry{}, ErrMeasurementNoEntry
	}
	entry := posting.JournalEntry{
		ID: entryID, CompanyID: companyID, JournalID: journalID,
		EntryDate: result.AsOf, Date: result.AsOf, CreatedAt: result.AsOf,
		Status: posting.EntryDraft, Classification: posting.EntryClassRegular,
		Lines: lines, CreatedBy: createdBy, SourceModule: "ifrs9",
		Reference: fmt.Sprintf("ifrs9-%s-measurement-%s", result.Category, result.InstrumentID),
		DocumentReference: fmt.Sprintf("sha256:%s;business_model:%s;sppi:%s",
			result.InputFingerprint, result.Classification.BusinessModelReference,
			result.Classification.SPPIAssessmentReference),
	}
	keyHash := sha256.Sum256([]byte(seed))
	entry.IdempotencyKey = "ifrs9-measurement-" + hex.EncodeToString(keyHash[:])
	payload := seed + "|" + entry.DocumentReference
	for _, line := range entry.Lines {
		payload += fmt.Sprintf("|%s:%s:%s", line.AccountID, line.Debit.Amount(), line.Credit.Amount())
	}
	payloadHash := sha256.Sum256([]byte(payload))
	entry.IdempotencyPayloadHash = hex.EncodeToString(payloadHash[:])
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}
