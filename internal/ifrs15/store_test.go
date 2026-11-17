package ifrs15

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestInMemoryContractStore_CRUD(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryContractStore()
	companyA := uuid.New()
	companyB := uuid.New()

	obl := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Subscription",
		StandalonePrice:    money(t, "500.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	contractA, err := NewContract(uuid.New(), uuid.New(), money(t, "500.00", "USD"), []PerformanceObligation{obl})
	if err != nil {
		t.Fatal(err)
	}

	// Save to company A
	if err := store.SaveContract(ctx, companyA, contractA); err != nil {
		t.Fatalf("SaveContract failed: %v", err)
	}

	// Retrieve from company A
	got, err := store.GetContract(ctx, companyA, contractA.ID)
	if err != nil {
		t.Fatalf("GetContract failed: %v", err)
	}
	if got.ID != contractA.ID {
		t.Errorf("contract ID = %s, want %s", got.ID, contractA.ID)
	}

	// Isolation: company B cannot get company A's contract
	_, err = store.GetContract(ctx, companyB, contractA.ID)
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("expected ErrContractNotFound for company B, got %v", err)
	}

	// List
	list, err := store.ListContracts(ctx, companyA)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListContracts failed: list=%+v, err=%v", list, err)
	}

	// Mutation of returned contract does not affect store
	got.Obligations[0].Description = "corrupted in memory"
	retrievedAgain, _ := store.GetContract(ctx, companyA, contractA.ID)
	if retrievedAgain.Obligations[0].Description == "corrupted in memory" {
		t.Error("store was mutated by returned pointer/copy")
	}

	// Delete
	if err := store.DeleteContract(ctx, companyA, contractA.ID); err != nil {
		t.Fatalf("DeleteContract failed: %v", err)
	}
	_, err = store.GetContract(ctx, companyA, contractA.ID)
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("expected ErrContractNotFound after delete, got %v", err)
	}
}

func TestDurableWorkflowRecoveryAcrossRestart(t *testing.T) {
	// Acceptance criterion:
	// "استمرارية حالة العقد وإمكانية استئناف القياس بعد إعادة تشغيل المحرك."
	// Simulate engine restart:
	// 1. Workflow 1 starts, saves contract, recognizes part of obligation.
	// 2. Workflow 1 is destroyed/closed.
	// 3. Workflow 2 is instantiated with the same persistent store.
	// 4. Workflow 2 resumes recognition from durable state and completes the contract.
	ctx := context.Background()
	store := NewInMemoryContractStore()
	mapping := testMapping()
	app := &testRevenueApplication{}

	workflow1, err := NewRevenueWorkflow(mapping, app, WithWorkflowContractStore(store))
	if err != nil {
		t.Fatal(err)
	}

	obl := obligation("2026-01-01", "2026-12-31", OverTime, "1000")
	input := assessedInput(t, obl)
	contract, err := workflow1.AddContract(input)
	if err != nil {
		t.Fatalf("AddContract failed: %v", err)
	}
	if contract.Status != ContractStatusActive {
		t.Fatalf("expected initial status Active, got %s", contract.Status)
	}

	// First recognition of $400
	event1 := SatisfactionEvent{
		ID:           uuid.New(),
		ObligationID: obl.ID,
		Amount:       money(t, "400.00", "USD"),
		Date:         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Evidence: ServiceDeliveryEvidence{
			Satisfied:         true,
			MeasureBasis:      "Q1 Milestone certification",
			EvidenceReference: "QC-2026-01",
		},
	}
	updated1, _, err := workflow1.Recognize(ctx, contract.ID, event1)
	if err != nil {
		t.Fatalf("first recognition failed: %v", err)
	}
	if !updated1.RecognizedAmount.Amount().Equal(decimal.RequireFromString("400.00")) {
		t.Fatalf("recognized amount = %s, want 400.00", updated1.RecognizedAmount.Amount())
	}
	if updated1.Status != ContractStatusActive {
		t.Errorf("status = %s, want Active", updated1.Status)
	}

	// --- SIMULATE PROCESS RESTART ---
	// workflow1 is discarded. A new workflow is constructed with the SAME durable store.
	workflowRestarted, err := NewRevenueWorkflow(mapping, app, WithWorkflowContractStore(store))
	if err != nil {
		t.Fatalf("restarted workflow creation failed: %v", err)
	}

	// Verify contract recovered
	recovered, ok := workflowRestarted.Contract(contract.ID)
	if !ok {
		t.Fatalf("restarted workflow failed to find contract %s", contract.ID)
	}
	if !recovered.RecognizedAmount.Amount().Equal(decimal.RequireFromString("400.00")) {
		t.Fatalf("recovered recognized amount = %s, want 400.00", recovered.RecognizedAmount.Amount())
	}

	// Attempting to recognize $700 must fail because 400 + 700 = 1100 > 1000
	eventInvalid := SatisfactionEvent{
		ID:           uuid.New(),
		ObligationID: obl.ID,
		Amount:       money(t, "700.00", "USD"),
		Date:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Evidence: ServiceDeliveryEvidence{
			Satisfied:         true,
			MeasureBasis:      "Exceeded measure",
			EvidenceReference: "QC-ERR",
		},
	}
	_, _, err = workflowRestarted.Recognize(ctx, contract.ID, eventInvalid)
	if !errors.Is(err, ErrRecognitionExceeded) {
		t.Fatalf("expected ErrRecognitionExceeded, got %v", err)
	}

	// Recognize remaining $600 to satisfy contract
	event2 := SatisfactionEvent{
		ID:           uuid.New(),
		ObligationID: obl.ID,
		Amount:       money(t, "600.00", "USD"),
		Date:         time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Evidence: ServiceDeliveryEvidence{
			Satisfied:         true,
			MeasureBasis:      "Final project completion and acceptance",
			EvidenceReference: "QC-2026-FINAL",
		},
	}
	finalContract, _, err := workflowRestarted.Recognize(ctx, contract.ID, event2)
	if err != nil {
		t.Fatalf("second recognition failed on restarted workflow: %v", err)
	}
	if !finalContract.RecognizedAmount.Amount().Equal(decimal.RequireFromString("1000.00")) {
		t.Errorf("final recognized amount = %s, want 1000.00", finalContract.RecognizedAmount.Amount())
	}
	if finalContract.Status != ContractStatusSatisfied {
		t.Errorf("final status = %s, want %s", finalContract.Status, ContractStatusSatisfied)
	}

	// Verify store also holds the satisfied status
	persistedFinal, err := store.GetContract(ctx, mapping.CompanyID, contract.ID)
	if err != nil {
		t.Fatalf("store.GetContract failed: %v", err)
	}
	if persistedFinal.Status != ContractStatusSatisfied {
		t.Errorf("persisted status = %s, want Satisfied", persistedFinal.Status)
	}
}
