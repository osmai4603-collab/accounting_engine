package memory

import (
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func TestPostedInvoiceRequiresControlAccountAndSavesWithPostedEntry(t *testing.T) {
	driver := New()
	companyID, controlID, revenueID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(masterdata.Company{
		ID: companyID, Code: "T2", LegalName: "Phase Two", FunctionalCurrency: "USD", PresentationCurrency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	control := masterdata.Account{
		CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsControlAccount: true, IsReconcilable: true,
	}
	if err := driver.CreateAccount(control); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(masterdata.Account{
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
	companyID, controlID, revenueID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(masterdata.Company{
		ID: companyID, Code: "T2-ATOMIC", LegalName: "Atomic Posting",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(masterdata.Account{
		CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsControlAccount: true, IsReconcilable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(masterdata.Account{
		CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
		AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(year); err != nil {
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
	engine := posting.NewEngine(driver, masterdata.NewService(driver))
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
	if _, err := driver.GetOpenLine(line.ID); !errors.Is(err, subledgers.ErrNotFound) {
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
	storedLine, err := driver.GetOpenLine(postedLine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedLine.JournalEntryID != entry.ID || !storedLine.OriginalAmount.Equal(amount) {
		t.Fatalf("stored open line is not linked to posted entry: %+v", storedLine)
	}
}
