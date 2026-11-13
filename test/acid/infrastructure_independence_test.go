package acid

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/shopspring/decimal"
)

func TestInfrastructureIndependence(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Failed to build engine with memory store: %v", err)
	}
	defer engine.Close()

	companyUUID := uuid.New()
	companyID := types.CompanyID(companyUUID)
	now := time.Now().UTC()
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

	var wg sync.WaitGroup
	numEntries := 200
	var failures int64

	for i := 0; i < numEntries; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			amt, _ := types.NewMoneyFromString("50.00", "USD")
			zero, _ := types.NewMoneyFromString("0.00", "USD")
			entry := ledger.JournalEntry{
				ID:            types.NewJournalEntryID(),
				CompanyID:     companyID,
				EffectiveDate: now,
				Description:   "Concurrent memory store test",
				CreatedBy:     "tester",
				Lines: []ledger.EntryLine{
					{AccountID: types.AccountID(cashID), TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amt, FunctionalCredit: zero},
					{AccountID: types.AccountID(revenueID), TransactionAmount: amt, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amt},
				},
			}
			if _, err := engine.Ledger().PostEntry(ctx, entry); err != nil {
				atomic.AddInt64(&failures, 1)
			}
		}(i)
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("expected all concurrent posts to succeed, got %d failures", failures)
	}

	tb, err := engine.Reporting().GetTrialBalance(ctx, companyID, now, "USD")
	if err != nil {
		t.Fatalf("Failed to retrieve trial balance after concurrent load: %v", err)
	}
	if tb.CompanyID != companyID {
		t.Fatalf("Trial balance company ID mismatch")
	}
}
