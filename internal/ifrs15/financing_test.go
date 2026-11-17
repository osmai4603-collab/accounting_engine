package ifrs15

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func TestFinancingComponent_PracticalExpedient(t *testing.T) {
	tTransfer := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tPaymentShort := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) // 243 days <= 365
	tPaymentLong := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)  // 730 days > 365

	if !IsPracticalExpedientApplicable(tTransfer, tPaymentShort) {
		t.Error("expected practical expedient to be applicable for 8-month period")
	}
	if IsPracticalExpedientApplicable(tTransfer, tPaymentLong) {
		t.Error("expected practical expedient NOT to be applicable for 2-year period")
	}

	nominal, _ := financialtypes.NewMoneyFromString("100000.00", "USD")
	fc, err := CalculateFinancingComponent(
		uuid.New(), nominal, decimal.RequireFromString("0.05"),
		tTransfer, tPaymentShort, EntityFinancing,
	)
	if err != nil {
		t.Fatalf("CalculateFinancingComponent failed: %v", err)
	}
	if !fc.PracticalExpedient {
		t.Error("expected PracticalExpedient to be true")
	}
	if !fc.TransactionPrice.Amount().Equal(nominal.Amount()) {
		t.Errorf("transaction price = %s, want nominal %s", fc.TransactionPrice.Amount(), nominal.Amount())
	}
}

func TestFinancingComponent_CustomerFinancing_AdvancePayment(t *testing.T) {
	// Scenario: Customer pays $100,000 upfront on 2026-01-01.
	// Goods are transferred 2 years later on 2028-01-01.
	// Annual discount rate = 6% (0.06).
	// Period 1: Interest = 100,000 * 0.06 = 6,000. Ending = 106,000.
	// Period 2: Interest = 106,000 * 0.06 = 6,360. Ending = 112,360.
	// Transaction price at delivery = $112,360.
	contractID := uuid.New()
	nominal, _ := financialtypes.NewMoneyFromString("100000.00", "USD")
	rate := decimal.RequireFromString("0.06")
	paymentDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	transferDate := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)

	fc, err := CalculateFinancingComponent(contractID, nominal, rate, transferDate, paymentDate, CustomerFinancing)
	if err != nil {
		t.Fatalf("CalculateFinancingComponent failed: %v", err)
	}

	if fc.PracticalExpedient {
		t.Fatal("expected practical expedient to be false for 2-year period")
	}
	wantFV := decimal.RequireFromString("112360.00")
	if !fc.TransactionPrice.Amount().Equal(wantFV) {
		t.Errorf("transaction price (FV) = %s, want %s", fc.TransactionPrice.Amount(), wantFV)
	}
	wantTotalInterest := decimal.RequireFromString("12360.00")
	if !fc.TotalInterest.Amount().Equal(wantTotalInterest) {
		t.Errorf("total interest = %s, want %s", fc.TotalInterest.Amount(), wantTotalInterest)
	}
	if len(fc.Schedule) != 2 {
		t.Fatalf("expected 2 periods, got %d", len(fc.Schedule))
	}

	// Verify period 1
	p1 := fc.Schedule[0]
	if !p1.BeginningAmount.Amount().Equal(decimal.RequireFromString("100000.00")) {
		t.Errorf("p1 beginning = %s, want 100000.00", p1.BeginningAmount.Amount())
	}
	if !p1.InterestAmount.Amount().Equal(decimal.RequireFromString("6000.00")) {
		t.Errorf("p1 interest = %s, want 6000.00", p1.InterestAmount.Amount())
	}
	if !p1.EndingAmount.Amount().Equal(decimal.RequireFromString("106000.00")) {
		t.Errorf("p1 ending = %s, want 106000.00", p1.EndingAmount.Amount())
	}

	// Test Journal Entry for Period 1
	journalID := uuid.New()
	interestExpenseAcct := uuid.New()
	contractLiabilityAcct := uuid.New()
	customerID := uuid.New()

	entry, err := BuildFinancingInterestJournal(fc, p1, journalID, p1.EndDate, interestExpenseAcct, contractLiabilityAcct, customerID)
	if err != nil {
		t.Fatalf("BuildFinancingInterestJournal failed: %v", err)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("expected 2 journal lines, got %d", len(entry.Lines))
	}
	// Debit Interest Expense, Credit Contract Liability
	if entry.Lines[0].AccountID != interestExpenseAcct || !entry.Lines[0].Debit.Amount().Equal(decimal.RequireFromString("6000.00")) {
		t.Errorf("expected debit to interest expense 6000.00, got %+v", entry.Lines[0])
	}
	if entry.Lines[1].AccountID != contractLiabilityAcct || !entry.Lines[1].Credit.Amount().Equal(decimal.RequireFromString("6000.00")) {
		t.Errorf("expected credit to contract liability 6000.00, got %+v", entry.Lines[1])
	}
}

func TestFinancingComponent_EntityFinancing_PaymentInArrears(t *testing.T) {
	// Scenario: Goods are delivered on 2026-01-01. Customer pays $121,000 on 2028-01-01 (2 years later).
	// Annual discount rate = 10% (0.10).
	// PV = $121,000 / (1.10)^2 = $100,000.
	// Revenue recognized at inception = $100,000 (cash selling price).
	// Total Interest income over 2 years = $21,000.
	contractID := uuid.New()
	nominal, _ := financialtypes.NewMoneyFromString("121000.00", "USD")
	rate := decimal.RequireFromString("0.10")
	transferDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	paymentDate := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)

	fc, err := CalculateFinancingComponent(contractID, nominal, rate, transferDate, paymentDate, EntityFinancing)
	if err != nil {
		t.Fatalf("CalculateFinancingComponent failed: %v", err)
	}

	wantPV := decimal.RequireFromString("100000.00")
	if !fc.TransactionPrice.Amount().Equal(wantPV) {
		t.Errorf("transaction price (PV) = %s, want %s", fc.TransactionPrice.Amount(), wantPV)
	}
	wantTotalInterest := decimal.RequireFromString("21000.00")
	if !fc.TotalInterest.Amount().Equal(wantTotalInterest) {
		t.Errorf("total interest = %s, want %s", fc.TotalInterest.Amount(), wantTotalInterest)
	}

	// Test Journal Entry for Period 1
	p1 := fc.Schedule[0]
	journalID := uuid.New()
	receivableAcct := uuid.New()
	interestIncomeAcct := uuid.New()
	customerID := uuid.New()

	entry, err := BuildFinancingInterestJournal(fc, p1, journalID, p1.EndDate, interestIncomeAcct, receivableAcct, customerID)
	if err != nil {
		t.Fatalf("BuildFinancingInterestJournal failed: %v", err)
	}
	// Debit Receivable, Credit Interest Income
	if entry.Lines[0].AccountID != receivableAcct || !entry.Lines[0].Debit.Amount().Equal(decimal.RequireFromString("10000.00")) {
		t.Errorf("expected debit to receivable 10000.00, got %+v", entry.Lines[0])
	}
	if entry.Lines[1].AccountID != interestIncomeAcct || !entry.Lines[1].Credit.Amount().Equal(decimal.RequireFromString("10000.00")) {
		t.Errorf("expected credit to interest income 10000.00, got %+v", entry.Lines[1])
	}
}

func TestFinancingComponent_ValidationErrors(t *testing.T) {
	nominal, _ := financialtypes.NewMoneyFromString("1000.00", "USD")
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)

	// Negative discount rate
	_, err := CalculateFinancingComponent(uuid.New(), nominal, decimal.RequireFromString("-0.05"), t1, t2, EntityFinancing)
	if !errors.Is(err, ErrInvalidDiscountRate) {
		t.Errorf("expected ErrInvalidDiscountRate, got %v", err)
	}

	// Inverted dates for CustomerFinancing
	_, err = CalculateFinancingComponent(uuid.New(), nominal, decimal.RequireFromString("0.05"), t1, t2, CustomerFinancing)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("expected ErrInvalidPeriod for inverted customer financing dates, got %v", err)
	}
}

