package ifrs16

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

func TestFutureLeasePaymentBuckets(t *testing.T) {
	contract, err := NewLeaseContract(
		date(2026, 1, 1), decimal.RequireFromString("0.10"),
		[]LeasePayment{
			{DueDate: date(2026, 7, 1), Amount: money(t, "1000", "USD")},
			{DueDate: date(2027, 7, 1), Amount: money(t, "1000", "USD")},
			{DueDate: date(2028, 1, 1), Amount: money(t, "1000", "USD")},
			{DueDate: date(2032, 1, 1), Amount: money(t, "500", "USD")},
		},
		money(t, "0", "USD"), money(t, "0", "USD"), money(t, "0", "USD"), 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract = contract.WithReference("disclosure-lease")

	buckets, err := FutureLeasePaymentBuckets(contract, date(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 6 {
		t.Fatalf("bucket count = %d, want 6", len(buckets))
	}
	expectations := map[string]string{
		"within_one_year":     "1000.0000",
		"one_to_two_years":    "2000.0000",
		"two_to_three_years":  "0.0000",
		"three_to_four_years": "0.0000",
		"four_to_five_years":  "0.0000",
		"beyond_five_years":   "500.0000",
	}
	for _, bucket := range buckets {
		index := bucketIndex(t, buckets, bucket.Label)
		if buckets[index].Amount != expectations[bucket.Label] {
			t.Fatalf("bucket %s = %s, want %s", bucket.Label, bucket.Amount, expectations[bucket.Label])
		}
	}

	// Payments due on or before the reporting date are excluded.
	afterFirst, err := FutureLeasePaymentBuckets(contract, date(2026, 7, 1))
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst[bucketIndex(t, afterFirst, "within_one_year")].Amount != "2000.0000" {
		t.Fatalf("post-payment analysis = %s, want 2000",
			afterFirst[bucketIndex(t, afterFirst, "within_one_year")].Amount)
	}
}

func TestLeaseCarryingLiability(t *testing.T) {
	contract := testContract(t)
	carrying, err := LeaseCarryingLiability(contract, date(2026, 1, 15))
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, carrying, contract.InitialLeaseLiability.Amount().String())

	carrying, err = LeaseCarryingLiability(contract, date(2026, 3, 1))
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, carrying, schedule[len(schedule)-1].ClosingLiability.Amount().String())
}

func TestBuildLeaseExpenseDisclosure(t *testing.T) {
	payload, err := BuildLeaseExpenseDisclosure(LeaseExpenseDisclosureInput{
		CompanyID:                  apitypes.CompanyID(uuid.New()),
		PeriodStart:                date(2026, 1, 1),
		PeriodEnd:                  date(2026, 12, 31),
		Currency:                   "usd",
		ROUDepreciation:            money(t, "111.1135", "USD"),
		InterestOnLeaseLiabilities: money(t, "2.7731", "USD"),
		ShortTermLeaseExpense:      money(t, "500", "USD"),
		OperatingLeaseIncome:       money(t, "1200", "USD"),
		Narrative:                  " Lessee and lessor lease balances for 2026. ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Code != DisclosureCodeLeaseExpense || payload.StandardReference != DisclosureStandardReference ||
		payload.Version != DisclosureSchemaVersion {
		t.Fatalf("disclosure identity fields are incomplete: %+v", payload)
	}
	if payload.Currency != "USD" {
		t.Fatalf("currency = %s, want USD", payload.Currency)
	}
	if payload.Data["depreciation_of_right_of_use_assets"] != "111.1135" ||
		payload.Data["interest_on_lease_liabilities"] != "2.7731" ||
		payload.Data["short_term_lease_expense"] != "500.0000" ||
		payload.Data["operating_lease_income"] != "1200.0000" {
		t.Fatalf("unexpected expense payload data: %+v", payload.Data)
	}
	if payload.Data["total_expense"] != "613.8866" || payload.Data["total_income"] != "1200.0000" {
		t.Fatalf("unexpected expense totals: %+v", payload.Data)
	}
	if payload.Narrative != "Lessee and lessor lease balances for 2026." {
		t.Fatalf("narrative not trimmed: %q", payload.Narrative)
	}
}

func TestBuildLeaseExpenseDisclosureValidation(t *testing.T) {
	valid := LeaseExpenseDisclosureInput{
		CompanyID:       apitypes.CompanyID(uuid.New()),
		PeriodStart:     date(2026, 1, 1),
		PeriodEnd:       date(2026, 12, 31),
		Currency:        "USD",
		ROUDepreciation: money(t, "100", "USD"),
	}
	cases := []struct {
		name   string
		mutate func(LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput
	}{
		{"no amounts supplied", func(in LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput {
			in.ROUDepreciation = types.Money{}
			return in
		}},
		{"currency mismatch", func(in LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput {
			in.ROUDepreciation = money(t, "100", "EUR")
			return in
		}},
		{"missing company", func(in LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput {
			in.CompanyID = apitypes.CompanyID{}
			return in
		}},
		{"reversed period", func(in LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput {
			in.PeriodStart, in.PeriodEnd = in.PeriodEnd, in.PeriodStart
			return in
		}},
		{"missing currency", func(in LeaseExpenseDisclosureInput) LeaseExpenseDisclosureInput {
			in.Currency = "  "
			return in
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildLeaseExpenseDisclosure(test.mutate(valid)); err == nil {
				t.Fatal("expected disclosure validation error")
			}
		})
	}
}

func TestBuildLeaseMaturityDisclosure(t *testing.T) {
	contract := testContract(t)
	carrying, err := LeaseCarryingLiability(contract, date(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := FutureLeasePaymentBuckets(contract, date(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildLeaseMaturityDisclosure(LeaseMaturityDisclosureInput{
		CompanyID:              apitypes.CompanyID(uuid.New()),
		PeriodStart:            date(2026, 1, 1),
		PeriodEnd:              date(2026, 12, 31),
		Currency:               "USD",
		LeaseLiabilityCarrying: carrying,
		Buckets:                buckets,
		SourceReferences: []publicreporting.DisclosureSource{
			{Kind: "lease-contract", Reference: "test-lease"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Code != DisclosureCodeMaturity {
		t.Fatalf("code = %s, want %s", payload.Code, DisclosureCodeMaturity)
	}
	if payload.Data["total_undiscounted_payments"] != "200.0000" {
		t.Fatalf("total = %v, want 200.0000", payload.Data["total_undiscounted_payments"])
	}
	if len(payload.Source) != 2 {
		t.Fatalf("source count = %d, want module plus host reference", len(payload.Source))
	}

	if _, err := BuildLeaseMaturityDisclosure(LeaseMaturityDisclosureInput{
		CompanyID:              apitypes.CompanyID(uuid.New()),
		PeriodStart:            date(2026, 1, 1),
		PeriodEnd:              date(2026, 12, 31),
		Currency:               "USD",
		LeaseLiabilityCarrying: carrying,
		Buckets: []MaturityBucket{
			{Label: "within_one_year", Months: 12, Amount: "not-a-number"},
		},
	}); err == nil {
		t.Fatal("expected bucket amount validation error")
	}
}

// TestDisclosurePayloadsPassRegistry verifies the payloads satisfy the P9-05
// disclosure registry contract: normalization, scope matching, and content-hash
// generation.
func TestDisclosurePayloadsPassRegistry(t *testing.T) {
	companyID := apitypes.CompanyID(uuid.New())
	period := publicreporting.DisclosurePeriod{StartDate: date(2026, 1, 1), EndDate: date(2026, 12, 31)}

	contract := testContract(t)
	buckets, err := FutureLeasePaymentBuckets(contract, date(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	carrying, err := LeaseCarryingLiability(contract, date(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	expense, err := BuildLeaseExpenseDisclosure(LeaseExpenseDisclosureInput{
		CompanyID:       companyID,
		PeriodStart:     period.StartDate,
		PeriodEnd:       period.EndDate,
		Currency:        "USD",
		ROUDepreciation: money(t, "111.1135", "USD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	maturity, err := BuildLeaseMaturityDisclosure(LeaseMaturityDisclosureInput{
		CompanyID:              companyID,
		PeriodStart:            period.StartDate,
		PeriodEnd:              period.EndDate,
		Currency:               "USD",
		LeaseLiabilityCarrying: carrying,
		Buckets:                buckets,
	})
	if err != nil {
		t.Fatal(err)
	}

	provider := disclosureTestProvider{name: "ifrs16", disclosures: []publicreporting.StructuredDisclosure{expense, maturity}}
	registry, err := reporting.NewDisclosureRegistry([]standards.DisclosureProvider{provider})
	if err != nil {
		t.Fatal(err)
	}
	results, err := registry.GetDisclosures(context.Background(), publicreporting.DisclosureQuery{
		CompanyID:         companyID,
		Period:            period,
		Currency:          "USD",
		StandardReference: DisclosureStandardReference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("registry returned %d disclosures, want 2", len(results))
	}
	for _, result := range results {
		if result.ContentHash == "" {
			t.Fatalf("registry did not generate a content hash for %s", result.Code)
		}
	}
}

type disclosureTestProvider struct {
	name        string
	disclosures []publicreporting.StructuredDisclosure
}

func (p disclosureTestProvider) Name() string { return p.name }

func (p disclosureTestProvider) GetDisclosures(
	_ context.Context, _ publicreporting.DisclosureQuery,
) ([]publicreporting.StructuredDisclosure, error) {
	return p.disclosures, nil
}

func bucketIndex(t *testing.T, buckets []MaturityBucket, label string) int {
	t.Helper()
	for i, bucket := range buckets {
		if bucket.Label == label {
			return i
		}
	}
	t.Fatalf("missing bucket %q", label)
	return -1
}
