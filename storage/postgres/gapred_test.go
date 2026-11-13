//go:build gapred

// Red tests for the Phase 0 traceability matrix at the PostgreSQL boundary.
// They need a live PostgreSQL (an isolated schema is created per test and the
// test skips when the server is unreachable). Run with:
//
//	go test -tags gapred ./storage/postgres -run TestGAP
package postgres_test

import (
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func gapPGMoney(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

// GAP-GOV-01: UpdatePeriodState issues a raw UPDATE and never calls
// masterdata.ValidatePeriodTransition, so a hard-closed period can be reopened.
func TestGAP_GOV_01_HardClosedPeriodCannotBeReopened(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, env.PeriodID, masterdata.PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, env.PeriodID, masterdata.PeriodHardClosed); err != nil {
		t.Fatal(err)
	}
	err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, env.PeriodID, masterdata.PeriodOpen)
	if err == nil {
		t.Fatal("GAP-GOV-01: reopened a hard-closed fiscal period")
	}
}

// GAP-PST-01: posting re-resolves the sequence but never re-checks or locks the
// fiscal period inside the posting transaction. A draft saved while the period
// was open can still be posted after the period is hard closed.
func TestGAP_PST_01_PostingRechecksPeriodInsideTransaction(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	draft := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, gapPGMoney(t, "100.00"), time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	postingPeriod, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID, draft.EntryDate)
	if err != nil {
		t.Fatal(err)
	}

	if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, postingPeriod.ID, masterdata.PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, postingPeriod.ID, masterdata.PeriodHardClosed); err != nil {
		t.Fatal(err)
	}
	closedPeriod, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID, draft.EntryDate)
	if err != nil {
		t.Fatal(err)
	}
	if closedPeriod.State != masterdata.PeriodHardClosed {
		t.Fatalf("period state after hard close = %q, want %q", closedPeriod.State, masterdata.PeriodHardClosed)
	}

	if _, err := env.Driver.PostAndSequenceContext(ctx, draft); err == nil {
		t.Fatal("GAP-PST-01: posted into a hard-closed period")
	}
}

// GAP-PST-05: PostReversalAtomicContext updates the original and posts the
// reversal draft without proving that the reversal lines invert the original.
func TestGAP_PST_05_ReversalMustInvertOriginalLines(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	original := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, gapPGMoney(t, "100.00"), time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := engine.PostEntryContext(ctx, &original, "poster"); err != nil {
		t.Fatal(err)
	}

	// Same direction as the original: balanced, but not an inversion.
	reversal, err := posting.NewJournalEntry(env.JournalID, time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), "maker",
		[]posting.JournalEntryLine{
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID,
				Debit: gapPGMoney(t, "100.00"), Credit: gapPGMoney(t, "0.00")},
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID,
				Debit: gapPGMoney(t, "0.00"), Credit: gapPGMoney(t, "100.00")},
		})
	if err != nil {
		t.Fatal(err)
	}
	reversal.CompanyID = env.CompanyID
	reversal.Classification = posting.EntryClassReversing
	originalID := original.ID
	reversal.OriginalEntryID = &originalID
	if err := engine.CreateDraftContext(ctx, reversal); err != nil {
		t.Fatal(err)
	}

	if _, err := env.Driver.PostReversalAtomicContext(ctx, original, reversal); err == nil {
		t.Fatal("GAP-PST-05: accepted a reversal that does not invert the original lines")
	}
}

// GAP-PST-07: bulk posting resolves each entry's fiscal period outside the
// batch transaction, so the period can change between resolution and commit.
func TestGAP_PST_07_BulkResolvesPeriodInsideTransaction(t *testing.T) {
	t.Skip("GAP-PST-07: bulk period resolution happens in prepareForPosting before the batch transaction; a deterministic red test needs the P1-T6 transactional bulk API")
}
