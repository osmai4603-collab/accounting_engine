package ias12

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func testProfile() TaxProfile {
	return TaxProfile{
		ID: "profile-us-v1", CompanyID: uuid.New(), Jurisdiction: "US-TEST",
		Currency: "USD", Version: "v1",
		EffectiveFrom:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		SourceReference: "tax-profile-review-2025",
		Rates: []TaxRate{
			{Value: decimal.RequireFromString("0.25"), Enacted: true,
				EnactedOn:       time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
				EffectiveFrom:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				SourceReference: "law-2025"},
			{Value: decimal.RequireFromString("0.20"), Enacted: true,
				EnactedOn:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
				EffectiveFrom:   time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
				SourceReference: "law-2027"},
		},
	}
}

func TestMeasureWithProfileUsesEffectiveRatesAndBoundedRecognitionEvidence(t *testing.T) {
	profile := testProfile()
	probableEvidence := &DTARecognitionEvidence{
		Reference: "profit-forecast-v4", AssessedOn: testPeriod(Annual).AsOf, Probable: true,
		SupportedDeductibleAmount: testMoney(t, "50", "USD"),
	}
	measurement, err := MeasureWithProfile(profile, []TaxBalance{
		{
			ID: "asset-taxable", Kind: Asset,
			CarryingAmount: testMoney(t, "100", "USD"), TaxBase: testMoney(t, "70", "USD"),
			ExpectedReversalDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			CarryingAmountSource: "fixed-asset-register", TaxBaseSource: "tax-depreciation-schedule",
		},
		{
			ID: "asset-deductible", Kind: Asset,
			CarryingAmount: testMoney(t, "20", "USD"), TaxBase: testMoney(t, "100", "USD"),
			ExpectedReversalDate: time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC),
			CarryingAmountSource: "provision-ledger", TaxBaseSource: "tax-deduction-schedule",
			RecognitionEvidence: probableEvidence,
		},
	}, testPeriod(Annual))
	if err != nil {
		t.Fatal(err)
	}
	if measurement.TaxProfileID != profile.ID || measurement.Currency != "USD" ||
		measurement.CompanyID != profile.CompanyID || measurement.Jurisdiction != profile.Jurisdiction ||
		measurement.ProfileVersion != profile.Version ||
		measurement.ProfileSourceReference != profile.SourceReference {
		t.Fatalf("profile metadata = %+v", measurement)
	}
	if got := measurement.Balances[0].DTL.Amount().String(); got != "7.5" {
		t.Fatalf("2026 DTL = %s, want 7.5", got)
	}
	if got := measurement.Balances[1].DTA.Amount().String(); got != "10" {
		t.Fatalf("2027 DTA = %s, want 10 (50 supported at 20%%)", got)
	}
	if measurement.Balances[1].Rate.SourceReference != "law-2027" ||
		measurement.Balances[1].RecognitionEvidenceReference != probableEvidence.Reference ||
		measurement.Balances[1].TaxBaseSource != "tax-deduction-schedule" {
		t.Fatalf("rate or source provenance missing: %+v", measurement.Balances[1])
	}
}

func TestMeasureWithProfileRequiresDataAndDTAEvidence(t *testing.T) {
	profile := testProfile()
	deductible := TaxBalance{
		ID: "deductible", Kind: Asset,
		CarryingAmount: testMoney(t, "80", "USD"), TaxBase: testMoney(t, "100", "USD"),
		ExpectedReversalDate: testPeriod(Annual).AsOf,
		CarryingAmountSource: "ledger", TaxBaseSource: "tax-schedule",
	}
	if _, err := MeasureWithProfile(profile, []TaxBalance{deductible}, testPeriod(Annual)); !errors.Is(err, ErrMissingDTAEvidence) {
		t.Fatalf("missing evidence error = %v, want %v", err, ErrMissingDTAEvidence)
	}
	deductible.RecognitionEvidence = &DTARecognitionEvidence{
		Reference: "forecast", AssessedOn: testPeriod(Annual).AsOf.AddDate(0, 0, 1),
		Probable: true, SupportedDeductibleAmount: testMoney(t, "20", "USD"),
	}
	if _, err := MeasureWithProfile(profile, []TaxBalance{deductible}, testPeriod(Annual)); !errors.Is(err, ErrMissingDTAEvidence) {
		t.Fatalf("future-dated evidence error = %v, want %v", err, ErrMissingDTAEvidence)
	}
	deductible.RecognitionEvidence = &DTARecognitionEvidence{
		Reference: "forecast", AssessedOn: testPeriod(Annual).AsOf,
		Probable: false, SupportedDeductibleAmount: testMoney(t, "20", "USD"),
	}
	measurement, err := MeasureWithProfile(profile, []TaxBalance{deductible}, testPeriod(Annual))
	if err != nil {
		t.Fatal(err)
	}
	if !measurement.DTA.Amount().IsZero() {
		t.Fatalf("DTA recognized without probable evidence: %s", measurement.DTA)
	}
	deductible.TaxBaseSource = ""
	if _, err := MeasureWithProfile(profile, []TaxBalance{deductible}, testPeriod(Annual)); !errors.Is(err, ErrMissingTaxProvenance) {
		t.Fatalf("missing tax-base source error = %v, want %v", err, ErrMissingTaxProvenance)
	}
}

func TestRateForRequiresRateEffectiveByReversalDate(t *testing.T) {
	profile := testProfile()
	rate, err := profile.RateFor(testPeriod(Annual).AsOf, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rate.Value.String() != "0.25" {
		t.Fatalf("rate effective in 2026 = %s, want 0.25", rate.Value)
	}
	if _, err := profile.RateFor(testPeriod(Annual).AsOf, time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("unavailable rate error = %v, want %v", err, ErrRateUnavailable)
	}
}

func TestMeasureCurrentTaxUsesPeriodEffectiveRateAndSource(t *testing.T) {
	profile := testProfile()
	input := CurrentTaxInput{
		Period: testPeriod(Annual), TaxableProfit: testMoney(t, "1000", "USD"),
		SourceReference: "taxable-profit-workpaper",
	}
	measurement, err := MeasureCurrentTax(profile, input)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.TaxExpense.Amount().String() != "250" ||
		measurement.Rate.SourceReference != "law-2025" ||
		measurement.SourceReference != input.SourceReference ||
		measurement.ProfileVersion != profile.Version ||
		measurement.ProfileSourceReference != profile.SourceReference {
		t.Fatalf("unexpected current tax measurement: %+v", measurement)
	}
	input.SourceReference = ""
	if _, err := MeasureCurrentTax(profile, input); !errors.Is(err, ErrMissingTaxProvenance) {
		t.Fatalf("missing current-tax source error = %v, want %v", err, ErrMissingTaxProvenance)
	}
}

func TestConfiguredEntriesRequireMappingAndApplication(t *testing.T) {
	profile := testProfile()
	current, err := MeasureCurrentTax(profile, CurrentTaxInput{
		Period: testPeriod(Annual), TaxableProfit: testMoney(t, "100", "USD"),
		SourceReference: "tax-workpaper",
	})
	if err != nil {
		t.Fatal(err)
	}
	config := ConfiguredPosting{CompanyID: profile.CompanyID}
	if _, err := GenerateConfiguredCurrentTaxEntry(current, config, uuid.New(), "tax"); !errors.Is(err, ErrPostingNotConfigured) {
		t.Fatalf("unconfigured current-tax posting error = %v, want %v", err, ErrPostingNotConfigured)
	}
	config.JournalMappingReference = "mapping-2026"
	config.ApplicationConfigurationReference = "app-tax-posting-v1"
	config.CurrentExpenseAccountID, config.CurrentPayableAccountID = uuid.New(), uuid.New()
	wrongCompany := config
	wrongCompany.CompanyID = uuid.New()
	if _, err := GenerateConfiguredCurrentTaxEntry(current, wrongCompany, uuid.New(), "tax"); !errors.Is(err, ErrPostingNotConfigured) {
		t.Fatalf("wrong-company posting error = %v, want %v", err, ErrPostingNotConfigured)
	}
	entry, err := GenerateConfiguredCurrentTaxEntry(current, config, uuid.New(), "tax")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
	if entry.Reference == "" || !strings.Contains(entry.Reference, "mapping=mapping-2026") ||
		!strings.Contains(entry.Reference, "application=app-tax-posting-v1") ||
		!strings.Contains(entry.Reference, "source=tax-workpaper") {
		t.Fatalf("current-tax entry lost configuration/source trace: %q", entry.Reference)
	}

	deferred, err := MeasureWithProfile(profile, []TaxBalance{{
		ID: "fixed-asset", Kind: Asset,
		CarryingAmount: testMoney(t, "100", "USD"), TaxBase: testMoney(t, "70", "USD"),
		ExpectedReversalDate: time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC),
		CarryingAmountSource: "asset-register", TaxBaseSource: "tax-schedule",
	}}, testPeriod(Annual))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateConfiguredDeferredTaxEntry(deferred, DeferredTaxPosition{}, config, uuid.New(), "tax"); !errors.Is(err, ErrPostingNotConfigured) {
		t.Fatalf("unmapped deferred-tax posting error = %v, want %v", err, ErrPostingNotConfigured)
	}
	config.DeferredAccounts = DeferredTaxAccounts{
		Expense: uuid.New(), DTA: uuid.New(), DTL: uuid.New(),
	}
	entry, err = GenerateConfiguredDeferredTaxEntry(deferred, DeferredTaxPosition{}, config, uuid.New(), "tax")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
	if entry.Reference == "" || !strings.Contains(entry.Reference, "profile=profile-us-v1") ||
		!strings.Contains(entry.Reference, "application=app-tax-posting-v1") ||
		!strings.Contains(entry.Reference, "mapping=mapping-2026") {
		t.Fatalf("deferred-tax entry lost profile/mapping trace: %q", entry.Reference)
	}
}
