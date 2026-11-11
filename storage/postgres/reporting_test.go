package postgres_test

import (
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func TestBalanceIntegrityDetectsAuditsAndRebuildsMaterializedBalances(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	amount, err := financialtypes.NewMoneyFromString("37.25", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(env.JournalID, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "maker", []posting.JournalEntryLine{
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID, entry.PeriodID = env.CompanyID, env.PeriodID
	if err := env.Driver.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status, entry.PostedBy = posting.EntryPosted, "checker"
	if _, err := env.Driver.PostAndSequence(ctx, entry); err != nil {
		t.Fatal(err)
	}

	if _, err := env.Driver.Pool().Exec(ctx, `UPDATE account_balances
		SET period_debit=period_debit+0.01
		WHERE company_id=$1 AND fiscal_period_id=$2 AND account_id=$3 AND currency_id='USD'`,
		env.CompanyID, env.PeriodID, env.DebitAccID); err != nil {
		t.Fatal(err)
	}
	unit := reporting.NewBalanceIntegrityUnit(env.Driver)
	detail, err := unit.Reconcile(ctx, env.CompanyID, env.PeriodID, "USD")
	if !errors.Is(err, reporting.ErrBalanceDriftDetected) || detail.ZeroDrift || len(detail.Drifts) != 1 {
		t.Fatalf("drift report = %+v, error %v", detail, err)
	}
	var criticalAlerts int
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM financial_audit_logs
			WHERE company_id=$1 AND entity_type='account_balance_integrity_alert'
			  AND action='critical_balance_drift_detected' AND entity_id=$2`,
			env.CompanyID, env.PeriodID).Scan(&criticalAlerts)
	}); err != nil {
		t.Fatal(err)
	}
	if criticalAlerts != 1 {
		t.Fatalf("critical drift audit alerts = %d, want 1", criticalAlerts)
	}
	if err := unit.RebuildBalancesFromLines(ctx, env.CompanyID, env.PeriodID, "USD"); err != nil {
		t.Fatalf("RebuildBalancesFromLines() error = %v", err)
	}
	detail, err = unit.Reconcile(ctx, env.CompanyID, env.PeriodID, "USD")
	if err != nil || !detail.ZeroDrift || detail.DriftedAccounts() != 0 {
		t.Fatalf("rebuilt balances report = %+v, error %v", detail, err)
	}
}

func TestFinancialTraceabilityPreservesAndFollowsSourceEvent(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	amount, err := financialtypes.NewMoneyFromString("12.34", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(env.JournalID, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), "maker", []posting.JournalEntryLine{
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID, entry.PeriodID = env.CompanyID, env.PeriodID
	event := subledgers.EconomicEvent{
		EventID: uuid.New(), CompanyID: env.CompanyID, SourceSystem: "test", ExternalID: "INV-TRACE-12",
		IdempotencyKey: "trace-" + uuid.NewString(), EventType: "invoice_issued",
		Date: entry.EntryDate, Amount: amount, PayloadJSON: []byte("{ \"document\" : \"invoice-12\" }"),
		Reference: "INV-TRACE-12", ResultEntryID: entry.ID,
	}
	entry.SourceEventID = &event.EventID
	expectedPayload := append([]byte(nil), event.PayloadJSON...)
	if err := env.Driver.SaveEventAndDraft(ctx, event, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status, entry.PostedBy = posting.EntryPosted, "checker"
	if _, err := env.Driver.PostAndSequence(ctx, entry); err != nil {
		t.Fatal(err)
	}

	var eventID uuid.UUID
	if err := env.Driver.Pool().QueryRow(ctx, `SELECT event_id FROM subledger_events
		WHERE company_id=$1 AND idempotency_key=$2`, env.CompanyID, event.IdempotencyKey).
		Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if eventID == uuid.Nil {
		t.Fatal("stored event ID is nil")
	}
	var persistedPayload []byte
	if err := env.Driver.Pool().QueryRow(ctx, `SELECT payload_bytes FROM subledger_events
		WHERE company_id=$1 AND event_id=$2`, env.CompanyID, eventID).Scan(&persistedPayload); err != nil {
		t.Fatal(err)
	}
	if string(persistedPayload) != string(expectedPayload) {
		t.Fatalf("stored payload bytes = %q, want exact %q", persistedPayload, expectedPayload)
	}
	var sourceEventID uuid.UUID
	if err := env.Driver.Pool().QueryRow(ctx, `SELECT source_event_id FROM account_moves
		WHERE company_id=$1 AND id=$2`, env.CompanyID, entry.ID).Scan(&sourceEventID); err != nil {
		t.Fatal(err)
	}
	if sourceEventID != eventID {
		t.Fatalf("move source_event_id = %s, want event ID %s", sourceEventID, eventID)
	}

	unit := reporting.NewFinancialTraceabilityUnit(env.Driver, env.CompanyID, time.Time{},
		time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), "USD")
	trace, err := unit.TraceStatementItem(ctx, "BALANCE_SHEET", "1000")
	if err != nil {
		t.Fatalf("TraceStatementItem() error = %v", err)
	}
	if !trace.Reconciled || !trace.StatementAmount.Equal(amount.Amount()) ||
		len(trace.MoveLines) != 1 || len(trace.JournalEntries) != 1 ||
		len(trace.SourceEvents) != 1 || trace.SourceEvents[0].EventID != eventID ||
		trace.SourceEvents[0].ExternalID != "INV-TRACE-12" ||
		string(trace.SourceEvents[0].Payload) != string(expectedPayload) {
		t.Fatalf("trace result = %+v", trace)
	}
	statementTrace, err := unit.TraceStatementItem(ctx, "BALANCE_SHEET", "TOTAL_ASSETS")
	if err != nil {
		t.Fatalf("TraceStatementItem(TOTAL_ASSETS) error = %v", err)
	}
	if !statementTrace.Reconciled ||
		!statementTrace.StatementAmount.Equal(amount.Amount()) ||
		!statementTrace.LedgerAmount.Equal(amount.Amount()) {
		t.Fatalf("balance sheet statement trace = %+v", statementTrace)
	}
	reverseTrace, err := unit.TraceEconomicEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("TraceEconomicEvent() error = %v", err)
	}
	if len(reverseTrace.MoveLines) != 2 || len(reverseTrace.JournalEntries) != 1 ||
		len(reverseTrace.SourceEvents) != 1 || reverseTrace.SourceEvents[0].EventID != eventID ||
		!containsString(reverseTrace.StatementItems, "TOTAL_ASSETS") ||
		!containsString(reverseTrace.StatementItems, "REVENUE") {
		t.Fatalf("reverse trace result = %+v", reverseTrace)
	}
}

func containsString(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func TestFinancialStatementNotesAreCompanyScopedAndUpserted(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	unit := reporting.NewNoteDisclosureUnit(env.Driver)
	statementDate := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	note := reporting.NoteDisclosure{
		ReportCode: "COMPARATIVE_FINANCIAL_STATEMENTS", StatementDate: statementDate,
		Code: "N1", Title: "Cash and cash equivalents", Description: "Bank balances",
		Amount: decimal.RequireFromString("125.1250"), Currency: "USD",
	}
	if err := unit.Save(ctx, env.CompanyID, note); err != nil {
		t.Fatalf("Save note: %v", err)
	}
	note.Amount = decimal.RequireFromString("126.1250")
	note.Description = "Updated disclosure"
	if err := unit.Save(ctx, env.CompanyID, note); err != nil {
		t.Fatalf("upsert note: %v", err)
	}
	notes, err := unit.List(ctx, env.CompanyID, note.ReportCode, statementDate)
	if err != nil {
		t.Fatalf("List notes: %v", err)
	}
	if len(notes) != 1 || !notes[0].Amount.Equal(note.Amount) || notes[0].Description != note.Description {
		t.Fatalf("stored note disclosures = %+v, want one updated note", notes)
	}
}
