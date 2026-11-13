package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
)

type approvedEntryChecker bool

func (c approvedEntryChecker) IsApproved(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return bool(c), nil
}

func TestApprovalBackdatingExceptionAuthorizerUsesEntryApproval(t *testing.T) {
	companyID, entryID := uuid.New(), uuid.New()
	authorizer := policy.NewApprovalBackdatingExceptionAuthorizer(approvedEntryChecker(true))
	approved, err := authorizer.IsBackdatingExceptionApproved(context.Background(), companyID, entryID, "poster")
	if err != nil || !approved {
		t.Fatalf("approved entry = %v, error %v", approved, err)
	}
	denied := policy.NewApprovalBackdatingExceptionAuthorizer(approvedEntryChecker(false))
	approved, err = denied.IsBackdatingExceptionApproved(context.Background(), companyID, entryID, "poster")
	if err != nil || approved {
		t.Fatalf("unapproved entry = %v, error %v", approved, err)
	}
	var missing *policy.ApprovalBackdatingExceptionAuthorizer
	approved, err = missing.IsBackdatingExceptionApproved(context.Background(), companyID, entryID, "poster")
	if err != nil || approved {
		t.Fatalf("missing approval source = %v, error %v", approved, err)
	}
}

func TestCurrencyPolicyISO4217Rounding(t *testing.T) {
	p := policy.CurrencyPolicy{
		CompanyID: uuid.New(), FunctionalCurrency: "SAR", PresentationCurrency: "SAR",
		RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
		AllowedCurrencies: []string{"SAR", "JPY", "KWD"}, EffectiveDate: time.Now(),
	}
	for _, test := range []struct {
		currency string
		input    string
		expected string
	}{
		{"JPY", "12.5", "12"},
		{"KWD", "12.3456", "12.346"},
		{"SAR", "12.345", "12.34"},
	} {
		amount, err := financialtypes.NewMoneyFromString(test.input, test.currency)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Round(amount)
		if err != nil {
			t.Fatal(err)
		}
		if got.Amount().String() != test.expected {
			t.Errorf("%s rounding: got %s, want %s", test.currency, got.Amount(), test.expected)
		}
	}
	amount, _ := financialtypes.NewMoneyFromString("12.345", "SAR")
	if err := p.ValidateMoney(amount); !errors.Is(err, policy.ErrRoundingMismatch) {
		t.Fatalf("ValidateMoney error = %v, want rounding mismatch", err)
	}
	amount, _ = financialtypes.NewMoneyFromString("1.00", "USD")
	if err := p.ValidateMoney(amount); !errors.Is(err, policy.ErrCurrencyNotAllowed) {
		t.Fatalf("ValidateMoney error = %v, want currency not allowed", err)
	}
}

func TestVersionedCurrencyPolicyAsOf(t *testing.T) {
	ctx := context.Background()
	repository := memory.New()
	unit := policy.NewUnit(repository)
	companyID, policyID := uuid.New(), uuid.New()
	for version, year := range []int{2024, 2025} {
		date := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		header := policy.EffectivePolicyHeader{
			PolicyID: policyID, CompanyID: companyID, Version: version + 1,
			EffectiveFrom: date, CreatedBy: "maker", ApprovedBy: "approver",
		}
		value := policy.CurrencyPolicy{
			FunctionalCurrency: "SAR", PresentationCurrency: []string{"USD", "EUR"}[version],
			RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
			AllowedCurrencies: []string{"SAR", "USD", "EUR"}, EffectiveDate: date,
		}
		if err := unit.SaveCurrencyPolicy(ctx, header, value); err != nil {
			t.Fatal(err)
		}
	}
	old, err := unit.GetCurrencyPolicyAsOf(ctx, companyID, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if old.PresentationCurrency != "USD" {
		t.Fatalf("historical presentation currency = %s, want USD", old.PresentationCurrency)
	}
	current, err := unit.GetCurrencyPolicyAsOf(ctx, companyID, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if current.PresentationCurrency != "EUR" {
		t.Fatalf("current presentation currency = %s, want EUR", current.PresentationCurrency)
	}
	amount, _ := financialtypes.NewMoneyFromString("12.345", "SAR")
	round, err := (policy.CurrencyPolicy{RoundingMethod: policy.RoundingHalfUp}).Round(amount)
	if err != nil || round.Amount().String() != "12.35" {
		t.Fatalf("half-up result = %v, error %v", round.Amount(), err)
	}
}
