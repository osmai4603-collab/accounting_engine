package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/shopspring/decimal"
)

var (
	ErrFinancialTraceabilityUnavailable = errors.New("financial traceability is not supported by this repository")
	ErrInvalidTraceScope                = errors.New("company and report scope are required for financial traceability")
	ErrTraceItemNotFound                = errors.New("financial statement item was not found")
)

type TracedMoveLine struct {
	MoveID       uuid.UUID
	LineID       uuid.UUID
	AccountID    uuid.UUID
	Debit        decimal.Decimal
	Credit       decimal.Decimal
	Currency     string
	EntryDate    time.Time
	JournalEntry posting.JournalEntry
}

type TracedEconomicEvent struct {
	EventID        uuid.UUID
	IdempotencyKey string
	EventType      string
	ExternalID     string
	Payload        []byte
}

type TraceResult struct {
	StatementItem   string
	StatementItems  []string
	AccountIDs      []uuid.UUID
	MoveLines       []TracedMoveLine
	JournalEntries  []posting.JournalEntry
	SourceEvents    []TracedEconomicEvent
	StatementAmount decimal.Decimal
	LedgerAmount    decimal.Decimal
	Reconciled      bool
	AuditChainProof string
}

type FinancialTraceabilityRepository interface {
	TraceFinancialStatementItem(
		context.Context,
		uuid.UUID,
		string,
		string,
		time.Time,
		time.Time,
		string,
	) (TraceResult, error)
	TraceEconomicEvent(context.Context, uuid.UUID, uuid.UUID) (TraceResult, error)
}

type FinancialTraceabilityUnit struct {
	repository FinancialTraceabilityRepository
	companyID  uuid.UUID
	start      time.Time
	end        time.Time
	currency   string
}

func NewFinancialTraceabilityUnit(
	repository FinancialTraceabilityRepository,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) *FinancialTraceabilityUnit {
	return &FinancialTraceabilityUnit{
		repository: repository, companyID: companyID,
		start: start, end: end, currency: strings.ToUpper(strings.TrimSpace(currency)),
	}
}

// TraceStatementItem retrieves the account lines, journal entries and source
// event payloads that compose a statement item for this unit's fixed scope.
func (u *FinancialTraceabilityUnit) TraceStatementItem(
	ctx context.Context,
	reportID string,
	lineItem string,
) (TraceResult, error) {
	if ctx == nil {
		return TraceResult{}, errors.New("financial traceability context is required")
	}
	if err := ctx.Err(); err != nil {
		return TraceResult{}, err
	}
	if u == nil || u.companyID == uuid.Nil || strings.TrimSpace(reportID) == "" {
		return TraceResult{}, ErrInvalidTraceScope
	}
	if u.repository == nil {
		return TraceResult{}, ErrFinancialTraceabilityUnavailable
	}
	if strings.TrimSpace(lineItem) == "" {
		return TraceResult{}, ErrTraceItemNotFound
	}
	result, err := u.repository.TraceFinancialStatementItem(
		ctx, u.companyID, reportID, lineItem, u.start, u.end, u.currency,
	)
	if err != nil {
		return TraceResult{}, fmt.Errorf("trace financial statement item %q: %w", lineItem, err)
	}
	if len(result.AccountIDs) == 0 {
		return TraceResult{}, ErrTraceItemNotFound
	}
	return result, nil
}

// TraceEconomicEvent follows a source event forward through its posted move
// and reports the statement classifications touched by its accounts.
func (u *FinancialTraceabilityUnit) TraceEconomicEvent(
	ctx context.Context,
	eventID uuid.UUID,
) (TraceResult, error) {
	if ctx == nil {
		return TraceResult{}, errors.New("financial traceability context is required")
	}
	if err := ctx.Err(); err != nil {
		return TraceResult{}, err
	}
	if u == nil || u.companyID == uuid.Nil || eventID == uuid.Nil {
		return TraceResult{}, ErrInvalidTraceScope
	}
	if u.repository == nil {
		return TraceResult{}, ErrFinancialTraceabilityUnavailable
	}
	result, err := u.repository.TraceEconomicEvent(ctx, u.companyID, eventID)
	if err != nil {
		return TraceResult{}, fmt.Errorf("trace economic event %s: %w", eventID, err)
	}
	return result, nil
}

// DecodeSourceEventPayload exposes a detached JSON value while retaining the
// original payload bytes in TraceResult for audit and document retrieval.
func (event TracedEconomicEvent) DecodeSourceEventPayload(target any) error {
	if len(event.Payload) == 0 {
		return errors.New("source event payload is empty")
	}
	if err := json.Unmarshal(event.Payload, target); err != nil {
		return fmt.Errorf("decode source event %s payload: %w", event.EventID, err)
	}
	return nil
}
