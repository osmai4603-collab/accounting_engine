package legacyengine

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
)

// TestEngineObservabilityInstrumentsPosting proves that the engine wiring feeds
// real posting activity into the Prometheus registry and emits a structured,
// context-enriched JSON log record.
func TestEngineObservabilityInstrumentsPosting(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	var logs bytes.Buffer
	metrics := observability.NewMetrics()
	engine, err := New(driver, Config{Observability: &ObservabilityConfig{
		Logger:  observability.NewLogger(&logs, nil),
		Metrics: metrics,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if engine.Metrics() != metrics {
		t.Fatal("engine metrics registry is not the configured registry")
	}

	companyID, journalID := uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "OBS-CO", LegalName: "Observability Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	cashID, revenueID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	amount, err := financialtypes.NewMoneyFromString("125.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entry, err := posting.NewJournalEntry(journalID, time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		"maker", []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = year.Periods[0].ID
	if err := engine.Posting.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Posting.Post(companyID, entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}

	if got := metrics.PostedEntries(); got != 1 {
		t.Fatalf("posted entries metric = %d, want 1", got)
	}
	if got := metrics.PostingDurationCount(); got != 1 {
		t.Fatalf("posting duration observations = %d, want 1", got)
	}

	logOutput := logs.String()
	for _, want := range []string{
		`"msg":"journal entry posted"`,
		`"company_id":"` + companyID.String() + `"`,
		`"entry_id":"` + entry.ID.String() + `"`,
		`"trace_id":"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("structured log output missing %s:\n%s", want, logOutput)
		}
	}

	// A rejected re-post must increment the failure counter exposed by the
	// Prometheus exporter, without incrementing the success counter.
	if err := engine.Posting.Post(companyID, entry.ID, "checker"); err == nil {
		t.Fatal("expected the second posting attempt to be rejected")
	}
	if got := metrics.PostedEntries(); got != 1 {
		t.Fatalf("posted entries metric after rejection = %d, want 1", got)
	}
	var exposition bytes.Buffer
	if err := metrics.WritePrometheus(&exposition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exposition.String(), "accounting_posting_failures_total 1") {
		t.Fatalf("metrics exposition missing posting failure counter:\n%s", exposition.String())
	}

	// The HTTP /metrics handler serves the same registry.
	recorder := httptest.NewRecorder()
	engine.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("/metrics content type = %q, want Prometheus text format", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "accounting_posted_entries_total 1") {
		t.Fatalf("/metrics body missing posted entries counter:\n%s", recorder.Body.String())
	}
}

// TestEngineAlwaysExposesMetricsRegistry proves that a default metrics registry
// is created even when the deployment does not configure observability.
func TestEngineAlwaysExposesMetricsRegistry(t *testing.T) {
	engine, err := New(memory.New(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if engine.Metrics() == nil {
		t.Fatal("engine metrics registry is nil")
	}
	recorder := httptest.NewRecorder()
	engine.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
}
