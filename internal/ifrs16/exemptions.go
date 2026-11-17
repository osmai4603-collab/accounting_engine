package ifrs16

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrExemptionIneligible   = errors.New("lease is not eligible for the requested exemption")
	ErrInvalidExemption      = errors.New("invalid lease exemption")
	ErrInvalidExemptionAccnt = errors.New("invalid exemption journal accounts")
)

// MaxShortTermLeaseMonths is the IFRS 16 short-term lease boundary: a lease
// term of, at most, twelve months at the commencement date.
const MaxShortTermLeaseMonths = 12

// LeaseExemption identifies the host-elected lessee exemption applied to a
// lease as an alternative accounting treatment instead of recognizing a
// right-of-use asset and a lease liability.
type LeaseExemption string

const (
	// ShortTermExemption applies to leases with a term of at most twelve
	// months that contain no purchase option the lessee is reasonably
	// certain to exercise.
	ShortTermExemption LeaseExemption = "short_term"
	// LowValueExemption applies to underlying assets of low individual value
	// when new, measured against a host-configurable monetary threshold.
	LowValueExemption LeaseExemption = "low_value"
)

// ExemptibleLease holds a host-elected exemption lease. Lease payments are
// charged to profit or loss on a straight-line basis over the lease term; when
// payments are uneven, the difference between recognized expense and cash paid
// accrues as a liability (expense ahead of cash) or a prepaid asset (cash ahead
// of expense).
type ExemptibleLease struct {
	Reference          string         `json:"reference,omitempty"`
	CommencementDate   time.Time      `json:"commencement_date"`
	Currency           string         `json:"currency"`
	Payments           []LeasePayment `json:"payments"`
	Exemption          LeaseExemption `json:"exemption"`
	LeaseTermMonths    int            `json:"lease_term_months"`
	PurchaseOption     bool           `json:"purchase_option"`
	AssetFairValue     types.Money    `json:"asset_fair_value"`
	LowValueThreshold  types.Money    `json:"low_value_threshold"`
	TotalLeasePayments types.Money    `json:"total_lease_payments"`
}

// ExemptionPeriod is one dated straight-line expense roll-forward. AccrualMovement
// and CumulativeAccrual are signed: positive means expense exceeded cash (accrued
// lease expense liability); negative means cash exceeded expense (prepaid asset).
type ExemptionPeriod struct {
	LeaseReference    string
	Period            int
	DueDate           time.Time
	CashPaid          types.Money
	LeaseExpense      types.Money
	AccrualMovement   types.Money
	CumulativeAccrual types.Money
}

// WithReference returns a copy identified by the caller's stable contract key.
func (l ExemptibleLease) WithReference(reference string) ExemptibleLease {
	l.Reference = reference
	return l
}

// NewExemptibleLease validates a lease and its exemption mechanics. The
// election of an exemption is a host accounting-policy choice; this constructor
// verifies only mechanical eligibility:
//
//   - short_term: a host-supplied lease term of at most twelve months, no
//     purchase option, and all payments within the term.
//   - low_value: a supplied positive asset fair value at or below the
//     host-configurable threshold.
func NewExemptibleLease(
	commencementDate time.Time,
	payments []LeasePayment,
	exemption LeaseExemption,
	leaseTermMonths int,
	purchaseOption bool,
	assetFairValue, lowValueThreshold types.Money,
) (ExemptibleLease, error) {
	if commencementDate.IsZero() || len(payments) == 0 || leaseTermMonths < 1 {
		return ExemptibleLease{}, ErrInvalidExemption
	}
	if exemption != ShortTermExemption && exemption != LowValueExemption {
		return ExemptibleLease{}, ErrInvalidExemption
	}
	currency := payments[0].Amount.Currency()
	if currency == "" {
		currency = assetFairValue.Currency()
	}
	if currency == "" {
		currency = lowValueThreshold.Currency()
	}
	if currency == "" {
		return ExemptibleLease{}, ErrInvalidExemption
	}
	leasePayments, err := validateRemainingPayments(commencementDate, payments, currency)
	if err != nil {
		return ExemptibleLease{}, err
	}

	lease := ExemptibleLease{
		CommencementDate: commencementDate.UTC(),
		Currency:         currency,
		Payments:         leasePayments,
		Exemption:        exemption,
		LeaseTermMonths:  leaseTermMonths,
		PurchaseOption:   purchaseOption,
	}
	total := mustZero(currency)
	for _, payment := range leasePayments {
		sum, err := total.Add(payment.Amount)
		if err != nil {
			return ExemptibleLease{}, err
		}
		total = sum
	}
	lease.TotalLeasePayments = total

	switch exemption {
	case ShortTermExemption:
		if leaseTermMonths > MaxShortTermLeaseMonths || purchaseOption {
			return ExemptibleLease{}, fmt.Errorf("%w: short-term term or purchase option", ErrExemptionIneligible)
		}
		termEnd := commencementDate.AddDate(0, leaseTermMonths, 0)
		lastPayment := leasePayments[len(leasePayments)-1].DueDate
		if lastPayment.After(termEnd) {
			return ExemptibleLease{}, fmt.Errorf("%w: payment beyond the lease term", ErrExemptionIneligible)
		}
	case LowValueExemption:
		if assetFairValue.Currency() != currency || !assetFairValue.Amount().IsPositive() {
			return ExemptibleLease{}, fmt.Errorf("%w: asset fair value", ErrExemptionIneligible)
		}
		if lowValueThreshold.Currency() != currency || !lowValueThreshold.Amount().IsPositive() {
			return ExemptibleLease{}, fmt.Errorf("%w: low-value threshold", ErrExemptionIneligible)
		}
		if assetFairValue.Amount().GreaterThan(lowValueThreshold.Amount()) {
			return ExemptibleLease{}, fmt.Errorf("%w: asset value above the threshold", ErrExemptionIneligible)
		}
		lease.AssetFairValue = assetFairValue
		lease.LowValueThreshold = lowValueThreshold
	}
	return lease, nil
}

// ExpenseSchedule returns the straight-line expense schedule without mutating
// the lease. The final period absorbs the expense rounding residual.
func (l ExemptibleLease) ExpenseSchedule() ([]ExemptionPeriod, error) {
	if l.TotalLeasePayments.Currency() == "" || len(l.Payments) == 0 {
		return nil, ErrInvalidExemption
	}
	count := int64(len(l.Payments))
	baseExpense := l.TotalLeasePayments.Amount().DivRound(decimal.NewFromInt(count), scale)
	recognized := mustZero(l.Currency)
	cumulativeAccrual := mustZero(l.Currency)
	result := make([]ExemptionPeriod, 0, len(l.Payments))
	for i, payment := range l.Payments {
		expenseAmount := baseExpense
		if int64(i) == count-1 {
			expenseAmount = l.TotalLeasePayments.Amount().Sub(recognized.Amount())
		}
		expense, err := types.NewMoney(expenseAmount, l.Currency)
		if err != nil {
			return nil, err
		}
		movement, err := expense.Subtract(payment.Amount)
		if err != nil {
			return nil, err
		}
		cumulativeAccrual, err = cumulativeAccrual.Add(movement)
		if err != nil {
			return nil, err
		}
		recognized, err = recognized.Add(expense)
		if err != nil {
			return nil, err
		}
		result = append(result, ExemptionPeriod{
			LeaseReference:    l.Reference,
			Period:            i + 1,
			DueDate:           payment.DueDate,
			CashPaid:          payment.Amount,
			LeaseExpense:      expense,
			AccrualMovement:   movement,
			CumulativeAccrual: cumulativeAccrual,
		})
	}
	return result, nil
}

// ExemptionAccounts identifies the general-ledger accounts used by exemption
// expense entries.
type ExemptionAccounts struct {
	Cash                uuid.UUID `json:"cash"`
	LeaseExpense        uuid.UUID `json:"lease_expense"`
	AccruedLeaseExpense uuid.UUID `json:"accrued_lease_expense"`
	PrepaidLease        uuid.UUID `json:"prepaid_lease"`
}

// ExemptionPostingCapability must be backed by the host application's configured
// ledger-posting capability and resolve the selected real journal mapping.
type ExemptionPostingCapability interface {
	LedgerPostingAvailable() bool
	ResolveExemptionAccounts(context.Context, uuid.UUID, string) (ExemptionAccounts, error)
}

// ExemptionEntryBuilder only creates drafts after the host confirms posting
// capability and resolves configured accounts for the requested mapping.
type ExemptionEntryBuilder struct {
	capability ExemptionPostingCapability
}

func NewExemptionEntryBuilder(capability ExemptionPostingCapability) (*ExemptionEntryBuilder, error) {
	if capability == nil || !capability.LedgerPostingAvailable() {
		return nil, ErrPostingCapabilityUnavailable
	}
	return &ExemptionEntryBuilder{capability: capability}, nil
}

func (b *ExemptionEntryBuilder) resolve(
	ctx context.Context, companyID uuid.UUID, mappingID string,
) (ExemptionAccounts, error) {
	if b == nil || b.capability == nil || !b.capability.LedgerPostingAvailable() {
		return ExemptionAccounts{}, ErrPostingCapabilityUnavailable
	}
	if strings.TrimSpace(mappingID) == "" {
		return ExemptionAccounts{}, ErrJournalMappingUnavailable
	}
	accounts, err := b.capability.ResolveExemptionAccounts(ctx, companyID, mappingID)
	if err != nil {
		return ExemptionAccounts{}, err
	}
	return accounts, nil
}

// BuildExpenseEntry creates a draft for one period's straight-line lease
// expense, cash payment, and accrued/prepaid movement.
func (b *ExemptionEntryBuilder) BuildExpenseEntry(
	ctx context.Context, period ExemptionPeriod, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildExemptionExpenseEntry(period, accounts, companyID, journalID, createdBy)
}

// BuildExemptionExpenseEntry creates a balanced draft for one period's straight-line lease expense.
func BuildExemptionExpenseEntry(
	period ExemptionPeriod, accounts ExemptionAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildExemptionExpenseEntry(period, accounts, companyID, journalID, createdBy)
}

func buildExemptionExpenseEntry(
	period ExemptionPeriod, accounts ExemptionAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(period.LeaseReference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
	if !period.LeaseExpense.Amount().IsPositive() || period.CashPaid.Currency() == "" ||
		accounts.LeaseExpense == uuid.Nil || accounts.Cash == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidExemptionAccnt
	}
	lines := []posting.JournalEntryLine{debitLine(accounts.LeaseExpense, period.LeaseExpense)}
	movement := period.AccrualMovement.Amount()
	switch {
	case movement.IsPositive():
		if accounts.AccruedLeaseExpense == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidExemptionAccnt
		}
		lines = append(lines, creditLine(accounts.AccruedLeaseExpense, period.AccrualMovement))
	case movement.IsNegative():
		if accounts.PrepaidLease == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidExemptionAccnt
		}
		prepaid, err := types.NewMoney(movement.Abs(), period.AccrualMovement.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, debitLine(accounts.PrepaidLease, prepaid))
	}
	if period.CashPaid.Amount().IsPositive() {
		lines = append(lines, creditLine(accounts.Cash, period.CashPaid))
	}
	entry, err := newEntry(period.DueDate, companyID, journalID, createdBy, lines)
	return identifyEntry(entry, period.LeaseReference,
		fmt.Sprintf("exemption-expense:%d:%s", period.Period, period.DueDate.Format("2006-01-02")), err)
}
