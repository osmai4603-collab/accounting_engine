package main

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

const (
	maxHTTPTextBytes        = 4096
	maxHTTPEventFieldBytes  = 256
	maxHTTPDimensionKeySize = 128
	maxHTTPDimensionValue   = 512
)

func validateHTTPEntry(entry ledger.JournalEntry) error {
	if uuid.UUID(entry.CompanyID) == uuid.Nil {
		return errors.New("company_id is required")
	}
	if len(entry.Lines) > maxEntryLines {
		return errors.New("entry contains too many lines")
	}
	if len(entry.Description) > maxHTTPTextBytes ||
		len(entry.ReferenceID) > maxHTTPEventFieldBytes ||
		len(entry.SourceModule) > maxHTTPEventFieldBytes ||
		len(entry.ReversalReason) > maxHTTPTextBytes {
		return errors.New("entry text field exceeds the maximum length")
	}
	if entry.TransactionCurrency != "" {
		if len(entry.TransactionCurrency) > 3 {
			return errors.New("invalid transaction currency")
		}
		if _, ok := types.LookupCurrency(entry.TransactionCurrency); !ok {
			return errors.New("invalid transaction currency")
		}
	}
	for _, line := range entry.Lines {
		if len(line.MatchingNumber) > maxHTTPEventFieldBytes {
			return errors.New("entry line field exceeds the maximum length")
		}
	}
	return nil
}

func validateHTTPEvent(event events.AccountingEvent) error {
	if uuid.UUID(event.CompanyID) == uuid.Nil {
		return errors.New("company_id is required")
	}
	if strings.TrimSpace(event.EventID) == "" || len(event.EventID) > maxHTTPEventFieldBytes ||
		strings.TrimSpace(event.EventType) == "" || len(event.EventType) > maxHTTPEventFieldBytes {
		return errors.New("event_id and event_type are required and must be within the maximum length")
	}
	if event.EffectiveDate.IsZero() {
		return errors.New("effective_date is required")
	}
	if len(event.SourceSystem) > maxHTTPEventFieldBytes || len(event.LedgerID) > maxHTTPEventFieldBytes ||
		len(event.IdempotencyKey) > maxHTTPEventFieldBytes {
		return errors.New("event text field exceeds the maximum length")
	}
	if len(event.Dimensions) > maxEventDimensions {
		return errors.New("event contains too many dimensions")
	}
	for key, value := range event.Dimensions {
		if len(key) == 0 || len(key) > maxHTTPDimensionKeySize || len(value) > maxHTTPDimensionValue {
			return errors.New("event dimension exceeds the maximum length")
		}
	}
	if len(event.Payload) > maxEventDimensions {
		return errors.New("event contains too many payload fields")
	}
	if event.JournalDraft != nil {
		if err := validateHTTPEntry(*event.JournalDraft); err != nil {
			return errors.New("journal_draft is invalid")
		}
	}
	return nil
}
