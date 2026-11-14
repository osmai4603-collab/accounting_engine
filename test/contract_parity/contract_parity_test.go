package contractparity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

func TestMain(m *testing.M) {
	code := m.Run()

	for _, path := range []string{"parity_report.md", "../../docs/reports/contract_parity_report.md"} {
		if err := GlobalReporter.WriteReportToFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "write contract parity report %q: %v\n", path, err)
			code = 1
		}
	}

	os.Exit(code)
}

// ------------------------------------------------------------- Test 1: Core Lifecycle

func TestContractParity_CoreLifecycle(t *testing.T) {
	ForEachAdapter(t, "CoreLifecycle", func(t *testing.T, h *AdapterHarness, f UnifiedFixture) {
		ctx := context.Background()

		amount150, err := types.NewMoneyFromString("150.00", f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("create amount: %v", err)
		}
		zero, err := types.Zero(f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("create zero: %v", err)
		}

		effDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
		cmdCtx := publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    "parity-tester",
				CompanyIDs: []types.CompanyID{f.Company.ID},
			},
			CorrelationID: "parity-cor-1",
			SourceSystem:  "parity-suite",
		}

		// 1. Create Draft
		createCmd := publicapplication.CreateDraftCommand{
			Context:             cmdCtx,
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.OpenPeriod.ID,
			EffectiveDate:       effDate,
			PostingDate:         effDate,
			Description:         "Core Lifecycle Cash Sale",
			SourceModule:        "parity-suite",
			ReferenceID:         "REF-CORE-1",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate.String(),
			Classification:      ledger.EntryClassRegular,
			Lines: []ledger.EntryLine{
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.CashAccount.ID,
					TransactionAmount:   amount150,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     amount150,
					FunctionalCredit:    zero,
				},
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.RevenueAccount.ID,
					TransactionAmount:   amount150,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     zero,
					FunctionalCredit:    amount150,
				},
			},
		}

		draftRes, err := h.AppService.CreateDraft(ctx, createCmd)
		if err != nil {
			t.Fatalf("CreateDraft failed: %v", err)
		}
		draft := draftRes.Entry
		if draft.Status != ledger.EntryDraft {
			t.Errorf("draft status = %q, want draft", draft.Status)
		}

		// 2. Validate
		valErr := h.AppService.Validate(ctx, publicapplication.ValidateEntryCommand{
			Context:   cmdCtx,
			CompanyID: f.Company.ID,
			EntryID:   draft.ID,
		})
		if valErr != nil {
			t.Fatalf("Validate failed: %v", valErr)
		}

		// 3. Post
		postRes, err := h.AppService.Post(ctx, publicapplication.PostEntryCommand{
			Context:   cmdCtx,
			CompanyID: f.Company.ID,
			EntryID:   draft.ID,
		})
		if err != nil {
			t.Fatalf("Post failed: %v", err)
		}
		posted := postRes.Entry
		if posted.Status != ledger.EntryPosted {
			t.Errorf("posted status = %q, want posted", posted.Status)
		}
		if posted.SequenceNumber < 1 {
			t.Errorf("posted sequence number = %d, want >= 1", posted.SequenceNumber)
		}
		if posted.PostedBy != "parity-tester" {
			t.Errorf("posted actor = %q, want parity-tester", posted.PostedBy)
		}

		// 4. GetEntry
		readBack, err := h.Engine.Ledger().GetEntry(ctx, f.Company.ID, posted.ID)
		if err != nil {
			t.Fatalf("GetEntry failed: %v", err)
		}
		if readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 || !sameEntry(posted, readBack) {
			t.Errorf("read-back invalid: status=%q, lines=%d", readBack.Status, len(readBack.Lines))
		}

		// 5. Check Trial Balance
		tb, err := h.Engine.Reporting().GetTrialBalance(ctx, f.Company.ID, effDate, f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("GetTrialBalance failed: %v", err)
		}
		if !tb.IsBalanced {
			t.Errorf("trial balance is not balanced")
		}
		wantAmt := decimal.NewFromInt(150)
		if !tb.TotalDebit.Amount().Equal(wantAmt) || !tb.TotalCredit.Amount().Equal(wantAmt) {
			t.Errorf("trial balance amounts = %s / %s, want %s", tb.TotalDebit, tb.TotalCredit, wantAmt)
		}

	})
}

// ------------------------------------------------------------- Test 2: Reversal Lifecycle

func TestContractParity_ReversalLifecycle(t *testing.T) {
	ForEachAdapter(t, "ReversalLifecycle", func(t *testing.T, h *AdapterHarness, f UnifiedFixture) {
		ctx := context.Background()

		amount, _ := types.NewMoneyFromString("200.00", f.FunctionalCurrency)
		zero, _ := types.Zero(f.FunctionalCurrency)
		effDate := time.Date(2026, time.January, 18, 0, 0, 0, 0, time.UTC)

		cmdCtx := publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    "reversal-tester",
				CompanyIDs: []types.CompanyID{f.Company.ID},
			},
			CorrelationID: "rev-cor-1",
			SourceSystem:  "parity-suite",
		}

		// Post initial entry
		createCmd := publicapplication.CreateDraftCommand{
			Context:             cmdCtx,
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.OpenPeriod.ID,
			EffectiveDate:       effDate,
			PostingDate:         effDate,
			Description:         "Entry to be reversed",
			SourceModule:        "parity-suite",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate.String(),
			Lines: []ledger.EntryLine{
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.CashAccount.ID,
					TransactionAmount:   amount,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     amount,
					FunctionalCredit:    zero,
				},
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.RevenueAccount.ID,
					TransactionAmount:   amount,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     zero,
					FunctionalCredit:    amount,
				},
			},
		}

		draft, err := h.AppService.CreateDraft(ctx, createCmd)
		if err != nil {
			t.Fatalf("CreateDraft: %v", err)
		}
		posted, err := h.AppService.Post(ctx, publicapplication.PostEntryCommand{
			Context:   cmdCtx,
			CompanyID: f.Company.ID,
			EntryID:   draft.Entry.ID,
		})
		if err != nil {
			t.Fatalf("Post: %v", err)
		}

		// Execute Reversal
		revRes, err := h.AppService.Reverse(ctx, publicapplication.ReverseEntryCommand{
			Context:       cmdCtx,
			CompanyID:     f.Company.ID,
			EntryID:       posted.Entry.ID,
			Reason:        "Correction of test entry",
			EffectiveDate: effDate,
		})
		if err != nil {
			t.Fatalf("Reverse: %v", err)
		}
		reversal := revRes.Entry
		if reversal.Status != ledger.EntryPosted {
			t.Errorf("reversal status = %q, want posted", reversal.Status)
		}
		if reversal.Classification != ledger.EntryClassReversing {
			t.Errorf("reversal classification = %q, want reversing", reversal.Classification)
		}

		// Verify original marked as Reversed
		origAfterRev, err := h.Engine.Ledger().GetEntry(ctx, f.Company.ID, posted.Entry.ID)
		if err != nil {
			t.Fatalf("GetEntry on original: %v", err)
		}
		if origAfterRev.Status != ledger.EntryReversed {
			t.Errorf("original status after reversal = %q, want reversed", origAfterRev.Status)
		}

		// Verify trial balance is net zero
		tb, err := h.Engine.Reporting().GetTrialBalance(ctx, f.Company.ID, effDate, f.FunctionalCurrency)
		if err != nil {
			t.Fatalf("GetTrialBalance: %v", err)
		}
		if !tb.IsBalanced || !tb.TotalDebit.Equal(tb.TotalCredit) {
			t.Errorf("trial balance after reversal is unbalanced: debit=%s, credit=%s", tb.TotalDebit, tb.TotalCredit)
		}

		// Verify Double Reversal is rejected across all adapters
		_, doubleRevErr := h.AppService.Reverse(ctx, publicapplication.ReverseEntryCommand{
			Context:       cmdCtx,
			CompanyID:     f.Company.ID,
			EntryID:       posted.Entry.ID,
			Reason:        "Second reversal attempt",
			EffectiveDate: effDate,
		})
		if doubleRevErr == nil {
			t.Errorf("second reversal succeeded, want rejection")
		} else {
			originalAfterRetry, getErr := h.Engine.Ledger().GetEntry(ctx, f.Company.ID, posted.Entry.ID)
			if getErr != nil {
				t.Fatalf("GetEntry after rejected second reversal: %v", getErr)
			}
			if originalAfterRetry.Status != ledger.EntryReversed || originalAfterRetry.ReversedByID == nil {
				t.Errorf("rejected second reversal changed original state: status=%q reversedBy=%v",
					originalAfterRetry.Status, originalAfterRetry.ReversedByID)
			}
		}

	})
}

// ------------------------------------------------------------- Test 3: Event Idempotency

func TestContractParity_EventIdempotency(t *testing.T) {
	ForEachAdapter(t, "EventIdempotency", func(t *testing.T, h *AdapterHarness, f UnifiedFixture) {
		ctx := context.Background()

		amount, _ := types.NewMoneyFromString("300.00", f.FunctionalCurrency)
		zero, _ := types.Zero(f.FunctionalCurrency)
		effDate := time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC)
		idemKey := fmt.Sprintf("idem-key-%s", uuid.NewString()[:8])

		eventDraft := ledger.JournalEntry{
			ID:                  types.NewJournalEntryID(),
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.OpenPeriod.ID,
			Status:              ledger.EntryDraft,
			EffectiveDate:       effDate,
			PostingDate:         effDate,
			Description:         "Event Idempotency Test",
			SourceModule:        "parity-events",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate,
			Lines: []ledger.EntryLine{
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.CashAccount.ID,
					TransactionAmount:   amount,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     amount,
					FunctionalCredit:    zero,
				},
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.RevenueAccount.ID,
					TransactionAmount:   amount,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     zero,
					FunctionalCredit:    amount,
				},
			},
		}

		event1 := events.AccountingEvent{
			EventID:        "evt-" + uuid.NewString()[:8],
			EventType:      "SALES_INVOICE",
			CompanyID:      f.Company.ID,
			EffectiveDate:  effDate,
			IdempotencyKey: idemKey,
			JournalDraft:   &eventDraft,
		}

		// 1. First event submission
		res1, err := h.Engine.Events().ProcessEvent(ctx, event1)
		if err != nil {
			t.Fatalf("first ProcessEvent failed: %v", err)
		}
		if res1.Status != ledger.EntryPosted {
			t.Errorf("first event status = %q, want posted", res1.Status)
		}

		// 2. Exact same key & payload re-submission
		res2, err := h.Engine.Events().ProcessEvent(ctx, event1)
		if err != nil {
			t.Fatalf("second identical ProcessEvent failed: %v", err)
		}
		if res2.ID != res1.ID {
			t.Errorf("second ProcessEvent returned ID %s, want %s (duplicate entry created!)", res2.ID, res1.ID)
		}
		if res2.SequenceNumber != res1.SequenceNumber {
			t.Errorf("second ProcessEvent sequence %d != %d", res2.SequenceNumber, res1.SequenceNumber)
		}

		// 3. Same key with DIFFERENT payload -> MUST return conflict
		diffAmount, _ := types.NewMoneyFromString("999.00", f.FunctionalCurrency)
		conflictingDraft := eventDraft
		conflictingDraft.Lines = []ledger.EntryLine{
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           f.CashAccount.ID,
				TransactionAmount:   diffAmount,
				TransactionCurrency: f.FunctionalCurrency,
				ExchangeRate:        f.ExchangeRate,
				FunctionalDebit:     diffAmount,
				FunctionalCredit:    zero,
			},
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           f.RevenueAccount.ID,
				TransactionAmount:   diffAmount,
				TransactionCurrency: f.FunctionalCurrency,
				ExchangeRate:        f.ExchangeRate,
				FunctionalDebit:     zero,
				FunctionalCredit:    diffAmount,
			},
		}
		eventConflicting := events.AccountingEvent{
			EventID:        "evt-conflict-" + uuid.NewString()[:8],
			EventType:      "SALES_INVOICE",
			CompanyID:      f.Company.ID,
			EffectiveDate:  effDate,
			IdempotencyKey: idemKey, // same key!
			JournalDraft:   &conflictingDraft,
		}

		_, conflictErr := h.Engine.Events().ProcessEvent(ctx, eventConflicting)
		if conflictErr == nil {
			t.Errorf("conflicting payload with same idempotency key succeeded, want conflict")
		} else if !errors.Is(conflictErr, sdk.ErrIdempotencyKeyConflict) &&
			!errors.Is(conflictErr, publicapplication.ErrConflict) &&
			!errors.Is(conflictErr, ErrExternalConflict) {
			t.Errorf("conflicting payload returned %v, want a semantic conflict", conflictErr)
		}

	})
}

// ------------------------------------------------------------- Test 4: Semantic Errors & Invariants

func TestContractParity_SemanticErrorsAndInvariants(t *testing.T) {
	ForEachAdapter(t, "SemanticErrorsAndInvariants", func(t *testing.T, h *AdapterHarness, f UnifiedFixture) {
		ctx := context.Background()

		amt100, _ := types.NewMoneyFromString("100.00", f.FunctionalCurrency)
		amt90, _ := types.NewMoneyFromString("90.00", f.FunctionalCurrency)
		zero, _ := types.Zero(f.FunctionalCurrency)

		cmdCtx := publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    "invariants-tester",
				CompanyIDs: []types.CompanyID{f.Company.ID},
			},
			CorrelationID: "inv-cor-1",
			SourceSystem:  "parity-suite",
		}

		// A. Closed Fiscal Period Invariant
		closedDate := time.Date(2026, time.February, 10, 0, 0, 0, 0, time.UTC)
		closedCmd := publicapplication.CreateDraftCommand{
			Context:             cmdCtx,
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.ClosedPeriod.ID, // Period is Closed!
			EffectiveDate:       closedDate,
			PostingDate:         closedDate,
			Description:         "Posting to closed period",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate.String(),
			Lines: []ledger.EntryLine{
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.CashAccount.ID,
					TransactionAmount:   amt100,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     amt100,
					FunctionalCredit:    zero,
				},
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.RevenueAccount.ID,
					TransactionAmount:   amt100,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     zero,
					FunctionalCredit:    amt100,
				},
			},
		}

		draftClosed, err := h.AppService.CreateDraft(ctx, closedCmd)
		if err == nil {
			_, postClosedErr := h.AppService.Post(ctx, publicapplication.PostEntryCommand{
				Context:   cmdCtx,
				CompanyID: f.Company.ID,
				EntryID:   draftClosed.Entry.ID,
			})
			if postClosedErr == nil {
				t.Errorf("posting to closed period succeeded, want rejection")
			} else if !errors.Is(postClosedErr, publicapplication.ErrPeriodClosed) &&
				!errors.Is(postClosedErr, publicapplication.ErrConflict) &&
				!errors.Is(postClosedErr, types.ErrFiscalPeriodClosed) {
				t.Errorf("closed period returned %v, want a period-closed or conflict error", postClosedErr)
			} else {
				t.Logf("closed period correctly rejected: %v", postClosedErr)
			}
		}

		// B. Unbalanced Entry Invariant (Debit $100 != Credit $90)
		unbalancedCmd := publicapplication.CreateDraftCommand{
			Context:             cmdCtx,
			CompanyID:           f.Company.ID,
			JournalID:           types.JournalID(f.Journal.ID),
			FiscalPeriodID:      f.OpenPeriod.ID,
			EffectiveDate:       time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
			PostingDate:         time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
			Description:         "Unbalanced Entry",
			TransactionCurrency: f.FunctionalCurrency,
			ExchangeRate:        f.ExchangeRate.String(),
			Lines: []ledger.EntryLine{
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.CashAccount.ID,
					TransactionAmount:   amt100,
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     amt100,
					FunctionalCredit:    zero,
				},
				{
					ID:                  types.NewJournalEntryLineID(),
					AccountID:           f.RevenueAccount.ID,
					TransactionAmount:   amt90, // Unbalanced!
					TransactionCurrency: f.FunctionalCurrency,
					ExchangeRate:        f.ExchangeRate,
					FunctionalDebit:     zero,
					FunctionalCredit:    amt90,
				},
			},
		}

		draftUnbal, err := h.AppService.CreateDraft(ctx, unbalancedCmd)
		if err == nil {
			valErr := h.AppService.Validate(ctx, publicapplication.ValidateEntryCommand{
				Context:   cmdCtx,
				CompanyID: f.Company.ID,
				EntryID:   draftUnbal.Entry.ID,
			})
			if valErr == nil {
				t.Errorf("validating unbalanced entry succeeded, want validation failure")
			} else if !errors.Is(valErr, publicapplication.ErrValidation) &&
				!errors.Is(valErr, types.ErrUnbalancedMove) {
				t.Errorf("unbalanced entry returned %v, want a validation error", valErr)
			} else {
				t.Logf("unbalanced entry correctly failed validation: %v", valErr)
			}
		}

		// C. Non-existent accounts are rejected through the common SPI error contract.
		badAccountCmd := unbalancedCmd
		badAccountCmd.Description = "Unknown account entry"
		badAccountCmd.Lines = append([]ledger.EntryLine(nil), unbalancedCmd.Lines...)
		badAccountCmd.Lines[0].ID = types.NewJournalEntryLineID()
		badAccountCmd.Lines[0].AccountID = types.NewAccountID()
		badAccountCmd.Lines[1].ID = types.NewJournalEntryLineID()
		badAccountCmd.Lines[1].TransactionAmount = amt100
		badAccountCmd.Lines[1].FunctionalCredit = amt100
		draftBadAccount, createErr := h.AppService.CreateDraft(ctx, badAccountCmd)
		if createErr == nil {
			validationErr := h.AppService.Validate(ctx, publicapplication.ValidateEntryCommand{
				Context:   cmdCtx,
				CompanyID: f.Company.ID,
				EntryID:   draftBadAccount.Entry.ID,
			})
			if validationErr == nil {
				t.Errorf("validating an entry with an unknown account succeeded")
			} else if !errors.Is(validationErr, publicapplication.ErrValidation) &&
				!errors.Is(validationErr, publicapplication.ErrNotFound) &&
				!errors.Is(validationErr, types.ErrAccountNotFound) {
				t.Errorf("unknown account returned %v, want a validation or not-found error", validationErr)
			}
		}

		// D. Cross-Company Isolation Invariant
		otherCompanyID := types.NewCompanyID()
		tenantCmd := unbalancedCmd
		tenantCmd.Description = "Tenant isolation draft"
		tenantCmd.Lines = append([]ledger.EntryLine(nil), unbalancedCmd.Lines...)
		tenantCmd.Lines[0].ID = types.NewJournalEntryLineID()
		tenantCmd.Lines[1].ID = types.NewJournalEntryLineID()
		tenantCmd.Lines[1].TransactionAmount = amt100
		tenantCmd.Lines[1].FunctionalCredit = amt100
		tenantDraft, createTenantErr := h.AppService.CreateDraft(ctx, tenantCmd)
		if createTenantErr != nil {
			t.Fatalf("create tenant-isolation draft: %v", createTenantErr)
		}
		_, crossErr := h.Engine.Ledger().GetEntry(ctx, otherCompanyID, tenantDraft.Entry.ID)
		if crossErr == nil {
			t.Errorf("querying across company boundary succeeded, want error")
		} else {
			t.Logf("cross-company access correctly rejected: %v", crossErr)
		}

	})
}

// ------------------------------------------------------------- Test 5: Capabilities Matrix

func TestContractParity_CapabilitiesMatrix(t *testing.T) {
	ForEachAdapter(t, "CapabilitiesMatrix", func(t *testing.T, h *AdapterHarness, f UnifiedFixture) {
		caps := h.Capabilities

		switch h.Name {
		case "postgres":
			if !caps.GuaranteesDurability {
				t.Errorf("postgres must declare GuaranteesDurability=true")
			}
			if !caps.EnforcesForeignKeys {
				t.Errorf("postgres must declare EnforcesForeignKeys=true")
			}
		case "memory":
			if caps.GuaranteesDurability {
				t.Errorf("memory must not declare GuaranteesDurability=true")
			}
			if caps.EnforcesForeignKeys {
				t.Errorf("memory must not declare EnforcesForeignKeys=true")
			}
		case "external":
			if caps.GuaranteesDurability {
				t.Errorf("external must not declare GuaranteesDurability=true")
			}
			if caps.EnforcesForeignKeys {
				t.Errorf("external must not declare EnforcesForeignKeys=true")
			}
		}

	})
}

// ------------------------------------------------------------- Test 6: Cross-Adapter Equivalence

func TestContractParity_CrossAdapterSemanticEquivalence(t *testing.T) {
	// Execute an identical scenario on all three adapters and verify normalized output parity.
	ctx := context.Background()
	start := time.Now()
	defer func() {
		unexpected := []string(nil)
		if t.Failed() {
			unexpected = []string{"cross-adapter assertions failed; see the corresponding go test output"}
		}
		GlobalReporter.Record(ScenarioResult{
			ScenarioName:          "CrossAdapterSemanticEquivalence",
			AdapterName:           "all",
			Passed:                !t.Failed() && !t.Skipped(),
			Skipped:               t.Skipped(),
			UnexpectedDifferences: unexpected,
			ExecutionDuration:     time.Since(start),
		})
	}()

	companyID := types.NewCompanyID()
	journalID := publicmasterdata.NewJournalID()
	cashID := types.NewAccountID()
	revenueID := types.NewAccountID()
	yearID := types.NewFiscalYearID()
	periodID := types.NewFiscalPeriodID()
	entryID := types.NewJournalEntryID()
	line1ID := types.NewJournalEntryLineID()
	line2ID := types.NewJournalEntryLineID()
	effDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	fixture := UnifiedFixture{
		Company: publicmasterdata.Company{
			ID: companyID, Code: "EQUIV-CO", LegalName: "Equivalence Corp",
			FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
		},
		Journal: publicmasterdata.Journal{
			ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General",
		},
		CashAccount: publicmasterdata.Account{
			ID: cashID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		ReceivableAccount: publicmasterdata.Account{
			ID: types.NewAccountID(), CompanyID: companyID, Code: "1200", Name: "Receivables",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		PayableAccount: publicmasterdata.Account{
			ID: types.NewAccountID(), CompanyID: companyID, Code: "2000", Name: "Payables",
			Type: publicmasterdata.AccountTypeLiability, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
		EquityAccount: publicmasterdata.Account{
			ID: types.NewAccountID(), CompanyID: companyID, Code: "3000", Name: "Equity",
			Type: publicmasterdata.AccountTypeEquity, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
		RevenueAccount: publicmasterdata.Account{
			ID: revenueID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
		ExpenseAccount: publicmasterdata.Account{
			ID: types.NewAccountID(), CompanyID: companyID, Code: "5000", Name: "Expenses",
			Type: publicmasterdata.AccountTypeExpense, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		FiscalYear: publicmasterdata.FiscalYear{
			ID: yearID, CompanyID: companyID, Name: "FY2026",
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			State:     publicmasterdata.FiscalYearOpen,
		},
		OpenPeriod: publicmasterdata.FiscalPeriod{
			ID: periodID, CompanyID: companyID, FiscalYearID: yearID, Number: 1, Name: "Jan",
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
			State:     publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
		},
		ClosedPeriod: publicmasterdata.FiscalPeriod{
			ID: types.NewFiscalPeriodID(), CompanyID: companyID, FiscalYearID: yearID, Number: 2, Name: "Feb",
			StartDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC),
			State:     publicmasterdata.PeriodHardClosed, Type: publicmasterdata.FiscalPeriodTypeNormal,
		},
		FunctionalCurrency: "USD",
		ExchangeRate:       decimal.NewFromInt(1),
	}

	amt, _ := types.NewMoneyFromString("500.00", "USD")
	zero, _ := types.Zero("USD")

	runScenario := func(h *AdapterHarness) (
		ledger.JournalEntry,
		ledger.JournalEntry,
		ledger.JournalEntry,
		reporting.TrialBalance,
		reporting.GeneralLedgerReport,
	) {
		entry := ledger.JournalEntry{
			ID:                  entryID,
			CompanyID:           companyID,
			JournalID:           types.JournalID(journalID),
			FiscalPeriodID:      periodID,
			Status:              ledger.EntryDraft,
			EffectiveDate:       effDate,
			PostingDate:         effDate,
			Description:         "Equivalence Sale",
			SourceModule:        "equivalence-test",
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			Lines: []ledger.EntryLine{
				{
					ID:                  line1ID,
					AccountID:           cashID,
					TransactionAmount:   amt,
					TransactionCurrency: "USD",
					ExchangeRate:        decimal.NewFromInt(1),
					FunctionalDebit:     amt,
					FunctionalCredit:    zero,
				},
				{
					ID:                  line2ID,
					AccountID:           revenueID,
					TransactionAmount:   amt,
					TransactionCurrency: "USD",
					ExchangeRate:        decimal.NewFromInt(1),
					FunctionalDebit:     zero,
					FunctionalCredit:    amt,
				},
			},
		}

		posted, err := h.Engine.Ledger().PostEntry(ctx, entry)
		if err != nil {
			t.Fatalf("%s: PostEntry: %v", h.Name, err)
		}

		commandContext := publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    "equivalence-tester",
				CompanyIDs: []types.CompanyID{companyID},
			},
			SourceSystem: "parity-suite",
		}
		reversalResult, err := h.AppService.Reverse(ctx, publicapplication.ReverseEntryCommand{
			Context:       commandContext,
			CompanyID:     companyID,
			EntryID:       posted.ID,
			Reason:        "Cross-adapter parity reversal",
			EffectiveDate: effDate,
		})
		if err != nil {
			t.Fatalf("%s: Reverse: %v", h.Name, err)
		}
		originalAfterReversal, err := h.Engine.Ledger().GetEntry(ctx, companyID, posted.ID)
		if err != nil {
			t.Fatalf("%s: GetEntry after reversal: %v", h.Name, err)
		}

		tb, err := h.Engine.Reporting().GetTrialBalance(ctx, companyID, effDate, "USD")
		if err != nil {
			t.Fatalf("%s: GetTrialBalance: %v", h.Name, err)
		}

		gl, err := h.Engine.Reporting().GetGeneralLedger(ctx, companyID, cashID, time.Time{}, effDate)
		if err != nil {
			t.Fatalf("%s: GetGeneralLedger: %v", h.Name, err)
		}
		if len(gl.Entries) != 1 || gl.Entries[0].EntryID != reversalResult.Entry.ID {
			t.Fatalf("%s: general ledger entries do not identify the reversal: %#v", h.Name, gl.Entries)
		}

		return posted, reversalResult.Entry, originalAfterReversal, tb, gl
	}

	// 1. Run Memory
	memH := NewMemoryHarness(t, fixture)
	defer memH.Cleanup()
	memEntry, memReversal, memOriginal, memBalance, memLedger := runScenario(memH)

	// 2. Run External
	extH := NewExternalHarness(t, fixture)
	defer extH.Cleanup()
	extEntry, extReversal, extOriginal, extBalance, extLedger := runScenario(extH)

	// 3. Run Postgres
	pgH := NewPostgresHarness(t, fixture)
	if pgH != nil {
		defer pgH.Cleanup()
		pgEntry, pgReversal, pgOriginal, pgBalance, pgLedger := runScenario(pgH)

		if !sameTrialBalance(memBalance, pgBalance) || !sameTrialBalance(memBalance, extBalance) {
			t.Fatalf("trial balance mismatch across adapters: memory=%s external=%s postgres=%s",
				trialBalanceSummary(memBalance), trialBalanceSummary(extBalance), trialBalanceSummary(pgBalance))
		}

		if !sameEntry(memEntry, pgEntry) || !sameEntry(memEntry, extEntry) {
			t.Fatalf("posted entry differs across adapters")
		}
		if memEntry.ID != pgEntry.ID || memEntry.ID != extEntry.ID {
			t.Fatalf("posted entry identity differs across adapters")
		}
		if !sameReversal(memReversal, pgReversal) || !sameReversal(memReversal, extReversal) {
			t.Fatalf("reversal entry differs across adapters: memory=%s external=%s postgres=%s",
				reversalSummary(memReversal), reversalSummary(extReversal), reversalSummary(pgReversal))
		}
		if !reversedOriginalsMatch(memOriginal, pgOriginal) || !reversedOriginalsMatch(memOriginal, extOriginal) {
			t.Fatalf("original reversal state differs across adapters")
		}
		if *memOriginal.ReversedByID != memReversal.ID ||
			*pgOriginal.ReversedByID != pgReversal.ID ||
			*extOriginal.ReversedByID != extReversal.ID {
			t.Fatalf("original-to-reversal identity link differs across adapters")
		}
		if !sameGeneralLedger(memLedger, pgLedger) || !sameGeneralLedger(memLedger, extLedger) {
			t.Fatalf("general ledger mismatch across adapters: memory=%s external=%s postgres=%s",
				generalLedgerSummary(memLedger), generalLedgerSummary(extLedger), generalLedgerSummary(pgLedger))
		}
	}

	if !sameTrialBalance(memBalance, extBalance) {
		t.Fatalf("trial balance differs between memory and external adapters")
	}
	if !sameEntry(memEntry, extEntry) {
		t.Fatalf("posted entry differs between memory and external adapters")
	}
	if memEntry.ID != extEntry.ID {
		t.Fatalf("posted entry identity differs between memory and external adapters")
	}
	if !sameReversal(memReversal, extReversal) {
		t.Fatalf("reversal entry differs between memory and external adapters")
	}
	if !reversedOriginalsMatch(memOriginal, extOriginal) {
		t.Fatalf("original reversal state differs between memory and external adapters")
	}
	if *memOriginal.ReversedByID != memReversal.ID || *extOriginal.ReversedByID != extReversal.ID {
		t.Fatalf("original-to-reversal identity link differs between memory and external adapters")
	}
	if !sameGeneralLedger(memLedger, extLedger) {
		t.Fatalf("general ledger differs between memory and external adapters")
	}
}

func sameEntry(a, b ledger.JournalEntry) bool {
	type lineSnapshot struct {
		ID                  types.JournalEntryLineID
		AccountID           types.AccountID
		TransactionAmount   string
		TransactionCurrency string
		ExchangeRate        string
		FunctionalDebit     string
		FunctionalCredit    string
	}
	type entrySnapshot struct {
		ID                  types.JournalEntryID
		CompanyID           types.CompanyID
		JournalID           types.JournalID
		FiscalPeriodID      types.FiscalPeriodID
		Status              ledger.EntryStatus
		SequenceNumber      int64
		EntryNumber         string
		EffectiveDate       time.Time
		PostingDate         time.Time
		Description         string
		TransactionCurrency string
		ExchangeRate        string
		Classification      ledger.EntryClassification
		ReferenceID         string
		CreatedBy           string
		PostedBy            string
		ReversalOfID        string
		ReversalReason      string
		Lines               []lineSnapshot
	}
	snapshot := func(entry ledger.JournalEntry) entrySnapshot {
		result := entrySnapshot{
			ID: entry.ID, CompanyID: entry.CompanyID, JournalID: entry.JournalID,
			FiscalPeriodID: entry.FiscalPeriodID, Status: entry.Status,
			SequenceNumber: entry.SequenceNumber, EntryNumber: entry.EntryNumber,
			EffectiveDate: entry.EffectiveDate, PostingDate: entry.PostingDate,
			Description: entry.Description, TransactionCurrency: entry.TransactionCurrency,
			ExchangeRate: entry.ExchangeRate.String(), Classification: entry.Classification,
			ReferenceID: entry.ReferenceID, CreatedBy: entry.CreatedBy, PostedBy: entry.PostedBy,
			ReversalReason: entry.ReversalReason,
		}
		if entry.ReversalOfID != nil {
			result.ReversalOfID = uuid.UUID(*entry.ReversalOfID).String()
		}
		for _, line := range entry.Lines {
			result.Lines = append(result.Lines, lineSnapshot{
				ID: line.ID, AccountID: line.AccountID,
				TransactionAmount:   line.TransactionAmount.String(),
				TransactionCurrency: line.TransactionCurrency,
				ExchangeRate:        line.ExchangeRate.String(),
				FunctionalDebit:     line.FunctionalDebit.String(),
				FunctionalCredit:    line.FunctionalCredit.String(),
			})
		}
		return result
	}
	return reflect.DeepEqual(snapshot(a), snapshot(b))
}

func sameReversal(a, b ledger.JournalEntry) bool {
	if a.Status != ledger.EntryPosted || b.Status != ledger.EntryPosted ||
		a.Classification != ledger.EntryClassReversing || b.Classification != ledger.EntryClassReversing ||
		a.ReversalOfID == nil || b.ReversalOfID == nil || *a.ReversalOfID != *b.ReversalOfID ||
		a.ReversalReason != b.ReversalReason {
		return false
	}
	a.ID, b.ID = types.JournalEntryID{}, types.JournalEntryID{}
	for i := range a.Lines {
		a.Lines[i].ID = types.JournalEntryLineID{}
	}
	for i := range b.Lines {
		b.Lines[i].ID = types.JournalEntryLineID{}
	}
	return sameEntry(a, b)
}

func reversalSummary(entry ledger.JournalEntry) string {
	var lines string
	for _, line := range entry.Lines {
		lines += fmt.Sprintf("%s:%s/%s;", line.AccountID, line.FunctionalDebit, line.FunctionalCredit)
	}
	return fmt.Sprintf("status=%s classification=%s sequence=%d number=%q reversalOf=%v reason=%q date=%s lines=[%s]",
		entry.Status, entry.Classification, entry.SequenceNumber, entry.EntryNumber,
		entry.ReversalOfID, entry.ReversalReason, entry.EffectiveDate.Format(time.RFC3339), lines)
}

func reversedOriginalsMatch(a, b ledger.JournalEntry) bool {
	return a.ID == b.ID && a.Status == ledger.EntryReversed && b.Status == ledger.EntryReversed &&
		a.ReversedByID != nil && b.ReversedByID != nil
}

func sameTrialBalance(a, b reporting.TrialBalance) bool {
	type lineSnapshot struct {
		AccountID                 types.AccountID
		AccountCode, AccountName  string
		PeriodDebit, PeriodCredit string
		EndingDebit, EndingCredit string
	}
	snapshot := func(balance reporting.TrialBalance) (string, string, bool, map[string]lineSnapshot) {
		lines := make(map[string]lineSnapshot, len(balance.Lines))
		for _, line := range balance.Lines {
			lines[line.AccountCode] = lineSnapshot{
				AccountID: line.AccountID, AccountCode: line.AccountCode, AccountName: line.AccountName,
				PeriodDebit: line.PeriodDebit.String(), PeriodCredit: line.PeriodCredit.String(),
				EndingDebit: line.EndingDebit.String(), EndingCredit: line.EndingCredit.String(),
			}
		}
		return balance.TotalDebit.String(), balance.TotalCredit.String(), balance.IsBalanced, lines
	}
	aDebit, aCredit, aBalanced, aLines := snapshot(a)
	bDebit, bCredit, bBalanced, bLines := snapshot(b)
	return aDebit == bDebit && aCredit == bCredit && aBalanced == bBalanced && reflect.DeepEqual(aLines, bLines)
}

func trialBalanceSummary(balance reporting.TrialBalance) string {
	var out string
	for _, line := range balance.Lines {
		out += fmt.Sprintf("%s:%s/%s/%s/%s;", line.AccountCode,
			line.PeriodDebit, line.PeriodCredit, line.EndingDebit, line.EndingCredit)
	}
	return fmt.Sprintf("debit=%s credit=%s balanced=%t [%s]",
		balance.TotalDebit, balance.TotalCredit, balance.IsBalanced, out)
}

func sameGeneralLedger(a, b reporting.GeneralLedgerReport) bool {
	type entrySnapshot struct {
		EntryNumber, Description string
		EffectiveDate            time.Time
		Debit, Credit, Running   string
	}
	snapshot := func(report reporting.GeneralLedgerReport) (string, string, string, []entrySnapshot) {
		entries := make([]entrySnapshot, 0, len(report.Entries))
		for _, entry := range report.Entries {
			entries = append(entries, entrySnapshot{
				EntryNumber: entry.EntryNumber,
				Description: entry.Description, EffectiveDate: entry.EffectiveDate,
				Debit: entry.Debit.String(), Credit: entry.Credit.String(),
				Running: entry.RunningBalance.String(),
			})
		}
		return report.AccountCode, report.AccountName, report.OpeningBalance.String() + "/" + report.EndingBalance.String(), entries
	}
	aCode, aName, aBalances, aEntries := snapshot(a)
	bCode, bName, bBalances, bEntries := snapshot(b)
	return aCode == bCode && aName == bName && aBalances == bBalances && reflect.DeepEqual(aEntries, bEntries)
}

func generalLedgerSummary(report reporting.GeneralLedgerReport) string {
	var out string
	for _, entry := range report.Entries {
		out += fmt.Sprintf("%s/%s/%s/%s/%s/%s;",
			entry.EntryNumber, entry.Description, entry.EffectiveDate.Format(time.RFC3339),
			entry.Debit, entry.Credit, entry.RunningBalance)
	}
	return fmt.Sprintf("%s/%s opening=%s ending=%s [%s]",
		report.AccountCode, report.AccountName, report.OpeningBalance, report.EndingBalance, out)
}
