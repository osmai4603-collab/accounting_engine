package posting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

func authorizedAdjustingEntry(
	t *testing.T,
	companyID, journalID, debitAccount, creditAccount uuid.UUID,
	date time.Time,
) posting.JournalEntry {
	t.Helper()
	entry := policyEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	entry.Classification = posting.EntryClassAdjusting
	entry.AdjustingEntryType = posting.AdjustingEntryAccrual
	entry.AdjustmentReason = "Services received before invoice"
	entry.AdjustmentApprovalReference = "APP-2026-03"
	entry.AdjustmentAuthorized = true
	entry.CreatedBy = "maker"
	entry.ApprovedBy = "approver"
	approvedAt := date
	entry.ApprovedAt = &approvedAt
	entry.CreatedAt = date
	return entry
}

func TestAdjustingEntryPostsInOpenNormalPeriodWithDelegation(t *testing.T) {
	engine, _, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	date := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	entry := authorizedAdjustingEntry(t, companyID, journalID, debitAccount, creditAccount, date)

	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatalf("create adjusting draft: %v", err)
	}
	if err := engine.PostEntryContext(context.Background(), &entry, "poster"); err != nil {
		t.Fatalf("post adjusting entry in open period: %v", err)
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("entry status = %s, want posted", entry.Status)
	}
}

func TestAdjustmentPeriodRequiresAdjustingClassificationAndDelegation(t *testing.T) {
	engine, _, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	date := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	regular := policyEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	regular.AdjustmentAuthorized = true
	regular.TargetPeriodNumber = 13
	if err := engine.CreateDraftContext(context.Background(), regular); err != nil {
		t.Fatalf("create regular draft: %v", err)
	}
	if err := engine.PostEntryContext(context.Background(), &regular, "poster"); !errors.Is(err, posting.ErrInvalidAdjustingEntryMetadata) {
		t.Fatalf("regular entry in period 13 error = %v, want adjusting-classification rejection", err)
	}

	unauthorized := authorizedAdjustingEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	unauthorized.TargetPeriodNumber = 13
	unauthorized.AdjustmentAuthorized = false
	if err := engine.CreateDraftContext(context.Background(), unauthorized); err != nil {
		t.Fatalf("create unauthorized adjustment draft: %v", err)
	}
	if err := engine.PostEntryContext(context.Background(), &unauthorized, "poster"); !errors.Is(err, posting.ErrAdjustmentDelegationRequired) {
		t.Fatalf("unapproved entry in period 13 error = %v, want delegation rejection", err)
	}
}

func TestAuthorizedClosingEntryPostsInPeriod13(t *testing.T) {
	engine, _, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	date := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	entry := policyEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	entry.Classification = posting.EntryClassClosing
	entry.TargetPeriodNumber = 13
	entry.AdjustmentAuthorized = true

	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatalf("create closing draft: %v", err)
	}
	if err := engine.PostEntryContext(context.Background(), &entry, "closer"); err != nil {
		t.Fatalf("post authorized closing entry in period 13: %v", err)
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("closing entry status = %s, want posted", entry.Status)
	}
}

func TestAdjustingEntryPreCloseSummaryRequiresDelegationAndCreatorAudit(t *testing.T) {
	date := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	entry := posting.JournalEntry{
		ID: uuid.New(), Status: posting.EntryPosted, Classification: posting.EntryClassAdjusting,
		AdjustingEntryType: posting.AdjustingEntryAccrual,
		AdjustmentReason:   "Accrued services", AdjustmentApprovalReference: "APP-2026-03",
		AdjustmentAuthorized: true, CreatedBy: "maker", CreatedAt: date,
		ApprovedBy: "approver", ApprovedAt: &date,
	}
	summary := posting.SummarizeAdjustingEntriesForPreClose([]posting.JournalEntry{entry})
	if !summary.Ready() || summary.TotalAdjustingEntries != 1 || summary.MissingAuditMetadata != 0 {
		t.Fatalf("complete adjusting entry summary = %+v, want ready", summary)
	}

	entry.AdjustmentAuthorized = false
	summary = posting.SummarizeAdjustingEntriesForPreClose([]posting.JournalEntry{entry})
	if summary.Ready() || summary.MissingAuditMetadata != 1 {
		t.Fatalf("unauthorized adjusting entry summary = %+v, want an audit blocker", summary)
	}
}
