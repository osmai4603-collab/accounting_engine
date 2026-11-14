package consumer_test

import (
	"context"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer/consumer"
	"github.com/shopspring/decimal"
)

func TestExternalAdapterFullScenario(t *testing.T) {
	result, err := consumer.Run(context.Background())
	if err != nil {
		t.Fatalf("run external adapter scenario: %v", err)
	}

	if !result.ExchangeRate.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("exchange rate = %s, want 1", result.ExchangeRate)
	}

	draft := result.Draft
	if draft.Status != ledger.EntryDraft {
		t.Fatalf("draft status = %q, want %q", draft.Status, ledger.EntryDraft)
	}
	if draft.ID == (types.JournalEntryID{}) || len(draft.Lines) != 2 {
		t.Fatalf("draft identity/lines = %s/%d, want a non-empty id and 2 lines", draft.ID, len(draft.Lines))
	}

	posted := result.Posted
	if posted.ID != draft.ID {
		t.Fatalf("posted id = %s, want draft id %s", posted.ID, draft.ID)
	}
	if posted.Status != ledger.EntryPosted {
		t.Fatalf("posted status = %q, want %q", posted.Status, ledger.EntryPosted)
	}
	if posted.SequenceNumber != 1 || posted.EntryNumber != "1" {
		t.Fatalf("posted sequence/number = %d/%q, want 1/\"1\"", posted.SequenceNumber, posted.EntryNumber)
	}

	readBack := result.ReadBack
	if readBack.ID != posted.ID || readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 {
		t.Fatalf("read-back = id %s status %q lines %d, want %s/posted/2",
			readBack.ID, readBack.Status, len(readBack.Lines), posted.ID)
	}
	if readBack.Description != posted.Description {
		t.Fatalf("read-back description = %q, want %q", readBack.Description, posted.Description)
	}

	reversal := result.Reversal
	if reversal.ID == posted.ID || reversal.Status != ledger.EntryPosted {
		t.Fatalf("reversal = id %s status %q, want a distinct posted entry", reversal.ID, reversal.Status)
	}
	if reversal.Classification != ledger.EntryClassReversing {
		t.Fatalf("reversal classification = %q, want %q", reversal.Classification, ledger.EntryClassReversing)
	}
	if reversal.ReversalOfID == nil || *reversal.ReversalOfID != posted.ID {
		t.Fatalf("reversal original id = %v, want %s", reversal.ReversalOfID, posted.ID)
	}
	if reversal.ReversalReason != "external adapter scenario correction" {
		t.Fatalf("reversal reason = %q", reversal.ReversalReason)
	}
	if reversal.SequenceNumber != 2 || reversal.EntryNumber != "2" {
		t.Fatalf("reversal sequence/number = %d/%q, want 2/\"2\"", reversal.SequenceNumber, reversal.EntryNumber)
	}

	original := result.OriginalAfterReversal
	if original.Status != ledger.EntryReversed {
		t.Fatalf("original status = %q, want %q", original.Status, ledger.EntryReversed)
	}
	if original.ReversedByID == nil || *original.ReversedByID != reversal.ID {
		t.Fatalf("original reversed-by = %v, want %s", original.ReversedByID, reversal.ID)
	}

	trialBalance := result.TrialBalance
	if !trialBalance.IsBalanced {
		t.Fatalf("trial balance is not balanced: debit %s credit %s", trialBalance.TotalDebit, trialBalance.TotalCredit)
	}
	if !trialBalance.TotalDebit.Equal(trialBalance.TotalCredit) {
		t.Fatalf("trial balance totals differ: debit %s credit %s", trialBalance.TotalDebit, trialBalance.TotalCredit)
	}
	if len(trialBalance.Lines) != 2 {
		t.Fatalf("trial balance lines = %d, want 2", len(trialBalance.Lines))
	}
	for _, line := range trialBalance.Lines {
		if !line.EndingDebit.IsZero() || !line.EndingCredit.IsZero() {
			t.Fatalf("account %s ends with %s debit / %s credit, want zero after reversal",
				line.AccountCode, line.EndingDebit, line.EndingCredit)
		}
		if !line.PeriodDebit.Equal(line.PeriodCredit) {
			t.Fatalf("account %s period debit %s != period credit %s",
				line.AccountCode, line.PeriodDebit, line.PeriodCredit)
		}
	}

	generalLedger := result.GeneralLedger
	if len(generalLedger.Entries) != 2 {
		t.Fatalf("general ledger entries = %d, want posting + reversal", len(generalLedger.Entries))
	}
	if !generalLedger.OpeningBalance.IsZero() || !generalLedger.EndingBalance.IsZero() {
		t.Fatalf("general ledger opening/ending = %s/%s, want zero/zero",
			generalLedger.OpeningBalance, generalLedger.EndingBalance)
	}
}
