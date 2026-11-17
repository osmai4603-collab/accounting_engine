package ifrs9

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func measurementMoney(t *testing.T, value string) financialtypes.Money {
	t.Helper()
	amount, err := financialtypes.NewMoneyFromString(value, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return amount
}

func validHostClassification(category MeasurementCategory) HostClassification {
	return HostClassification{
		MeasurementCategory: category, BusinessModelReference: "host-bm-assessment",
		SPPIAssessmentReference: "host-sppi-assessment",
	}
}

func TestMeasureSubsequentPeriodByCategory(t *testing.T) {
	tests := []struct {
		name                string
		category            MeasurementCategory
		stage               Stage
		openingGross        string
		openingCarrying     string
		allowance           string
		principal           string
		fairValue           string
		rate                string
		wantInterest        string
		wantClosing         string
		wantFairValueChange string
	}{
		{
			name: "amortised cost effective interest", category: AmortisedCost, stage: Stage1,
			openingGross: "1000", principal: "100", rate: "0.1",
			wantInterest: "100.00", wantClosing: "1000.00", wantFairValueChange: "0.00",
		},
		{
			name: "fvoci interest and other comprehensive income", category: FVOCI, stage: Stage1,
			openingGross: "1000", openingCarrying: "1020", allowance: "0",
			fairValue: "1050", rate: "0.1",
			wantInterest: "100.00", wantClosing: "1050.00", wantFairValueChange: "-70.00",
		},
		{
			name: "fvtpl fair value through profit and loss", category: FVTPL, stage: Stage1,
			openingGross: "1000", openingCarrying: "1000", allowance: "0",
			fairValue: "950", rate: "0",
			wantInterest: "0.00", wantClosing: "950.00", wantFairValueChange: "-50.00",
		},
		{
			name: "stage three interest on net carrying amount", category: AmortisedCost, stage: Stage3,
			openingGross: "1000", allowance: "200", principal: "0", rate: "0.1",
			wantInterest: "80.00", wantClosing: "880.00", wantFairValueChange: "0.00",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := MeasurementInput{
				InstrumentID: uuid.New(), AsOf: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
				Classification: validHostClassification(test.category), Stage: test.stage,
				OpeningGrossCarrying:  measurementMoney(t, test.openingGross),
				ExistingLossAllowance: measurementMoney(t, defaultMoney(test.allowance)),
				PrincipalRepayments:   measurementMoney(t, defaultMoney(test.principal)),
				EffectiveInterestRate: decimal.RequireFromString(test.rate), Period: 365 * 24 * time.Hour,
			}
			if test.openingCarrying != "" {
				input.OpeningCarryingAmount = measurementMoney(t, test.openingCarrying)
				input.FairValue = measurementMoney(t, test.fairValue)
			}
			result, err := MeasureSubsequentPeriod(input)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.InterestIncome.Amount().StringFixed(2); got != test.wantInterest {
				t.Errorf("interest income = %s, want %s", got, test.wantInterest)
			}
			if got := result.ClosingCarryingAmount.Amount().StringFixed(2); got != test.wantClosing {
				t.Errorf("closing carrying amount = %s, want %s", got, test.wantClosing)
			}
			if got := result.FairValueAdjustment.Amount().StringFixed(2); got != test.wantFairValueChange {
				t.Errorf("fair-value adjustment = %s, want %s", got, test.wantFairValueChange)
			}
		})
	}
}

func TestMeasureSubsequentPeriodRequiresHostAssessmentAndRejectsFVTPLAllowance(t *testing.T) {
	base := MeasurementInput{
		InstrumentID: uuid.New(), AsOf: time.Now(), Classification: validHostClassification(AmortisedCost),
		Stage: Stage1, OpeningGrossCarrying: measurementMoney(t, "100"),
		ExistingLossAllowance: measurementMoney(t, "0"), PrincipalRepayments: measurementMoney(t, "0"),
		EffectiveInterestRate: decimal.Zero, Period: 24 * time.Hour,
	}
	base.Classification.SPPIAssessmentReference = " "
	if _, err := MeasureSubsequentPeriod(base); !errors.Is(err, ErrInvalidMeasurement) {
		t.Fatalf("missing host evidence error = %v, want ErrInvalidMeasurement", err)
	}
	base.Classification = validHostClassification(FVTPL)
	base.OpeningCarryingAmount = measurementMoney(t, "100")
	base.FairValue = measurementMoney(t, "100")
	base.ExistingLossAllowance = measurementMoney(t, "1")
	if _, err := MeasureSubsequentPeriod(base); !errors.Is(err, ErrInvalidMeasurement) {
		t.Fatalf("FVTPL loss allowance error = %v, want ErrInvalidMeasurement", err)
	}
}

func TestCalculateEADFromLedgerAndUndrawnCommitments(t *testing.T) {
	ead, err := CalculateEAD(measurementMoney(t, "125"), measurementMoney(t, "75"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ead.Amount().StringFixed(2); got != "200.00" {
		t.Fatalf("EAD = %s, want 200.00", got)
	}
	eur, err := financialtypes.NewMoneyFromString("10", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CalculateEAD(measurementMoney(t, "1"), eur); !errors.Is(err, ErrInvalidGeneralExposure) {
		t.Fatalf("currency mismatch error = %v, want ErrInvalidGeneralExposure", err)
	}
}

func TestCalculateEffectiveInterestRate(t *testing.T) {
	initialDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rate, err := CalculateEffectiveInterestRate(measurementMoney(t, "1000"), initialDate, []ContractualCashFlow{
		{Date: initialDate.AddDate(1, 0, 0), Amount: measurementMoney(t, "1100")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rate.Sub(decimal.RequireFromString("0.1")).Abs().LessThan(decimal.RequireFromString("0.000000000001")) {
		t.Fatalf("effective interest rate = %s, want 0.1", rate)
	}
	_, err = CalculateEffectiveInterestRate(measurementMoney(t, "1000"), initialDate, []ContractualCashFlow{
		{Date: initialDate, Amount: measurementMoney(t, "1100")},
	})
	if !errors.Is(err, ErrInvalidContractualCashFlows) {
		t.Fatalf("invalid cash flow error = %v, want ErrInvalidContractualCashFlows", err)
	}
}

func TestBuildMeasurementEntryRoutesFairValueToCategoryAccount(t *testing.T) {
	for _, test := range []struct {
		category MeasurementCategory
		account  string
	}{
		{category: FVOCI, account: "oci"},
		{category: FVTPL, account: "pnl"},
	} {
		t.Run(string(test.category), func(t *testing.T) {
			input := MeasurementInput{
				InstrumentID: uuid.New(), AsOf: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
				Classification: validHostClassification(test.category), Stage: Stage1,
				OpeningGrossCarrying:  measurementMoney(t, "1000"),
				OpeningCarryingAmount: measurementMoney(t, "1000"),
				ExistingLossAllowance: measurementMoney(t, "0"),
				PrincipalRepayments:   measurementMoney(t, "0"),
				FairValue:             measurementMoney(t, "900"),
				EffectiveInterestRate: decimal.Zero, Period: 365 * 24 * time.Hour,
			}
			result, err := MeasureSubsequentPeriod(input)
			if err != nil {
				t.Fatal(err)
			}
			accounts := MeasurementAccounts{
				Asset: uuid.New(), Cash: uuid.New(), InterestIncome: uuid.New(),
				OCIReserve: uuid.New(), FairValueGainLoss: uuid.New(),
			}
			entry, err := BuildMeasurementEntry(result, accounts, uuid.New(), uuid.New(), "reviewer")
			if err != nil {
				t.Fatal(err)
			}
			var debits, credits decimal.Decimal
			var foundDestination bool
			destination := accounts.FairValueGainLoss
			if test.account == "oci" {
				destination = accounts.OCIReserve
			}
			for _, line := range entry.Lines {
				debits = debits.Add(line.Debit.Amount())
				credits = credits.Add(line.Credit.Amount())
				foundDestination = foundDestination || line.AccountID == destination
			}
			if !debits.Equal(credits) || !foundDestination {
				t.Fatalf("entry not balanced or category destination missing: debits=%s credits=%s", debits, credits)
			}
			companyID, journalID := uuid.New(), uuid.New()
			first, err := BuildMeasurementEntry(result, accounts, companyID, journalID, "risk_officer")
			if err != nil {
				t.Fatal(err)
			}
			retry, err := BuildMeasurementEntry(result, accounts, companyID, journalID, "risk_officer")
			if err != nil {
				t.Fatal(err)
			}
			if first.ID != retry.ID || first.IdempotencyKey != retry.IdempotencyKey ||
				first.IdempotencyPayloadHash != retry.IdempotencyPayloadHash {
				t.Fatal("measurement retry does not have a deterministic idempotent identity")
			}
			for index := range first.Lines {
				if first.Lines[index].ID != retry.Lines[index].ID {
					t.Fatal("measurement retry generated different journal line IDs")
				}
			}
		})
	}
}

func defaultMoney(value string) string {
	if value == "" {
		return "0"
	}
	return value
}
