package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"
	"github.com/google/uuid"
)

func setupMemorySubledgerEnv(t *testing.T) (*memory.Driver, *masterdata.Service, *posting.Engine, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	driver := memory.New()
	ctx := context.Background()

	companyID, journalID := uuid.New(), uuid.New()
	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "SUB-CO", LegalName: "Subledger Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	acc1ID, acc2ID := uuid.New(), uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc1ID, Code: "1500", Name: "Fixed Asset",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc2ID, Code: "1509", Name: "Accumulated Depreciation",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	engine := posting.NewEngine(driver, masterData)
	return driver, masterData, engine, companyID, journalID, acc1ID, acc2ID
}

func TestT2_1_AtomicAssetDepreciationInMemory(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()

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
	expectedBook, _ := financialtypes.NewMoneyFromString("11000.00", "USD")
	if !updatedAsset.CurrentBookValue.Equal(expectedBook) {
		t.Fatalf("expected book value %v, got %v", expectedBook, updatedAsset.CurrentBookValue)
	}
}

func TestT2_3_AtomicReceivableWriteOffInMemory(t *testing.T) {
	driver, _, engine, companyID, journalID, controlAcc, adjAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()

	account, _ := driver.GetAccountByID(ctx, controlAcc)
	account.IsControlAccount = true
	account.IsReconcilable = true
	_ = driver.CreateAccount(ctx, account)

	origAmount, _ := financialtypes.NewMoneyFromString("500.00", "USD")
	date := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	partnerID := uuid.New()
	lineID := uuid.New()
	entryID := uuid.New()

	entry, _ := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: controlAcc, Debit: origAmount, Credit: financialtypes.Money{}},
		{CompanyID: companyID, ID: uuid.New(), AccountID: adjAcc, Debit: financialtypes.Money{}, Credit: origAmount},
	})
	entry.ID = entryID
	entry.CompanyID = companyID
	_ = engine.CreateDraftContext(ctx, entry)

	openLineAtt := posting.OpenLineAttachment{
		ID: lineID, CompanyID: companyID, JournalEntryID: entryID,
		PartnerID: partnerID, ControlAccountID: controlAcc, Kind: "receivable",
		Amount: origAmount, DocumentDate: date, DueDate: date.AddDate(0, 0, 30),
		Reference: "INV-WO",
	}
	if err := engine.PostEntryWithOpenLineContext(ctx, &entry, openLineAtt, "checker"); err != nil {
		t.Fatal(err)
	}

	recLedger := subledgers.NewReceivableLedgerForCompany(driver, companyID)

	woAmount, _ := financialtypes.NewMoneyFromString("200.00", "USD")
	params := subledgers.WriteOffParams{
		JournalID: journalID, AdjustmentAccountID: adjAcc, Description: "Partial Bad Debt", Date: date,
	}

	woEntry, err := recLedger.WriteOffAtomic(ctx, engine, lineID, woAmount, params, "checker")
	if err != nil {
		t.Fatalf("unexpected error in WriteOffAtomic: %v", err)
	}

	if woEntry.Status != posting.EntryPosted {
		t.Fatalf("expected writeoff entry posted, got %v", woEntry.Status)
	}

	updatedLine, err := driver.GetOpenLine(ctx, companyID, lineID)
	if err != nil {
		t.Fatal(err)
	}
	expectedResidual, _ := financialtypes.NewMoneyFromString("300.00", "USD")
	if !updatedLine.ResidualAmount.Equal(expectedResidual) {
		t.Fatalf("expected residual %v, got %v", expectedResidual, updatedLine.ResidualAmount)
	}
}

func TestT2_8_ConcurrentAssetDepreciationsNoDeadlock(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()

	cost, _ := financialtypes.NewMoneyFromString("12000.00", "USD")
	salvage, _ := financialtypes.Zero("USD")

	asset1ID, asset2ID := uuid.New(), uuid.New()
	a1 := subledgers.FixedAsset{
		CompanyID: companyID, ID: asset1ID,
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: salvage, UsefulLifeMonths: 12,
		Method: subledgers.StraightLine, AccumulatedDepreciation: salvage,
	}
	a2 := subledgers.FixedAsset{
		CompanyID: companyID, ID: asset2ID,
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: salvage, UsefulLifeMonths: 12,
		Method: subledgers.StraightLine, AccumulatedDepreciation: salvage,
	}
	_ = driver.SaveFixedAsset(ctx, a1)
	_ = driver.SaveFixedAsset(ctx, a2)

	faLedger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)

	var wg sync.WaitGroup
	wg.Add(2)
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	errChan := make(chan error, 2)

	go func() {
		defer wg.Done()
		_, err := faLedger.DepreciateAssetAtomic(ctx, engine, asset1ID, journalID, asOf, "poster1")
		if err != nil {
			errChan <- err
		}
	}()

	go func() {
		defer wg.Done()
		_, err := faLedger.DepreciateAssetAtomic(ctx, engine, asset2ID, journalID, asOf, "poster2")
		if err != nil {
			errChan <- err
		}
	}()

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatalf("concurrent atomic depreciation error: %v", err)
	}
}
