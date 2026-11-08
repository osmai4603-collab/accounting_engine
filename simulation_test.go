package accounting_engine_test

import (
	"context"
	"testing"
	"time"

	accountingengine "accounting_engine"
	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"

	"github.com/google/uuid"
)

func TestFullFiscalYearSimulationFacade(t *testing.T) {
	driver := memory.New()
	engine, err := accountingengine.New(accountingengine.DefaultConfig(), driver)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	year := masterdata.FiscalYear{ID: uuid.New(), Name: "FY2026", StartDate: start, EndDate: end}
	for month := 1; month <= 12; month++ {
		periodStart := time.Date(2026, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 1, 0).Add(-time.Nanosecond)
		year.Periods = append(year.Periods, masterdata.FiscalPeriod{
			ID: uuid.New(), FiscalYearID: year.ID, Number: month, Name: periodStart.Month().String(),
			StartDate: periodStart, EndDate: periodEnd, State: masterdata.PeriodOpen,
		})
	}
	if err := driver.CreateFiscalYear(year); err != nil {
		t.Fatal(err)
	}

	accounts := map[string]uuid.UUID{}
	definitions := []masterdata.Account{
		{Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset},
		{Code: "1200", Name: "Accounts receivable", AccountType: masterdata.AccountTypeAsset},
		{Code: "1500", Name: "Fixed assets", AccountType: masterdata.AccountTypeAsset},
		{Code: "3000", Name: "Share capital", AccountType: masterdata.AccountTypeEquity},
		{Code: "4000", Name: "Sales", AccountType: masterdata.AccountTypeIncome},
		{Code: "5000", Name: "Expenses", AccountType: masterdata.AccountTypeExpense},
	}
	for _, account := range definitions {
		account.ID = uuid.New()
		if err := engine.MasterData.RegisterAccount(account); err != nil {
			t.Fatal(err)
		}
		accounts[account.Code] = account.ID
	}

	journalID := uuid.New()
	amount, _ := financialtypes.NewMoneyFromString("100000", "USD")
	capital, err := posting.NewJournalEntry(journalID, start.Add(24*time.Hour), "maker",
		[]posting.JournalEntryLine{
			{ID: uuid.New(), AccountID: accounts["1000"], Debit: amount},
			{ID: uuid.New(), AccountID: accounts["3000"], Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Posting.CreateDraft(capital); err != nil {
		t.Fatal(err)
	}
	if err := engine.Posting.Post(capital.ID, "checker"); err != nil {
		t.Fatal(err)
	}

	asset, _ := financialtypes.NewMoneyFromString("60000", "USD")
	sales, _ := financialtypes.NewMoneyFromString("40000", "USD")
	for _, entry := range []posting.JournalEntry{
		mustEntry(t, journalID, start.Add(30*24*time.Hour), accounts["1500"], accounts["1000"], asset, true),
		mustEntry(t, journalID, start.Add(60*24*time.Hour), accounts["1200"], accounts["4000"], sales, true),
		mustEntry(t, journalID, start.Add(90*24*time.Hour), accounts["5000"], accounts["1000"], sales, true),
	} {
		if err := engine.Posting.CreateDraft(entry); err != nil {
			t.Fatal(err)
		}
		if err := engine.Posting.Post(entry.ID, "checker"); err != nil {
			t.Fatal(err)
		}
	}

	revenueExpense, _ := financialtypes.NewMoneyFromString("40000", "USD")
	partner := uuid.New()
	if _, err := engine.Subledgers.Receivables.AddInvoice(partner, revenueExpense, start, end, "INV-1"); err != nil {
		t.Fatal(err)
	}
	event := subledgers.EconomicEvent{IdempotencyKey: "simulation-1", EventType: "sale", Amount: revenueExpense, Date: start.Add(60 * 24 * time.Hour), PartnerID: &partner}
	if err := engine.Subledgers.Repository.SaveEvent(event); err != nil {
		t.Fatal(err)
	}

	trial, err := engine.Reporting.TrialBalance.Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !trial.Balanced || !trial.DebitTotal.Equal(trial.CreditTotal) {
		t.Fatalf("trial balance is not balanced: debit=%s credit=%s", trial.DebitTotal, trial.CreditTotal)
	}

	for i := 0; i < 25; i++ {
		log := governance.FinancialAuditLog{
			EntityType: "move", EntityID: uuid.New(), Action: "POST", ActorID: uuid.New(),
			PayloadBefore: []byte("null"), PayloadAfter: []byte(`{"posted":true}`),
			Timestamp: time.Now().UTC(),
		}
		if err := engine.Governance.Append(context.Background(), log); err != nil {
			t.Fatal(err)
		}
	}
	valid, err := engine.Governance.VerifyIntegrity(context.Background())
	if err != nil || !valid {
		t.Fatalf("audit integrity = (%v, %v)", valid, err)
	}

	balanceSheet, err := engine.Reporting.BalanceSheet.Generate(end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !balanceSheet.Balanced {
		t.Fatal("balance sheet is not balanced")
	}
}

func mustEntry(t *testing.T, journalID uuid.UUID, date time.Time, debitAccount, creditAccount uuid.UUID, amount financialtypes.Money, debitThenCredit bool) posting.JournalEntry {
	t.Helper()
	if !debitThenCredit {
		debitAccount, creditAccount = creditAccount, debitAccount
	}
	lines := []posting.JournalEntryLine{
		{ID: uuid.New(), AccountID: debitAccount, Debit: amount},
		{ID: uuid.New(), AccountID: creditAccount, Credit: amount},
	}
	entry, err := posting.NewJournalEntry(journalID, date, "maker", lines)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
