package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/storage/postgres"
)

func TestPostgresPostingWithOpenLineIsAtomic(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL atomic-posting test: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyID, journalID := uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("ATOMIC-%s", companyID.String()[:8]),
		LegalName: "Atomic Posting Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code) VALUES($1, $2, $3)`,
			journalID, companyID, fmt.Sprintf("ATOMIC-%s", journalID.String()[:8]))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	controlID, revenueID := uuid.New(), uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsControlAccount: true, IsReconcilable: true, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
		AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
		IsActive: true,
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
	entry.PeriodID = year.Periods[2].ID
	if err := driver.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	entry.PostedBy = "checker"
	postedAt := time.Now().UTC()
	entry.PostedAt = &postedAt

	partnerID := uuid.New()
	lineID := uuid.New()
	attachment := posting.OpenLineAttachment{
		ID: lineID, CompanyID: companyID, JournalEntryID: entry.ID,
		PartnerID: partnerID, ControlAccountID: controlID, Kind: string(subledgers.LineReceivable),
		Amount: amount, DocumentDate: entryDate, DueDate: entryDate.AddDate(0, 0, 30),
		Reference: "INV-ATOMIC",
	}
	attachment.Amount, err = financialtypes.NewMoneyFromString("124.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.PostAndSequenceWithOpenLine(ctx, entry, attachment); !errors.Is(err, subledgers.ErrInvalidOpenLine) {
		t.Fatalf("posting with mismatched amount error = %v, want invalid open line", err)
	}
	stored, err := driver.GetEntryByID(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("entry status after failed post = %s, want draft", stored.Status)
	}
	if _, err := driver.GetOpenLine(ctx, companyID, lineID); !errors.Is(err, subledgers.ErrNotFound) {
		t.Fatalf("open line after failed post error = %v, want not found", err)
	}
	var sequence int64
	if err := driver.Pool().QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
		companyID, journalID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if sequence != 0 {
		t.Fatalf("journal sequence after failed post = %d, want 0", sequence)
	}

	attachment.Amount = amount
	if _, err := driver.PostAndSequenceWithOpenLine(ctx, entry, attachment); err != nil {
		t.Fatalf("atomic post failed: %v", err)
	}
	stored, err = driver.GetEntryByID(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryPosted {
		t.Fatalf("entry status after successful post = %s, want posted", stored.Status)
	}
	line, err := driver.GetOpenLine(ctx, companyID, lineID)
	if err != nil {
		t.Fatal(err)
	}
	if line.JournalEntryID != entry.ID || !line.OriginalAmount.Equal(amount) {
		t.Fatalf("stored open line is not linked to posted entry: %+v", line)
	}
}

func TestPostgresEventAccountingRulesPersist(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL event-rule test: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("RULE-%s", companyID.String()[:8]),
		LegalName: "Event Rule Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	rule := subledgers.EventAccountingRule{
		ID: uuid.New(), CompanyID: companyID, EventType: "INVOICE_ISSUED",
		RuleName: "sales with VAT", JournalID: uuid.New(), Priority: 10,
		Conditions: map[string]string{"country": "SA"},
		Lines: []subledgers.EventLineTemplate{
			{AccountLookupKey: "AR", Side: subledgers.EventLineDebit, AmountFormula: "amount"},
			{AccountLookupKey: "REVENUE", Side: subledgers.EventLineCredit, AmountFormula: "amount - vat"},
			{AccountLookupKey: "VAT", Side: subledgers.EventLineCredit, AmountFormula: "vat"},
		},
		Accounts: map[string]uuid.UUID{
			"AR": uuid.New(), "REVENUE": uuid.New(), "VAT": uuid.New(),
		},
	}
	if err := driver.SaveEventAccountingRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	rules, err := driver.ListEventAccountingRulesForCompany(ctx, companyID, rule.EventType)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != rule.ID || len(rules[0].Lines) != 3 ||
		rules[0].Conditions["country"] != "SA" || rules[0].Accounts["VAT"] != rule.Accounts["VAT"] {
		t.Fatalf("persisted event accounting rule differs from saved rule: %+v", rules)
	}
}

func TestPostgresFinancialDimensionsAreCompanyScoped(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("Skipping PostgreSQL financial-dimensions test: isolated schema unavailable (%v)", err)
	}

	companyID, accountID, centerID, projectID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("DIM-%s", companyID.String()[:8]),
		LegalName: "Financial Dimensions Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		ID: accountID, CompanyID: companyID, Code: "5000", Name: "Operating expense",
		AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
		RequiresCostCenter: true, IsActive: true,
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
