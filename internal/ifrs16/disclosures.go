package ifrs16

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/types"
	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

var ErrInvalidDisclosureInput = errors.New("ifrs16: invalid disclosure input")

const (
	// DisclosureStandardReference is the standard reference stamped on every
	// IFRS 16 structured disclosure payload.
	DisclosureStandardReference = "IFRS 16"
	// DisclosureSchemaVersion is the schema version of the payload builders.
	DisclosureSchemaVersion = "1.0.0"
	// DisclosureCodeLeaseExpense identifies the lease expense and income note.
	DisclosureCodeLeaseExpense = "IFRS16-LEASE-EXPENSE"
	// DisclosureCodeMaturity identifies the future lease payments maturity note.
	DisclosureCodeMaturity = "IFRS16-LIABILITY-MATURITY"
)

// OpenEndedMonths marks the maturity bucket for payments due beyond five years.
const OpenEndedMonths = 0

// MaturityBucket is one time band of undiscounted future lease payments. Months
// is the inclusive upper boundary of the band in months from the reporting date;
// OpenEndedMonths (0) marks the open band beyond five years.
type MaturityBucket struct {
	Label  string `json:"label"`
	Months int    `json:"within_months"`
	Amount string `json:"amount"`
}

// StandardMaturityBuckets returns the standard maturity band labels used by the
// future lease payments analysis: one year, then yearly up to five years, and
// the open band beyond five years.
func StandardMaturityBuckets() []MaturityBucket {
	return []MaturityBucket{
		{Label: "within_one_year", Months: 12},
		{Label: "one_to_two_years", Months: 24},
		{Label: "two_to_three_years", Months: 36},
		{Label: "three_to_four_years", Months: 48},
		{Label: "four_to_five_years", Months: 60},
		{Label: "beyond_five_years", Months: OpenEndedMonths},
	}
}

// FutureLeasePaymentBuckets derives the undiscounted maturity analysis of
// future lease payments from a lessee lease schedule as of the reporting date.
// The lease reference of every bucket is left empty; the caller stamps its own
// source references on the disclosure.
func FutureLeasePaymentBuckets(contract LeaseContract, asOf time.Time) ([]MaturityBucket, error) {
	if contract.Currency == "" || len(contract.Payments) == 0 || asOf.IsZero() {
		return nil, ErrInvalidDisclosureInput
	}
	buckets := StandardMaturityBuckets()
	totals := make([]decimal.Decimal, len(buckets))
	utc := asOf.UTC()
	for _, payment := range contract.Payments {
		if payment.Amount.Currency() != contract.Currency || payment.DueDate.Before(utc) {
			continue
		}
		months := wholeMonths(utc, payment.DueDate)
		placed := false
		for i, bucket := range buckets {
			if bucket.Months != OpenEndedMonths && months <= bucket.Months {
				totals[i] = totals[i].Add(payment.Amount.Amount())
				placed = true
				break
			}
		}
		if !placed {
			totals[len(totals)-1] = totals[len(totals)-1].Add(payment.Amount.Amount())
		}
	}
	result := make([]MaturityBucket, 0, len(buckets))
	for i, bucket := range buckets {
		result = append(result, MaturityBucket{
			Label:  bucket.Label,
			Months: bucket.Months,
			Amount: totals[i].Round(scale).StringFixed(int32(scale)),
		})
	}
	return result, nil
}

// LeaseCarryingLiability returns the lease liability carrying amount from the
// schedule period due on or before the reporting date, or the initial liability
// when no payment is due yet.
func LeaseCarryingLiability(contract LeaseContract, asOf time.Time) (types.Money, error) {
	if contract.Currency == "" || asOf.IsZero() {
		return types.Money{}, ErrInvalidDisclosureInput
	}
	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		return types.Money{}, err
	}
	utc := asOf.UTC()
	carrying := contract.InitialLeaseLiability
	for _, period := range schedule {
		if period.DueDate.After(utc) {
			break
		}
		carrying = period.ClosingLiability
	}
	return carrying, nil
}

// LeaseExpenseDisclosureInput collects the host-supplied lease expense and
// income balances for one reporting period. Amounts left as zero-value Money
// are omitted from the payload; at least one amount must be supplied.
type LeaseExpenseDisclosureInput struct {
	CompanyID                  apitypes.CompanyID
	PeriodStart                time.Time
	PeriodEnd                  time.Time
	Currency                   string
	ROUDepreciation            types.Money
	InterestOnLeaseLiabilities types.Money
	ShortTermLeaseExpense      types.Money
	LowValueLeaseExpense       types.Money
	OperatingLeaseIncome       types.Money
	FinanceLeaseIncome         types.Money
	SourceReferences           []publicreporting.DisclosureSource
	Narrative                  string
}

// BuildLeaseExpenseDisclosure builds the IFRS 16 lease expense and income
// disclosure payload for the P9-05 disclosure framework. The payload carries a
// deterministic structure; the registry owns normalization and the content hash.
func BuildLeaseExpenseDisclosure(input LeaseExpenseDisclosureInput) (publicreporting.StructuredDisclosure, error) {
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if err := validateDisclosureScope(input.CompanyID, input.PeriodStart, input.PeriodEnd, currency); err != nil {
		return publicreporting.StructuredDisclosure{}, err
	}
	data := map[string]any{}
	totalExpense := decimal.Zero
	totalIncome := decimal.Zero
	for _, item := range []struct {
		key      string
		amount   types.Money
		isIncome bool
	}{
		{key: "depreciation_of_right_of_use_assets", amount: input.ROUDepreciation},
		{key: "interest_on_lease_liabilities", amount: input.InterestOnLeaseLiabilities},
		{key: "short_term_lease_expense", amount: input.ShortTermLeaseExpense},
		{key: "low_value_lease_expense", amount: input.LowValueLeaseExpense},
		{key: "operating_lease_income", amount: input.OperatingLeaseIncome, isIncome: true},
		{key: "finance_lease_income", amount: input.FinanceLeaseIncome, isIncome: true},
	} {
		if item.amount.Currency() == "" {
			continue
		}
		if item.amount.Currency() != currency {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: %s currency", ErrInvalidDisclosureInput, item.key)
		}
		data[item.key] = item.amount.Amount().Round(scale).StringFixed(int32(scale))
		if item.isIncome {
			totalIncome = totalIncome.Add(item.amount.Amount())
		} else {
			totalExpense = totalExpense.Add(item.amount.Amount())
		}
	}
	if len(data) == 0 {
		return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: no lease amounts supplied", ErrInvalidDisclosureInput)
	}
	data["total_expense"] = totalExpense.Round(scale).StringFixed(int32(scale))
	data["total_income"] = totalIncome.Round(scale).StringFixed(int32(scale))

	sources, err := withModuleSource(input.SourceReferences)
	if err != nil {
		return publicreporting.StructuredDisclosure{}, err
	}
	return publicreporting.StructuredDisclosure{
		CompanyID:         input.CompanyID,
		Code:              DisclosureCodeLeaseExpense,
		Title:             "Lease expense and lease income",
		StandardReference: DisclosureStandardReference,
		Period: publicreporting.DisclosurePeriod{
			StartDate: normalizeDate(input.PeriodStart),
			EndDate:   normalizeDate(input.PeriodEnd),
		},
		Currency:  strings.ToUpper(currency),
		Version:   DisclosureSchemaVersion,
		Source:    sources,
		Data:      data,
		Narrative: strings.TrimSpace(input.Narrative),
	}, nil
}

// LeaseMaturityDisclosureInput collects the host-supplied future lease payments
// analysis and the lease liability carrying amount for the maturity note.
type LeaseMaturityDisclosureInput struct {
	CompanyID              apitypes.CompanyID
	PeriodStart            time.Time
	PeriodEnd              time.Time
	Currency               string
	LeaseLiabilityCarrying types.Money
	Buckets                []MaturityBucket
	SourceReferences       []publicreporting.DisclosureSource
	Narrative              string
}

// BuildLeaseMaturityDisclosure builds the future lease payments maturity
// disclosure payload for the P9-05 disclosure framework.
func BuildLeaseMaturityDisclosure(input LeaseMaturityDisclosureInput) (publicreporting.StructuredDisclosure, error) {
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if err := validateDisclosureScope(input.CompanyID, input.PeriodStart, input.PeriodEnd, currency); err != nil {
		return publicreporting.StructuredDisclosure{}, err
	}
	if input.LeaseLiabilityCarrying.Currency() != currency {
		return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: lease liability currency", ErrInvalidDisclosureInput)
	}
	if len(input.Buckets) == 0 {
		return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: maturity buckets are required", ErrInvalidDisclosureInput)
	}
	total := decimal.Zero
	serialized := make([]MaturityBucket, 0, len(input.Buckets))
	for _, bucket := range input.Buckets {
		label := strings.TrimSpace(bucket.Label)
		if label == "" || bucket.Months < OpenEndedMonths {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: maturity bucket label or boundary", ErrInvalidDisclosureInput)
		}
		amount, err := types.NewMoneyFromString(bucket.Amount, currency)
		if err != nil {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: maturity bucket amount %q", ErrInvalidDisclosureInput, bucket.Amount)
		}
		if amount.IsNegative() {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf("%w: negative maturity bucket amount", ErrInvalidDisclosureInput)
		}
		total = total.Add(amount.Amount())
		serialized = append(serialized, MaturityBucket{Label: label, Months: bucket.Months, Amount: bucket.Amount})
	}
	data := map[string]any{
		"lease_liability_carrying_amount": input.LeaseLiabilityCarrying.Amount().Round(scale).StringFixed(int32(scale)),
		"total_undiscounted_payments":     total.Round(scale).StringFixed(int32(scale)),
		"maturity_analysis":               serialized,
	}
	sources, err := withModuleSource(input.SourceReferences)
	if err != nil {
		return publicreporting.StructuredDisclosure{}, err
	}
	return publicreporting.StructuredDisclosure{
		CompanyID:         input.CompanyID,
		Code:              DisclosureCodeMaturity,
		Title:             "Future lease payments maturity analysis",
		StandardReference: DisclosureStandardReference,
		Period: publicreporting.DisclosurePeriod{
			StartDate: normalizeDate(input.PeriodStart),
			EndDate:   normalizeDate(input.PeriodEnd),
		},
		Currency:  strings.ToUpper(currency),
		Version:   DisclosureSchemaVersion,
		Source:    sources,
		Data:      data,
		Narrative: strings.TrimSpace(input.Narrative),
	}, nil
}

func validateDisclosureScope(
	companyID apitypes.CompanyID, periodStart, periodEnd time.Time, currency string,
) error {
	if companyID == (apitypes.CompanyID{}) {
		return fmt.Errorf("%w: company is required", ErrInvalidDisclosureInput)
	}
	if periodStart.IsZero() || periodEnd.IsZero() || periodEnd.Before(periodStart) {
		return fmt.Errorf("%w: reporting period", ErrInvalidDisclosureInput)
	}
	if strings.TrimSpace(currency) == "" {
		return fmt.Errorf("%w: currency is required", ErrInvalidDisclosureInput)
	}
	return nil
}

func withModuleSource(sourceReferences []publicreporting.DisclosureSource) ([]publicreporting.DisclosureSource, error) {
	sources := append([]publicreporting.DisclosureSource{{Kind: "standard-module", Reference: "ifrs16"}}, sourceReferences...)
	for _, source := range sources {
		if strings.TrimSpace(source.Kind) == "" || strings.TrimSpace(source.Reference) == "" {
			return nil, fmt.Errorf("%w: source kind and reference are required", ErrInvalidDisclosureInput)
		}
	}
	return sources, nil
}

func normalizeDate(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func wholeMonths(from, to time.Time) int {
	months := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if to.Day() > from.Day() {
		months++
	}
	if months < 0 {
		months = 0
	}
	return months
}
