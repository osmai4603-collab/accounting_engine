package memory_test

import (
	"context"
	"errors"
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

type failedAttachment struct{}

func (failedAttachment) AttachmentKind() string {
	return "injected_failure"
}

func (failedAttachment) ValidateAttachment(context.Context, posting.JournalEntry) error {
	return nil
}

func (failedAttachment) ExecuteAtomicMutation(context.Context, posting.AtomicTransaction, posting.JournalEntry) error {
	return errors.New("injected attachment failure")
}

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
	var depreciationExpense financialtypes.Money
	for _, line := range entry.Lines {
		if line.AccountID == accumAcc {
			depreciationExpense = line.Debit
			break
		}
	}
	expectedBook, err := cost.Subtract(depreciationExpense)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedAsset.CurrentBookValue.Equal(expectedBook) {
		t.Fatalf("expected book value %v, got %v", expectedBook, updatedAsset.CurrentBookValue)
	}
}

func TestPhase3_AtomicAssetDisposalAndImpairmentInMemory(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()
	cost, _ := financialtypes.NewMoneyFromString("10000", "USD")
	accumulated, _ := financialtypes.NewMoneyFromString("1000", "USD")
	bookValue, _ := cost.Subtract(accumulated)
	zero, _ := financialtypes.Zero("USD")
	disposalDate := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	disposalAssetID := uuid.New()
	disposalAsset := subledgers.FixedAsset{
		CompanyID: companyID, ID: disposalAssetID, HistoricalCost: cost, SalvageValue: zero,
		AccumulatedDepreciation: accumulated, CurrentBookValue: bookValue,
		UsefulLifeMonths: 24, Method: subledgers.StraightLine,
		InServiceDate:  disposalDate.AddDate(-1, 0, 0),
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, DisposalCashAccountID: assetAcc,
	}
	ledger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)
	if err := ledger.RegisterAssetContext(ctx, disposalAsset); err != nil {
		t.Fatal(err)
	}
	disposal, err := ledger.DisposeAssetAtomic(ctx, engine, disposalAssetID, disposalDate, bookValue, journalID, "poster")
	if err != nil {
		t.Fatalf("DisposeAssetAtomic: %v", err)
	}
	if disposal.Status != posting.EntryPosted {
		t.Fatalf("disposal status = %s, want posted", disposal.Status)
	}
	disposed, err := driver.GetFixedAsset(ctx, companyID, disposalAssetID)
	if err != nil {
		t.Fatal(err)
	}
	if disposed.DisposedAt == nil || !disposed.DisposedAt.Equal(disposalDate) ||
		disposed.PendingOperation != "" || disposed.PendingOperationEntryID != uuid.Nil {
		t.Fatalf("disposal did not atomically finalize asset: %+v", disposed)
	}

	impairmentAssetID := uuid.New()
	impairmentAsset := disposalAsset
	impairmentAsset.ID = impairmentAssetID
	impairmentAsset.DisposedAt = nil
	if err := ledger.RegisterAssetContext(ctx, impairmentAsset); err != nil {
		t.Fatal(err)
	}
	loss, _ := financialtypes.NewMoneyFromString("500", "USD")
	impairment, err := ledger.ImpairAssetAtomic(ctx, engine, impairmentAssetID, loss, journalID, disposalDate, "poster")
	if err != nil {
		t.Fatalf("ImpairAssetAtomic: %v", err)
	}
	if impairment.Status != posting.EntryPosted {
		t.Fatalf("impairment status = %s, want posted", impairment.Status)
	}
	impaired, err := driver.GetFixedAsset(ctx, companyID, impairmentAssetID)
	if err != nil {
		t.Fatal(err)
	}
	expectedBook, _ := bookValue.Subtract(loss)
	if !impaired.CurrentBookValue.Equal(expectedBook) ||
		!impaired.AccumulatedImpairment.Equal(loss) ||
		impaired.PendingOperation != "" || impaired.PendingOperationEntryID != uuid.Nil {
		t.Fatalf("impairment did not atomically update asset: %+v", impaired)
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

func TestT2_6_PartialFailureRollsBackPostingAndAssetMutation(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAcc, expenseAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()

	cost, _ := financialtypes.NewMoneyFromString("12000.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	assetID := uuid.New()
	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		AssetAccountID: assetAcc, AccumulatedAccountID: expenseAcc,
		ExpenseAccountID: expenseAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: zero,
		UsefulLifeMonths: 12, Method: subledgers.StraightLine,
		AccumulatedDepreciation: zero,
	}
	if err := driver.SaveFixedAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}

	amount, _ := financialtypes.NewMoneyFromString("1000.00", "USD")
	date := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: expenseAcc, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: assetAcc, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}

	updatedAsset := asset
	updatedAsset.CurrentBookValue, _ = cost.Subtract(amount)
	updatedAsset.AccumulatedDepreciation = amount
	updatedAsset.LastDepreciationDate = date
	attachment := &subledgers.DepreciationAttachment{
		AssetID: assetID, CompanyID: companyID, Amount: amount, AsOf: date, UpdatedAsset: updatedAsset,
	}
	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, "poster", attachment, failedAttachment{}); err == nil {
		t.Fatal("expected injected attachment failure")
	}

	if entry.Status != posting.EntryDraft {
		t.Fatalf("expected in-memory entry to remain draft, got %v", entry.Status)
	}
	savedEntry, err := driver.GetEntryByIDForCompany(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if savedEntry.Status != posting.EntryDraft {
		t.Fatalf("expected persisted entry to remain draft, got %v", savedEntry.Status)
	}
	savedAsset, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !savedAsset.CurrentBookValue.Equal(cost) || !savedAsset.AccumulatedDepreciation.Equal(zero) ||
		!savedAsset.LastDepreciationDate.IsZero() {
		t.Fatalf("asset mutation was not rolled back: %+v", savedAsset)
	}
	for _, accountID := range []uuid.UUID{assetAcc, expenseAcc} {
		balance, err := driver.GetAccountBalance(ctx, companyID, accountID)
		if err != nil {
			t.Fatal(err)
		}
		if !balance.Amount().IsZero() {
			t.Fatalf("expected account %s balance to remain zero, got %v", accountID, balance)
		}
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
