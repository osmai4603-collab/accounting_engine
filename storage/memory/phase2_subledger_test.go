package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func TestFinancialDimensionsAreCompanyScopedAndActive(t *testing.T) {
	driver := New()
	ctx := context.Background()
	companyID, accountID, centerID, projectID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "DIM", LegalName: "Dimensions", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		ID: accountID, CompanyID: companyID, Code: "5000", Name: "Operating expense",
		AccountType: masterdata.AccountTypeExpense, RequiresCostCenter: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.SaveCostCenter(ctx, masterdata.CostCenter{
		ID: centerID, CompanyID: companyID, Code: "OPS", Name: "Operations", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.SaveAnalyticalDimension(ctx, masterdata.AnalyticalDimensionValue{
		ID: projectID, CompanyID: companyID, Kind: masterdata.DimensionProject,
		Code: "PROJECT-1", Name: "Project 1", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if required, err := driver.RequiresCostCenter(ctx, companyID, accountID); err != nil || !required {
		t.Fatalf("cost center policy = %v, %v; want required", required, err)
	}
	if err := driver.ValidateCostCenter(ctx, companyID, centerID); err != nil {
		t.Fatalf("active cost center validation: %v", err)
	}
	if err := driver.ValidateCostCenter(ctx, uuid.New(), centerID); !errors.Is(err, masterdata.ErrInvalidCostCenter) {
		t.Fatalf("cross-company cost center validation = %v", err)
	}
	if err := driver.ValidateAnalyticalDimension(ctx, companyID, masterdata.DimensionProject, projectID); err != nil {
		t.Fatalf("active project validation: %v", err)
	}
	if err := driver.ValidateAnalyticalDimension(ctx, companyID, masterdata.DimensionSegment, projectID); !errors.Is(err, masterdata.ErrInvalidAnalyticalDimension) {
		t.Fatalf("wrong-kind project validation = %v", err)
	}
}

func TestPostedInvoiceRequiresControlAccountAndSavesWithPostedEntry(t *testing.T) {
	driver := New()
	ctx := context.Background()
	companyID, controlID, revenueID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "T2", LegalName: "Phase Two", FunctionalCurrency: "USD", PresentationCurrency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period := masterdata.FiscalPeriod{
		CompanyID: companyID, ID: uuid.New(), FiscalYearID: uuid.New(), Number: 1,
		StartDate: now.Add(-time.Hour), EndDate: now.Add(time.Hour), State: masterdata.PeriodOpen,
	}
	if err := driver.CreateFiscalYear(ctx, masterdata.FiscalYear{
		CompanyID: companyID, ID: period.FiscalYearID, StartDate: period.StartDate,
		EndDate: period.EndDate, Periods: []masterdata.FiscalPeriod{period},
	}); err != nil {
		t.Fatal(err)
	}
	control := masterdata.Account{
		CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsControlAccount: true, IsReconcilable: true,
	}
	if err := driver.CreateAccount(ctx, control); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
		AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
	}); err != nil {
		t.Fatal(err)
	}
	amount, err := financialtypes.NewMoneyFromString("125.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entry, err := posting.NewJournalEntry(journalID, time.Now().UTC(), "test",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: controlID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = period.ID
	if err := driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := (subledgers.NewReceivableLedgerForCompany(driver, companyID)).AddInvoiceFromPostedEntry(
		uuid.New(), controlID, entry.ID, amount, entry.EntryDate, entry.EntryDate.AddDate(0, 0, 30), "INV-1",
	); !errors.Is(err, posting.ErrEntryNotPosted) {
		t.Fatalf("invoice linked to draft entry: error=%v", err)
	}
	entry.Status = posting.EntryPosted
	if err := driver.PostAtomic(entry); err != nil {
		t.Fatal(err)
	}
	line, err := subledgers.NewReceivableLedgerForCompany(driver, companyID).AddInvoiceFromPostedEntry(
		uuid.New(), controlID, entry.ID, amount, entry.EntryDate, entry.EntryDate.AddDate(0, 0, 30), "INV-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if line.JournalEntryID != entry.ID || line.ControlAccountID != controlID || !line.OriginalAmount.Equal(amount) {
		t.Fatalf("posted invoice was not linked correctly: %+v", line)
	}
}

func TestPostEntryWithOpenLineIsAtomic(t *testing.T) {
	driver := New()
	ctx := context.Background()
	companyID, controlID, revenueID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "T2-ATOMIC", LegalName: "Atomic Posting",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsControlAccount: true, IsReconcilable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
		AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	amount, err := financialtypes.NewMoneyFromString("125.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entryDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	entry, err := posting.NewJournalEntry(journalID, entryDate, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: controlID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	engine := posting.NewEngine(driver, masterdata.NewService(masterdata.NewRepositoryAdapter(driver)))
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}

	partnerID := uuid.New()
	line := subledgers.OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partnerID,
		ControlAccountID: controlID, JournalEntryID: entry.ID,
		Kind: subledgers.LineReceivable, OriginalAmount: amount, ResidualAmount: amount,
		DocumentDate: entryDate, DueDate: entryDate.AddDate(0, 0, 30), Reference: "INV-ATOMIC",
	}
	attachment, err := subledgers.NewPostingOpenLineAttachment(line)
	if err != nil {
		t.Fatal(err)
	}
	attachment.Amount, err = financialtypes.NewMoneyFromString("124.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryWithOpenLine(&entry, attachment, "checker"); !errors.Is(err, subledgers.ErrInvalidOpenLine) {
		t.Fatalf("posting with mismatched open-line amount error = %v, want invalid open line", err)
	}
	stored, err := driver.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("entry status after failed atomic post = %s, want draft", stored.Status)
	}
	if _, err := driver.GetOpenLine(ctx, companyID, line.ID); !errors.Is(err, subledgers.ErrNotFound) {
		t.Fatalf("open line after failed atomic post error = %v, want not found", err)
	}

	postedLine, err := subledgers.NewReceivableLedgerForCompany(driver, companyID).PostInvoiceEntry(
		engine, &entry, partnerID, controlID, amount, entryDate, entryDate.AddDate(0, 0, 30),
		"INV-ATOMIC", "checker",
	)
	if err != nil {
		t.Fatalf("atomic invoice posting failed: %v", err)
	}
	stored, err = driver.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryPosted {
		t.Fatalf("entry status after successful atomic post = %s, want posted", stored.Status)
	}
	storedLine, err := driver.GetOpenLine(ctx, companyID, postedLine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedLine.JournalEntryID != entry.ID || !storedLine.OriginalAmount.Equal(amount) {
		t.Fatalf("stored open line is not linked to posted entry: %+v", storedLine)
	}
}
