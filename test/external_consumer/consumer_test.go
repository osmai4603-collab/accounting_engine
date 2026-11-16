package externalconsumer_test

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
	"github.com/shopspring/decimal"
)

func TestPublicConsumerJourney(t *testing.T) {
	entry, err := runJourney(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != ledger.EntryPosted {
		t.Fatalf("entry status = %q, want %q", entry.Status, ledger.EntryPosted)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("entry has %d lines, want 2", len(entry.Lines))
	}
}

func Example_publicConsumerJourney() {
	entry, err := runJourney(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Printf("status=%s lines=%d\n", entry.Status, len(entry.Lines))
	// Output: status=posted lines=2
}

func runJourney(ctx context.Context) (ledger.JournalEntry, error) {
	driver := memory.New()
	masterData := driver.MasterDataStore()
	var _ masterdata.Store = masterData

	companyID := types.NewCompanyID()
	if err := masterData.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "ACME", LegalName: "Acme Example Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create company: %w", err)
	}
	createdCompany, err := masterData.GetCompany(ctx, companyID)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("read created company: %w", err)
	}
	if createdCompany.Code != "ACME" {
		return ledger.JournalEntry{}, fmt.Errorf("read created company with code %q, want ACME", createdCompany.Code)
	}
	journalID := masterdata.NewJournalID()
	if err := masterData.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GENERAL", Name: "General journal",
	}); err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create journal: %w", err)
	}
	createdJournal, err := masterData.GetJournalByCode(ctx, companyID, "GENERAL")
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("read created journal: %w", err)
	}
	if createdJournal.ID != journalID {
		return ledger.JournalEntry{}, fmt.Errorf("read journal with ID %s, want %s", createdJournal.ID, journalID)
	}

	cashAccountID := types.NewAccountID()
	revenueAccountID := types.NewAccountID()
	for _, account := range []masterdata.Account{
		{ID: cashAccountID, CompanyID: companyID, Code: "1000", Name: "Cash", Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: revenueAccountID, CompanyID: companyID, Code: "4000", Name: "Revenue", Type: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, Currency: "USD", IsActive: true},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("create account %s: %w", account.Code, err)
		}
		createdAccount, err := masterData.GetAccount(ctx, companyID, account.ID)
		if err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("read created account %s: %w", account.Code, err)
		}
		if createdAccount.Code != account.Code {
			return ledger.JournalEntry{}, fmt.Errorf("read created account code %q, want %q", createdAccount.Code, account.Code)
		}
	}

	yearID := types.NewFiscalYearID()
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, masterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: end, State: masterdata.FiscalYearOpen,
	}); err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create fiscal year: %w", err)
	}
	createdYear, err := masterData.GetFiscalYear(ctx, companyID, yearID)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("read created fiscal year: %w", err)
	}
	if createdYear.Name != "FY2026" {
		return ledger.JournalEntry{}, fmt.Errorf("read created fiscal year named %q, want FY2026", createdYear.Name)
	}

	periodID := types.NewFiscalPeriodID()
	if err := masterData.CreateFiscalPeriod(ctx, masterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create fiscal period: %w", err)
	}
	createdPeriod, err := masterData.GetFiscalPeriod(ctx, companyID, periodID)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("read created fiscal period: %w", err)
	}
	if createdPeriod.Number != 1 {
		return ledger.JournalEntry{}, fmt.Errorf("read created fiscal period number %d, want 1", createdPeriod.Number)
	}

	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(driver.Ports())).Build(ctx)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("build SDK engine: %w", err)
	}
	defer engine.Close()

	amount, err := types.NewMoneyFromString("125.00", "USD")
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create amount: %w", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("create zero amount: %w", err)
	}
	entry := ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: companyID, JournalID: types.JournalID(journalID),
		FiscalPeriodID: periodID,
		Status:         ledger.EntryDraft, Classification: ledger.EntryClassRegular,
		EffectiveDate: time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		PostingDate:   time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Description:   "Cash sale", SourceModule: "external-consumer",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		CreatedBy: "external-consumer",
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: cashAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: revenueAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("post journal entry: %w", err)
	}
	readBack, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("read journal entry: %w", err)
	}
	if readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 {
		return ledger.JournalEntry{}, fmt.Errorf("unexpected read-back entry: status=%q lines=%d", readBack.Status, len(readBack.Lines))
	}
	return readBack, nil
}

func TestNoInternalImports(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(importPath, "github.com/osmai4603-collab/accounting_engine/internal/") {
				return fmt.Errorf("%s imports forbidden internal package %q", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCoreLedgerHasNoIndustryDependencies(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate external-consumer test source")
	}
	postingDir := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", "internal", "posting"))
	industryTerms := []string{
		"HARVEST_CROP", "POS_SALE", "CropYield", "HarvestOrder",
		"ProductionOrder", "ManufacturingOrder", "SubscriptionRevenue",
	}
	var violations []string
	err := filepath.WalkDir(postingDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, term := range industryTerms {
			if strings.Contains(string(source), term) {
				relative, _ := filepath.Rel(postingDir, path)
				violations = append(violations, relative+": "+term)
			}
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.Contains(importPath, "/ifrs15") ||
				strings.Contains(importPath, "/ifrs16") ||
				strings.Contains(importPath, "/ifrs9") ||
				strings.Contains(importPath, "/consolidation") {
				relative, _ := filepath.Rel(postingDir, path)
				violations = append(violations, relative+": imports "+importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan core ledger sources: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("core ledger must remain industry-independent: %s", strings.Join(violations, ", "))
	}
}
