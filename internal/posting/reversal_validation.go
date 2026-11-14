package posting

import (
	"github.com/google/uuid"
)

func ValidateReversalLines(original, reversal JournalEntry) error {
	if original.CompanyID == uuid.Nil || original.CompanyID != reversal.CompanyID ||
		original.ID == uuid.Nil || original.ID == reversal.ID ||
		reversal.OriginalEntryID == nil || *reversal.OriginalEntryID != original.ID ||
		len(original.Lines) != len(reversal.Lines) {
		return ErrInvalidReversal
	}

	matched := make([]bool, len(reversal.Lines))
	for _, originalLine := range original.Lines {
		originalResolved, err := normalizeLineCurrencies(originalLine)
		if err != nil {
			return ErrInvalidReversal
		}
		found := false
		for i, reversalLine := range reversal.Lines {
			if matched[i] || !sameReversalLineDimensions(originalLine, reversalLine) {
				continue
			}
			reversalResolved, err := normalizeLineCurrencies(reversalLine)
			if err != nil {
				return ErrInvalidReversal
			}
			if originalResolved.TransactionAmount.Equal(reversalResolved.TransactionAmount) &&
				originalResolved.TransactionCurrency == reversalResolved.TransactionCurrency &&
				originalResolved.ExchangeRate.Equal(reversalResolved.ExchangeRate) &&
				reversalLine.ReconciledAmount.IsZero() &&
				originalResolved.FunctionalDebit.Equal(reversalResolved.FunctionalCredit) &&
				originalResolved.FunctionalCredit.Equal(reversalResolved.FunctionalDebit) {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
			return ErrInvalidReversal
		}
	}
	return nil
}

func sameReversalLineDimensions(original, reversal JournalEntryLine) bool {
	return original.CompanyID == reversal.CompanyID &&
		original.AccountID == reversal.AccountID &&
		equalUUIDPointer(original.PartnerID, reversal.PartnerID) &&
		equalUUIDPointer(original.CostCenterID, reversal.CostCenterID) &&
		equalUUIDPointer(original.ProjectID, reversal.ProjectID) &&
		equalUUIDPointer(original.SegmentID, reversal.SegmentID) &&
		equalUUIDPointer(original.BusinessUnitID, reversal.BusinessUnitID) &&
		original.MatchingNumber == reversal.MatchingNumber
}
