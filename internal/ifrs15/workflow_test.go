package ifrs15

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/shopspring/decimal"
)

type testRevenueApplication struct {
	err     error
	entries []posting.JournalEntry
	keys    []string
}

func (a *testRevenueApplication) PostRevenueEntry(_ context.Context, entry posting.JournalEntry, key string) error {
	a.entries = append(a.entries, entry)
	a.keys = append(a.keys, key)
	return a.err
}

func testMapping() RevenueJournalMapping {
	return RevenueJournalMapping{
		CompanyID: uuid.New(), JournalID: uuid.New(), RevenueAccountID: uuid.New(),
		ContractAssetAccountID: uuid.New(), ContractLiabilityAccount: uuid.New(),
	}
}

func assessedInput(t *testing.T, obligations ...PerformanceObligation) FiveStepAssessment {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(obligations))
	for _, obligation := range obligations {
		ids = append(ids, obligation.ID)
	}
	return FiveStepAssessment{
		ContractID: uuid.New(), CustomerID: uuid.New(),
		Approved: true, EnforceableRightsIdentified: true, PaymentTermsIdentified: true,
		CommercialSubstance: true, CollectibilityProbable: true, FixedTransactionPrice: true,
		DistinctObligationIDs: ids,
		TransactionPrice:      money(t, "1000.00", "USD"), Obligations: obligations,
	}
}

func TestAssessedContractValidatesFiveStepInputs(t *testing.T) {
	first := obligation("2025-01-01", "2025-01-01", PointInTime, "800")
	second := obligation("2025-01-01", "2025-12-31", OverTime, "400")
	input := assessedInput(t, first, second)
	contract, err := NewAssessedContract(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := contract.AllocatedPrices[first.ID].Amount(); !got.Equal(decimal.RequireFromString("666.67")) {
		t.Errorf("step-four allocation = %s, want 666.67", got)
	}
	if got := contract.AllocatedPrices[second.ID].Amount(); !got.Equal(decimal.RequireFromString("333.33")) {
		t.Errorf("step-four allocation = %s, want 333.33", got)
	}

	tests := []struct {
		name   string
		change func(*FiveStepAssessment)
	}{
		{"contract not approved", func(v *FiveStepAssessment) { v.Approved = false }},
		{"rights not identified", func(v *FiveStepAssessment) { v.EnforceableRightsIdentified = false }},
		{"payment terms missing", func(v *FiveStepAssessment) { v.PaymentTermsIdentified = false }},
		{"no commercial substance", func(v *FiveStepAssessment) { v.CommercialSubstance = false }},
		{"collectibility not probable", func(v *FiveStepAssessment) { v.CollectibilityProbable = false }},
		{"price not fixed", func(v *FiveStepAssessment) { v.FixedTransactionPrice = false }},
		{"variable consideration unsupported", func(v *FiveStepAssessment) { v.HasVariableConsideration = true }},
		{"financing unsupported", func(v *FiveStepAssessment) { v.HasSignificantFinancing = true }},
		{"distinct assessment missing", func(v *FiveStepAssessment) { v.DistinctObligationIDs = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := assessedInput(t, first)
			test.change(&invalid)
			if _, err := NewAssessedContract(invalid); !errors.Is(err, ErrRevenueAssessmentRequired) {
				t.Fatalf("NewAssessedContract error = %v, want assessment error", err)
			}
		})
	}
}

func TestRevenueWorkflowRequiresMappingAndApplication(t *testing.T) {
	if _, err := NewRevenueWorkflow(RevenueJournalMapping{}, &testRevenueApplication{}); !errors.Is(err, ErrRevenueMappingRequired) {
		t.Fatalf("workflow with no mapping error = %v", err)
	}
	if _, err := NewRevenueWorkflow(testMapping(), nil); !errors.Is(err, ErrRevenueApplicationRequired) {
		t.Fatalf("workflow with no posting application error = %v", err)
	}
	po := obligation("2025-01-01", "2025-01-01", PointInTime, "100")
	workflow, err := NewRevenueWorkflow(testMapping(), &testRevenueApplication{})
	if err != nil {
		t.Fatal(err)
	}
	input := assessedInput(t, po)
	input.Approved = false
	if _, err := workflow.AddContract(input); !errors.Is(err, ErrRevenueAssessmentRequired) {
		t.Fatalf("AddContract without five-step assessment error = %v", err)
	}
	input = assessedInput(t, po)
	contract, err := workflow.AddContract(input)
	if err != nil {
		t.Fatal(err)
	}
	contract.Obligations[0].Description = "changed after registration"
	stored, ok := workflow.Contract(contract.ID)
	if !ok || stored.Obligations[0].Description == contract.Obligations[0].Description {
		t.Fatal("returned contract mutation changed registered workflow state")
	}
}

func TestRecognitionCommitsOnlyAfterPostingAndRetriesIdempotently(t *testing.T) {
	po := obligation("2025-01-01", "2025-12-31", OverTime, "100")
	app := &testRevenueApplication{err: errors.New("temporary posting failure")}
	workflow, err := NewRevenueWorkflow(testMapping(), app)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := workflow.AddContract(assessedInput(t, po))
	if err != nil {
		t.Fatal(err)
	}
	event := SatisfactionEvent{
		ID: uuid.New(), ObligationID: po.ID, Amount: money(t, "25", "USD"),
		Date: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		Evidence: ServiceDeliveryEvidence{
			Satisfied: true, MeasureBasis: "host-certified progress measure",
			EvidenceReference: "delivery-record-042",
		},
	}
	if _, _, err := workflow.Recognize(context.Background(), contract.ID, event); err == nil {
		t.Fatal("expected posting failure")
	}
	afterFailure, ok := workflow.Contract(contract.ID)
	if !ok || !afterFailure.RecognizedAmount.Amount().IsZero() {
		t.Fatalf("failed posting confirmed contract state: found=%t recognized=%v", ok, afterFailure.RecognizedAmount)
	}

	app.err = nil
	updated, entry, err := workflow.Recognize(context.Background(), contract.ID, event)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.RecognizedAmount.Amount().Equal(decimal.NewFromInt(25)) {
		t.Fatalf("recognized amount = %s, want 25", updated.RecognizedAmount.Amount())
	}
	if len(app.entries) != 2 || app.entries[0].ID != app.entries[1].ID || app.keys[0] != app.keys[1] {
		t.Fatalf("retry did not reuse journal identity and idempotency key: entries=%v keys=%v", len(app.entries), app.keys)
	}
	if entry.ID != app.entries[1].ID || entry.IdempotencyKey != app.keys[1] {
		t.Fatalf("returned entry not the posted retry entry: %+v", entry)
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("recognition journal invalid: %v", err)
	}
	if entry.CompanyID != workflow.mapping.CompanyID || entry.JournalID != workflow.mapping.JournalID ||
		entry.Lines[0].AccountID != workflow.mapping.ContractAssetAccountID ||
		entry.Lines[len(entry.Lines)-1].AccountID != workflow.mapping.RevenueAccountID {
		t.Fatalf("journal does not use configured real mapping: %+v", entry)
	}

	again, duplicate, err := workflow.Recognize(context.Background(), contract.ID, event)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.entries) != 2 || duplicate.ID != entry.ID ||
		!again.RecognizedAmount.Amount().Equal(updated.RecognizedAmount.Amount()) {
		t.Fatal("successful duplicate event was posted or confirmed more than once")
	}
}

func TestRecognitionRequiresExternalSatisfactionEvidenceAndRejectsEventReuse(t *testing.T) {
	po := obligation("2025-01-01", "2025-01-01", PointInTime, "100")
	app := &testRevenueApplication{}
	workflow, err := NewRevenueWorkflow(testMapping(), app)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := workflow.AddContract(assessedInput(t, po))
	if err != nil {
		t.Fatal(err)
	}
	event := SatisfactionEvent{
		ID: uuid.New(), ObligationID: po.ID, Amount: money(t, "10", "USD"),
		Date: time.Now(), Evidence: ServiceDeliveryEvidence{Satisfied: true, MeasureBasis: "completion"},
	}
	if _, _, err := workflow.Recognize(context.Background(), contract.ID, event); !errors.Is(err, ErrRevenueEvidenceRequired) {
		t.Fatalf("missing evidence error = %v", err)
	}
	if len(app.entries) != 0 {
		t.Fatal("missing external evidence reached posting application")
	}
	event.Evidence.EvidenceReference = "completion-record-1"
	if _, _, err := workflow.Recognize(context.Background(), contract.ID, event); err != nil {
		t.Fatal(err)
	}
	event.Amount = mustMoney("11", "USD")
	if _, _, err := workflow.Recognize(context.Background(), contract.ID, event); !errors.Is(err, ErrRevenueEventConflict) {
		t.Fatalf("reused event ID error = %v, want conflict", err)
	}
	if len(app.entries) != 1 {
		t.Fatalf("conflicting duplicate reached posting application %d times", len(app.entries))
	}
}

func TestAssessedContractRejectsObligationSetMismatch(t *testing.T) {
	po := obligation("2025-01-01", "2025-01-01", PointInTime, "100")
	input := assessedInput(t, po)
	input.DistinctObligationIDs = []uuid.UUID{uuid.New()}
	if _, err := NewAssessedContract(input); !errors.Is(err, ErrRevenueAssessmentRequired) {
		t.Fatalf("obligation mismatch error = %v, want assessment error", err)
	}
}

func TestRecognitionRequiresNonemptyServiceEvidence(t *testing.T) {
	po := obligation("2025-01-01", "2025-01-01", PointInTime, "100")
	workflow, err := NewRevenueWorkflow(testMapping(), &testRevenueApplication{})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := workflow.AddContract(assessedInput(t, po))
	if err != nil {
		t.Fatal(err)
	}
	event := SatisfactionEvent{
		ID: uuid.New(), ObligationID: po.ID, Amount: mustMoney("1", "USD"), Date: time.Now(),
		Evidence: ServiceDeliveryEvidence{Satisfied: true, MeasureBasis: "externally measured"},
	}
	if _, _, err := workflow.Recognize(context.Background(), contract.ID, event); !errors.Is(err, ErrRevenueEvidenceRequired) {
		t.Fatalf("missing evidence reference error = %v, want evidence required", err)
	}
}

func TestGoldenScenario_GS07_IFRS15RevenueRecognition(t *testing.T) {
	licencePO := obligation("2026-01-01", "2026-01-01", PointInTime, "8000")
	licencePO.Description = "Software Licence"
	supportPO := obligation("2026-01-01", "2026-12-31", OverTime, "4000")
	supportPO.Description = "Maintenance & Support"

	input := assessedInput(t, licencePO, supportPO)
	input.TransactionPrice = money(t, "10000.00", "USD")

	app := &testRevenueApplication{}
	mapping := testMapping()
	workflow, err := NewRevenueWorkflow(mapping, app)
	if err != nil {
		t.Fatalf("NewRevenueWorkflow: %v", err)
	}

	contract, err := workflow.AddContract(input)
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	// Verify Relative-SSP Allocation
	allocatedLicence := contract.AllocatedPrices[licencePO.ID].Amount()
	allocatedSupport := contract.AllocatedPrices[supportPO.ID].Amount()
	if !allocatedLicence.Equal(decimal.NewFromInt(666667).Shift(-2)) {
		t.Fatalf("Licence allocation = %s, want 6666.67", allocatedLicence)
	}
	if !allocatedSupport.Equal(decimal.NewFromInt(333333).Shift(-2)) {
		t.Fatalf("Support allocation = %s, want 3333.33", allocatedSupport)
	}
	if !allocatedLicence.Add(allocatedSupport).Equal(decimal.NewFromInt(10000)) {
		t.Fatalf("Sum of allocations %s != 10000.00", allocatedLicence.Add(allocatedSupport))
	}

	// Step 5: Deliver licence and recognize revenue
	eventID := uuid.New()
	event := SatisfactionEvent{
		ID:           eventID,
		ObligationID: licencePO.ID,
		Amount:       contract.AllocatedPrices[licencePO.ID],
		Date:         time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		Evidence: ServiceDeliveryEvidence{
			EvidenceReference: "DLV-2026-001",
			Satisfied:         true,
			MeasureBasis:      "customer_delivery_signoff",
		},
	}

	updated, entry, err := workflow.Recognize(context.Background(), contract.ID, event)
	if err != nil {
		t.Fatalf("Recognize licence revenue: %v", err)
	}
	if len(app.entries) != 1 {
		t.Fatalf("expected 1 posted entry, got %d", len(app.entries))
	}
	if !updated.RecognizedAmount.Amount().Equal(allocatedLicence) {
		t.Fatalf("contract recognized = %s, want %s", updated.RecognizedAmount.Amount(), allocatedLicence)
	}

	// Validate balanced entry
	var debitTotal, creditTotal decimal.Decimal
	for _, line := range entry.Lines {
		debitTotal = debitTotal.Add(line.Debit.Amount())
		creditTotal = creditTotal.Add(line.Credit.Amount())
	}
	if !debitTotal.Equal(creditTotal) || !debitTotal.Equal(decimal.NewFromInt(666667).Shift(-2)) {
		t.Fatalf("entry not balanced: debit=%s credit=%s", debitTotal, creditTotal)
	}

	// Retry idempotency
	_, cachedEntry, err := workflow.Recognize(context.Background(), contract.ID, event)
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if cachedEntry.ID != entry.ID || len(app.entries) != 1 {
		t.Fatalf("retry caused duplicate posting: entries=%d", len(app.entries))
	}
}

var _ RevenuePostingApplication = (*testRevenueApplication)(nil)
