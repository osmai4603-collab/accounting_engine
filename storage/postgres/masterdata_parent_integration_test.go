package postgres_test

import (
	"context"
	"errors"
	"testing"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
)

func TestUpdateAccountParentPostgresCompanyScope(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("Skipping PostgreSQL test: %v", err)
	}
	defer driver.Close()

	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "TREE-" + companyID.String()[:8], LegalName: "Account Tree Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))

	rootA := masterdata.Account{
		CompanyID: companyID, ID: uuid.New(), Code: "1000", Name: "Root A",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}
	rootB := masterdata.Account{
		CompanyID: companyID, ID: uuid.New(), Code: "1100", Name: "Root B",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}
	child := masterdata.Account{
		CompanyID: companyID, ID: uuid.New(), Code: "1001", Name: "Child",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
		ParentID: &rootA.ID,
	}
	for _, account := range []masterdata.Account{rootA, rootB, child} {
		if err := service.RegisterAccount(account); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.UpdateAccountParentContext(ctx, child, &rootB.ID); err != nil {
		t.Fatalf("update account parent: %v", err)
	}
	updated, err := driver.GetAccountByIDForCompanyContext(ctx, companyID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentID == nil || *updated.ParentID != rootB.ID {
		t.Fatalf("updated parent = %v, want %s", updated.ParentID, rootB.ID)
	}

	if err := service.UpdateAccountParentContext(ctx, rootB, &child.ID); !errors.Is(err, masterdata.ErrAccountCycle) {
		t.Fatalf("cyclic parent update error = %v, want ErrAccountCycle", err)
	}
}
