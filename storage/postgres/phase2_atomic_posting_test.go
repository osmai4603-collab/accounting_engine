package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func setupPostgresPostingEnv(t *testing.T) (context.Context, *postgres.Driver, *masterdata.Service, *posting.Engine, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL test: %v", err)
	}

	companyID, journalID := uuid.New(), uuid.New()
	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("P2-%s", companyID.String()[:8]),
		LegalName: "Phase 2 Test Co", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code) VALUES($1, $2, $3)`,
			journalID, companyID, fmt.Sprintf("J-%s", journalID.String()[:8]))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	acc1ID, acc2ID := uuid.New(), uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc1ID, Code: "1000", Name: "Asset Acc",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc2ID, Code: "2000", Name: "Expense Acc",
		AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	engine := posting.NewEngine(driver, masterData)
	return ctx, driver, masterData, engine, companyID, journalID, acc1ID, acc2ID
}

func TestT2_1_AtomicAssetDepreciationPostgres(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupPostgresPostingEnv(t)
	defer driver.Close()

	cost, _ := financialtypes.NewMoneyFromString("24000.00", "USD")
	salvage, _ := financialtypes.Zero("USD")
	assetID := uuid.New()

	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: salvage,
		UsefulLifeMonths: 24, Method: subledgers.StraightLine,
		AccumulatedDepreciation: salvage,
	}
	if err := driver.SaveFixedAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}

	faLedger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)

	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	entry, err := faLedger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, "poster")
	if err != nil {
		t.Fatalf("unexpected error in DepreciateAssetAtomic: %v", err)
	}

	if entry.Status != posting.EntryPosted {
		t.Fatalf("expected entry posted, got %v", entry.Status)
	}

	updatedAsset, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	expectedBook, _ := financialtypes.NewMoneyFromString("23000.00", "USD")
	if !updatedAsset.CurrentBookValue.Equal(expectedBook) {
		t.Fatalf("expected book value %v, got %v", expectedBook, updatedAsset.CurrentBookValue)
	}
}

func TestT2_2_FailedDepreciationRollsBackPostgres(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupPostgresPostingEnv(t)
	defer driver.Close()

	cost, _ := financialtypes.NewMoneyFromString("12000.00", "USD")
	salvage, _ := financialtypes.Zero("USD")
	assetID := uuid.New()

	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: salvage,
		UsefulLifeMonths: 12, Method: subledgers.StraightLine,
		AccumulatedDepreciation: salvage,
	}
	if err := driver.SaveFixedAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}

	faLedger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	// First depreciation succeeds
	_, err := faLedger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, "poster")
	if err != nil {
		t.Fatal(err)
	}

	// Duplicate depreciation for same month fails
	_, err = faLedger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, "poster")
	if err == nil {
		t.Fatal("expected error on duplicate depreciation, got nil")
	}

	// Verify asset book value remains unchanged after failed attempt
	updatedAsset, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	expectedBook, _ := financialtypes.NewMoneyFromString("11000.00", "USD")
	if !updatedAsset.CurrentBookValue.Equal(expectedBook) {
		t.Fatalf("expected book value %v, got %v", expectedBook, updatedAsset.CurrentBookValue)
	}
}
