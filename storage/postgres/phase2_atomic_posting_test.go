package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

type postgresMarkerAttachment struct {
	companyID uuid.UUID
	assetID   uuid.UUID
	marker    string
	fail      bool
}

func (a postgresMarkerAttachment) AttachmentKind() string {
	return a.marker
}

func (a postgresMarkerAttachment) ValidateAttachment(context.Context, posting.JournalEntry) error {
	return nil
}

func (a postgresMarkerAttachment) ExecuteAtomicMutation(
	ctx context.Context,
	tx posting.AtomicTransaction,
	entry posting.JournalEntry,
) error {
	if err := tx.Exec(ctx, `UPDATE fixed_assets
		SET asset_data = jsonb_set(asset_data, ARRAY[$3]::text[], 'true'::jsonb, true)
		WHERE company_id=$1 AND id=$2`, a.companyID, a.assetID, a.marker); err != nil {
		return err
	}
	if a.fail {
		return errors.New("injected attachment failure")
	}
	return nil
}

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

func createDraftTestEntry(
	t *testing.T,
	ctx context.Context,
	engine *posting.Engine,
	companyID, journalID, debitAccountID, creditAccountID uuid.UUID,
	amount financialtypes.Money,
	date time.Time,
) posting.JournalEntry {
	t.Helper()
	zero, err := financialtypes.Zero(amount.Currency())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestMultiCurrencyTriggerPostingBalance verifies the posted-move balance
// trigger sums the authoritative functional minor-unit columns: a multi-
// currency entry whose transaction legs are EUR and USD but whose functional
// SAR totals balance must post, while a functionally unbalanced entry must be
// rejected at the database boundary.
func TestMultiCurrencyTriggerPostingBalance(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL test: %v", err)
	}
	defer driver.Close()

	companyID, journalID := uuid.New(), uuid.New()
	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("MC-%s", companyID.String()[:8]),
		LegalName: "Multi-Currency Trigger Co", FunctionalCurrency: "SAR",
		PresentationCurrency: "SAR", Active: true,
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
	cashID, revenueID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{CompanyID: companyID, ID: cashID, Code: "1010", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: companyID, ID: revenueID, Code: "4010", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	engine := posting.NewEngine(driver, masterData)

	eurAmount, err := financialtypes.NewMoneyFromString("50.00", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	usdAmount, err := financialtypes.NewMoneyFromString("41.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	functionalSAR, err := financialtypes.NewMoneyFromString("205.00", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	zeroSAR, err := financialtypes.Zero("SAR")
	if err != nil {
		t.Fatal(err)
	}

	entryDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	balanced := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: entryDate, Date: entryDate, Status: posting.EntryDraft,
		CreatedBy: "maker", CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: cashID,
				TransactionAmount: eurAmount, TransactionCurrency: "EUR",
				ExchangeRate:    decimal.NewFromFloat(4.10),
				FunctionalDebit: functionalSAR, FunctionalCredit: zeroSAR,
			},
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: revenueID,
				TransactionAmount: usdAmount, TransactionCurrency: "USD",
				ExchangeRate:    decimal.NewFromFloat(5.00),
				FunctionalDebit: zeroSAR, FunctionalCredit: functionalSAR,
			},
		},
	}
	if err := engine.CreateDraftContext(ctx, balanced); err != nil {
		t.Fatalf("create multi-currency draft: %v", err)
	}
	if err := engine.PostForCompany(ctx, companyID, balanced.ID, "poster"); err != nil {
		t.Fatalf("expected functionally balanced multi-currency entry to post, got: %v", err)
	}
	posted, err := driver.GetEntryByIDForCompany(ctx, companyID, balanced.ID)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Status != posting.EntryPosted {
		t.Fatalf("expected posted status, got %v", posted.Status)
	}

	// A draft whose stored functional credit no longer matches the functional
	// debit must be rejected by the balance trigger itself.
	unbalancedDraft := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: entryDate, Date: entryDate, Status: posting.EntryDraft,
		CreatedBy: "maker", CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: cashID,
				TransactionAmount: eurAmount, TransactionCurrency: "EUR",
				ExchangeRate:    decimal.NewFromFloat(4.10),
				FunctionalDebit: functionalSAR, FunctionalCredit: zeroSAR,
			},
			{
				CompanyID: companyID, ID: uuid.New(), AccountID: revenueID,
				TransactionAmount: usdAmount, TransactionCurrency: "USD",
				ExchangeRate:    decimal.NewFromFloat(5.00),
				FunctionalDebit: zeroSAR, FunctionalCredit: functionalSAR,
			},
		},
	}
	if err := engine.CreateDraftContext(ctx, unbalancedDraft); err != nil {
		t.Fatalf("create second multi-currency draft: %v", err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE account_move_lines
			SET functional_credit_minor = 20000
			WHERE company_id=$1 AND move_id=$2 AND line_id=$3`,
			companyID, unbalancedDraft.ID, unbalancedDraft.Lines[1].ID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() != 1 {
			return fmt.Errorf("expected one corrupted line, got %d", ct.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_moves
			SET status='posted', entry=jsonb_set(entry, '{Status}', '"posted"', true)
			WHERE company_id=$1 AND id=$2`,
			companyID, unbalancedDraft.ID)
		return err
	})
	if err == nil {
		t.Fatal("expected the balance trigger to reject the functionally unbalanced move")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation from balance trigger, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("expected unbalanced move error, got: %v", err)
	}
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

func TestT2_2_FailedPostingRollsBackPostgres(t *testing.T) {
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

	amount, _ := financialtypes.NewMoneyFromString("1000.00", "USD")
	entryDate := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	entry := createDraftTestEntry(t, ctx, engine, companyID, journalID, assetAcc, accumAcc, amount, entryDate)

	if err := engine.PostEntryWithAttachmentContext(
		ctx, &entry, "poster", postgresMarkerAttachment{
			companyID: companyID, assetID: assetID, marker: "phase2_injected_marker", fail: true,
		},
	); err == nil {
		t.Fatal("expected injected attachment failure")
	}

	savedEntry, err := driver.GetEntryByIDForCompany(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if savedEntry.Status != posting.EntryDraft || entry.Status != posting.EntryDraft {
		t.Fatalf("expected entry to remain draft after rollback, got saved=%v returned=%v", savedEntry.Status, entry.Status)
	}

	var markerPresent bool
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT asset_data ? 'phase2_injected_marker'
			FROM fixed_assets WHERE company_id=$1 AND id=$2`, companyID, assetID).Scan(&markerPresent)
	}); err != nil {
		t.Fatal(err)
	}
	if markerPresent {
		t.Fatal("attachment mutation persisted despite posting rollback")
	}
	for _, accountID := range []uuid.UUID{assetAcc, accumAcc} {
		balance, err := driver.GetAccountBalance(ctx, companyID, accountID)
		if err != nil {
			t.Fatal(err)
		}
		if !balance.Amount().IsZero() {
			t.Fatalf("expected account %s balance to remain zero, got %v", accountID, balance)
		}
	}
}

func TestT2_3_AtomicReceivableWriteOffPostgres(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, _, adjustmentAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	controlAccountID := uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: controlAccountID, Code: "1100", Name: "Receivable Control",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsActive: true, IsControlAccount: true, IsReconcilable: true,
	}); err != nil {
		t.Fatal(err)
	}

	originalAmount, _ := financialtypes.NewMoneyFromString("500.00", "USD")
	date := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	entry := createDraftTestEntry(
		t, ctx, engine, companyID, journalID, controlAccountID, adjustmentAccountID, originalAmount, date,
	)
	lineID := uuid.New()
	openLine := posting.OpenLineAttachment{
		ID: lineID, CompanyID: companyID, JournalEntryID: entry.ID,
		PartnerID: uuid.New(), ControlAccountID: controlAccountID, Kind: "receivable",
		Amount: originalAmount, DocumentDate: date, DueDate: date.AddDate(0, 0, 30),
		Reference: "INV-PHASE2",
	}
	if err := engine.PostEntryWithOpenLine(&entry, openLine, "checker"); err != nil {
		t.Fatal(err)
	}

	ledger := subledgers.NewReceivableLedgerForCompany(driver, companyID)
	writeOffAmount, _ := financialtypes.NewMoneyFromString("200.00", "USD")
	writeOffEntry, err := ledger.WriteOffAtomic(ctx, engine, lineID, writeOffAmount, subledgers.WriteOffParams{
		JournalID: journalID, AdjustmentAccountID: adjustmentAccountID,
		Description: "Phase 2 partial write-off", Date: date,
	}, "checker")
	if err != nil {
		t.Fatalf("WriteOffAtomic: %v", err)
	}
	if writeOffEntry.Status != posting.EntryPosted {
		t.Fatalf("expected write-off entry posted, got %v", writeOffEntry.Status)
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

func TestT2_5_PostEntryWithMultipleAttachmentsPostgres(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	assetID := uuid.New()
	cost, _ := financialtypes.NewMoneyFromString("12000.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	if err := driver.SaveFixedAsset(ctx, subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: zero,
		AccumulatedDepreciation: zero, UsefulLifeMonths: 12, Method: subledgers.StraightLine,
	}); err != nil {
		t.Fatal(err)
	}

	amount, _ := financialtypes.NewMoneyFromString("100.00", "USD")
	date := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	entry := createDraftTestEntry(t, ctx, engine, companyID, journalID, debitAccountID, creditAccountID, amount, date)
	attachments := []posting.LedgerAttachment{
		postgresMarkerAttachment{companyID: companyID, assetID: assetID, marker: "attachment_one"},
		postgresMarkerAttachment{companyID: companyID, assetID: assetID, marker: "attachment_two"},
	}
	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, "checker", attachments...); err != nil {
		t.Fatalf("posting with multiple attachments: %v", err)
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("expected entry posted, got %v", entry.Status)
	}
	var markerOne, markerTwo bool
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT asset_data ? 'attachment_one', asset_data ? 'attachment_two'
			FROM fixed_assets WHERE company_id=$1 AND id=$2`, companyID, assetID).
			Scan(&markerOne, &markerTwo)
	}); err != nil {
		t.Fatal(err)
	}
	if !markerOne || !markerTwo {
		t.Fatalf("expected both attachment mutations persisted, got first=%v second=%v", markerOne, markerTwo)
	}
}

func TestT2_8_ConcurrentAssetDepreciationsPostgres(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupPostgresPostingEnv(t)
	defer driver.Close()

	cost, _ := financialtypes.NewMoneyFromString("24000.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	assetIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for _, assetID := range assetIDs {
		if err := driver.SaveFixedAsset(ctx, subledgers.FixedAsset{
			CompanyID: companyID, ID: assetID,
			AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
			ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: zero,
			AccumulatedDepreciation: zero, UsefulLifeMonths: 24, Method: subledgers.StraightLine,
		}); err != nil {
			t.Fatal(err)
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ledger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)
	type result struct {
		entry posting.JournalEntry
		err   error
	}
	results := make(chan result, len(assetIDs))
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	for _, assetID := range assetIDs {
		assetID := assetID
		go func() {
			entry, err := ledger.DepreciateAssetAtomic(callCtx, engine, assetID, journalID, asOf, "poster")
			results <- result{entry: entry, err: err}
		}()
	}
	for range assetIDs {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent depreciation failed: %v", result.err)
		}
		if result.entry.Status != posting.EntryPosted {
			t.Fatalf("expected concurrent entry posted, got %v", result.entry.Status)
		}
	}
	for _, assetID := range assetIDs {
		asset, err := driver.GetFixedAsset(ctx, companyID, assetID)
		if err != nil {
			t.Fatal(err)
		}
		if !asset.CurrentBookValue.LessThan(cost) || asset.AccumulatedDepreciation.Amount().IsZero() {
			t.Fatalf("asset %s was not independently depreciated: %+v", assetID, asset)
		}
	}
}

func TestDuplicateDepreciationRejectedPostgres(t *testing.T) {
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
	firstEntry, err := faLedger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, "poster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := faLedger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, "poster"); err == nil {
		t.Fatal("expected duplicate depreciation to fail")
	}
	updatedAsset, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	var depreciationExpense financialtypes.Money
	for _, line := range firstEntry.Lines {
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
