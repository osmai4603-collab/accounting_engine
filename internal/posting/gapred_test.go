//go:build gapred

// Red tests for the Phase 0 traceability matrix. They encode the confirmed
// gaps from docs/index.md against the current code. Run with:
//
//	go test -tags gapred ./internal/posting -run TestGAP
//
// Every test in this file is expected to FAIL until the corresponding phase
// task lands. They are excluded from the default suite so the G0 green gate
// stays meaningful.
package posting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
)

func gapMoney(t *testing.T, amount string) (financialtypes.Money, financialtypes.Money) {
	t.Helper()
	debit, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	return debit, zero
}

func gapEntry(t *testing.T, companyID, journalID, periodID uuid.UUID, reference string) posting.JournalEntry {
	t.Helper()
	debit, zero := gapMoney(t, "100.00")
	entry, err := posting.NewJournalEntry(journalID, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), "maker",
		[]posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: gapAccountID(companyID, "debit"), Debit: debit, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: gapAccountID(companyID, "credit"), Debit: zero, Credit: debit},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = periodID
	entry.Reference = reference
	return entry
}

func gapAccountID(companyID uuid.UUID, side string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(companyID.String()+"/"+side))
}

// GAP-PST-02: DraftContentMatches must compare every accounting-relevant
// field. PeriodID, TargetPeriodNumber, AdjustmentAuthorized, and
// OriginalEntryID are currently omitted.
func TestGAP_PST_02_DraftContentMatchesPeriodAndAuthorization(t *testing.T) {
	companyID, journalID := uuid.New(), uuid.New()
	saved := gapEntry(t, companyID, journalID, uuid.New(), "REF")

	cases := map[string]func(posting.JournalEntry) posting.JournalEntry{
		"PeriodID": func(entry posting.JournalEntry) posting.JournalEntry {
			entry.PeriodID = uuid.New()
			return entry
		},
		"TargetPeriodNumber": func(entry posting.JournalEntry) posting.JournalEntry {
			entry.TargetPeriodNumber = 13
			return entry
		},
		"AdjustmentAuthorized": func(entry posting.JournalEntry) posting.JournalEntry {
			entry.AdjustmentAuthorized = true
			return entry
		},
		"OriginalEntryID": func(entry posting.JournalEntry) posting.JournalEntry {
			originalID := uuid.New()
			entry.OriginalEntryID = &originalID
			return entry
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := mutate(saved)
			if posting.DraftContentMatches(saved, candidate) {
				t.Fatalf("GAP-PST-02: DraftContentMatches ignored a changed %s", name)
			}
		})
	}
}

// GAP-PST-03: the approval fingerprint omits SourceEventID, so a draft can be
// re-bound to a different economic event after approval without invalidating
// the fingerprint.
func TestGAP_PST_03_EntryContentHashCoversSourceEventID(t *testing.T) {
	companyID, journalID := uuid.New(), uuid.New()
	first := gapEntry(t, companyID, journalID, uuid.Nil, "REF")
	second := first
	firstEvent, secondEvent := uuid.New(), uuid.New()
	first.SourceEventID = &firstEvent
	second.SourceEventID = &secondEvent

	firstHash, err := posting.EntryContentHash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := posting.EntryContentHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatal("GAP-PST-03: EntryContentHash ignored SourceEventID")
	}
}

// legacyRepository intentionally exposes only the minimal posting.Repository
// surface so the engine takes its sequential (non-atomic) fallbacks.
type legacyRepository struct {
	driver         *memory.Driver
	failOnStatus   posting.EntryStatus
	failOnRef      string
	postedReversal uuid.UUID
}

var errInjectedStorageFailure = errors.New("injected storage failure")

func (r *legacyRepository) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	return r.driver.SaveDraftContext(ctx, entry)
}

func (r *legacyRepository) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	return r.driver.GetEntryByIDForCompany(ctx, companyID, entryID)
}

func (r *legacyRepository) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) error {
	if entry.Classification == posting.EntryClassReversing {
		r.postedReversal = entry.ID
	}
	if r.failOnStatus != "" && entry.Status == r.failOnStatus {
		return errInjectedStorageFailure
	}
	if r.failOnRef != "" && entry.Reference == r.failOnRef {
		return errInjectedStorageFailure
	}
	return r.driver.PostAtomicContext(ctx, entry)
}

func (r *legacyRepository) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	return r.driver.GetNextSequenceForCompanyContext(ctx, companyID, journalID)
}

func newLegacyEngine(t *testing.T) (*posting.Engine, *legacyRepository, *memory.Driver, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	driver := memory.New()
	companyID := uuid.New()
	if err := driver.CreateCompany(context.Background(), masterdata.Company{
		ID: companyID, Code: "GAP-" + companyID.String(), LegalName: "Gap Test Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []masterdata.Account{
		{ID: gapAccountID(companyID, "debit"), CompanyID: companyID, Code: "1000", Name: "Debit", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit},
		{ID: gapAccountID(companyID, "credit"), CompanyID: companyID, Code: "2000", Name: "Credit", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit},
	} {
		if err := driver.CreateAccount(context.Background(), account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(context.Background(), year); err != nil {
		t.Fatal(err)
	}
	repo := &legacyRepository{driver: driver}
	engine := posting.NewEngine(repo, nil)
	return engine, repo, driver, companyID, uuid.New(), year.Periods[2].ID
}

// GAP-PST-04: a repository without atomic reversal support must fail before
// creating a reversal or changing the original.
func TestGAP_PST_04_ReversalRequiresAtomicRepository(t *testing.T) {
	ctx := context.Background()
	engine, repo, driver, companyID, journalID, periodID := newLegacyEngine(t)
	original := gapEntry(t, companyID, journalID, periodID, "ORIGINAL")
	if err := engine.CreateDraftContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(ctx, &original, "poster"); err != nil {
		t.Fatal(err)
	}

	repo.failOnStatus = posting.EntryReversed
	_, err := engine.ReverseDualContext(ctx, companyID, original.ID, "maker", "poster",
		time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, posting.ErrAtomicReversalRequired) {
		t.Fatalf("GAP-PST-04: reversal error = %v, want %v", err, posting.ErrAtomicReversalRequired)
	}
	if repo.postedReversal != uuid.Nil {
		t.Fatalf("GAP-PST-04: unsupported repository posted reversal %s", repo.postedReversal)
	}
	storedOriginal, err := driver.GetEntryByIDForCompany(ctx, companyID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != posting.EntryPosted || storedOriginal.ReversedEntryID != nil {
		t.Fatalf("GAP-PST-04: original changed despite unsupported reversal: %+v", storedOriginal)
	}
}

// GAP-PST-06: a repository without atomic bulk support must reject the batch
// without posting any entry.
func TestGAP_PST_06_BulkRequiresAtomicRepository(t *testing.T) {
	ctx := context.Background()
	engine, repo, driver, companyID, journalID, periodID := newLegacyEngine(t)
	first := gapEntry(t, companyID, journalID, periodID, "BULK-OK")
	second := gapEntry(t, companyID, journalID, periodID, "BULK-FAIL")
	for _, entry := range []posting.JournalEntry{first, second} {
		if err := engine.CreateDraftContext(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	repo.failOnRef = "BULK-FAIL"
	if err := engine.PostBulkContext(ctx, []posting.JournalEntry{first, second}, "poster"); !errors.Is(err, posting.ErrAtomicBulkPostingRequired) {
		t.Fatalf("GAP-PST-06: bulk error = %v, want %v", err, posting.ErrAtomicBulkPostingRequired)
	}
	posted, err := driver.GetEntryByIDForCompany(ctx, companyID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Status == posting.EntryPosted {
		t.Fatal("GAP-PST-06: first entry stayed posted after the second entry failed")
	}
}

// GAP-PST-08: there is no explicit idempotency key for posting/reversal, so a
// retried delivery cannot be distinguished from a legitimate second entry.
func TestGAP_PST_08_NoIdempotencyKey(t *testing.T) {
	entry := gapEntry(t, uuid.New(), uuid.New(), uuid.Nil, "REF")
	// IdempotencyKey is the contract proposed by P1-T10. Its absence is a
	// compile-time gap; keep the red test explicit once the field exists.
	t.Skip("GAP-PST-08: JournalEntry has no IdempotencyKey; tracked by P1-T10")
	_ = entry
}

// GAP-PST-10: no optimistic version is carried by JournalEntry, so two
// concurrent writers can each overwrite the other after reading the same row.
func TestGAP_PST_10_NoOptimisticVersion(t *testing.T) {
	entry := gapEntry(t, uuid.New(), uuid.New(), uuid.Nil, "REF")
	t.Skip("GAP-PST-10: JournalEntry has no Version field; tracked by P1-T11")
	_ = entry
}

// GAP-GOV-02 is a governance contract rather than a code path today: the
// reopening flow has no authorization/audit API to exercise.
func TestGAP_GOV_02_ReopenRequiresAuthorizationAndAudit(t *testing.T) {
	t.Skip("GAP-GOV-02: no period-reopen API exists yet; tracked by P2-T4")
}

var _ = masterdata.ErrCompanyScopeRequired
