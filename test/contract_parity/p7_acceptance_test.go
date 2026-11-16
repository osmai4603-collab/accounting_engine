package contractparity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

func TestP7_GoldenCoreScenarioAcrossAdapters(t *testing.T) {
	started := time.Now()
	fixture := NewUnifiedFixture()
	fixture.CashAccount.Code = "1010"
	fixture.ExpenseAccount.Code = "1500"
	fixture.ExpenseAccount.Name = "Equipment"
	fixture.ExpenseAccount.Type = publicmasterdata.AccountTypeAsset
	fixture.PayableAccount.Code = "2100"
	fixture.EquityAccount.Code = "3000"

	memoryHarness := NewMemoryHarness(t, fixture)
	defer memoryHarness.Cleanup()
	externalHarness := NewExternalHarness(t, fixture)
	defer externalHarness.Cleanup()
	postgresHarness := NewPostgresHarness(t, fixture)
	if postgresHarness != nil {
		defer postgresHarness.Cleanup()
	}

	type outcome struct {
		harness *AdapterHarness
		balance reporting.TrialBalance
	}
	outcomes := []outcome{
		{harness: memoryHarness, balance: postGoldenCapitalAndEquipment(t, memoryHarness, fixture)},
		{harness: externalHarness, balance: postGoldenCapitalAndEquipment(t, externalHarness, fixture)},
	}
	if postgresHarness != nil {
		outcomes = append(outcomes, outcome{
			harness: postgresHarness,
			balance: postGoldenCapitalAndEquipment(t, postgresHarness, fixture),
		})
	}

	reference := outcomes[0].balance
	for _, current := range outcomes {
		differences := []string{"none; account balances and trial-balance totals match GS-01"}
		if !sameTrialBalance(reference, current.balance) {
			differences = []string{
				"trial balance differs from memory",
				"memory: " + trialBalanceSummary(reference),
				current.harness.Name + ": " + trialBalanceSummary(current.balance),
			}
			t.Errorf("GS-01 semantic trial-balance difference on %s: %v", current.harness.Name, differences)
		}
		GlobalReporter.Record(ScenarioResult{
			ScenarioName:          "P7-GS-01-CoreDoubleEntry",
			AdapterName:           current.harness.Name,
			EngineVersion:         EngineVersion(),
			Passed:                len(differences) == 1,
			SemanticDifferences:   differences,
			CapabilityDifferences: capabilityDifferences(current.harness),
			ExecutionDuration:     time.Since(started),
		})
	}
}

func postGoldenCapitalAndEquipment(t *testing.T, h *AdapterHarness, f UnifiedFixture) reporting.TrialBalance {
	t.Helper()
	ctx := context.Background()
	cmdContext := publicapplication.Context{
		Principal: publicapplication.Principal{
			ActorID:    "p7-golden-consumer",
			CompanyIDs: []types.CompanyID{f.Company.ID},
		},
		CorrelationID: "p7-gs-01",
		SourceSystem:  "p7-acceptance",
	}
	post := func(description string, date time.Time, lines []ledger.EntryLine) {
		t.Helper()
		draft, err := h.AppService.CreateDraft(ctx, publicapplication.CreateDraftCommand{
			Context:             cmdContext,
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.OpenPeriod.ID,
			EffectiveDate:       date,
			PostingDate:         date,
			Description:         description,
			SourceModule:        "p7-golden",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate.String(),
			Classification:      ledger.EntryClassRegular,
			Lines:               lines,
		})
		if err != nil {
			t.Fatalf("%s: create draft: %v", h.Name, err)
		}
		if err := h.AppService.Validate(ctx, publicapplication.ValidateEntryCommand{
			Context: cmdContext, CompanyID: f.Company.ID, EntryID: draft.Entry.ID,
		}); err != nil {
			t.Fatalf("%s: validate %s: %v", h.Name, description, err)
		}
		posted, err := h.AppService.Post(ctx, publicapplication.PostEntryCommand{
			Context: cmdContext, CompanyID: f.Company.ID, EntryID: draft.Entry.ID,
		})
		if err != nil {
			t.Fatalf("%s: post %s: %v", h.Name, description, err)
		}
		if posted.Entry.Status != ledger.EntryPosted {
			t.Fatalf("%s: %s status = %q, want posted", h.Name, description, posted.Entry.Status)
		}
	}
	makeLine := func(accountID types.AccountID, debit, credit string) ledger.EntryLine {
		debitAmount, err := types.NewMoneyFromString(debit, f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("create debit amount: %v", err)
		}
		creditAmount, err := types.NewMoneyFromString(credit, f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("create credit amount: %v", err)
		}
		transactionAmount := debitAmount
		if transactionAmount.IsZero() {
			transactionAmount = creditAmount
		}
		return ledger.EntryLine{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           accountID,
			TransactionAmount:   transactionAmount,
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate,
			FunctionalDebit:     debitAmount,
			FunctionalCredit:    creditAmount,
		}
	}
	date := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	post("GS-01 TX-01: capital contribution", date, []ledger.EntryLine{
		makeLine(f.CashAccount.ID, "500000", "0"),
		makeLine(f.EquityAccount.ID, "0", "500000"),
	})
	post("GS-01 TX-02: equipment purchase", date, []ledger.EntryLine{
		makeLine(f.ExpenseAccount.ID, "120000", "0"),
		makeLine(f.CashAccount.ID, "0", "20000"),
		makeLine(f.PayableAccount.ID, "0", "100000"),
	})

	balance, err := h.Engine.Reporting().GetTrialBalance(ctx, f.Company.ID, date, f.FunctionalCurrency)
	if err != nil {
		t.Fatalf("%s: get GS-01 trial balance: %v", h.Name, err)
	}
	if !balance.IsBalanced ||
		!balance.TotalDebit.Amount().Equal(decimal.NewFromInt(600000)) ||
		!balance.TotalCredit.Amount().Equal(decimal.NewFromInt(600000)) {
		t.Fatalf("%s: GS-01 ending totals = %s/%s balanced=%t, want 600000/600000",
			h.Name, balance.TotalDebit, balance.TotalCredit, balance.IsBalanced)
	}
	assertBalance := func(code, side, expected string) {
		t.Helper()
		for _, line := range balance.Lines {
			if line.AccountCode != code {
				continue
			}
			actual := line.EndingDebit
			opposite := line.EndingCredit
			if side == "credit" {
				actual = line.EndingCredit
				opposite = line.EndingDebit
			}
			want, _ := decimal.NewFromString(expected)
			if !actual.Amount().Equal(want) {
				t.Errorf("%s: GS-01 account %s ending %s = %s, want %s",
					h.Name, code, side, actual, expected)
			}
			if !opposite.Amount().IsZero() {
				t.Errorf("%s: GS-01 account %s unexpected opposite-side balance %s",
					h.Name, code, opposite)
			}
			return
		}
		t.Errorf("%s: GS-01 trial balance omitted account %s", h.Name, code)
	}
	assertBalance("1010", "debit", "480000")
	assertBalance("1500", "debit", "120000")
	assertBalance("2100", "credit", "100000")
	assertBalance("3000", "credit", "500000")
	return balance
}

func capabilityDifferences(h *AdapterHarness) []string {
	caps := h.Capabilities
	return []string{
		fmt.Sprintf("durability=%t", caps.GuaranteesDurability),
		fmt.Sprintf("foreign_keys=%t", caps.EnforcesForeignKeys),
		fmt.Sprintf("serializable=%t", caps.SupportsSerializable),
		fmt.Sprintf("savepoints=%t", caps.SupportsSavepoints),
	}
}
