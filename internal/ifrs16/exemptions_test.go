package ifrs16

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestShortTermExemptionEligibilityAndExpense(t *testing.T) {
	lease, err := NewExemptibleLease(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 2, 1), Amount: money(t, "200", "USD")},
			{DueDate: date(2026, 4, 1), Amount: money(t, "400", "USD")},
		},
		ShortTermExemption, 3, false,
		money(t, "0", "USD"), money(t, "0", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	lease = lease.WithReference("short-term-1")

	assertAmount(t, lease.TotalLeasePayments, "600")

	schedule, err := lease.ExpenseSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 2 {
		t.Fatalf("schedule length = %d, want 2", len(schedule))
	}
	// Straight-line expense is 300 per period regardless of uneven cash.
	assertAmount(t, schedule[0].LeaseExpense, "300")
	assertAmount(t, schedule[1].LeaseExpense, "300")
	// Period 1: expense 300 exceeds cash 200, so lease expense accrues.
	assertAmount(t, schedule[0].AccrualMovement, "100")
	assertAmount(t, schedule[0].CumulativeAccrual, "100")
	// Period 2: cash 400 exceeds expense 300, so the accrual clears to zero.
	assertAmount(t, schedule[1].AccrualMovement, "-100")
	assertAmount(t, schedule[1].CumulativeAccrual, "0")

	builder := testExemptionEntryBuilder(t, testExemptionAccounts())
	companyID, journalID := uuid.New(), uuid.New()
	for _, period := range schedule {
		entry, err := builder.BuildExpenseEntry(context.Background(), period, companyID, journalID, "exempt-leases", "tester")
		if err != nil {
			t.Fatal(err)
		}
		assertBalanced(t, entry)
	}
}

func TestShortTermExemptionEvenPayments(t *testing.T) {
	payments := make([]LeasePayment, 0, 12)
	for month := 2; month <= 13; month++ {
		year, calMonth := 2026, month
		if month > 12 {
			year, calMonth = 2027, month-12
		}
		payments = append(payments, LeasePayment{
			DueDate: date(year, time.Month(calMonth), 1), Amount: money(t, "100", "USD"),
		})
	}
	lease, err := NewExemptibleLease(date(2026, 1, 1), payments, ShortTermExemption, 12, false,
		money(t, "0", "USD"), money(t, "0", "USD"))
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := lease.ExpenseSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 12 {
		t.Fatalf("schedule length = %d, want 12", len(schedule))
	}
	assertAmount(t, schedule[11].LeaseExpense, "100")
	assertAmount(t, schedule[11].CumulativeAccrual, "0")
}

func TestShortTermExemptionRejections(t *testing.T) {
	sixMonths := []LeasePayment{
		{DueDate: date(2026, 7, 1), Amount: money(t, "100", "USD")},
	}
	cases := []struct {
		name     string
		term     int
		purchase bool
		payments []LeasePayment
	}{
		{"term exceeds twelve months", 13, false, sixMonths},
		{"purchase option", 6, true, sixMonths},
		{"payment beyond the term", 6, false, []LeasePayment{
			{DueDate: date(2026, 12, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2027, 6, 1), Amount: money(t, "100", "USD")},
		}},
		{"unknown exemption type", 6, false, []LeasePayment{{DueDate: date(2026, 7, 1), Amount: money(t, "100", "USD")}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			exemption := ShortTermExemption
			if test.name == "unknown exemption type" {
				exemption = LeaseExemption("bogus")
			}
			if _, err := NewExemptibleLease(
				date(2026, 1, 1), test.payments, exemption, test.term, test.purchase,
				money(t, "0", "USD"), money(t, "0", "USD"),
			); err == nil {
				t.Fatal("expected exemption eligibility error")
			}
		})
	}
}

func TestLowValueExemptionThresholdAndExpense(t *testing.T) {
	lease, err := NewExemptibleLease(
		date(2026, 1, 1),
		[]LeasePayment{
			{DueDate: date(2026, 6, 1), Amount: money(t, "300", "USD")},
			{DueDate: date(2026, 9, 1), Amount: money(t, "500", "USD")},
		},
		LowValueExemption, 8, false,
		money(t, "3000", "USD"), money(t, "5000", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	lease = lease.WithReference("low-value-1")

	assertAmount(t, lease.AssetFairValue, "3000")
	assertAmount(t, lease.LowValueThreshold, "5000")

	schedule, err := lease.ExpenseSchedule()
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, schedule[0].LeaseExpense, "400")
	assertAmount(t, schedule[1].LeaseExpense, "400")
	assertAmount(t, schedule[1].CumulativeAccrual, "0")

	builder := testExemptionEntryBuilder(t, testExemptionAccounts())
	entry, err := builder.BuildExpenseEntry(
		context.Background(), schedule[0], uuid.New(), uuid.New(), "exempt-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
}

func TestLowValueExemptionRejections(t *testing.T) {
	payments := []LeasePayment{{DueDate: date(2026, 6, 1), Amount: money(t, "100", "USD")}}
	cases := []struct {
		name      string
		fairValue string
		threshold string
	}{
		{"asset above the threshold", "6000", "5000"},
		{"zero asset fair value", "0", "5000"},
		{"zero threshold", "1000", "0"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewExemptibleLease(
				date(2026, 1, 1), payments, LowValueExemption, 5, false,
				money(t, test.fairValue, "USD"), money(t, test.threshold, "USD"),
			); err == nil {
				t.Fatal("expected exemption eligibility error")
			}
		})
	}
}

func TestExemptionEntryRetryStability(t *testing.T) {
	lease, err := NewExemptibleLease(
		date(2026, 1, 1),
		[]LeasePayment{{DueDate: date(2026, 4, 1), Amount: money(t, "200", "USD")}},
		ShortTermExemption, 3, false,
		money(t, "0", "USD"), money(t, "0", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	lease = lease.WithReference("short-term-retry")
	schedule, err := lease.ExpenseSchedule()
	if err != nil {
		t.Fatal(err)
	}
	builder := testExemptionEntryBuilder(t, testExemptionAccounts())
	companyID, journalID := uuid.New(), uuid.New()
	ctx := context.Background()

	first, err := builder.BuildExpenseEntry(ctx, schedule[0], companyID, journalID, "exempt-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := builder.BuildExpenseEntry(ctx, schedule[0], companyID, journalID, "exempt-leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != retry.ID || first.IdempotencyKey != retry.IdempotencyKey ||
		first.IdempotencyPayloadHash != retry.IdempotencyPayloadHash {
		t.Fatal("retry did not reproduce the same exemption entry identity and payload")
	}
	if first.SourceModule != "ifrs16" || first.Reference != lease.Reference || first.DocumentReference == "" {
		t.Fatalf("exemption traceability fields are incomplete: %+v", first)
	}
}

type testExemptionPostingCapability struct {
	accounts  ExemptionAccounts
	available bool
}

func (c testExemptionPostingCapability) LedgerPostingAvailable() bool { return c.available }

func (c testExemptionPostingCapability) ResolveExemptionAccounts(
	_ context.Context, _ uuid.UUID, mappingID string,
) (ExemptionAccounts, error) {
	if mappingID != "exempt-leases" {
		return ExemptionAccounts{}, ErrJournalMappingUnavailable
	}
	return c.accounts, nil
}

func testExemptionEntryBuilder(t *testing.T, accounts ExemptionAccounts) *ExemptionEntryBuilder {
	t.Helper()
	builder, err := NewExemptionEntryBuilder(testExemptionPostingCapability{accounts: accounts, available: true})
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func testExemptionAccounts() ExemptionAccounts {
	return ExemptionAccounts{
		Cash: uuid.New(), LeaseExpense: uuid.New(),
		AccruedLeaseExpense: uuid.New(), PrepaidLease: uuid.New(),
	}
}
