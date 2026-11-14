package posting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/apperrors"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func reversalMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func parseDate(t *testing.T, dateStr string) time.Time {
	t.Helper()
	dt, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		t.Fatal(err)
	}
	return dt.UTC()
}

// TestReversalNetZeroInvariantTable tests the fundamental accounting invariant:
// For any posted entry and its reversal, the combined net functional balance for
// each account must be exactly zero, the combined net transaction amount for each
// transaction currency must be exactly zero, and all analytical dimensions
// (Partner, CostCenter, Project, Segment, BusinessUnit) are preserved 1:1.
func TestReversalNetZeroInvariantTable(t *testing.T) {
	companyID := uuid.New()
	journalID := uuid.New()
	partnerID := uuid.New()
	costCenterID := uuid.New()
	projectID := uuid.New()
	segmentID := uuid.New()
	businessUnitID := uuid.New()

	acc1 := uuid.New()
	acc2 := uuid.New()
	acc3 := uuid.New()

	tests := []struct {
		name                string
		date                string
		transactionCurrency string
		exchangeRate        string
		lines               []posting.JournalEntryLine
	}{
		{
			name:                "Simple 2-line single currency with analytical dimensions",
			date:                "2026-03-15",
			transactionCurrency: "SAR",
			exchangeRate:        "1.00",
			lines: []posting.JournalEntryLine{
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc1,
					PartnerID:           &partnerID,
					CostCenterID:        &costCenterID,
					ProjectID:           &projectID,
					SegmentID:           &segmentID,
					BusinessUnitID:      &businessUnitID,
					MatchingNumber:      "MATCH-SIMPLE-001",
					TransactionAmount:   reversalMoney(t, "500.00", "SAR"),
					TransactionCurrency: "SAR",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "500.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "0.00", "SAR"),
				},
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc2,
					PartnerID:           &partnerID,
					CostCenterID:        &costCenterID,
					ProjectID:           &projectID,
					SegmentID:           &segmentID,
					BusinessUnitID:      &businessUnitID,
					MatchingNumber:      "MATCH-SIMPLE-001",
					TransactionAmount:   reversalMoney(t, "500.00", "SAR"),
					TransactionCurrency: "SAR",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "0.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "500.00", "SAR"),
				},
			},
		},
		{
			name:                "Compound 3-line entry (Receivable, Revenue, VAT)",
			date:                "2026-03-18",
			transactionCurrency: "SAR",
			exchangeRate:        "1.00",
			lines: []posting.JournalEntryLine{
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc1, // AR
					PartnerID:           &partnerID,
					MatchingNumber:      "MATCH-COMP-001",
					TransactionAmount:   reversalMoney(t, "1150.00", "SAR"),
					TransactionCurrency: "SAR",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "1150.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "0.00", "SAR"),
				},
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc2, // Revenue
					CostCenterID:        &costCenterID,
					SegmentID:           &segmentID,
					TransactionAmount:   reversalMoney(t, "1000.00", "SAR"),
					TransactionCurrency: "SAR",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "0.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "1000.00", "SAR"),
				},
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc3, // VAT
					TransactionAmount:   reversalMoney(t, "150.00", "SAR"),
					TransactionCurrency: "SAR",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "0.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "150.00", "SAR"),
				},
			},
		},
		{
			name:                "Multi-currency entry with foreign exchange rates",
			date:                "2026-03-20",
			transactionCurrency: "EUR",
			exchangeRate:        "3.75",
			lines: []posting.JournalEntryLine{
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc1,
					PartnerID:           &partnerID,
					MatchingNumber:      "MATCH-FX-001",
					TransactionAmount:   reversalMoney(t, "100.00", "EUR"),
					TransactionCurrency: "EUR",
					ExchangeRate:        decimal.RequireFromString("3.75"),
					FunctionalDebit:     reversalMoney(t, "375.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "0.00", "SAR"),
				},
				{
					CompanyID:           companyID,
					ID:                  uuid.New(),
					AccountID:           acc2,
					PartnerID:           &partnerID,
					MatchingNumber:      "MATCH-FX-001",
					TransactionAmount:   reversalMoney(t, "375.00", "USD"),
					TransactionCurrency: "USD",
					ExchangeRate:        decimal.RequireFromString("1.00"),
					FunctionalDebit:     reversalMoney(t, "0.00", "SAR"),
					FunctionalCredit:    reversalMoney(t, "375.00", "SAR"),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := posting.NewMockRepository()
			engine := posting.NewEngine(repo, nil)

			entry, err := posting.NewJournalEntry(journalID, parseDate(t, tc.date), "creator", tc.lines)
			if err != nil {
				t.Fatalf("NewJournalEntry failed: %v", err)
			}
			entry.CompanyID = companyID
			entry.TransactionCurrency = tc.transactionCurrency
			entry.ExchangeRate = decimal.RequireFromString(tc.exchangeRate)

			if err := engine.CreateDraft(entry); err != nil {
				t.Fatalf("CreateDraft failed: %v", err)
			}
			if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
				t.Fatalf("Post failed: %v", err)
			}

			reversal, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
				posting.ReversalRequest{
					ActorID:        "reversal-actor",
					Reason:         "table test reversal",
					EffectiveDate:  parseDate(t, tc.date).AddDate(0, 0, 1),
					IdempotencyKey: "table-idem-" + tc.name,
				}, "reversal-actor")
			if err != nil {
				t.Fatalf("ReverseWithRequestContext failed: %v", err)
			}

			// 1. Verify Net Zero on functional currency for each account
			netFunctional := make(map[uuid.UUID]decimal.Decimal)
			netTransaction := make(map[string]decimal.Decimal)

			for _, e := range []posting.JournalEntry{entry, reversal} {
				for _, l := range e.Lines {
					deb, cred, _ := posting.LineFunctionalAmounts(l)
					netFunctional[l.AccountID] = netFunctional[l.AccountID].Add(deb).Sub(cred)

					txAmt := l.TransactionAmount.Amount()
					if cred.IsPositive() {
						txAmt = txAmt.Neg()
					}
					netTransaction[l.TransactionCurrency] = netTransaction[l.TransactionCurrency].Add(txAmt)
				}
			}

			for accID, net := range netFunctional {
				if !net.IsZero() {
					t.Fatalf("Account %s net functional balance = %s, expected 0.00", accID, net)
				}
			}

			// 2. Verify Net Zero on transaction currency
			for curr, net := range netTransaction {
				if !net.IsZero() {
					t.Fatalf("Currency %s net transaction amount = %s, expected 0.00", curr, net)
				}
			}

			// 3. Verify 1:1 preservation of analytical dimensions on every reversal line
			if len(reversal.Lines) != len(entry.Lines) {
				t.Fatalf("Reversal line count = %d, expected %d", len(reversal.Lines), len(entry.Lines))
			}
			for i, origLine := range entry.Lines {
				revLine := reversal.Lines[i]
				if revLine.AccountID != origLine.AccountID {
					t.Errorf("Line %d AccountID mismatch: got %v, want %v", i, revLine.AccountID, origLine.AccountID)
				}
				if !uuidPtrEqual(revLine.PartnerID, origLine.PartnerID) {
					t.Errorf("Line %d PartnerID mismatch: got %v, want %v", i, revLine.PartnerID, origLine.PartnerID)
				}
				if !uuidPtrEqual(revLine.CostCenterID, origLine.CostCenterID) {
					t.Errorf("Line %d CostCenterID mismatch: got %v, want %v", i, revLine.CostCenterID, origLine.CostCenterID)
				}
				if !uuidPtrEqual(revLine.ProjectID, origLine.ProjectID) {
					t.Errorf("Line %d ProjectID mismatch: got %v, want %v", i, revLine.ProjectID, origLine.ProjectID)
				}
				if !uuidPtrEqual(revLine.SegmentID, origLine.SegmentID) {
					t.Errorf("Line %d SegmentID mismatch: got %v, want %v", i, revLine.SegmentID, origLine.SegmentID)
				}
				if !uuidPtrEqual(revLine.BusinessUnitID, origLine.BusinessUnitID) {
					t.Errorf("Line %d BusinessUnitID mismatch: got %v, want %v", i, revLine.BusinessUnitID, origLine.BusinessUnitID)
				}
				if revLine.MatchingNumber != origLine.MatchingNumber {
					t.Errorf("Line %d MatchingNumber mismatch: got %q, want %q", i, revLine.MatchingNumber, origLine.MatchingNumber)
				}
				// Verify debit/credit swap
				origDeb, origCred, _ := posting.LineFunctionalAmounts(origLine)
				revDeb, revCred, _ := posting.LineFunctionalAmounts(revLine)
				if !revDeb.Equal(origCred) || !revCred.Equal(origDeb) {
					t.Errorf("Line %d amounts not cleanly swapped: orig(%s,%s) rev(%s,%s)",
						i, origDeb, origCred, revDeb, revCred)
				}
			}
		})
	}
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// TestReversalPreventsDraftReversal ensures an unposted draft entry cannot be reversed.
func TestReversalPreventsDraftReversal(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "draft-creator")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}

	_, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{ActorID: "reverser", Reason: "attempting to reverse draft"}, "reverser")
	if !errors.Is(err, posting.ErrEntryNotPosted) {
		t.Fatalf("Reverse draft error = %v, want ErrEntryNotPosted", err)
	}

	stored, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft || stored.ReversedEntryID != nil {
		t.Fatalf("Rejected reversal mutated the draft: %#v", stored)
	}
}

// TestReversalPreventsAlreadyReversedReversal ensures an entry cannot be reversed twice.
func TestReversalPreventsAlreadyReversedReversal(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "creator")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
		t.Fatal(err)
	}

	// First reversal should succeed
	firstReversal, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{ActorID: "reverser-1", Reason: "first reversal", IdempotencyKey: "idem-first"}, "reverser-1")
	if err != nil {
		t.Fatalf("First reversal failed: %v", err)
	}

	// Second reversal with distinct idempotency key must fail with ErrEntryAlreadyReversed
	_, err = engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{ActorID: "reverser-2", Reason: "second reversal attempt", IdempotencyKey: "idem-second"}, "reverser-2")
	if !errors.Is(err, posting.ErrEntryAlreadyReversed) {
		t.Fatalf("Second reversal error = %v, want ErrEntryAlreadyReversed", err)
	}

	storedOriginal, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != posting.EntryReversed || storedOriginal.ReversedEntryID == nil || *storedOriginal.ReversedEntryID != firstReversal.ID {
		t.Fatalf("Original entry state corrupted after rejected second reversal: %#v", storedOriginal)
	}
}

// TestReversalTenantIsolation ensures an entry belonging to company A cannot be reversed under company B context.
func TestReversalTenantIsolation(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "creator")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
		t.Fatal(err)
	}

	otherCompany := uuid.New()
	_, err := engine.ReverseWithRequestContext(context.Background(), otherCompany, entry.ID,
		posting.ReversalRequest{ActorID: "reverser", Reason: "cross company attempt"}, "reverser")
	if !errors.Is(err, posting.ErrEntryNotFound) {
		t.Fatalf("Cross-company reversal error = %v, want ErrEntryNotFound", err)
	}

	stored, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryPosted || stored.ReversedEntryID != nil {
		t.Fatalf("Cross-company attempt mutated the original entry: %#v", stored)
	}
}

// TestReversalRejectsEffectiveDateBeforeOriginal verifies that an explicit effective date
// strictly prior to the original entry's date is rejected with ErrReversalDateBeforeOriginal.
func TestReversalRejectsEffectiveDateBeforeOriginal(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "creator")
	entry.EntryDate = parseDate(t, "2026-03-15")
	entry.Date = entry.EntryDate
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
		t.Fatal(err)
	}

	// Attempt earlier date: 2026-03-14 vs original 2026-03-15
	_, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{
			ActorID:        "reverser",
			Reason:         "attempting earlier date",
			EffectiveDate:  parseDate(t, "2026-03-14"),
			IdempotencyKey: "early-date-key",
		}, "reverser")
	if !errors.Is(err, posting.ErrReversalDateBeforeOriginal) {
		t.Fatalf("Reversal with earlier date error = %v, want ErrReversalDateBeforeOriginal", err)
	}

	code, category := posting.ClassifyError(err)
	if code != apperrors.CodeInvalidEntry || category != apperrors.Validation {
		t.Fatalf("Error classified as (%s, %s), want (%s, %s)",
			code, category, apperrors.CodeInvalidEntry, apperrors.Validation)
	}

	stored, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryPosted || stored.ReversedEntryID != nil {
		t.Fatalf("Rejected earlier date mutated original: %#v", stored)
	}

	// Reversal with SAME date or LATER date must succeed
	sameDateReversal, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{
			ActorID:        "reverser",
			Reason:         "valid same-day reversal",
			EffectiveDate:  parseDate(t, "2026-03-15"),
			IdempotencyKey: "same-date-key",
		}, "reverser")
	if err != nil {
		t.Fatalf("Same-day reversal unexpectedly failed: %v", err)
	}
	if sameDateReversal.Status != posting.EntryPosted {
		t.Fatalf("Same-day reversal status = %v, want posted", sameDateReversal.Status)
	}
}

// TestReversalMatchingNumberAndReconciledAmountLifecycle ensures MatchingNumber is preserved
// as documentary linkage, while ReconciledAmount is explicitly reset to 0.00 on the reversal.
func TestReversalMatchingNumberAndReconciledAmountLifecycle(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "creator")
	for i := range entry.Lines {
		entry.Lines[i].MatchingNumber = "MATCH-CYCLE-2026"
		entry.Lines[i].ReconciledAmount = decimal.RequireFromString("75.50")
	}
	srcID := uuid.New()
	entry.ApprovalFingerprint = "ORIGINAL-APPROVAL-FINGERPRINT"
	entry.SourceEventID = &srcID
	entry.LegalNumber = "ORIGINAL-LEGAL-NUM"

	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
		t.Fatal(err)
	}

	reversal, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{
			ActorID: "reverser-7",
			Reason:  "subledger lifecycle test",
		}, "reverser-7")
	if err != nil {
		t.Fatalf("ReverseWithRequestContext failed: %v", err)
	}

	// Reversal checks:
	for i, line := range reversal.Lines {
		if line.MatchingNumber != "MATCH-CYCLE-2026" {
			t.Errorf("Reversal line %d MatchingNumber = %q, want MATCH-CYCLE-2026", i, line.MatchingNumber)
		}
		if !line.ReconciledAmount.IsZero() {
			t.Errorf("Reversal line %d ReconciledAmount = %s, want zero (0.00)", i, line.ReconciledAmount)
		}
	}
	// Reversal must not inherit approval fingerprints, legal numbers, or event IDs
	if reversal.ApprovalFingerprint != "" {
		t.Errorf("Reversal inherited ApprovalFingerprint: %q", reversal.ApprovalFingerprint)
	}
	if reversal.SourceEventID != nil {
		t.Errorf("Reversal inherited SourceEventID: %v", reversal.SourceEventID)
	}
	if reversal.LegalNumber != "" {
		t.Errorf("Reversal inherited LegalNumber: %q", reversal.LegalNumber)
	}

	// Original checks: untouched
	storedOriginal, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	invalidReversal := reversal
	invalidReversal.Lines = append([]posting.JournalEntryLine(nil), reversal.Lines...)
	invalidReversal.Lines[0].ReconciledAmount = decimal.RequireFromString("1.00")
	if err := posting.ValidateReversalLines(storedOriginal, invalidReversal); !errors.Is(err, posting.ErrInvalidReversal) {
		t.Errorf("Reversal with a reconciled amount error = %v, want ErrInvalidReversal", err)
	}
	for i, line := range storedOriginal.Lines {
		if line.MatchingNumber != "MATCH-CYCLE-2026" {
			t.Errorf("Original line %d MatchingNumber mutated: %q", i, line.MatchingNumber)
		}
		if !line.ReconciledAmount.Equal(decimal.RequireFromString("75.50")) {
			t.Errorf("Original line %d ReconciledAmount mutated: %s", i, line.ReconciledAmount)
		}
	}
}

// TestReversalAuditAndMetadataPersistence tests recording of actor, reason, reference,
// and idempotency key on entry and verifies empty actor rejection.
func TestReversalAuditAndMetadataPersistence(t *testing.T) {
	repo := posting.NewMockRepository()
	engine := posting.NewEngine(repo, nil)

	entry := makeBalancedTestEntry(t, "creator")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "poster"); err != nil {
		t.Fatal(err)
	}

	// 1. Empty actor must be rejected
	_, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID,
		posting.ReversalRequest{
			ActorID: "   ",
			Reason:  "empty actor test",
		}, "poster")
	if !errors.Is(err, posting.ErrReversalActorRequired) {
		t.Fatalf("Empty actor error = %v, want ErrReversalActorRequired", err)
	}
	code, cat := posting.ClassifyError(err)
	if code != apperrors.CodeInvalidEntry || cat != apperrors.Validation {
		t.Fatalf("Empty actor classified as (%s, %s), want validation invalid entry", code, cat)
	}

	// 2. Full metadata reversal
	req := posting.ReversalRequest{
		ActorID:        "compliance-officer-99",
		Reason:         "Customer dispute resolution #482",
		Reference:      "DISPUTE-482-REF",
		SourceSystem:   "dispute-service",
		IdempotencyKey: "audit-idem-test-key",
	}

	reversal, err := engine.ReverseWithRequestContext(context.Background(), entry.CompanyID, entry.ID, req, req.ActorID)
	if err != nil {
		t.Fatalf("ReverseWithRequestContext failed: %v", err)
	}

	if reversal.CreatedBy != req.ActorID {
		t.Errorf("reversal CreatedBy = %q, want %q", reversal.CreatedBy, req.ActorID)
	}
	if reversal.PostedBy != req.ActorID {
		t.Errorf("reversal PostedBy = %q, want %q", reversal.PostedBy, req.ActorID)
	}
	if reversal.ReversalReason != req.Reason {
		t.Errorf("reversal ReversalReason = %q, want %q", reversal.ReversalReason, req.Reason)
	}
	if reversal.ReversalReference != req.Reference {
		t.Errorf("reversal ReversalReference = %q, want %q", reversal.ReversalReference, req.Reference)
	}
	if reversal.SourceModule != req.SourceSystem {
		t.Errorf("reversal SourceModule = %q, want %q", reversal.SourceModule, req.SourceSystem)
	}
	if reversal.IdempotencyKey != req.IdempotencyKey {
		t.Errorf("reversal IdempotencyKey = %q, want %q", reversal.IdempotencyKey, req.IdempotencyKey)
	}
	if reversal.OriginalEntryID == nil || *reversal.OriginalEntryID != entry.ID {
		t.Errorf("reversal OriginalEntryID = %v, want %s", reversal.OriginalEntryID, entry.ID)
	}

	storedOriginal, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != posting.EntryReversed {
		t.Errorf("original Status = %v, want reversed", storedOriginal.Status)
	}
	if storedOriginal.ReversedEntryID == nil || *storedOriginal.ReversedEntryID != reversal.ID {
		t.Errorf("original ReversedEntryID = %v, want %s", storedOriginal.ReversedEntryID, reversal.ID)
	}
}

func makeBalancedTestEntry(t *testing.T, creator string) posting.JournalEntry {
	t.Helper()
	companyID := uuid.New()
	journalID := uuid.New()
	debit := reversalMoney(t, "100.00", "USD")
	credit := reversalMoney(t, "100.00", "USD")
	zero := reversalMoney(t, "0.00", "USD")

	entry, err := posting.NewJournalEntry(journalID, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), creator, []posting.JournalEntryLine{
		{
			CompanyID:           companyID,
			ID:                  uuid.New(),
			AccountID:           uuid.New(),
			TransactionAmount:   debit,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     debit,
			FunctionalCredit:    zero,
		},
		{
			CompanyID:           companyID,
			ID:                  uuid.New(),
			AccountID:           uuid.New(),
			TransactionAmount:   credit,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     zero,
			FunctionalCredit:    credit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.TransactionCurrency = "USD"
	entry.ExchangeRate = decimal.NewFromInt(1)
	return entry
}
