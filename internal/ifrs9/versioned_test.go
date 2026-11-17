package ifrs9

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func versionedGoldenInput(t *testing.T) VersionedECLInput {
	t.Helper()
	allowance, err := financialtypes.NewMoneyFromString("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	firstBalance, err := financialtypes.NewMoneyFromString("900", "USD")
	if err != nil {
		t.Fatal(err)
	}
	firstCommitments, err := financialtypes.NewMoneyFromString("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	secondBalance, err := financialtypes.NewMoneyFromString("400", "USD")
	if err != nil {
		t.Fatal(err)
	}
	secondCommitments, err := financialtypes.NewMoneyFromString("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	firstAllowance, err := financialtypes.NewMoneyFromString("60", "USD")
	if err != nil {
		t.Fatal(err)
	}
	secondAllowance, err := financialtypes.NewMoneyFromString("40", "USD")
	if err != nil {
		t.Fatal(err)
	}
	return VersionedECLInput{
		EvaluationDate: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
		ModelVersion:   "credit-risk-v1.2",
		SourceDataID:   "portfolio-close-2026-11-10",
		SourceDataHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DiscountMethod: DiscountMethodEIRSingleExpectedLossDate,
		Assumptions: []NamedAssumption{
			{Name: "stage1_horizon", Value: "12 months"},
			{Name: "stage2_backstop_days", Value: "30"},
			{Name: "stage3_backstop_days", Value: "90"},
			{Name: "discount_method", Value: DiscountMethodEIRSingleExpectedLossDate},
			{Name: "forward_looking_basis", Value: "two weighted scenarios"},
		},
		Scenarios:         []VersionedScenario{{Name: "base", Weight: decimal.RequireFromString("0.6")}, {Name: "downside", Weight: decimal.RequireFromString("0.4")}},
		ExistingAllowance: allowance,
		Exposures: []VersionedExposure{
			{
				ID:            uuid.MustParse("10000000-0000-0000-0000-000000000001"),
				LedgerBalance: firstBalance, UndrawnCommitments: firstCommitments,
				ExistingAllowance: firstAllowance,
				Classification: HostClassification{
					MeasurementCategory: AmortisedCost, BusinessModelReference: "bm-1", SPPIAssessmentReference: "sppi-1",
				},
				RemainingLife: 2 * 365 * 24 * time.Hour, EffectiveInterestRate: decimal.RequireFromString("0.1"),
				LGD:           decimal.RequireFromString("0.5"),
				StageEvidence: StageEvidence{PreviousStage: Stage1, DaysPastDue: 31, SICR: true},
				ScenarioPDs: []ScenarioPDInput{
					{ScenarioName: "base", PD12Month: decimal.RequireFromString("0.1"), PDLifetime: decimal.RequireFromString("0.2")},
					{ScenarioName: "downside", PD12Month: decimal.RequireFromString("0.3"), PDLifetime: decimal.RequireFromString("0.5")},
				},
			},
			{
				ID:            uuid.MustParse("10000000-0000-0000-0000-000000000002"),
				LedgerBalance: secondBalance, UndrawnCommitments: secondCommitments,
				ExistingAllowance: secondAllowance,
				Classification: HostClassification{
					MeasurementCategory: AmortisedCost, BusinessModelReference: "bm-2", SPPIAssessmentReference: "sppi-2",
				},
				RemainingLife: 2 * 365 * 24 * time.Hour, EffectiveInterestRate: decimal.Zero,
				LGD:           decimal.RequireFromString("0.4"),
				StageEvidence: StageEvidence{PreviousStage: Stage3, CureConfirmed: true},
				ScenarioPDs: []ScenarioPDInput{
					{ScenarioName: "base", PD12Month: decimal.RequireFromString("0.1"), PDLifetime: decimal.RequireFromString("0.2")},
					{ScenarioName: "downside", PD12Month: decimal.RequireFromString("0.3"), PDLifetime: decimal.RequireFromString("0.5")},
				},
			},
		},
	}
}

func TestEvaluateVersionedECLGolden(t *testing.T) {
	input := versionedGoldenInput(t)
	result, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelVersion != input.ModelVersion || result.SourceDataID != input.SourceDataID ||
		result.SourceDataHash != input.SourceDataHash ||
		result.EvaluationDate != input.EvaluationDate {
		t.Fatalf("provenance not preserved in result: %+v", result)
	}
	if got := result.Exposures[0].ECL.Amount().StringFixed(2); got != "132.23" {
		t.Errorf("Stage 2 lifetime ECL = %s, want 132.23", got)
	}
	if got := result.Exposures[1].ECL.Amount().StringFixed(2); got != "36.00" {
		t.Errorf("cured Stage 1 12-month ECL = %s, want 36.00", got)
	}
	if got := result.RequiredAllowance.Amount().StringFixed(2); got != "168.23" {
		t.Errorf("required allowance = %s, want 168.23", got)
	}
	if got := result.AllowanceChange.Amount().StringFixed(2); got != "68.23" {
		t.Errorf("allowance change = %s, want 68.23", got)
	}
	if got := result.Exposures[0].Transition; got != "increased_credit_risk" {
		t.Errorf("Stage 1 to Stage 2 transition = %q", got)
	}
	if got := result.Exposures[1].Transition; got != "cured_credit_impaired" {
		t.Errorf("Stage 3 cure transition = %q", got)
	}
	if got := result.Exposures[1].Horizon; got != stage1MeasurementHorizon {
		t.Errorf("Stage 1 discount horizon = %s, want %s", got, stage1MeasurementHorizon)
	}
	if len(result.InputFingerprint) != 64 {
		t.Fatalf("input fingerprint %q is not a SHA-256 digest", result.InputFingerprint)
	}
	if result.InputFingerprint != "162bafe0f74ad06bdc92cf7c4b9f40c7f69a49229898adb5ed7dff480b73a1d9" {
		t.Errorf("golden input fingerprint = %s", result.InputFingerprint)
	}
}

func TestEvaluateVersionedECLFingerprintAndResultsAreOrderIndependent(t *testing.T) {
	input := versionedGoldenInput(t)
	first, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Exposures[0], input.Exposures[1] = input.Exposures[1], input.Exposures[0]
	input.Scenarios[0], input.Scenarios[1] = input.Scenarios[1], input.Scenarios[0]
	input.Assumptions[0], input.Assumptions[1] = input.Assumptions[1], input.Assumptions[0]
	input.Exposures[0].ScenarioPDs[0], input.Exposures[0].ScenarioPDs[1] =
		input.Exposures[0].ScenarioPDs[1], input.Exposures[0].ScenarioPDs[0]
	reordered, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.InputFingerprint != first.InputFingerprint ||
		reordered.RequiredAllowance.Amount().String() != first.RequiredAllowance.Amount().String() ||
		reordered.Exposures[0].ExposureID != first.Exposures[0].ExposureID {
		t.Fatalf("input order changed the deterministic result: first=%+v reordered=%+v", first, reordered)
	}

	input.ModelVersion = "credit-risk-v1.3"
	changed, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	if changed.InputFingerprint == first.InputFingerprint {
		t.Fatal("changing the model version did not change the input fingerprint")
	}
}

func TestEvaluateVersionedECLFingerprintTracksProvenanceAndAssumptions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*VersionedECLInput)
	}{
		{name: "source snapshot", mutate: func(input *VersionedECLInput) {
			input.SourceDataHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "evaluation date", mutate: func(input *VersionedECLInput) { input.EvaluationDate = input.EvaluationDate.AddDate(0, 0, 1) }},
		{name: "named assumption", mutate: func(input *VersionedECLInput) { input.Assumptions[4].Value = "revised scenario basis" }},
		{name: "scenario estimate", mutate: func(input *VersionedECLInput) {
			input.Exposures[0].ScenarioPDs[0].PDLifetime = decimal.RequireFromString("0.21")
		}},
		{name: "host classification evidence", mutate: func(input *VersionedECLInput) {
			input.Exposures[0].Classification.BusinessModelReference = "revised-host-assessment"
		}},
		{name: "undrawn commitment", mutate: func(input *VersionedECLInput) {
			input.Exposures[0].UndrawnCommitments, _ = financialtypes.NewMoneyFromString("110", "USD")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := versionedGoldenInput(t)
			first, err := EvaluateVersionedECL(input)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&input)
			changed, err := EvaluateVersionedECL(input)
			if err != nil {
				t.Fatal(err)
			}
			if changed.InputFingerprint == first.InputFingerprint {
				t.Fatal("changed input did not change the fingerprint")
			}
		})
	}
}

func TestVersionedECLSeparatesACAndFVOCIAllowances(t *testing.T) {
	input := versionedGoldenInput(t)
	input.Exposures[1].Classification.MeasurementCategory = FVOCI
	result, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CategoryChanges) != 2 {
		t.Fatalf("category changes = %d, want AC and FVOCI", len(result.CategoryChanges))
	}
	if result.CategoryChanges[0].Category != AmortisedCost ||
		result.CategoryChanges[0].AllowanceChange.Amount().StringFixed(2) != "72.23" ||
		result.CategoryChanges[1].Category != FVOCI ||
		result.CategoryChanges[1].AllowanceChange.Amount().StringFixed(2) != "-4.00" {
		t.Fatalf("category allowance movements = %+v", result.CategoryChanges)
	}
	accounts := ECLCategoryAccounts{
		AmortisedCost: ECLAccounts{ImpairmentLoss: uuid.New(), Allowance: uuid.New()},
		FVOCI:         ECLAccounts{ImpairmentLoss: uuid.New(), OCIReserve: uuid.New()},
	}
	entries, err := BuildVersionedECLEntries(result, accounts, uuid.New(), uuid.New(), "risk_officer")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("category entries = %d, want 2", len(entries))
	}
	if entries[0].Lines[1].AccountID != accounts.AmortisedCost.Allowance ||
		entries[1].Lines[0].AccountID != accounts.FVOCI.OCIReserve {
		t.Fatalf("category entries use incorrect allowance destinations: %+v", entries)
	}
	combined, err := BuildVersionedECLApplicationEntry(result, accounts, uuid.New(), uuid.New(), "risk_officer")
	if err != nil {
		t.Fatal(err)
	}
	if len(combined.Lines) != 4 {
		t.Fatalf("combined ECL journal lines = %d, want 4", len(combined.Lines))
	}
}

func TestEvaluateVersionedECLRequiresCompleteProvenanceAndAssumptions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*VersionedECLInput)
	}{
		{name: "model version", mutate: func(input *VersionedECLInput) { input.ModelVersion = "" }},
		{name: "source identifier", mutate: func(input *VersionedECLInput) { input.SourceDataID = "" }},
		{name: "source hash", mutate: func(input *VersionedECLInput) { input.SourceDataHash = "" }},
		{name: "malformed source hash", mutate: func(input *VersionedECLInput) { input.SourceDataHash = "sha256:not-a-digest" }},
		{name: "assumptions", mutate: func(input *VersionedECLInput) { input.Assumptions = nil }},
		{name: "stage assumptions", mutate: func(input *VersionedECLInput) { input.Assumptions[1].Value = "31" }},
		{name: "evaluation date", mutate: func(input *VersionedECLInput) { input.EvaluationDate = time.Time{} }},
		{name: "missing host classification", mutate: func(input *VersionedECLInput) {
			input.Exposures[0].Classification.SPPIAssessmentReference = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := versionedGoldenInput(t)
			test.mutate(&input)
			if _, err := EvaluateVersionedECL(input); !errors.Is(err, ErrInvalidVersionedInput) {
				t.Fatalf("EvaluateVersionedECL() error = %v, want ErrInvalidVersionedInput", err)
			}
		})
	}
}

func TestVersionedStageTransitionBackstopsAndCure(t *testing.T) {
	tests := []struct {
		name      string
		evidence  StageEvidence
		wantStage Stage
		wantMove  string
		wantErr   bool
	}{
		{name: "initial Stage 1", evidence: StageEvidence{}, wantStage: Stage1, wantMove: "initial"},
		{name: "SICR increases stage", evidence: StageEvidence{PreviousStage: Stage1, SICR: true}, wantStage: Stage2, wantMove: "increased_credit_risk"},
		{name: "30 day backstop boundary", evidence: StageEvidence{PreviousStage: Stage1, DaysPastDue: 30}, wantStage: Stage1, wantMove: "no_change"},
		{name: "31 day backstop", evidence: StageEvidence{PreviousStage: Stage1, DaysPastDue: 31}, wantStage: Stage2, wantMove: "increased_credit_risk"},
		{name: "90 day backstop boundary", evidence: StageEvidence{PreviousStage: Stage2, DaysPastDue: 90}, wantStage: Stage2, wantMove: "no_change"},
		{name: "91 day default backstop", evidence: StageEvidence{PreviousStage: Stage2, DaysPastDue: 91}, wantStage: Stage3, wantMove: "credit_impaired"},
		{name: "improved credit risk", evidence: StageEvidence{PreviousStage: Stage2}, wantStage: Stage1, wantMove: "improved_credit_risk"},
		{name: "default overrides other evidence", evidence: StageEvidence{PreviousStage: Stage1, Defaulted: true}, wantStage: Stage3, wantMove: "credit_impaired"},
		{name: "Stage 3 cannot cure implicitly", evidence: StageEvidence{PreviousStage: Stage3}, wantStage: Stage3, wantMove: "no_change"},
		{name: "explicit cure can improve stage", evidence: StageEvidence{PreviousStage: Stage3, CureConfirmed: true}, wantStage: Stage1, wantMove: "cured_credit_impaired"},
		{name: "cure confirmation only from Stage 3", evidence: StageEvidence{PreviousStage: Stage2, CureConfirmed: true}, wantErr: true},
		{name: "invalid previous stage", evidence: StageEvidence{PreviousStage: "stage_4"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, transition, err := assessStage(test.evidence)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidStageTransition) {
					t.Fatalf("assessStage() error = %v, want ErrInvalidStageTransition", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage != test.wantStage || transition != test.wantMove {
				t.Fatalf("assessStage() = (%s, %q), want (%s, %q)", stage, transition, test.wantStage, test.wantMove)
			}
		})
	}
}

func TestEvaluateVersionedECLRejectsIncompleteScenarioData(t *testing.T) {
	input := versionedGoldenInput(t)
	input.Scenarios[0].Weight = decimal.RequireFromString("0.5")
	if _, err := EvaluateVersionedECL(input); !errors.Is(err, ErrInvalidVersionedInput) {
		t.Fatalf("invalid scenario weights error = %v, want ErrInvalidVersionedInput", err)
	}

	input = versionedGoldenInput(t)
	input.Exposures[0].ScenarioPDs = input.Exposures[0].ScenarioPDs[:1]
	if _, err := EvaluateVersionedECL(input); !errors.Is(err, ErrInvalidVersionedInput) {
		t.Fatalf("incomplete scenario PDs error = %v, want ErrInvalidVersionedInput", err)
	}
}

func TestEvaluateVersionedECLRejectsFVTPL(t *testing.T) {
	input := versionedGoldenInput(t)
	input.Exposures[0].Classification.MeasurementCategory = FVTPL
	if _, err := EvaluateVersionedECL(input); !errors.Is(err, ErrECLNotApplicable) {
		t.Fatalf("FVTPL ECL error = %v, want ErrECLNotApplicable", err)
	}
}

type mockECLPostingCap struct {
	accounts  ECLAccounts
	available bool
}

func (m mockECLPostingCap) LedgerPostingAvailable() bool { return m.available }
func (m mockECLPostingCap) ResolveECLAccounts(_ context.Context, _ uuid.UUID, mappingID string) (ECLAccounts, error) {
	if mappingID != "ecl_standard" {
		return ECLAccounts{}, ErrECLJournalMappingUnavailable
	}
	return m.accounts, nil
}

type mockECLPostingApp struct {
	posted []posting.JournalEntry
	keys   map[string]posting.JournalEntry
}

func (m *mockECLPostingApp) PostECLEntry(_ context.Context, entry posting.JournalEntry, key string) error {
	if m.keys == nil {
		m.keys = make(map[string]posting.JournalEntry)
	}
	if existing, ok := m.keys[key]; ok {
		if existing.IdempotencyPayloadHash != entry.IdempotencyPayloadHash {
			return errors.New("idempotency conflict")
		}
		return nil // idempotent retry
	}
	m.posted = append(m.posted, entry)
	m.keys[key] = entry
	return nil
}

func TestGoldenScenario_GS09_IFRS9VersionedECLPostingAndIdempotency(t *testing.T) {
	input := versionedGoldenInput(t)
	result, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatalf("EvaluateVersionedECL: %v", err)
	}

	companyID := uuid.New()
	journalID := uuid.New()
	accounts := ECLAccounts{
		ImpairmentLoss: uuid.New(),
		Allowance:      uuid.New(),
	}

	cap := mockECLPostingCap{accounts: accounts, available: true}
	app := &mockECLPostingApp{}

	ctx := context.Background()
	entry1, err := PostVersionedECL(ctx, cap, app, result, companyID, journalID, "ecl_standard", "risk_officer")
	if err != nil {
		t.Fatalf("PostVersionedECL first attempt: %v", err)
	}

	if len(app.posted) != 1 {
		t.Fatalf("expected 1 posted entry, got %d", len(app.posted))
	}
	if entry1.Status != posting.EntryPosted {
		t.Fatalf("entry status = %s, want posted", entry1.Status)
	}

	// Verify balance
	var debits, credits decimal.Decimal
	for _, line := range entry1.Lines {
		debits = debits.Add(line.Debit.Amount())
		credits = credits.Add(line.Credit.Amount())
	}
	if !debits.Equal(credits) || !debits.Equal(result.AllowanceChange.Amount().Abs()) {
		t.Fatalf("ECL entry unbalanced or incorrect: debits=%s credits=%s, change=%s",
			debits, credits, result.AllowanceChange.Amount().Abs())
	}

	// Retry idempotency
	entry2, err := PostVersionedECL(ctx, cap, app, result, companyID, journalID, "ecl_standard", "risk_officer")
	if err != nil {
		t.Fatalf("PostVersionedECL retry attempt: %v", err)
	}
	if entry1.ID != entry2.ID || entry1.IdempotencyKey != entry2.IdempotencyKey {
		t.Fatalf("retry identity mismatch: %s vs %s", entry1.ID, entry2.ID)
	}
	if len(app.posted) != 1 {
		t.Fatalf("duplicate entry posted to application store on retry: count=%d", len(app.posted))
	}
}
