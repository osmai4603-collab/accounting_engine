package subledgers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
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

func TestMonthlyDepreciationUsesPartialFirstMonthForNonComponentAsset(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewFixedAssetLedger(repo, nil)
	start := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		CompanyID: uuid.New(), ID: uuid.New(),
		HistoricalCost: money(t, "1200"), SalvageValue: money(t, "0"),
		UsefulLifeMonths: 12, Method: StraightLine, InServiceDate: start,
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}
	if err := ledger.RegisterAsset(asset); err != nil {
		t.Fatal(err)
	}
	_, amount, err := ledger.MonthlyDepreciation(
		asset.ID, uuid.New(), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	// 1200 USD over 365 days for the 16 days from January 16 is 52.6027 in
	// internal precision, rounded to the USD minor unit for posting.
	want := money(t, "52.60")
	if !amount.Equal(want) {
		t.Fatalf("first-month depreciation = %s, want partial-period amount %s", amount, want)
	}
}

func TestDepreciationAttachmentValidatesEntryAgainstAssetSchedule(t *testing.T) {
	companyID := uuid.New()
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	asset := FixedAsset{
		CompanyID: companyID, ID: uuid.New(),
		HistoricalCost: money(t, "1200.00"), CurrentBookValue: money(t, "1200.00"),
		SalvageValue: money(t, "0.00"), AccumulatedDepreciation: money(t, "0.00"),
		UsefulLifeMonths: 12, Method: StraightLine,
		InServiceDate:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpenseAccountID: uuid.New(), AccumulatedAccountID: uuid.New(),
	}
	amount, updated, err := depreciationUpdate(asset, asOf)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := depreciationJournalLines(asset, asOf, amount)
	if err != nil {
		t.Fatal(err)
	}
	attachment := &DepreciationAttachment{
		AssetID: asset.ID, CompanyID: companyID, Amount: amount, AsOf: asOf,
		expectedAsset: asset, UpdatedAsset: updated,
	}
	entry := posting.JournalEntry{
		ID: uuid.New(), CompanyID: companyID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryDraft, DocumentReference: depreciationReference(asset.ID, asOf),
		Lines: lines,
	}

	if err := attachment.ValidateAttachment(t.Context(), entry); err != nil {
		t.Fatalf("valid depreciation attachment rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*DepreciationAttachment, *posting.JournalEntry)
	}{
		{
			name: "entry amount mismatch",
			mutate: func(_ *DepreciationAttachment, entry *posting.JournalEntry) {
				entry.Lines[0].Debit = money(t, "101.00")
			},
		},
		{
			name: "entry date mismatch",
			mutate: func(_ *DepreciationAttachment, entry *posting.JournalEntry) {
				entry.EntryDate = asOf.AddDate(0, 0, 1)
			},
		},
		{
			name: "depreciation schedule mismatch",
			mutate: func(_ *DepreciationAttachment, entry *posting.JournalEntry) {
				entry.Lines[0].AccountID = uuid.New()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalidAttachment := *attachment
			invalidEntry := entry
			invalidEntry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
			tt.mutate(&invalidAttachment, &invalidEntry)
			if err := invalidAttachment.ValidateAttachment(t.Context(), invalidEntry); err == nil {
				t.Fatal("mismatched depreciation entry was accepted")
			}
		})
	}
}
