package posting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

// ErrEntryHashUnavailable is returned when an entry cannot be reduced to a
// canonical approval fingerprint, typically because one of its lines is not a
// valid currency-layered line.
var ErrEntryHashUnavailable = errors.New("journal entry cannot be hashed")

// entryHashLine is the canonical, currency-normalized representation of a
// journal line used for approval fingerprints. Mutable posting metadata
// (status, sequence, posted-by/at) is deliberately excluded so that an entry
// keeps the same fingerprint between draft approval and posting.
type entryHashLine struct {
	ID                  uuid.UUID  `json:"id"`
	AccountID           uuid.UUID  `json:"account_id"`
	PartnerID           *uuid.UUID `json:"partner_id,omitempty"`
	CostCenterID        *uuid.UUID `json:"cost_center_id,omitempty"`
	ProjectID           *uuid.UUID `json:"project_id,omitempty"`
	SegmentID           *uuid.UUID `json:"segment_id,omitempty"`
	BusinessUnitID      *uuid.UUID `json:"business_unit_id,omitempty"`
	MatchingNumber      string     `json:"matching_number"`
	ReconciledAmount    string     `json:"reconciled_amount"`
	TransactionAmount   string     `json:"transaction_amount"`
	TransactionCurrency string     `json:"transaction_currency"`
	ExchangeRate        string     `json:"exchange_rate"`
	FunctionalDebit     string     `json:"functional_debit"`
	FunctionalCredit    string     `json:"functional_credit"`
	FunctionalCurrency  string     `json:"functional_currency"`
}

type entryHashPayload struct {
	CompanyID            uuid.UUID       `json:"company_id"`
	ID                   uuid.UUID       `json:"id"`
	JournalID            uuid.UUID       `json:"journal_id"`
	PeriodID             uuid.UUID       `json:"period_id"`
	EntryDate            string          `json:"entry_date"`
	DocumentReference    string          `json:"document_reference"`
	Reference            string          `json:"reference"`
	CreatedBy            string          `json:"created_by"`
	Classification       string          `json:"classification"`
	TargetPeriodNumber   int             `json:"target_period_number"`
	AdjustmentAuthorized bool            `json:"adjustment_authorized"`
	OriginalEntryID      *uuid.UUID      `json:"original_entry_id,omitempty"`
	Lines                []entryHashLine `json:"lines"`
}

// EntryContentHash returns a stable SHA-256 fingerprint of the accounting
// content of an entry (its identity, dates, references and currency-layered
// lines). It intentionally ignores posting state so the hash computed while a
// draft is approved matches the hash computed immediately before posting.
func EntryContentHash(entry JournalEntry) (string, error) {
	entryDate := entry.EntryDate
	if entryDate.IsZero() {
		entryDate = entry.Date
	}
	if entryDate.IsZero() || entry.CompanyID == uuid.Nil || entry.ID == uuid.Nil || entry.JournalID == uuid.Nil {
		return "", ErrEntryHashUnavailable
	}
	classification := entry.Classification
	if classification == "" {
		classification = EntryClassRegular
	}
	payload := entryHashPayload{
		CompanyID:            entry.CompanyID,
		ID:                   entry.ID,
		JournalID:            entry.JournalID,
		PeriodID:             entry.PeriodID,
		EntryDate:            entryDate.UTC().Format(time.RFC3339Nano),
		DocumentReference:    entry.DocumentReference,
		Reference:            entry.Reference,
		CreatedBy:            entry.CreatedBy,
		Classification:       string(classification),
		TargetPeriodNumber:   entry.TargetPeriodNumber,
		AdjustmentAuthorized: entry.AdjustmentAuthorized,
		OriginalEntryID:      entry.OriginalEntryID,
		Lines:                make([]entryHashLine, len(entry.Lines)),
	}
	for i, line := range entry.Lines {
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return "", errors.Join(ErrEntryHashUnavailable, err)
		}
		payload.Lines[i] = entryHashLine{
			ID:                  resolved.ID,
			AccountID:           resolved.AccountID,
			PartnerID:           resolved.PartnerID,
			CostCenterID:        resolved.CostCenterID,
			ProjectID:           resolved.ProjectID,
			SegmentID:           resolved.SegmentID,
			BusinessUnitID:      resolved.BusinessUnitID,
			MatchingNumber:      resolved.MatchingNumber,
			ReconciledAmount:    resolved.ReconciledAmount.String(),
			TransactionAmount:   resolved.TransactionAmount.Amount().String(),
			TransactionCurrency: resolved.TransactionCurrency,
			ExchangeRate:        resolved.ExchangeRate.String(),
			FunctionalDebit:     resolved.FunctionalDebit.Amount().String(),
			FunctionalCredit:    resolved.FunctionalCredit.Amount().String(),
			FunctionalCurrency:  resolved.FunctionalDebit.Currency(),
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Join(ErrEntryHashUnavailable, err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// EntryFunctionalTotal returns the balanced functional amount of an entry in
// the company functional currency. It is the amount used by approval and
// maker-checker policies; transaction-currency legs are never used for those
// thresholds.
func EntryFunctionalTotal(entry JournalEntry) (financialtypes.Money, error) {
	if err := entry.Validate(); err != nil {
		return financialtypes.Money{}, err
	}
	var debitTotal, creditTotal financialtypes.Money
	currency := ""
	for i, line := range entry.Lines {
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return financialtypes.Money{}, err
		}
		lineCurrency := resolved.FunctionalDebit.Currency()
		if lineCurrency == "" {
			lineCurrency = resolved.FunctionalCredit.Currency()
		}
		if currency == "" {
			currency = lineCurrency
			debitTotal, err = financialtypes.Zero(currency)
			if err != nil {
				return financialtypes.Money{}, err
			}
			creditTotal, err = financialtypes.Zero(currency)
			if err != nil {
				return financialtypes.Money{}, err
			}
		}
		if lineCurrency != currency {
			return financialtypes.Money{}, financialtypes.ErrCurrencyMismatch
		}
		debitTotal, err = debitTotal.Add(resolved.FunctionalDebit)
		if err != nil {
			return financialtypes.Money{}, err
		}
		creditTotal, err = creditTotal.Add(resolved.FunctionalCredit)
		if err != nil {
			return financialtypes.Money{}, err
		}
		if i == len(entry.Lines)-1 && !debitTotal.Amount().Equal(creditTotal.Amount()) {
			return financialtypes.Money{}, financialtypes.ErrUnbalancedMove
		}
	}
	if currency == "" || !debitTotal.Amount().IsPositive() {
		return financialtypes.Money{}, ErrInvalidEntry
	}
	return debitTotal, nil
}
