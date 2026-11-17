package ifrs15

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestContractModification_TypeA_SeparateContract(t *testing.T) {
	// IFRS 15.20: An entity accounts for a contract modification as a separate contract
	// if the scope increases because of additional distinct goods/services at standalone selling prices.
	obl1 := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Product A - 100 units",
		StandalonePrice:    money(t, "10000.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	existingContract, err := NewContract(uuid.New(), uuid.New(), money(t, "10000.00", "USD"), []PerformanceObligation{obl1})
	if err != nil {
		t.Fatal(err)
	}

	// Modification: Customer orders 50 additional distinct units of Product B for $5,000 (reflects SSP)
	obl2 := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Product B - 50 units",
		StandalonePrice:    money(t, "5000.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	mod := ContractModification{
		ID:                    uuid.New(),
		ContractID:            existingContract.ID,
		Type:                  ModificationSeparateContract,
		Date:                  time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Description:           "Additional 50 units at standalone price",
		AdditionalPrice:       money(t, "5000.00", "USD"),
		AdditionalObligations: []PerformanceObligation{obl2},
	}

	newContractID := uuid.New()
	orig, newContract, err := ApplySeparateContractModification(existingContract, mod, newContractID)
	if err != nil {
		t.Fatalf("ApplySeparateContractModification failed: %v", err)
	}

	// Original contract remains unchanged
	if !orig.TransactionPrice.Amount().Equal(decimal.RequireFromString("10000.00")) {
		t.Errorf("original price changed = %s", orig.TransactionPrice.Amount())
	}
	if len(orig.Obligations) != 1 {
		t.Errorf("original obligations changed = %d", len(orig.Obligations))
	}

	// New contract is distinct
	if newContract.ID != newContractID {
		t.Errorf("new contract ID = %s, want %s", newContract.ID, newContractID)
	}
	if !newContract.TransactionPrice.Amount().Equal(decimal.RequireFromString("5000.00")) {
		t.Errorf("new contract price = %s, want 5000.00", newContract.TransactionPrice.Amount())
	}
	if len(newContract.Obligations) != 1 || newContract.Obligations[0].ID != obl2.ID {
		t.Errorf("new contract obligations mismatch: %+v", newContract.Obligations)
	}
}

func TestContractModification_TypeB_Prospective(t *testing.T) {
	// IFRS 15.21(a): Prospective treatment (termination of existing contract & creation of new contract)
	// when remaining goods/services are distinct.
	// Contract: 120 products for $12,000 ($100 each).
	// Obligation 1: 60 products transferred in month 1 ($6,000 recognized).
	// Obligation 2: 60 products remaining to transfer.
	// Modification: Deliver 30 additional products for $2,400 ($80 each, discounted below SSP).
	// Total remaining consideration = $6,000 (remaining old) + $2,400 (new) = $8,400.
	// Allocated to remaining 90 products (60 old + 30 new).
	obl1 := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Batch 1 (60 units)",
		StandalonePrice:    money(t, "6000.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	obl2 := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Batch 2 (60 units)",
		StandalonePrice:    money(t, "6000.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
	}
	contract, err := NewContract(uuid.New(), uuid.New(), money(t, "12000.00", "USD"), []PerformanceObligation{obl1, obl2})
	if err != nil {
		t.Fatal(err)
	}

	// Recognize obl1 ($6,000)
	contract, err = contract.ApplyRecognition(money(t, "6000.00", "USD"))
	if err != nil {
		t.Fatal(err)
	}
	contract.RecognizedPrices[obl1.ID] = money(t, "6000.00", "USD")

	// Additional obligation: Batch 3 (30 units with standalone price $3,000)
	obl3 := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Batch 3 (30 units)",
		StandalonePrice:    money(t, "3000.00", "USD"),
		RecognitionPattern: PointInTime,
		StartDate:          time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	}
	mod := ContractModification{
		ID:                    uuid.New(),
		ContractID:            contract.ID,
		Type:                  ModificationProspective,
		Date:                  time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Description:           "Additional 30 units at discounted bundle rate",
		AdditionalPrice:       money(t, "2400.00", "USD"),
		AdditionalObligations: []PerformanceObligation{obl3},
	}

	modifiedContract, err := ApplyProspectiveModification(contract, mod)
	if err != nil {
		t.Fatalf("ApplyProspectiveModification failed: %v", err)
	}

	// 1. Previously recognized revenue remains unchanged ($6,000)
	if !modifiedContract.RecognizedAmount.Amount().Equal(decimal.RequireFromString("6000.00")) {
		t.Errorf("recognized amount changed = %s, want 6000.00", modifiedContract.RecognizedAmount.Amount())
	}
	// 2. New total transaction price = 6,000 recognized + 8,400 remaining = $14,400
	wantNewPrice := decimal.RequireFromString("14400.00")
	if !modifiedContract.TransactionPrice.Amount().Equal(wantNewPrice) {
		t.Errorf("new transaction price = %s, want %s", modifiedContract.TransactionPrice.Amount(), wantNewPrice)
	}
	// 3. Status must be Modified
	if modifiedContract.Status != ContractStatusModified {
		t.Errorf("status = %s, want %s", modifiedContract.Status, ContractStatusModified)
	}
	// 4. Remaining consideration ($8,400) allocated to obl2 (SSP $6,000) and obl3 (SSP $3,000):
	// Total SSP = 9,000.
	// obl2 gets 8,400 * 6,000 / 9,000 = $5,600.
	// obl3 gets 8,400 * 3,000 / 9,000 = $2,800.
	allocObl2 := modifiedContract.AllocatedPrices[obl2.ID]
	allocObl3 := modifiedContract.AllocatedPrices[obl3.ID]
	if !allocObl2.Amount().Equal(decimal.RequireFromString("5600.00")) {
		t.Errorf("obl2 allocation = %s, want 5600.00", allocObl2.Amount())
	}
	if !allocObl3.Amount().Equal(decimal.RequireFromString("2800.00")) {
		t.Errorf("obl3 allocation = %s, want 2800.00", allocObl3.Amount())
	}
}

func TestContractModification_TypeC_CumulativeCatchUp(t *testing.T) {
	// IFRS 15.21(b): Cumulative catch-up adjustment when remaining goods/services are not distinct.
	// Contract: Construction of a commercial building for $1,000,000.
	// Progress to date = 60%. Recognized revenue = $600,000.
	// Modification: Scope and specification changes increase contract price by $200,000 to $1,200,000.
	// Revised progress = 60%.
	// Cumulative revenue that should be recognized = 1,200,000 * 60% = $720,000.
	// Cumulative catch-up adjustment to revenue = $720,000 - $600,000 = $120,000.
	obl := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Commercial building construction",
		StandalonePrice:    money(t, "1000000.00", "USD"),
		RecognitionPattern: OverTime,
		StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:            time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	contract, err := NewContract(uuid.New(), uuid.New(), money(t, "1000000.00", "USD"), []PerformanceObligation{obl})
	if err != nil {
		t.Fatal(err)
	}

	// Recognize 60% ($600,000)
	contract, err = contract.ApplyRecognition(money(t, "600000.00", "USD"))
	if err != nil {
		t.Fatal(err)
	}

	progress60 := decimal.RequireFromString("0.60")
	mod := ContractModification{
		ID:              uuid.New(),
		ContractID:      contract.ID,
		Type:            ModificationCumulativeCatchUp,
		Date:            time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Description:     "Specification enhancement approved by customer",
		AdditionalPrice: money(t, "200000.00", "USD"),
		UpdatedProgress: &progress60,
	}

	journalID := uuid.New()
	revenueAcct := uuid.New()
	contractAssetAcct := uuid.New()
	contractLiabilityAcct := uuid.New()

	modified, entry, err := ApplyCumulativeCatchUpModification(
		contract, mod, journalID, revenueAcct, contractAssetAcct, contractLiabilityAcct,
	)
	if err != nil {
		t.Fatalf("ApplyCumulativeCatchUpModification failed: %v", err)
	}

	// Verify contract state
	wantPrice := decimal.RequireFromString("1200000.00")
	if !modified.TransactionPrice.Amount().Equal(wantPrice) {
		t.Errorf("transaction price = %s, want %s", modified.TransactionPrice.Amount(), wantPrice)
	}
	wantRecognized := decimal.RequireFromString("720000.00")
	if !modified.RecognizedAmount.Amount().Equal(wantRecognized) {
		t.Errorf("recognized amount = %s, want %s", modified.RecognizedAmount.Amount(), wantRecognized)
	}
	if modified.Status != ContractStatusModified {
		t.Errorf("status = %s, want %s", modified.Status, ContractStatusModified)
	}

	// Verify journal entry: Dr Contract Asset $120,000, Cr Revenue $120,000
	if len(entry.Lines) != 2 {
		t.Fatalf("expected 2 journal lines, got %d", len(entry.Lines))
	}
	wantCatchUp := decimal.RequireFromString("120000.00")
	if entry.Lines[0].AccountID != contractAssetAcct || !entry.Lines[0].Debit.Amount().Equal(wantCatchUp) {
		t.Errorf("expected debit to contract asset %s, got %+v", wantCatchUp, entry.Lines[0])
	}
	if entry.Lines[1].AccountID != revenueAcct || !entry.Lines[1].Credit.Amount().Equal(wantCatchUp) {
		t.Errorf("expected credit to revenue %s, got %+v", wantCatchUp, entry.Lines[1])
	}
}
