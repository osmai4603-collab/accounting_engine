package ifrs16

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestPhase6GoldenIFRS16MultipleLeasesAndRateModifications is the Phase 6
// golden lease benchmark. It covers three leases in three currencies, each with
// different payment cadence, direct costs, prepayments or incentives, and then
// remeasures every lease at a modified incremental borrowing rate. The expected
// schedules and remeasurement amounts are frozen as the audit reference.
func TestPhase6GoldenIFRS16MultipleLeasesAndRateModifications(t *testing.T) {
	type leaseCase struct {
		name       string
		currency   string
		rate       string
		payments   []LeasePayment
		direct     string
		prepay     string
		incentive  string
		usefulLife int

		initialLiability string
		initialROU       string
		schedule         []struct {
			interest, principal, closing, depreciation, accumulated, closingROU string
		}

		modificationDate string
		remaining        []LeasePayment
		newRate          string
		oldLiability     string
		newLiability     string
		liabilityDelta   string
		oldROU           string
		newROU           string
		rouDelta         string
		gain             string
	}

	leases := []leaseCase{
		{
			name: "USD quarterly lease with direct costs", currency: "USD", rate: "0.12",
			payments: []LeasePayment{
				{DueDate: date(2026, 4, 1), Amount: money(t, "1000", "USD")},
				{DueDate: date(2026, 7, 1), Amount: money(t, "1000", "USD")},
				{DueDate: date(2026, 10, 1), Amount: money(t, "1000", "USD")},
				{DueDate: date(2027, 1, 1), Amount: money(t, "1000", "USD")},
			},
			direct: "500", prepay: "0", incentive: "0", usefulLife: 4,
			initialLiability: "3729.3807", initialROU: "4229.3807",
			schedule: []struct {
				interest, principal, closing, depreciation, accumulated, closingROU string
			}{
				{"105.6838", "894.3162", "2835.0645", "1057.3452", "1057.3452", "3172.0355"},
				{"81.2458", "918.7542", "1916.3103", "1057.3452", "2114.6904", "2114.6903"},
				{"55.5288", "944.4712", "971.8391", "1057.3452", "3172.0356", "1057.3451"},
				{"28.1609", "971.8391", "0", "1057.3451", "4229.3807", "0"},
			},
			modificationDate: "2026-10-01",
			remaining: []LeasePayment{
				{DueDate: date(2027, 1, 1), Amount: money(t, "1200", "USD")},
				{DueDate: date(2027, 4, 1), Amount: money(t, "1200", "USD")},
			},
			newRate: "0.10", oldLiability: "1916.3103", newLiability: "2315.82",
			liabilityDelta: "399.5097", oldROU: "2114.6903", newROU: "2514.2",
			rouDelta: "399.5097", gain: "0",
		},
		{
			name: "EUR annual lease with prepayment", currency: "EUR", rate: "0",
			payments: []LeasePayment{
				{DueDate: date(2027, 1, 1), Amount: money(t, "2000", "EUR")},
				{DueDate: date(2028, 1, 1), Amount: money(t, "2000", "EUR")},
				{DueDate: date(2029, 1, 1), Amount: money(t, "2000", "EUR")},
			},
			direct: "0", prepay: "300", incentive: "0", usefulLife: 3,
			initialLiability: "6000", initialROU: "6300",
			schedule: []struct {
				interest, principal, closing, depreciation, accumulated, closingROU string
			}{
				{"0", "2000", "4000", "2100", "2100", "4200"},
				{"0", "2000", "2000", "2100", "4200", "2100"},
				{"0", "2000", "0", "2100", "6300", "0"},
			},
			modificationDate: "2027-01-01",
			remaining: []LeasePayment{
				{DueDate: date(2028, 1, 1), Amount: money(t, "2000", "EUR")},
				{DueDate: date(2029, 1, 1), Amount: money(t, "2000", "EUR")},
			},
			newRate: "0.03", oldLiability: "4000", newLiability: "3826.7867",
			liabilityDelta: "-173.2133", oldROU: "4200", newROU: "4026.7867",
			rouDelta: "-173.2133", gain: "0",
		},
		{
			name: "GBP semiannual lease with incentive", currency: "GBP", rate: "0",
			payments: []LeasePayment{
				{DueDate: date(2026, 7, 1), Amount: money(t, "1500", "GBP")},
				{DueDate: date(2027, 1, 1), Amount: money(t, "1500", "GBP")},
				{DueDate: date(2027, 7, 1), Amount: money(t, "1500", "GBP")},
				{DueDate: date(2028, 1, 1), Amount: money(t, "1500", "GBP")},
			},
			direct: "0", prepay: "0", incentive: "200", usefulLife: 4,
			initialLiability: "6000", initialROU: "5800",
			schedule: []struct {
				interest, principal, closing, depreciation, accumulated, closingROU string
			}{
				{"0", "1500", "4500", "1450", "1450", "4350"},
				{"0", "1500", "3000", "1450", "2900", "2900"},
				{"0", "1500", "1500", "1450", "4350", "1450"},
				{"0", "1500", "0", "1450", "5800", "0"},
			},
			modificationDate: "2027-01-01",
			remaining: []LeasePayment{
				{DueDate: date(2027, 7, 1), Amount: money(t, "1500", "GBP")},
				{DueDate: date(2028, 1, 1), Amount: money(t, "1500", "GBP")},
			},
			newRate: "0.08", oldLiability: "3000", newLiability: "2832.7211",
			liabilityDelta: "-167.2789", oldROU: "2900", newROU: "2732.7211",
			rouDelta: "-167.2789", gain: "0",
		},
	}

	for _, lease := range leases {
		lease := lease
		t.Run(lease.name, func(t *testing.T) {
			contract, err := NewLeaseContract(
				date(2026, 1, 1), decimal.RequireFromString(lease.rate), lease.payments,
				money(t, lease.direct, lease.currency), money(t, lease.prepay, lease.currency),
				money(t, lease.incentive, lease.currency), lease.usefulLife,
			)
			if err != nil {
				t.Fatalf("NewLeaseContract: %v", err)
			}
			contract = contract.WithReference(lease.name)
			assertAmount(t, contract.InitialLeaseLiability, lease.initialLiability)
			assertAmount(t, contract.InitialROUAsset, lease.initialROU)

			accounts := testAccounts()
			builder := testEntryBuilder(t, accounts)
			commencement, err := builder.BuildCommencementEntry(context.Background(), contract, uuid.New(), uuid.New(), "leases", "golden-maker")
			if err != nil {
				t.Fatalf("BuildCommencementEntry: %v", err)
			}
			assertBalanced(t, commencement)

			schedule, err := contract.AmortizationSchedule()
			if err != nil {
				t.Fatalf("AmortizationSchedule: %v", err)
			}
			if len(schedule) != len(lease.schedule) {
				t.Fatalf("schedule periods = %d, want %d", len(schedule), len(lease.schedule))
			}
			paid := money(t, "0", lease.currency)
			for i, period := range schedule {
				want := lease.schedule[i]
				assertAmount(t, period.InterestExpense, want.interest)
				assertAmount(t, period.PrincipalReduction, want.principal)
				assertAmount(t, period.ClosingLiability, want.closing)
				assertAmount(t, period.ROUDepreciation, want.depreciation)
				assertAmount(t, period.AccumulatedDepreciation, want.accumulated)
				assertAmount(t, period.ClosingROUAsset, want.closingROU)

				// Every payment entry and every depreciation entry must balance.
				payment, err := builder.BuildPaymentEntry(context.Background(), period, uuid.New(), uuid.New(), "leases", "golden-maker")
				if err != nil {
					t.Fatalf("BuildPaymentEntry period %d: %v", i+1, err)
				}
				assertBalanced(t, payment)
				depreciation, err := builder.BuildDepreciationEntry(context.Background(), period, uuid.New(), uuid.New(), "leases", "golden-maker")
				if err != nil {
					t.Fatalf("BuildDepreciationEntry period %d: %v", i+1, err)
				}
				assertBalanced(t, depreciation)

				paid, err = paid.Add(period.Payment)
				if err != nil {
					t.Fatal(err)
				}
			}
			assertAmount(t, schedule[len(schedule)-1].ClosingLiability, "0")
			assertAmount(t, schedule[len(schedule)-1].ClosingROUAsset, "0")
			assertAmount(t, schedule[len(schedule)-1].AccumulatedDepreciation, lease.initialROU)

			modificationDate := parseDate(t, lease.modificationDate)
			result, err := contract.RecalculateModification(
				modificationDate, lease.remaining, decimal.RequireFromString(lease.newRate),
				money(t, lease.oldLiability, lease.currency), money(t, lease.oldROU, lease.currency),
			)
			if err != nil {
				t.Fatalf("RecalculateModification: %v", err)
			}
			assertAmount(t, result.OldLeaseLiability, lease.oldLiability)
			assertAmount(t, result.RemeasuredLiability, lease.newLiability)
			assertAmount(t, result.LiabilityAdjustment, lease.liabilityDelta)
			assertAmount(t, result.OldROUAsset, lease.oldROU)
			assertAmount(t, result.NewROUAsset, lease.newROU)
			assertAmount(t, result.ROUAdjustment, lease.rouDelta)
			assertAmount(t, result.ModificationGain, lease.gain)

			modification, err := builder.BuildModificationEntry(context.Background(), result, uuid.New(), uuid.New(), "leases", "golden-maker")
			if err != nil {
				t.Fatalf("BuildModificationEntry: %v", err)
			}
			assertBalanced(t, modification)
		})
	}
}

func parseDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
