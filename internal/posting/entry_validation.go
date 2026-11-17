package posting

import (
	"fmt"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrMissingJournalID          = fmt.Errorf("%w: journal id is required", ErrInvalidEntry)
	ErrZeroEntryDate             = fmt.Errorf("%w: entry date cannot be zero", ErrInvalidEntry)
	ErrCompanyMismatch           = fmt.Errorf("%w: company id mismatch between header and lines", ErrInvalidEntry)
	ErrInvalidDimensionReference = fmt.Errorf("%w: dimension reference cannot be nil UUID", ErrInvalidEntry)
)

// ValidateISOCurrencyCode enforces ISO-4217 compliance: non-empty, exactly 3 uppercase ASCII letters,
// and registered in the currencies catalog.
func ValidateISOCurrencyCode(code string) error {
	if code == "" {
		return financialtypes.ErrInvalidCurrency
	}
	if len(code) != 3 {
		return financialtypes.ErrInvalidCurrency
	}
	for i := 0; i < 3; i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return financialtypes.ErrInvalidCurrency
		}
	}
	if _, ok := financialtypes.LookupCurrency(code); !ok {
		return financialtypes.ErrInvalidCurrency
	}
	return nil
}

// ValidateDimensionPointers ensures that any dimension pointer (PartnerID, CostCenterID,
// ProjectID, SegmentID, BusinessUnitID), if non-nil, does not point to uuid.Nil.
func ValidateDimensionPointers(l JournalEntryLine) error {
	for _, dim := range []*uuid.UUID{
		l.PartnerID,
		l.CostCenterID,
		l.ProjectID,
		l.SegmentID,
		l.BusinessUnitID,
	} {
		if dim != nil && *dim == uuid.Nil {
			return ErrInvalidDimensionReference
		}
	}
	return nil
}

// ValidateEntryWithRounding executes the comprehensive validation contract on a journal entry
// using the provided rounding function (or banker's rounding to minor units if nil).
func ValidateEntryWithRounding(e *JournalEntry, round func(financialtypes.Money) (financialtypes.Money, error)) error {
	if e == nil {
		return ErrInvalidEntry
	}
	if len(e.Lines) < 2 {
		return ErrMinimumLines
	}
	if e.SourceEventID != nil && *e.SourceEventID == uuid.Nil {
		return ErrInvalidEntry
	}
	if e.Classification != "" {
		switch e.Classification {
		case EntryClassRegular, EntryClassAdjusting, EntryClassClosing, EntryClassOpening, EntryClassReversing:
		default:
			return ErrInvalidEntry
		}
	}
	if e.AdjustingEntryType != "" {
		switch e.AdjustingEntryType {
		case AdjustingEntryAccrual, AdjustingEntryDeferral, AdjustingEntryProvisionEstimate, AdjustingEntryReclassification:
			if e.Classification != EntryClassAdjusting {
				return ErrInvalidAdjustingEntryMetadata
			}
		default:
			return ErrInvalidAdjustingEntryMetadata
		}
	}
	if e.Classification != EntryClassAdjusting &&
		(e.AdjustmentReason != "" || e.AdjustmentApprovalReference != "") {
		return ErrInvalidAdjustingEntryMetadata
	}

	totals := make(map[string]decimal.Decimal)
	hasDebit, hasCredit, layered := false, false, false
	for _, line := range e.Lines {
		if err := line.ValidateWithRounding(round); err != nil {
			return err
		}
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return err
		}
		if line.AccountID == uuid.Nil {
			return ErrInvalidEntry
		}
		if line.CompanyID != uuid.Nil {
			if e.CompanyID != uuid.Nil && line.CompanyID != e.CompanyID {
				return ErrCompanyMismatch
			}
			if e.CompanyID == uuid.Nil {
				return ErrCompanyMismatch
			}
		}
		hasDebit = hasDebit || resolved.FunctionalDebit.Amount().IsPositive()
		hasCredit = hasCredit || resolved.FunctionalCredit.Amount().IsPositive()
		currency := resolved.FunctionalDebit.Currency()
		totals[currency] = totals[currency].Add(
			resolved.FunctionalDebit.Amount().Sub(resolved.FunctionalCredit.Amount()),
		)
		layered = layered || hasExplicitCurrencyLayers(line)
	}
	if !hasDebit || !hasCredit {
		return ErrMinimumLines
	}
	if len(totals) != 1 {
		return financialtypes.ErrCurrencyMismatch
	}
	for _, total := range totals {
		if !total.IsZero() {
			if layered {
				return fmt.Errorf("%w: %w", ErrFunctionalBalanceMismatch, financialtypes.ErrUnbalancedMove)
			}
			return financialtypes.ErrUnbalancedMove
		}
	}

	if e.JournalID == uuid.Nil {
		return ErrMissingJournalID
	}
	if e.EntryDate.IsZero() && e.Date.IsZero() {
		return ErrZeroEntryDate
	}

	return nil
}
