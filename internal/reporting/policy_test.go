package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
)

func TestReportingCurrencyDefaultsToEffectivePresentationCurrency(t *testing.T) {
	ctx := context.Background()
	companyID := uuid.New()
	unit := policy.NewUnit(memory.New())
	effective := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := unit.SaveCurrencyPolicy(ctx, policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: companyID, Version: 1, EffectiveFrom: effective,
		CreatedBy: "maker", ApprovedBy: "approver",
	}, policy.CurrencyPolicy{
		FunctionalCurrency: "SAR", PresentationCurrency: "USD",
		RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
		AllowedCurrencies: []string{"SAR", "USD", "EUR"}, EffectiveDate: effective,
	}); err != nil {
		t.Fatal(err)
	}
	engine := reporting.NewEngine(nil)
	engine.SetPolicyUnit(unit)
	got, err := engine.ResolvePresentationCurrency(ctx, companyID, effective.AddDate(0, 2, 0), "")
	if err != nil || got != "USD" {
		t.Fatalf("default currency = %q, error %v", got, err)
	}
	got, err = engine.ResolvePresentationCurrency(ctx, companyID, effective, "EUR")
	if err != nil || got != "EUR" {
		t.Fatalf("explicit currency = %q, error %v", got, err)
	}
	_, err = engine.ResolvePresentationCurrency(ctx, companyID, effective, "JPY")
	if !errors.Is(err, policy.ErrCurrencyNotAllowed) {
		t.Fatalf("unauthorized translation error = %v", err)
	}
}
