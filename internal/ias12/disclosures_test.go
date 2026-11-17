package ias12

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	internalreporting "github.com/osmai4603-collab/accounting_engine/internal/reporting"
	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

func testTaxReconciliationDisclosureInput(t *testing.T) TaxReconciliationDisclosureInput {
	t.Helper()
	period := testPeriod(Annual)
	treatment := uncertainTreatment(t)
	treatment.Outcomes = []TaxOutcome{
		{ID: "full-adjustment", TaxAmount: testMoney(t, "120", "USD"), Probability: decimal.RequireFromString("0.6"), SourceReference: "position-scenarios"},
		{ID: "limited-adjustment", TaxAmount: testMoney(t, "80", "USD"), Probability: decimal.RequireFromString("0.4"), SourceReference: "position-scenarios"},
	}
	uncertainMeasurement, err := MeasureUncertainTaxTreatment(treatment, period.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Reconcile(TaxReconciliationInput{
		Period: period, AccountingProfitBeforeTax: testMoney(t, "1000", "USD"),
		StatutoryRate: testRate("0.20"),
		PermanentDifferences: []PermanentDifference{
			{Description: "Non-deductible penalty", Amount: testMoney(t, "50", "USD")},
			{Description: "Exempt income", Amount: testMoney(t, "-20", "USD")},
		},
		CurrentTaxExpense: testMoney(t, "215", "USD"), DeferredTaxExpense: testMoney(t, "30", "USD"),
		UncertainTaxTreatments: []UncertainTaxMeasurement{uncertainMeasurement},
	})
	if err != nil {
		t.Fatal(err)
	}
	return TaxReconciliationDisclosureInput{
		CompanyID:   apitypes.CompanyID(uuid.New()),
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   period.AsOf, Report: report,
		SourceReferences: []publicreporting.DisclosureSource{
			{Kind: "tax-workpaper", Reference: "tax-reconciliation-2026"},
		},
		Narrative: "Prepared from approved tax workpapers.",
	}
}

func TestBuildTaxReconciliationDisclosure(t *testing.T) {
	input := testTaxReconciliationDisclosureInput(t)
	disclosure, err := BuildTaxReconciliationDisclosure(input)
	if err != nil {
		t.Fatal(err)
	}
	if disclosure.CompanyID != input.CompanyID ||
		disclosure.Code != TaxReconciliationDisclosureCode ||
		disclosure.StandardReference != TaxReconciliationStandardReference ||
		disclosure.Currency != "USD" || disclosure.Version != TaxReconciliationDisclosureVersion ||
		len(disclosure.Source) != 2 || disclosure.Narrative != input.Narrative {
		t.Fatalf("unexpected disclosure scope or metadata: %+v", disclosure)
	}
	for key, want := range map[string]string{
		"accounting_profit_before_tax":        "1000",
		"statutory_tax_rate":                  "0.2",
		"expected_tax_expense":                "200",
		"permanent_differences_tax_effect":    "6",
		"current_tax_expense":                 "215",
		"deferred_tax_expense":                "30",
		"uncertain_tax_treatments_adjustment": "20",
		"unallocated_reconciling_difference":  "-11",
		"effective_tax_expense":               "245",
		"effective_tax_rate":                  "0.245",
	} {
		if got, ok := disclosure.Data[key].(string); !ok || got != want {
			t.Errorf("%s = %v, want %s", key, disclosure.Data[key], want)
		}
	}
	if got, ok := disclosure.Data["effective_tax_rate_available"].(bool); !ok || !got {
		t.Fatalf("effective tax rate availability = %v, want true", disclosure.Data["effective_tax_rate_available"])
	}
	if got, ok := disclosure.Data["permanent_differences"].([]map[string]string); !ok || len(got) != 2 ||
		got[0]["description"] != "Non-deductible penalty" || got[1]["tax_effect"] != "-4" {
		t.Fatalf("permanent difference details = %#v", disclosure.Data["permanent_differences"])
	}
	if got, ok := disclosure.Data["uncertain_tax_treatments"].([]map[string]any); !ok ||
		len(got) != 1 || got[0]["treatment_id"] != "UT-2026-01" ||
		got[0]["recognized_tax_position"] != "120" {
		t.Fatalf("uncertain tax treatment details = %#v", disclosure.Data["uncertain_tax_treatments"])
	}
}

func TestTaxReconciliationDisclosureProviderScopesAndRegistry(t *testing.T) {
	input := testTaxReconciliationDisclosureInput(t)
	provider, err := NewTaxReconciliationDisclosureProvider(input)
	if err != nil {
		t.Fatal(err)
	}
	query := publicreporting.DisclosureQuery{
		CompanyID: input.CompanyID,
		Period:    publicreporting.DisclosurePeriod{StartDate: input.PeriodStart, EndDate: input.PeriodEnd},
		Currency:  "usd", StandardReference: TaxReconciliationStandardReference,
		Version: TaxReconciliationDisclosureVersion,
	}
	disclosures, err := provider.GetDisclosures(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(disclosures) != 1 || disclosures[0].CompanyID != input.CompanyID {
		t.Fatalf("scoped disclosures = %+v, want one matching record", disclosures)
	}

	otherCompanyQuery := query
	otherCompanyQuery.CompanyID = apitypes.CompanyID(uuid.New())
	otherCompanyDisclosures, err := provider.GetDisclosures(context.Background(), otherCompanyQuery)
	if err != nil || len(otherCompanyDisclosures) != 0 {
		t.Fatalf("cross-company disclosure result = %+v, err=%v", otherCompanyDisclosures, err)
	}
	wrongPeriodQuery := query
	wrongPeriodQuery.Period.EndDate = wrongPeriodQuery.Period.EndDate.AddDate(0, 0, -1)
	wrongPeriodDisclosures, err := provider.GetDisclosures(context.Background(), wrongPeriodQuery)
	if err != nil || len(wrongPeriodDisclosures) != 0 {
		t.Fatalf("cross-period disclosure result = %+v, err=%v", wrongPeriodDisclosures, err)
	}
	if _, err := provider.GetDisclosures(nil, query); !errors.Is(err, ErrInvalidTaxReconciliationDisclosure) {
		t.Fatalf("nil-context error = %v", err)
	}

	registry, err := internalreporting.NewDisclosureRegistry([]standards.DisclosureProvider{provider})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := registry.GetDisclosures(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].ContentHash == "" {
		t.Fatalf("registry did not normalize/hash the IAS 12 disclosure: %+v", registered)
	}
}

func TestBuildTaxReconciliationDisclosureRejectsInvalidScopeAndSources(t *testing.T) {
	input := testTaxReconciliationDisclosureInput(t)
	input.SourceReferences = nil
	if _, err := BuildTaxReconciliationDisclosure(input); !errors.Is(err, ErrInvalidTaxReconciliationDisclosure) {
		t.Fatalf("missing source error = %v", err)
	}
	input = testTaxReconciliationDisclosureInput(t)
	input.PeriodEnd = input.PeriodEnd.AddDate(0, 0, -1)
	if _, err := BuildTaxReconciliationDisclosure(input); !errors.Is(err, ErrInvalidTaxReconciliationDisclosure) {
		t.Fatalf("report-period mismatch error = %v", err)
	}
	input = testTaxReconciliationDisclosureInput(t)
	input.Report.UncertainTaxTreatments = append(
		input.Report.UncertainTaxTreatments,
		input.Report.UncertainTaxTreatments[0],
	)
	if _, err := BuildTaxReconciliationDisclosure(input); !errors.Is(err, ErrInvalidTaxReconciliationDisclosure) {
		t.Fatalf("duplicate uncertain tax treatment error = %v", err)
	}
}
