package postgres_test

import (
	"context"
	"testing"
	"time"

	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/masterdata"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func TestPostgresRevenueContractsStorageAndIsolation(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated postgres driver unavailable: %v", err)
	}

	companyA := uuid.New()
	companyB := uuid.New()
	for _, cid := range []uuid.UUID{companyA, companyB} {
		if err := driver.CreateCompany(ctx, masterdata.Company{
			ID:                   cid,
			Code:                 cid.String()[:8],
			LegalName:            "IFRS 15 Tenant " + cid.String()[:8],
			FunctionalCurrency:   "SAR",
			PresentationCurrency: "SAR",
			Active:               true,
		}); err != nil {
			t.Fatalf("create company %s: %v", cid, err)
		}
	}

	priceA, _ := financialtypes.NewMoneyFromString("12000.00", "SAR")
	ssp1, _ := financialtypes.NewMoneyFromString("8000.00", "SAR")
	ssp2, _ := financialtypes.NewMoneyFromString("4000.00", "SAR")

	ob1ID := uuid.New()
	ob2ID := uuid.New()
	contractA, err := ifrs15.NewContract(
		uuid.New(),
		uuid.New(),
		priceA,
		[]ifrs15.PerformanceObligation{
			{
				ID:                 ob1ID,
				Description:        "Platform License",
				StandalonePrice:    ssp1,
				RecognitionPattern: ifrs15.PointInTime,
				StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:            time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				ID:                 ob2ID,
				Description:        "Annual Support",
				StandalonePrice:    ssp2,
				RecognitionPattern: ifrs15.OverTime,
				StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:            time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			},
		},
	)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}

	// 1. Save and Get in Company A
	if err := driver.SaveContract(ctx, companyA, contractA); err != nil {
		t.Fatalf("save contract A: %v", err)
	}

	gotA, err := driver.GetContract(ctx, companyA, contractA.ID)
	if err != nil {
		t.Fatalf("get contract A: %v", err)
	}
	if gotA.ID != contractA.ID || gotA.Currency != "SAR" {
		t.Fatalf("contract mismatch: got %+v, want %+v", gotA, contractA)
	}
	if len(gotA.Obligations) != 2 {
		t.Fatalf("expected 2 obligations, got %d", len(gotA.Obligations))
	}

	// 2. Cross-company isolation: Company B cannot see Company A's contract
	listB, err := driver.ListContracts(ctx, companyB)
	if err != nil {
		t.Fatalf("list contracts company B: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("expected 0 contracts for company B due to RLS, got %d", len(listB))
	}

	_, err = driver.GetContract(ctx, companyB, contractA.ID)
	if err == nil {
		t.Fatalf("expected error reading company A contract from company B context, got nil")
	}

	// 3. List in Company A
	listA, err := driver.ListContracts(ctx, companyA)
	if err != nil {
		t.Fatalf("list contracts company A: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != contractA.ID {
		t.Fatalf("expected 1 contract for company A, got %+v", listA)
	}

	// 4. Delete
	if err := driver.DeleteContract(ctx, companyA, contractA.ID); err != nil {
		t.Fatalf("delete contract A: %v", err)
	}
	_, err = driver.GetContract(ctx, companyA, contractA.ID)
	if err == nil {
		t.Fatalf("expected error after delete, got nil")
	}
}
