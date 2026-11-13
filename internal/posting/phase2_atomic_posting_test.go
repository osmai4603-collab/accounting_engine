package posting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
)

type dummyAttachment struct {
	kind       string
	shouldFail bool
	executed   *bool
}

func (d *dummyAttachment) AttachmentKind() string {
	return d.kind
}

func (d *dummyAttachment) ValidateAttachment(ctx context.Context, entry posting.JournalEntry) error {
	if d.shouldFail {
		return errors.New("validation failed")
	}
	return nil
}

func (d *dummyAttachment) ExecuteAtomicMutation(ctx context.Context, tx posting.AtomicTransaction, entry posting.JournalEntry) error {
	if d.shouldFail {
		return errors.New("mutation failed")
	}
	if d.executed != nil {
		*d.executed = true
	}
	return nil
}

func setupTestEngine(t *testing.T) (*posting.Engine, *memory.Driver, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	driver := memory.New()
	ctx := context.Background()

	companyID, journalID := uuid.New(), uuid.New()
	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "TEST-CO", LegalName: "Test Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	acc1ID, acc2ID := uuid.New(), uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc1ID, Code: "1000", Name: "Cash / Control",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
		IsControlAccount: true, IsReconcilable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: acc2ID, Code: "2000", Name: "Payable",
		AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	engine := posting.NewEngine(driver, masterData)
	return engine, driver, companyID, journalID, acc1ID, acc2ID
}

func TestT2_5_PostEntryWithMultipleAttachments(t *testing.T) {
	engine, driver, companyID, journalID, acc1, acc2 := setupTestEngine(t)
	ctx := context.Background()

	money100, _ := financialtypes.NewMoneyFromString("100.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	date := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: acc1, Debit: money100, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: acc2, Debit: zero, Credit: money100},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}

	ex1, ex2 := false, false
	att1 := &dummyAttachment{kind: "att1", executed: &ex1}
	att2 := &dummyAttachment{kind: "att2", executed: &ex2}

	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, "checker", att1, att2); err != nil {
		t.Fatalf("unexpected error posting with attachments: %v", err)
	}

	if !ex1 || !ex2 {
		t.Fatalf("expected both attachments executed: ex1=%v, ex2=%v", ex1, ex2)
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("expected entry status posted, got %v", entry.Status)
	}

	_ = driver
}

func TestT2_7_LegacyOpenLineAttachmentViaEngine(t *testing.T) {
	engine, driver, companyID, journalID, acc1, acc2 := setupTestEngine(t)
	ctx := context.Background()

	money100, _ := financialtypes.NewMoneyFromString("100.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	date := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: acc1, Debit: money100, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: acc2, Debit: zero, Credit: money100},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}

	partnerID := uuid.New()
	lineID := uuid.New()
	openLineAtt := posting.OpenLineAttachment{
		ID: lineID, CompanyID: companyID, JournalEntryID: entry.ID,
		PartnerID: partnerID, ControlAccountID: acc1, Kind: "receivable",
		Amount: money100, DocumentDate: date, DueDate: date.AddDate(0, 0, 30),
		Reference: "INV-LEGACY",
	}

	if err := engine.PostEntryWithOpenLineContext(ctx, &entry, openLineAtt, "checker"); err != nil {
		t.Fatalf("unexpected error posting with open line: %v", err)
	}

	if entry.Status != posting.EntryPosted {
		t.Fatalf("expected entry status posted, got %v", entry.Status)
	}

	line, err := driver.GetOpenLine(ctx, companyID, lineID)
	if err != nil {
		t.Fatalf("expected open line saved in subledger, got error: %v", err)
	}
	if line.ID != lineID {
		t.Fatalf("expected open line ID %v, got %v", lineID, line.ID)
	}
}
