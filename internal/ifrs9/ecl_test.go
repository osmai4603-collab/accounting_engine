package ifrs9

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func testConfig() Config {
	return Config{
		CompanyID:           uuid.New(),
		JournalID:           uuid.New(),
		ImpairmentAccountID: uuid.New(),
		AllowanceAccountID:  uuid.New(),
		Currency:            "USD",
		ProvisionMatrix: map[subledgers.AgingBucket]decimal.Decimal{
			subledgers.AgingCurrent: decimal.RequireFromString("0.01"),
			subledgers.Aging1To30:   decimal.RequireFromString("0.02"),
			subledgers.Aging31To60:  decimal.RequireFromString("0.05"),
			subledgers.Aging61To90:  decimal.RequireFromString("0.10"),
			subledgers.AgingOver90:  decimal.RequireFromString("0.25"),
		},
	}
}

func testExposure(companyID uuid.UUID, dueDate time.Time, amount string) Exposure {
	money, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		panic(err)
	}
	return Exposure{Receivable: subledgers.OpenLine{
		CompanyID:      companyID,
		ID:             uuid.New(),
		Kind:           subledgers.LineReceivable,
		OriginalAmount: money,
		ResidualAmount: money,
		DueDate:        dueDate,
	}}
}

func TestClassifyStageBackstopsAndQualitativeFlags(t *testing.T) {
	tests := []struct {
		name      string
		days      int
		sicr      bool
		defaulted bool
		want      Stage
	}{
		{name: "current", days: 0, want: Stage1},
		{name: "30 days", days: 30, want: Stage1},
		{name: "31 days", days: 31, want: Stage2},
		{name: "90 days", days: 90, want: Stage2},
		{name: "91 days", days: 91, want: Stage3},
		{name: "qualitative SICR", days: 0, sicr: true, want: Stage2},
		{name: "default overrides SICR", days: 0, sicr: true, defaulted: true, want: Stage3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyStage(tt.days, tt.sicr, tt.defaulted); got != tt.want {
				t.Fatalf("classifyStage() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCalculateProvisionMatrixAndAllowanceAdjustment(t *testing.T) {
	config := testConfig()
	asOf := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	exposure := testExposure(config.CompanyID, asOf.AddDate(0, 0, -40), "1000")
	existing, _ := financialtypes.NewMoneyFromString("30", "USD")

	result, err := Calculate(asOf, config, []Exposure{exposure}, existing)
	if err != nil {
		t.Fatal(err)
	}
	// Forty days overdue uses the 31-60 day matrix rate (5%).
	if got := result.RequiredAllowance.Amount().StringFixed(2); got != "50.00" {
		t.Fatalf("required allowance = %s, want 50.00", got)
	}
	if got := result.Adjustment.Amount().StringFixed(2); got != "20.00" {
		t.Fatalf("adjustment = %s, want 20.00", got)
	}
	if result.JournalEntry == nil {
		t.Fatal("expected an adjustment journal entry")
	}
	entry := result.JournalEntry
	if err := entry.Validate(); err != nil {
		t.Fatalf("generated entry is invalid: %v", err)
	}
	if entry.Classification != posting.EntryClassAdjusting ||
		entry.Lines[0].Debit.Currency() != "USD" || entry.Lines[0].Credit.Currency() != "" ||
		entry.Lines[1].Credit.Currency() != "USD" || entry.Lines[1].Debit.Currency() != "" {
		t.Fatalf("unexpected allowance increase entry: %+v", entry.Lines)
	}
	if entry.Lines[0].AccountID != config.ImpairmentAccountID ||
		entry.Lines[1].AccountID != config.AllowanceAccountID {
		t.Fatal("entry used the wrong accounts")
	}
	repeated, err := Calculate(asOf, config, []Exposure{exposure}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.JournalEntry.ID != entry.ID ||
		repeated.JournalEntry.Lines[0].ID != entry.Lines[0].ID ||
		repeated.JournalEntry.Lines[1].ID != entry.Lines[1].ID {
		t.Fatal("identical calculations did not produce deterministic journal IDs")
	}

	// A larger existing allowance generates a balanced release entry.
	existing, _ = financialtypes.NewMoneyFromString("70", "USD")
	result, err = Calculate(asOf, config, []Exposure{exposure}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Adjustment.Amount().StringFixed(2); got != "-20.00" {
		t.Fatalf("release adjustment = %s, want -20.00", got)
	}
	if err := result.JournalEntry.Validate(); err != nil {
		t.Fatalf("generated release entry is invalid: %v", err)
	}
	if result.JournalEntry.Lines[0].AccountID != config.ImpairmentAccountID ||
		result.JournalEntry.Lines[0].Credit.Currency() != "USD" ||
		result.JournalEntry.Lines[1].AccountID != config.AllowanceAccountID ||
		result.JournalEntry.Lines[1].Debit.Currency() != "USD" {
		t.Fatal("release entry did not reverse the accounts")
	}
}

func TestCalculateEmptyPortfolioAndNoAdjustment(t *testing.T) {
	config := testConfig()
	existing, _ := financialtypes.Zero(config.Currency)
	result, err := Calculate(time.Now(), config, nil, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RequiredAllowance.Amount().IsZero() ||
		!result.Adjustment.Amount().IsZero() || len(result.Receivables) != 0 {
		t.Fatalf("unexpected empty portfolio result: %+v", result)
	}
	if result.JournalEntry != nil {
		t.Fatal("zero adjustment should not produce a journal entry")
	}
}

func TestCalculateRejectsInvalidInputs(t *testing.T) {
	config := testConfig()
	asOf := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	zero, _ := financialtypes.Zero("USD")
	valid := testExposure(config.CompanyID, asOf, "100")

	tests := []struct {
		name      string
		date      time.Time
		exposures []Exposure
		allowance financialtypes.Money
		wantErr   error
	}{
		{name: "zero measurement date", date: time.Time{}, allowance: zero, wantErr: ErrInvalidDate},
		{name: "nil receivable id", date: asOf, exposures: []Exposure{{Receivable: subledgers.OpenLine{CompanyID: config.CompanyID, Kind: subledgers.LineReceivable, DueDate: asOf, ResidualAmount: mustMoney(t, "10", "USD")}}}, allowance: zero, wantErr: ErrInvalidExposure},
		{name: "nil company id", date: asOf, exposures: []Exposure{func() Exposure { e := valid; e.Receivable.CompanyID = uuid.Nil; return e }()}, allowance: zero, wantErr: ErrInvalidExposure},
		{name: "zero due date", date: asOf, exposures: []Exposure{func() Exposure { e := valid; e.Receivable.DueDate = time.Time{}; return e }()}, allowance: zero, wantErr: ErrInvalidExposure},
		{name: "negative balance", date: asOf, exposures: []Exposure{func() Exposure { e := valid; e.Receivable.ResidualAmount = mustMoney(t, "-1", "USD"); return e }()}, allowance: zero, wantErr: ErrInvalidExposure},
		{name: "mixed currency", date: asOf, exposures: []Exposure{func() Exposure { e := valid; e.Receivable.ResidualAmount = mustMoney(t, "1", "EUR"); return e }()}, allowance: zero, wantErr: ErrInvalidExposure},
		{name: "invalid allowance currency", date: asOf, allowance: mustMoney(t, "1", "EUR"), wantErr: ErrInvalidAllowance},
		{name: "negative allowance", date: asOf, allowance: mustMoney(t, "-1", "USD"), wantErr: ErrInvalidAllowance},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Calculate(tt.date, config, tt.exposures, tt.allowance)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Calculate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigRejectsMissingAndOutOfRangeRates(t *testing.T) {
	config := testConfig()
	delete(config.ProvisionMatrix, subledgers.AgingCurrent)
	if err := config.Validate(); !errors.Is(err, ErrMissingRate) {
		t.Fatalf("Validate() error = %v, want missing rate", err)
	}
	config = testConfig()
	config.ProvisionMatrix[subledgers.AgingCurrent] = decimal.RequireFromString("1.01")
	if err := config.Validate(); !errors.Is(err, ErrInvalidRate) {
		t.Fatalf("Validate() error = %v, want invalid rate", err)
	}
	config = testConfig()
	config.CompanyID = uuid.Nil
	if err := config.Validate(); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Validate() error = %v, want invalid configuration", err)
	}
	config = testConfig()
	config.JournalID = uuid.Nil
	if err := config.Validate(); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Validate() error = %v, want invalid journal ID", err)
	}
}

type testPortfolioSource []Exposure

func (s testPortfolioSource) ListReceivables(ctx context.Context, _ uuid.UUID) ([]Exposure, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

type testAllowanceSource financialtypes.Money

func (s testAllowanceSource) ExistingAllowance(ctx context.Context, _, _ uuid.UUID, _ string) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	return financialtypes.Money(s), nil
}

type testAsOfAllowanceSource financialtypes.Money

func (s testAsOfAllowanceSource) ExistingAllowance(ctx context.Context, _, _ uuid.UUID, _ string) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	return financialtypes.Money(s), nil
}

func (s testAsOfAllowanceSource) ExistingAllowanceAsOf(ctx context.Context, _, _ uuid.UUID, _ string, _ time.Time) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	return financialtypes.Money(s), nil
}

func TestEngineLoadsUsingContextAndReturnsDeterministicResults(t *testing.T) {
	config := testConfig()
	asOf := time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("local", 3*60*60))
	first := testExposure(config.CompanyID, asOf.AddDate(0, 0, -31), "100")
	second := testExposure(config.CompanyID, asOf, "100")
	zero, _ := financialtypes.Zero("USD")
	engine, err := NewEngine(config, testPortfolioSource{second, first}, testAsOfAllowanceSource(zero))
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Calculate(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Receivables) != 2 || result.Receivables[0].ReceivableID.String() > result.Receivables[1].ReceivableID.String() {
		t.Fatalf("receivables not deterministically ordered: %+v", result.Receivables)
	}
	var overdue *ReceivableResult
	for i := range result.Receivables {
		if result.Receivables[i].ReceivableID == first.Receivable.ID {
			overdue = &result.Receivables[i]
		}
	}
	if overdue == nil || overdue.Stage != Stage2 || overdue.DaysPastDue != 31 {
		t.Fatalf("unexpected stage result at 31 days: %+v", overdue)
	}
}

func TestEngineRejectsHistoricalAllowanceWithoutAsOfSource(t *testing.T) {
	config := testConfig()
	allowance, _ := financialtypes.Zero(config.Currency)
	engine, err := NewEngine(config, testPortfolioSource(nil), testAllowanceSource(allowance))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Calculate(context.Background(), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrHistoricalAllowanceUnavailable) {
		t.Fatalf("historical Calculate() error = %v, want %v", err, ErrHistoricalAllowanceUnavailable)
	}
}

func mustMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
