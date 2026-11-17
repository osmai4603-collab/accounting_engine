package sdkapplication

import (
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestJournalEntryMappingRoundTrip(t *testing.T) {
	now := time.Date(2026, time.November, 13, 0, 0, 0, 0, time.UTC)
	companyID := types.NewCompanyID()
	journalID := types.JournalID(uuid.New())
	entryID := types.NewJournalEntryID()
	periodID := types.NewFiscalPeriodID()
	lineID := types.NewJournalEntryLineID()
	accountID := types.NewAccountID()
	partnerID := types.NewPartnerID()
	costCenterID := types.NewCostCenterID()
	projectID := types.NewProjectID()
	segmentID := types.NewSegmentID()
	businessUnitID := types.NewBusinessUnitID()
	reversalID := types.NewJournalEntryID()
	amount, err := types.NewMoney(decimal.NewFromInt(42), "USD")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := now.Add(-24 * time.Hour)
	updatedAt := now.Add(-time.Hour)
	approvedAt := now.Add(-30 * time.Minute)
	postedAt := now.Add(30 * time.Minute)

	zero, err := types.NewMoney(decimal.Zero, "USD")
	if err != nil {
		t.Fatal(err)
	}

	entry := ledger.JournalEntry{
		ID:                          entryID,
		CompanyID:                   companyID,
		JournalID:                   journalID,
		FiscalPeriodID:              periodID,
		EntryNumber:                 "JE-42",
		Status:                      ledger.EntryPosted,
		Classification:              ledger.EntryClassAdjusting,
		AdjustingEntryType:          ledger.AdjustingEntryAccrual,
		AdjustmentReason:            "Accrued services received",
		AdjustmentApprovalReference: "APP-2026-03",
		AdjustmentAuthorized:        true,
		TargetPeriodNumber:          13,
		EffectiveDate:               now,
		PostingDate:                 now.Add(time.Hour),
		Description:                 "adjustment",
		SourceModule:                "payroll",
		ReferenceID:                 uuid.NewString(),
		TransactionCurrency:         "USD",
		ExchangeRate:                decimal.NewFromInt(1),
		CreatedBy:                   "creator",
		CreatedAt:                   createdAt,
		UpdatedAt:                   updatedAt,
		ApprovedBy:                  "approver",
		ApprovedAt:                  &approvedAt,
		PostedBy:                    "poster",
		PostedAt:                    &postedAt,
		ApprovalFingerprint:         "approval-hash",
		ReversalOfID:                &reversalID,
		ReversalReason:              "duplicate invoice",
		Lines: []ledger.EntryLine{{
			ID:                  lineID,
			AccountID:           accountID,
			PartnerID:           &partnerID,
			CostCenterID:        &costCenterID,
			ProjectID:           &projectID,
			SegmentID:           &segmentID,
			BusinessUnitID:      &businessUnitID,
			MatchingNumber:      "match-42",
			ReconciledAmount:    decimal.NewFromInt(10),
			TransactionAmount:   amount,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     amount,
			FunctionalCredit:    zero,
		}},
	}

	internal, err := toInternalEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fromInternalEntry(internal)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != entry.ID || got.CompanyID != entry.CompanyID ||
		got.JournalID != entry.JournalID || got.FiscalPeriodID != entry.FiscalPeriodID {
		t.Fatalf("entry identifiers changed during mapping: got %+v", got)
	}
	if got.EntryNumber != entry.EntryNumber || got.SourceModule != entry.SourceModule {
		t.Fatalf("entry number or source module was lost: got %q / %q", got.EntryNumber, got.SourceModule)
	}
	if got.Status != entry.Status || got.Classification != entry.Classification ||
		!got.EffectiveDate.Equal(entry.EffectiveDate) || !got.PostingDate.Equal(entry.PostingDate) ||
		got.Description != entry.Description || got.ReferenceID != entry.ReferenceID {
		t.Fatalf("entry fields changed during mapping: got %+v", got)
	}
	if got.AdjustingEntryType != entry.AdjustingEntryType ||
		got.AdjustmentReason != entry.AdjustmentReason ||
		got.AdjustmentApprovalReference != entry.AdjustmentApprovalReference ||
		got.AdjustmentAuthorized != entry.AdjustmentAuthorized ||
		got.TargetPeriodNumber != entry.TargetPeriodNumber {
		t.Fatalf("adjusting-entry metadata changed during mapping: got %+v", got)
	}
	if got.TransactionCurrency != entry.TransactionCurrency || !got.ExchangeRate.Equal(entry.ExchangeRate) ||
		got.CreatedBy != entry.CreatedBy || got.ApprovedBy != entry.ApprovedBy ||
		got.PostedBy != entry.PostedBy || got.ApprovalFingerprint != entry.ApprovalFingerprint ||
		!got.CreatedAt.Equal(entry.CreatedAt) || !got.UpdatedAt.Equal(entry.UpdatedAt) ||
		got.ApprovedAt == nil || !got.ApprovedAt.Equal(*entry.ApprovedAt) ||
		got.PostedAt == nil || !got.PostedAt.Equal(*entry.PostedAt) {
		t.Fatalf("entry metadata changed during mapping: got %+v", got)
	}
	if got.ReversalOfID == nil || *got.ReversalOfID != reversalID {
		t.Fatalf("reversal identifier changed during mapping: got %v", got.ReversalOfID)
	}
	if got.ReversalReason != entry.ReversalReason {
		t.Fatalf("reversal reason changed during mapping: got %q", got.ReversalReason)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("expected one line, got %d", len(got.Lines))
	}
	line := got.Lines[0]
	if line.ID != lineID || line.AccountID != accountID || line.PartnerID == nil || *line.PartnerID != partnerID ||
		line.CostCenterID == nil || *line.CostCenterID != costCenterID ||
		line.ProjectID == nil || *line.ProjectID != projectID ||
		line.SegmentID == nil || *line.SegmentID != segmentID ||
		line.BusinessUnitID == nil || *line.BusinessUnitID != businessUnitID {
		t.Fatalf("line identifiers changed during mapping: got %+v", line)
	}
	if line.MatchingNumber != entry.Lines[0].MatchingNumber ||
		!line.ReconciledAmount.Equal(entry.Lines[0].ReconciledAmount) ||
		!line.TransactionAmount.Equal(entry.Lines[0].TransactionAmount) ||
		line.TransactionCurrency != entry.Lines[0].TransactionCurrency ||
		!line.ExchangeRate.Equal(entry.Lines[0].ExchangeRate) ||
		!line.FunctionalDebit.Equal(entry.Lines[0].FunctionalDebit) ||
		!line.FunctionalCredit.Equal(entry.Lines[0].FunctionalCredit) {
		t.Fatalf("line fields changed during mapping: got %+v", line)
	}
}

func TestFromInternalEntryUsesSequenceForEntryNumber(t *testing.T) {
	entry, err := fromInternalEntry(posting.JournalEntry{
		CompanyID: uuid.New(), ID: uuid.New(), JournalID: uuid.New(), PeriodID: uuid.New(),
		SequenceNumber: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.EntryNumber != "42" {
		t.Fatalf("entry number = %q, want sequence 42", entry.EntryNumber)
	}
}
