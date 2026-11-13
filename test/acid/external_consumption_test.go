package acid

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/shopspring/decimal"
)

func TestExternalConsumption(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("External consumption build failed: %v", err)
	}
	defer engine.Close()

	companyUUID := uuid.New()
	companyID := types.CompanyID(companyUUID)
	entryID := types.NewJournalEntryID()
	now := time.Now().UTC()

	// Real posting path (P0-1) requires valid master data and open period.
	if err := store.CreateCompany(ctx, masterdata.Company{
		ID: companyUUID, Code: "CMP", LegalName: "Test Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("setup company: %v", err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyUUID
	for i := range year.Periods {
		year.Periods[i].CompanyID = companyUUID
	}
	if err := store.CreateFiscalYear(ctx, year); err != nil {
		t.Fatalf("setup fiscal year: %v", err)
	}
	cashID := uuid.New()
	revenueID := uuid.New()
	for _, acc := range []masterdata.Account{
		{ID: cashID, CompanyID: companyUUID, Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, CurrencyID: "USD", IsActive: true},
		{ID: revenueID, CompanyID: companyUUID, Code: "4000", Name: "Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, CurrencyID: "USD", IsActive: true},
	} {
		if err := store.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("setup account %s: %v", acc.Code, err)
		}
	}

	amt, _ := types.NewMoneyFromString("100.00", "USD")
	zeroAmt, _ := types.NewMoneyFromString("0.00", "USD")

	entry := ledger.JournalEntry{
		ID:            entryID,
		CompanyID:     companyID,
		EffectiveDate: now,
		Description:   "External consumption test entry",
		CreatedBy:     "tester",
		Lines: []ledger.EntryLine{
			{
				AccountID:           types.AccountID(cashID),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     amt,
				FunctionalCredit:    zeroAmt,
			},
			{
				AccountID:           types.AccountID(revenueID),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     zeroAmt,
				FunctionalCredit:    amt,
			},
		},
	}

	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("Failed to post entry externally: %v", err)
	}

	fetched, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve posted entry externally: %v", err)
	}

	if fetched.ID != entryID {
		t.Fatalf("Expected retrieved entry ID to match")
	}
	if fetched.Status != ledger.EntryPosted {
		t.Fatalf("Expected posted status, got %s", fetched.Status)
	}

	reversal, err := engine.Ledger().ReverseEntry(ctx, companyID, posted.ID, "Testing reversal")
	if err != nil {
		t.Fatalf("Failed to reverse entry: %v", err)
	}
	if reversal.ReversalOfID == nil || *reversal.ReversalOfID != entryID {
		t.Fatalf("Expected reversal reference to match original entry ID")
	}
	// P0-2: original must be marked reversed and reversal must be posted.
	originalAfter, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve original after reversal: %v", err)
	}
	if originalAfter.Status != ledger.EntryReversed {
		t.Fatalf("Expected original status reversed, got %s", originalAfter.Status)
	}

	tb, err := engine.Reporting().GetTrialBalance(ctx, companyID, now, "USD")
	if err != nil {
		t.Fatalf("Failed to generate trial balance: %v", err)
	}
	if tb.CompanyID != companyID {
		t.Fatalf("Trial balance company ID mismatch")
	}
}
