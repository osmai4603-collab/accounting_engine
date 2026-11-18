package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	publictypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

func setupIdempotentPostingEnv(t *testing.T) (*memory.Driver, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	driver := memory.New()
	ctx := context.Background()

	companyID, journalID := uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "IDEM-CO", LegalName: "Idempotent Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.MasterDataStore().CreateJournal(ctx, publicmasterdata.Journal{
		ID: publicmasterdata.JournalID(journalID), CompanyID: publictypes.CompanyID(companyID),
		Code: "GEN", Name: "General",
	}); err != nil {
		t.Fatal(err)
	}
	debitID, creditID := uuid.New(), uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: debitID, Code: "1000", Name: "Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: creditID, Code: "4000", Name: "Revenue",
		AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	return driver, companyID, journalID, debitID, creditID
}

func idempotentDraft(t *testing.T, companyID, journalID, debitID, creditID, entryID uuid.UUID, amount string) posting.JournalEntry {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	entryDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	return posting.JournalEntry{
		ID: entryID, CompanyID: companyID, JournalID: journalID,
		Status: posting.EntryDraft, EntryDate: entryDate, PostingDate: entryDate,
		SourceModule: "idempotency-test", CreatedBy: "tester",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		Lines: []posting.JournalEntryLine{
			{
				ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(entryID.String()+":line:0")), CompanyID: companyID, AccountID: debitID,
				TransactionAmount: money, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: money, FunctionalCredit: zero,
			},
			{
				ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(entryID.String()+":line:1")), CompanyID: companyID, AccountID: creditID,
				TransactionAmount: money, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: zero, FunctionalCredit: money,
			},
		},
	}
}

func prepareForPosting(entry posting.JournalEntry) posting.JournalEntry {
	now := time.Now().UTC()
	entry.Status = posting.EntryPosted
	entry.PostedBy = "tester"
	entry.PostedAt = &now
	entry.PostingDate = now
	entry.UpdatedAt = now
	return entry
}

func TestPostingIdempotencyReplayDoesNotConsumeSequence(t *testing.T) {
	driver, companyID, journalID, debitID, creditID := setupIdempotentPostingEnv(t)
	ctx := context.Background()

	entryID := uuid.New()
	first := idempotentDraft(t, companyID, journalID, debitID, creditID, entryID, "100")
	first.IdempotencyKey = "invoice-key"
	if err := driver.SaveDraftContext(ctx, first); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	posted, replayed, err := driver.PostAndSequenceIdempotentContext(ctx, prepareForPosting(first))
	if err != nil {
		t.Fatalf("first idempotent posting: %v", err)
	}
	if replayed {
		t.Fatal("first posting reported replay")
	}
	if posted.Status != posting.EntryPosted || posted.SequenceNumber != 1 {
		t.Fatalf("first posting = status %v sequence %d, want posted/1", posted.Status, posted.SequenceNumber)
	}
	if posted.IdempotencyPayloadHash == "" {
		t.Fatal("posted entry did not record the idempotency payload hash")
	}

	// A retry with the same content under the same key replays the original
	// posting without consuming a second sequence number.
	retry := idempotentDraft(t, companyID, journalID, debitID, creditID, entryID, "100")
	retry.IdempotencyKey = "invoice-key"
	replayedEntry, replayed, err := driver.PostAndSequenceIdempotentContext(ctx, prepareForPosting(retry))
	if err != nil {
		t.Fatalf("replayed posting: %v", err)
	}
	if !replayed {
		t.Fatal("identical retry did not report replay")
	}
	if replayedEntry.ID != entryID || replayedEntry.SequenceNumber != 1 {
		t.Fatalf("replay returned entry %s sequence %d, want the original entry at sequence 1",
			replayedEntry.ID, replayedEntry.SequenceNumber)
	}

	// The next genuinely new posting must take sequence 2: the replay did not
	// consume a number in between.
	next := idempotentDraft(t, companyID, journalID, debitID, creditID, uuid.New(), "40")
	next.IdempotencyKey = "invoice-key-2"
	if err := driver.SaveDraftContext(ctx, next); err != nil {
		t.Fatalf("save second draft: %v", err)
	}
	second, replayed, err := driver.PostAndSequenceIdempotentContext(ctx, prepareForPosting(next))
	if err != nil || replayed {
		t.Fatalf("second posting: replayed=%t err=%v", replayed, err)
	}
	if second.SequenceNumber != 2 {
		t.Fatalf("second posting sequence = %d, want 2 (replay must not consume a number)", second.SequenceNumber)
	}

	found, foundOK, err := driver.FindPostedEntryByIdempotencyKeyContext(ctx, companyID, "invoice-key")
	if err != nil || !foundOK {
		t.Fatalf("lookup by idempotency key: found=%t err=%v", foundOK, err)
	}
	if found.ID != entryID {
		t.Fatalf("lookup returned entry %s, want %s", found.ID, entryID)
	}
	if _, foundOK, err := driver.FindPostedEntryByIdempotencyKeyContext(ctx, companyID, "unknown-key"); err != nil || foundOK {
		t.Fatalf("unknown key lookup: found=%t err=%v", foundOK, err)
	}
}

func TestPostingIdempotencyRejectsDifferentPayloadUnderSameKey(t *testing.T) {
	driver, companyID, journalID, debitID, creditID := setupIdempotentPostingEnv(t)
	ctx := context.Background()

	entryID := uuid.New()
	first := idempotentDraft(t, companyID, journalID, debitID, creditID, entryID, "100")
	first.IdempotencyKey = "shared-key"
	if err := driver.SaveDraftContext(ctx, first); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if _, replayed, err := driver.PostAndSequenceIdempotentContext(ctx, prepareForPosting(first)); err != nil || replayed {
		t.Fatalf("first posting: replayed=%t err=%v", replayed, err)
	}

	// A different payload under the same key is a conflict, not a replay.
	different := idempotentDraft(t, companyID, journalID, debitID, creditID, uuid.New(), "250")
	different.IdempotencyKey = "shared-key"
	if err := driver.SaveDraftContext(ctx, different); err != nil {
		t.Fatalf("save conflicting draft: %v", err)
	}
	_, replayed, err := driver.PostAndSequenceIdempotentContext(ctx, prepareForPosting(different))
	if !errors.Is(err, posting.ErrIdempotencyConflict) {
		t.Fatalf("conflicting payload error = %v (replayed=%t), want ErrIdempotencyConflict", err, replayed)
	}
	if replayed {
		t.Fatal("conflicting payload reported replay")
	}

	// The conflicting draft must not have been posted.
	stored, err := driver.GetEntryByIDForCompanyContext(ctx, companyID, different.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("conflicting draft status = %v, want draft", stored.Status)
	}
}
