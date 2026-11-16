package ifrs16

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func TestLeaseContractPVAndSchedule(t *testing.T) {
	contract := testContract(t)
	assertAmount(t, contract.InitialLeaseLiability, "197.2269")
	assertAmount(t, contract.InitialROUAsset, "222.2269")

	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 2 {
		t.Fatalf("schedule length = %d, want 2", len(schedule))
	}
	assertAmount(t, schedule[0].InterestExpense, "1.9075")
	assertAmount(t, schedule[0].PrincipalReduction, "98.0925")
	assertAmount(t, schedule[0].ClosingLiability, "99.1344")
	assertAmount(t, schedule[1].InterestExpense, "0.8656")
	assertAmount(t, schedule[1].ClosingLiability, "0")
	assertAmount(t, schedule[0].ROUDepreciation, "111.1135")
	assertAmount(t, schedule[1].AccumulatedDepreciation, "222.2269")
	assertAmount(t, schedule[1].ClosingROUAsset, "0")
}

func TestIrregularPeriodLeaseDiscounting(t *testing.T) {
	contract, err := NewLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.12"),
		[]LeasePayment{
			{DueDate: date(2026, 2, 15), Amount: money(t, "100", "USD")},
			{DueDate: date(2026, 3, 31), Amount: money(t, "100", "USD")},
		},
		money(t, "0", "USD"), money(t, "0", "USD"), money(t, "0", "USD"), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, contract.InitialLeaseLiability, "195.8870")

	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, schedule[0].InterestExpense, "2.7561")
	assertAmount(t, schedule[1].InterestExpense, "1.3569")
	assertAmount(t, schedule[1].ClosingLiability, "0")
}

func TestLeaseJournalEntriesBalance(t *testing.T) {
	contract := testContract(t)
	accounts := testAccounts()
	builder := testEntryBuilder(t, accounts)
	companyID, journalID := uuid.New(), uuid.New()
	commencement, err := builder.BuildCommencementEntry(context.Background(), contract, companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, commencement)

	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	payment, err := builder.BuildPaymentEntry(context.Background(), schedule[0], companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, payment)
	depreciation, err := builder.BuildDepreciationEntry(context.Background(), schedule[0], companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, depreciation)
}

func TestEntryBuilderRequiresCapabilityAndMappingAndIsRetryStable(t *testing.T) {
	accounts := testAccounts()
	if _, err := NewEntryBuilder(testLeasePostingCapability{accounts: accounts}); err != ErrPostingCapabilityUnavailable {
		t.Fatalf("NewEntryBuilder without posting capability error = %v", err)
	}
	builder := testEntryBuilder(t, accounts)
	contract := testContract(t)
	companyID, journalID := uuid.New(), uuid.New()
	if _, err := builder.BuildCommencementEntry(context.Background(), contract, companyID, journalID, "", "tester"); err != ErrJournalMappingUnavailable {
		t.Fatalf("BuildCommencementEntry without mapping error = %v", err)
	}

	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	first, err := builder.BuildPaymentEntry(context.Background(), schedule[0], companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := builder.BuildPaymentEntry(context.Background(), schedule[0], companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != retry.ID || first.IdempotencyKey != retry.IdempotencyKey ||
		first.IdempotencyPayloadHash != retry.IdempotencyPayloadHash {
		t.Fatal("retry did not reproduce the same entry identity and payload")
	}
	if first.SourceModule != "ifrs16" || first.Reference != contract.Reference || first.DocumentReference == "" {
		t.Fatalf("entry traceability fields are incomplete: %+v", first)
	}
	remapped, err := testEntryBuilder(t, testAccounts()).BuildPaymentEntry(
		context.Background(), schedule[0], companyID, journalID, "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if remapped.IdempotencyKey != first.IdempotencyKey ||
		remapped.IdempotencyPayloadHash == first.IdempotencyPayloadHash {
		t.Fatal("remapping must retain operation identity and change the payload hash")
	}
}

func TestPaymentEntryHandlesNegativePrincipal(t *testing.T) {
	contract, err := NewLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.12"),
		[]LeasePayment{
			{DueDate: date(2026, 2, 1), Amount: money(t, "0.01", "USD")},
			{DueDate: date(2026, 3, 1), Amount: money(t, "100", "USD")},
		},
		money(t, "0", "USD"), money(t, "0", "USD"), money(t, "0", "USD"), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("negative-principal")
	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if !schedule[0].PrincipalReduction.Amount().IsNegative() {
		t.Fatal("expected first payment to increase the liability")
	}
	entry, err := testEntryBuilder(t, testAccounts()).BuildPaymentEntry(context.Background(), schedule[0], uuid.New(), uuid.New(), "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
}

func TestModificationRecalculationAndEntry(t *testing.T) {
	contract := testContract(t)
	result, err := contract.RecalculateModification(
		date(2026, 2, 1),
		[]LeasePayment{{DueDate: date(2026, 3, 1), Amount: money(t, "60", "USD")}},
		decimal.Zero,
		money(t, "100", "USD"),
		money(t, "20", "USD"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, result.RemeasuredLiability, "60")
	assertAmount(t, result.NewROUAsset, "0")
	assertAmount(t, result.ModificationGain, "20")
	entry, err := testEntryBuilder(t, testAccounts()).BuildModificationEntry(context.Background(), result, uuid.New(), uuid.New(), "leases", "tester")
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
}

func TestEarlyTerminationGainAndLossEntries(t *testing.T) {
	contract := testContract(t)
	accounts := testAccounts()
	for _, test := range []struct {
		name, liability, rou, payment, gainLoss string
	}{
		{"gain", "100", "80", "10", "10"},
		{"loss", "50", "80", "10", "-40"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := contract.EarlyTerminate(
				date(2026, 2, 1), money(t, test.liability, "USD"),
				money(t, test.rou, "USD"), money(t, test.payment, "USD"),
			)
			if err != nil {
				t.Fatal(err)
			}
			assertAmount(t, result.TerminationGainLoss, test.gainLoss)
			entry, err := testEntryBuilder(t, accounts).BuildTerminationEntry(context.Background(), result, uuid.New(), uuid.New(), "leases", "tester")
			if err != nil {
				t.Fatal(err)
			}
			assertBalanced(t, entry)
		})
	}
}

func TestLeaseValidation(t *testing.T) {
	zero := money(t, "0", "USD")
	validPayment := LeasePayment{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")}
	cases := []struct {
		name  string
		rate  decimal.Decimal
		terms []LeasePayment
	}{
		{"negative discount rate", decimal.NewFromInt(-1), []LeasePayment{validPayment}},
		{"zero payment", decimal.Zero, []LeasePayment{{DueDate: validPayment.DueDate, Amount: zero}}},
		{"wrong currency", decimal.Zero, []LeasePayment{{DueDate: validPayment.DueDate, Amount: money(t, "100", "EUR")}}},
		{"invalid date", decimal.Zero, []LeasePayment{{DueDate: date(2026, 1, 1), Amount: money(t, "100", "USD")}}},
		{"unordered dates", decimal.Zero, []LeasePayment{validPayment, validPayment}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewLeaseContract(
				date(2026, 1, 1), test.rate, test.terms, zero, zero, zero, 0,
			); err == nil {
				t.Fatal("expected invalid lease terms error")
			}
		})
	}
}

func TestUnitClosePeriodBuildsPaymentAndDepreciationEntries(t *testing.T) {
	unit := NewUnit()
	accounts := testAccounts()
	builder := testEntryBuilder(t, accounts)
	entries, err := unit.ClosePeriod(
		context.Background(), testContract(t), date(2026, 3, 1), builder, "leases", uuid.New(), uuid.New(), "lease-close",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("ClosePeriod() returned %d entries, want payment and depreciation for two periods", len(entries))
	}
	for _, entry := range entries {
		assertBalanced(t, entry)
	}
}

func TestScheduleRejectsPaymentThatOverpaysLiability(t *testing.T) {
	contract, err := NewLeaseContract(
		date(2026, 1, 1), decimal.Zero,
		[]LeasePayment{
			{DueDate: date(2026, 2, 1), Amount: money(t, "10", "USD")},
			{DueDate: date(2026, 3, 1), Amount: money(t, "11", "USD")},
		},
		money(t, "0", "USD"), money(t, "0", "USD"), money(t, "0", "USD"), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract.InitialLeaseLiability = money(t, "1", "USD")
	if _, err := contract.AmortizationSchedule(); err != ErrNegativeLiability {
		t.Fatalf("AmortizationSchedule error = %v, want %v", err, ErrNegativeLiability)
	}
}

func testContract(t *testing.T) LeaseContract {
	t.Helper()
	contract, err := NewLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.12"),
		[]LeasePayment{
			{DueDate: date(2026, 2, 1), Amount: money(t, "100", "USD")},
			{DueDate: date(2026, 3, 1), Amount: money(t, "100", "USD")},
		},
		money(t, "10", "USD"), money(t, "20", "USD"), money(t, "5", "USD"), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	return contract.WithReference("test-lease")
}

type testLeasePostingCapability struct {
	accounts  LeaseAccounts
	available bool
}

func (c testLeasePostingCapability) LedgerPostingAvailable() bool { return c.available }

func (c testLeasePostingCapability) ResolveLeaseAccounts(
	_ context.Context, _ uuid.UUID, mappingID string,
) (LeaseAccounts, error) {
	if mappingID != "leases" {
		return LeaseAccounts{}, ErrJournalMappingUnavailable
	}
	return c.accounts, nil
}

func testEntryBuilder(t *testing.T, accounts LeaseAccounts) *EntryBuilder {
	t.Helper()
	builder, err := NewEntryBuilder(testLeasePostingCapability{accounts: accounts, available: true})
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func testAccounts() LeaseAccounts {
	return LeaseAccounts{
		ROUAsset: uuid.New(), LeaseLiability: uuid.New(), Cash: uuid.New(), PrepaidLease: uuid.New(),
		InterestExpense: uuid.New(), DepreciationExpense: uuid.New(), AccumulatedDepreciation: uuid.New(),
		ModificationGain: uuid.New(), TerminationGain: uuid.New(), TerminationLoss: uuid.New(),
	}
}

func money(t *testing.T, amount, currency string) types.Money {
	t.Helper()
	value, err := types.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func assertAmount(t *testing.T, value types.Money, expected string) {
	t.Helper()
	want := decimal.RequireFromString(expected)
	if !value.Amount().Equal(want) {
		t.Fatalf("amount = %s, want %s", value.Amount(), want)
	}
}

func TestGoldenScenario_GS08_IFRS16Leases(t *testing.T) {
	commencement := date(2026, 1, 1)
	rate := decimal.RequireFromString("0.10") // 10%
	payments := []LeasePayment{
		{DueDate: date(2026, 7, 1), Amount: money(t, "1000", "USD")},
		{DueDate: date(2027, 1, 1), Amount: money(t, "1000", "USD")},
	}
	initialDirect := money(t, "50", "USD")
	prepayment := money(t, "100", "USD")
	incentive := money(t, "30", "USD")

	contract, err := NewLeaseContract(commencement, rate, payments, initialDirect, prepayment, incentive, 2)
	if err != nil {
		t.Fatalf("NewLeaseContract: %v", err)
	}
	contract = contract.WithReference("LEASE-GS08-2026")

	// 1. Initial measurement
	if !contract.InitialLeaseLiability.Amount().IsPositive() {
		t.Fatalf("expected positive initial liability, got %s", contract.InitialLeaseLiability.Amount())
	}
	// Initial ROU = Initial Liability + 50 + 100 - 30 = Initial Liability + 120
	expectedROU := contract.InitialLeaseLiability.Amount().Add(decimal.NewFromInt(120))
	if !contract.InitialROUAsset.Amount().Equal(expectedROU) {
		t.Fatalf("initial ROU = %s, want %s", contract.InitialROUAsset.Amount(), expectedROU)
	}

	// 2. Amortization schedule
	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatalf("AmortizationSchedule: %v", err)
	}
	if len(schedule) != 2 {
		t.Fatalf("schedule length = %d, want 2", len(schedule))
	}

	// Closing liability at maturity must be exactly 0.00
	if !schedule[1].ClosingLiability.Amount().IsZero() {
		t.Fatalf("final closing liability = %s, want 0.00", schedule[1].ClosingLiability.Amount())
	}
	// Total accumulated depreciation must equal initial ROU
	if !schedule[1].AccumulatedDepreciation.Amount().Equal(contract.InitialROUAsset.Amount()) {
		t.Fatalf("accumulated depreciation = %s, want initial ROU %s",
			schedule[1].AccumulatedDepreciation.Amount(), contract.InitialROUAsset.Amount())
	}
	if !schedule[1].ClosingROUAsset.Amount().IsZero() {
		t.Fatalf("final closing ROU = %s, want 0.00", schedule[1].ClosingROUAsset.Amount())
	}
}

func TestLeasePostingIdempotencyAndRetryPreventsDuplicatePosting(t *testing.T) {
	accounts := testAccounts()
	builder := testEntryBuilder(t, accounts)
	contract := testContract(t).WithReference("LEASE-DUP-PREVENT")
	companyID, journalID := uuid.New(), uuid.New()

	ctx := context.Background()
	entry1, err := builder.BuildCommencementEntry(ctx, contract, companyID, journalID, "leases", "auditor")
	if err != nil {
		t.Fatalf("BuildCommencementEntry: %v", err)
	}
	assertBalanced(t, entry1)

	// Build second time with identical inputs (retry)
	entry2, err := builder.BuildCommencementEntry(ctx, contract, companyID, journalID, "leases", "auditor")
	if err != nil {
		t.Fatalf("BuildCommencementEntry retry: %v", err)
	}

	if entry1.ID != entry2.ID {
		t.Fatalf("retry entry ID mismatch: %s vs %s", entry1.ID, entry2.ID)
	}
	if entry1.IdempotencyKey != entry2.IdempotencyKey {
		t.Fatalf("retry idempotency key mismatch: %s vs %s", entry1.IdempotencyKey, entry2.IdempotencyKey)
	}
	if entry1.IdempotencyPayloadHash != entry2.IdempotencyPayloadHash {
		t.Fatalf("retry payload hash mismatch: %s vs %s", entry1.IdempotencyPayloadHash, entry2.IdempotencyPayloadHash)
	}

	// Verify posting idempotency store semantics
	postedStore := make(map[string]posting.JournalEntry)
	postSim := func(e posting.JournalEntry) (posting.JournalEntry, bool, error) {
		if existing, found := postedStore[e.IdempotencyKey]; found {
			if existing.IdempotencyPayloadHash != e.IdempotencyPayloadHash {
				return posting.JournalEntry{}, false, errors.New("idempotency conflict: changed payload")
			}
			return existing, false, nil // idempotent return, not posted again
		}
		e.Status = posting.EntryPosted
		postedStore[e.IdempotencyKey] = e
		return e, true, nil
	}

	firstResult, created, err := postSim(entry1)
	if err != nil || !created {
		t.Fatalf("first post failed: err=%v, created=%v", err, created)
	}
	retryResult, created, err := postSim(entry2)
	if err != nil || created {
		t.Fatalf("retry post failed: err=%v, created=%v", err, created)
	}
	if retryResult.ID != firstResult.ID || len(postedStore) != 1 {
		t.Fatalf("duplicate posting occurred in store: len=%d", len(postedStore))
	}
}

func assertBalanced(t *testing.T, entry posting.JournalEntry) {
	t.Helper()
	if err := entry.Validate(); err != nil {
		t.Fatalf("entry not balanced/valid: %v", err)
	}
}
