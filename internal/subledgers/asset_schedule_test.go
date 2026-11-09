package subledgers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestCalculateProrataDepreciationUsesActualDays(t *testing.T) {
	start := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	amount, err := CalculateProrataDepreciation(decimal.NewFromInt(1200), decimal.Zero, 12, start, start)
	if err != nil {
		t.Fatal(err)
	}
	if !amount.Equal(decimal.NewFromInt(1200).Mul(decimal.NewFromInt(16)).Div(decimal.NewFromInt(365))) {
		t.Fatalf("prorated amount = %s", amount)
	}
}

func TestGenerateDepreciationScheduleCoversUsefulLife(t *testing.T) {
	start := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		ID: uuid.New(), HistoricalCost: money(t, "1200"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 12, Method: StraightLine, InServiceDate: start,
	}
	lines, err := GenerateDepreciationSchedule(asset)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 13 {
		t.Fatalf("schedule lines = %d, want 13 periods including partial first and last months", len(lines))
	}
	total := decimal.Zero
	for _, line := range lines {
		total = total.Add(line.Amount.Amount())
	}
	if !total.Equal(decimal.NewFromInt(1200)) || !lines[len(lines)-1].BookValue.Amount().IsZero() {
		t.Fatalf("schedule does not fully depreciate asset: total=%s final book value=%s", total, lines[len(lines)-1].BookValue)
	}
}

func TestGenerateDepreciationScheduleSupportsDistinctComponents(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		ID: uuid.New(), HistoricalCost: money(t, "1200"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 12, Method: StraightLine, InServiceDate: start,
		Components: []AssetComponent{
			{ID: uuid.New(), Name: "structure", Cost: money(t, "1000"), SalvageValue: money(t, "0"), UsefulLifeMonths: 10, Method: StraightLine, InServiceDate: start},
			{ID: uuid.New(), Name: "motor", Cost: money(t, "200"), SalvageValue: money(t, "0"), UsefulLifeMonths: 4, Method: StraightLine, InServiceDate: start},
		},
	}
	lines, err := GenerateDepreciationSchedule(asset)
	if err != nil {
		t.Fatal(err)
	}
	componentPeriods := map[uuid.UUID]int{}
	for _, line := range lines {
		componentPeriods[line.ComponentID]++
	}
	if len(componentPeriods) != 2 || componentPeriods[asset.Components[0].ID] != 10 || componentPeriods[asset.Components[1].ID] != 4 {
		t.Fatalf("component schedules do not use component lives: %+v", componentPeriods)
	}
}
