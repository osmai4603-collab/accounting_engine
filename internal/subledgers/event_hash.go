package subledgers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

var ErrIdempotencyPayloadConflict = errors.New("idempotency key was reused with a different payload")

type eventHashValue struct {
	Key      string `json:"key"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type eventHashAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type eventHashLine struct {
	AccountID           uuid.UUID `json:"account_id"`
	Side                string    `json:"side"`
	Amount              string    `json:"amount"`
	Currency            string    `json:"currency"`
	TransactionAmount   string    `json:"transaction_amount"`
	TransactionCurrency string    `json:"transaction_currency"`
	ExchangeRate        string    `json:"exchange_rate"`
}

type eventHashPayload struct {
	CompanyID       uuid.UUID            `json:"company_id"`
	SourceSystem    string               `json:"source_system,omitempty"`
	ExternalID      string               `json:"external_id,omitempty"`
	EventVersion    int                  `json:"event_version,omitempty"`
	PayloadJSON     []byte               `json:"payload_json,omitempty"`
	EventType       string               `json:"event_type"`
	Date            string               `json:"date"`
	Amount          string               `json:"amount"`
	Currency        string               `json:"currency"`
	Values          []eventHashValue     `json:"values"`
	Attributes      []eventHashAttribute `json:"attributes"`
	PartnerID       *uuid.UUID           `json:"partner_id,omitempty"`
	Reference       string               `json:"reference"`
	JournalID       uuid.UUID            `json:"journal_id"`
	AccountingLines []eventHashLine      `json:"accounting_lines"`
}

func EconomicEventPayloadHash(event EconomicEvent, entry posting.JournalEntry) (string, error) {
	if err := event.Validate(); err != nil {
		return "", err
	}
	if entry.CompanyID != event.CompanyID || entry.JournalID == uuid.Nil {
		return "", posting.ErrInvalidEntry
	}
	eventDate := event.Date.UTC().Format(time.RFC3339Nano)
	payload := eventHashPayload{
		CompanyID: event.CompanyID, SourceSystem: event.SourceSystem, ExternalID: event.ExternalID,
		EventVersion: event.EventVersion, PayloadJSON: event.PayloadJSON,
		EventType: event.EventType, Date: eventDate,
		Amount: event.Amount.Amount().String(), Currency: event.Amount.Currency(),
		PartnerID: event.PartnerID, Reference: event.Reference, JournalID: entry.JournalID,
		Values:          make([]eventHashValue, 0, len(event.Values)),
		Attributes:      make([]eventHashAttribute, 0, len(event.Attributes)),
		AccountingLines: make([]eventHashLine, 0, len(entry.Lines)),
	}
	for key, amount := range event.Values {
		payload.Values = append(payload.Values, eventHashValue{
			Key: key, Amount: amount.Amount().String(), Currency: amount.Currency(),
		})
	}
	sort.Slice(payload.Values, func(i, j int) bool { return payload.Values[i].Key < payload.Values[j].Key })
	for key, value := range event.Attributes {
		payload.Attributes = append(payload.Attributes, eventHashAttribute{Key: key, Value: value})
	}
	sort.Slice(payload.Attributes, func(i, j int) bool { return payload.Attributes[i].Key < payload.Attributes[j].Key })
	for _, line := range entry.Lines {
		resolved, err := posting.NormalizeLineCurrencies(line)
		if err != nil {
			return "", err
		}
		side, amount := "debit", resolved.FunctionalDebit
		if resolved.FunctionalCredit.Amount().IsPositive() {
			side, amount = "credit", resolved.FunctionalCredit
		}
		payload.AccountingLines = append(payload.AccountingLines, eventHashLine{
			AccountID: resolved.AccountID, Side: side, Amount: amount.Amount().String(),
			Currency: amount.Currency(), TransactionAmount: resolved.TransactionAmount.Amount().String(),
			TransactionCurrency: resolved.TransactionCurrency, ExchangeRate: resolved.ExchangeRate.String(),
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
