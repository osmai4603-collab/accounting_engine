package posting

import (
	"errors"
	"sync"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func money(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func balancedEntry(t *testing.T, creator string) JournalEntry {
	t.Helper()
	entry, err := NewJournalEntry(uuid.New(), utcDate(t, 2026, 1, 15), creator, []JournalEntryLine{
		{AccountID: uuid.New(), Debit: money(t, "100.0000", "USD"), Credit: money(t, "0", "USD")},
		{AccountID: uuid.New(), Credit: money(t, "100.0000", "USD"), Debit: money(t, "0", "USD")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func utcDate(t *testing.T, year, month, day int) time.Time {
	t.Helper()
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func TestRejectsUnbalancedEntry(t *testing.T) {
	entry := balancedEntry(t, "maker")
	entry.Lines[1].Credit = money(t, "99", "USD")
	if err := entry.Validate(); err != financialtypes.ErrUnbalancedMove {
		t.Fatalf("expected unbalanced move, got %v", err)
	}
}

func TestFunctionalCurrencyBalancesMultiCurrencyEntry(t *testing.T) {
	functionalDebit := money(t, "375.0000", "SAR")
	functionalCredit := money(t, "375.0000", "SAR")
	entry, err := NewJournalEntry(uuid.New(), utcDate(t, 2026, 1, 15), "maker", []JournalEntryLine{
		{
			AccountID: uuid.New(), Debit: money(t, "100.0000", "EUR"),
			TransactionAmount: money(t, "100.0000", "EUR"), TransactionCurrency: "EUR",
			ExchangeRate: decimal.RequireFromString("3.75"), FunctionalDebit: functionalDebit,
			FunctionalCredit: money(t, "0", "SAR"),
		},
		{
			AccountID: uuid.New(), Credit: money(t, "375.0000", "USD"),
			TransactionAmount: money(t, "375.0000", "USD"), TransactionCurrency: "USD",
			ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: money(t, "0", "SAR"),
			FunctionalCredit: functionalCredit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.Lines[1].FunctionalCredit = money(t, "374.99", "SAR")
	entry.Lines[1].ExchangeRate = decimal.RequireFromString("0.9999733333")
	if err := entry.Validate(); !errors.Is(err, financialtypes.ErrUnbalancedMove) {
		t.Fatalf("Validate() error = %v, want functional-currency imbalance", err)
	}
}

func TestCompanyScopedPostingRejectsCrossCompanyAccount(t *testing.T) {
	masterRepo := masterdata.NewMockRepository()
	companyA, companyB := uuid.New(), uuid.New()
	if err := masterRepo.CreateCompany(masterdata.Company{
		ID: companyA, Code: "CO-A", LegalName: "Company A",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	accountA := masterdata.Account{
		CompanyID: companyA, ID: uuid.New(), Code: "1000", Name: "Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
	}
	accountB := masterdata.Account{
		CompanyID: companyB, ID: uuid.New(), Code: "1000", Name: "Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
	}
	if err := masterRepo.CreateAccount(accountA); err != nil {
		t.Fatal(err)
	}
	if err := masterRepo.CreateAccount(accountB); err != nil {
		t.Fatal(err)
	}
	yearA := masterdata.GenerateFiscalYear(utcDate(t, 2026, 1, 1))
	yearA.CompanyID = companyA
	yearB := masterdata.GenerateFiscalYear(utcDate(t, 2026, 1, 1))
	yearB.CompanyID = companyB
	if err := masterRepo.CreateFiscalYear(yearA); err != nil {
		t.Fatal(err)
	}
	if err := masterRepo.CreateFiscalYear(yearB); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(NewMockRepository(), masterdata.NewService(masterRepo))
	entry := balancedEntry(t, "maker")
	entry.CompanyID = companyA
	entry.Lines[0].AccountID = accountA.ID
	entry.Lines[1].AccountID = accountB.ID
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); !errors.Is(err, masterdata.ErrAccountNotFound) {
		t.Fatalf("Post() error = %v, want cross-company account rejection", err)
	}
}

func TestPostingChecksCompanyFunctionalCurrency(t *testing.T) {
	functional := JournalEntry{
		CompanyID: uuid.New(),
		Lines: []JournalEntryLine{
			{
				AccountID: uuid.New(), Debit: money(t, "100", "EUR"),
				TransactionAmount: money(t, "100", "EUR"), TransactionCurrency: "EUR",
				ExchangeRate:    decimal.RequireFromString("3.75"),
				FunctionalDebit: money(t, "375", "SAR"), FunctionalCredit: money(t, "0", "SAR"),
			},
			{
				AccountID: uuid.New(), Credit: money(t, "375", "USD"),
				TransactionAmount: money(t, "375", "USD"), TransactionCurrency: "USD",
				ExchangeRate:    decimal.NewFromInt(1),
				FunctionalDebit: money(t, "0", "SAR"), FunctionalCredit: money(t, "375", "SAR"),
			},
		},
	}
	if err := validateFunctionalCurrency(functional, "SAR"); err != nil {
		t.Fatalf("valid functional currency was rejected: %v", err)
	}
	if err := validateFunctionalCurrency(functional, "USD"); !errors.Is(err, ErrFunctionalCurrencyMismatch) {
		t.Fatalf("validateFunctionalCurrency() error = %v, want mismatch", err)
	}
}

func TestDraftContentMatchDetectsLineChanges(t *testing.T) {
	saved := balancedEntry(t, "maker")
	candidate := saved
	candidate.Lines = append([]JournalEntryLine(nil), saved.Lines...)
	candidate.Lines[0].Debit = money(t, "99", "USD")
	if DraftContentMatches(saved, candidate) {
		t.Fatal("DraftContentMatches accepted modified draft lines")
	}
}

func TestMinimumLinesAndSingleSide(t *testing.T) {
	entry := JournalEntry{Lines: []JournalEntryLine{{AccountID: uuid.New()}}}
	if err := entry.Validate(); err != ErrMinimumLines {
		t.Fatalf("expected minimum-lines error, got %v", err)
	}

	entry = JournalEntry{Lines: []JournalEntryLine{
		{AccountID: uuid.New(), Debit: money(t, "10", "USD"), Credit: money(t, "10", "USD")},
		{AccountID: uuid.New(), Debit: money(t, "0", "USD"), Credit: money(t, "10", "USD")},
	}}
	if err := entry.Validate(); err != ErrLineMustHaveOneSide {
		t.Fatalf("expected single-side error, got %v", err)
	}
}

func TestPostedEntryCannotBeModified(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.GetEntryByID(entry.ID)
	stored.Status = EntryDraft
	if err := repo.SaveDraft(stored); err != financialtypes.ErrCannotModifyPostedMove {
		t.Fatalf("expected immutability error, got %v", err)
	}
}

func TestMakerChecker(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	engine.SetMakerCheckerThreshold(decimal.RequireFromString("500"))
	entry := balancedEntry(t, "maker")
	entry.Lines[0].Debit = money(t, "600", "USD")
	entry.Lines[1].Credit = money(t, "600", "USD")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, ""); !errors.Is(err, financialtypes.ErrMakerCheckerViolation) {
		t.Fatalf("expected empty poster identity to violate maker-checker, got %v", err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "maker"); err != financialtypes.ErrMakerCheckerViolation {
		t.Fatalf("expected maker-checker violation, got %v", err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
}

func TestEntryContentHashIncludesApprovalRelevantContext(t *testing.T) {
	entry := balancedEntry(t, "maker")
	entry.CompanyID = uuid.New()
	baseHash, err := EntryContentHash(entry)
	if err != nil {
		t.Fatal(err)
	}

	variants := map[string]func(*JournalEntry){
		"period":                   func(entry *JournalEntry) { entry.PeriodID = uuid.New() },
		"adjustment authorization": func(entry *JournalEntry) { entry.AdjustmentAuthorized = true },
		"original entry": func(entry *JournalEntry) {
			originalID := uuid.New()
			entry.OriginalEntryID = &originalID
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			changed := entry
			mutate(&changed)
			hash, err := EntryContentHash(changed)
			if err != nil {
				t.Fatal(err)
			}
			if hash == baseHash {
				t.Fatalf("content hash did not change after modifying %s", name)
			}
		})
	}

	posted := entry
	posted.Status = EntryPosted
	posted.SequenceNumber = 42
	posted.PostedBy = "poster"
	posted.PostedAt = timePointer(utcDate(t, 2026, 1, 16))
	hash, err := EntryContentHash(posted)
	if err != nil {
		t.Fatal(err)
	}
	if hash != baseHash {
		t.Fatal("posting metadata changed the approval content hash")
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func TestConcurrentSequences(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	journalID := uuid.New()
	const count = 50

	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		entry := balancedEntry(t, "maker")
		entry.JournalID = journalID
		if err := engine.CreateDraft(entry); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			errs <- engine.Post(entry.CompanyID, id, "checker")
		}(entry.ID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	seen := make(map[int64]bool)
	for _, entry := range repo.entries {
		if entry.Status != EntryPosted {
			continue
		}
		if seen[entry.SequenceNumber] {
			t.Fatalf("duplicate sequence %d", entry.SequenceNumber)
		}
		seen[entry.SequenceNumber] = true
	}
	if len(seen) != count || int64(len(seen)) != int64(count) {
		t.Fatalf("got %d posted sequence numbers, want %d", len(seen), count)
	}
	for sequence := int64(1); sequence <= count; sequence++ {
		if !seen[sequence] {
			t.Fatalf("sequence gap at %d", sequence)
		}
	}
}

func TestRejectedPostingDoesNotConsumeSequence(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	engine.SetMakerCheckerThreshold(decimal.NewFromInt(500))

	rejected := balancedEntry(t, "maker")
	rejected.Lines[0].Debit = money(t, "600", "USD")
	rejected.Lines[1].Credit = money(t, "600", "USD")
	if err := engine.CreateDraft(rejected); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(rejected.CompanyID, rejected.ID, "maker"); !errors.Is(err, financialtypes.ErrMakerCheckerViolation) {
		t.Fatalf("expected maker-checker rejection, got %v", err)
	}

	valid := balancedEntry(t, "maker")
	if err := engine.CreateDraft(valid); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(valid.CompanyID, valid.ID, "checker"); err != nil {
		t.Fatal(err)
	}
	posted, err := repo.GetEntryByID(valid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if posted.SequenceNumber != 1 {
		t.Fatalf("first successful posting got sequence %d, want 1", posted.SequenceNumber)
	}
}

func TestReversal(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
	reversal, err := engine.Reverse(entry.CompanyID, entry.ID, "checker")
	if err != nil {
		t.Fatal(err)
	}
	storedOriginal, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != EntryReversed || storedOriginal.ReversedEntryID == nil ||
		*storedOriginal.ReversedEntryID != reversal.ID {
		t.Fatalf("original entry was not linked to reversal: %#v", storedOriginal)
	}
	if reversal.Status != EntryPosted || reversal.OriginalEntryID == nil ||
		*reversal.OriginalEntryID != entry.ID {
		t.Fatalf("invalid reversal: %#v", reversal)
	}
}

func TestMultiCurrencyEntryWithFunctionalBalance(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)

	// Case 1: Transaction lines in different currencies (EUR debit, USD credit) balanced by SAR AmountCurrency
	eurDebit, err := financialtypes.NewMoney(decimal.NewFromInt(100), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	usdCredit, err := financialtypes.NewMoney(decimal.NewFromInt(108), "USD")
	if err != nil {
		t.Fatal(err)
	}
	sarEquivalent, err := financialtypes.NewMoney(decimal.NewFromInt(405), "SAR")
	if err != nil {
		t.Fatal(err)
	}

	entry := JournalEntry{
		ID:        uuid.New(),
		JournalID: uuid.New(),
		Date:      time.Now().UTC(),
		Status:    EntryDraft,
		CreatedBy: "maker",
		Lines: []JournalEntryLine{
			{
				ID:             uuid.New(),
				AccountID:      uuid.New(),
				Debit:          eurDebit,
				AmountCurrency: sarEquivalent,
			},
			{
				ID:             uuid.New(),
				AccountID:      uuid.New(),
				Credit:         usdCredit,
				AmountCurrency: sarEquivalent,
			},
		},
	}

	if err := engine.CreateDraft(entry); err != nil {
		t.Fatalf("failed to create multi-currency draft: %v", err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
		t.Fatalf("failed to post multi-currency entry: %v", err)
	}

	// Case 2: Unbalanced multi-currency entry should be rejected
	sarUnbalanced, err := financialtypes.NewMoney(decimal.NewFromInt(400), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	unbalancedEntry := JournalEntry{
		ID:        uuid.New(),
		JournalID: uuid.New(),
		Date:      time.Now().UTC(),
		Status:    EntryDraft,
		CreatedBy: "maker",
		Lines: []JournalEntryLine{
			{
				ID:             uuid.New(),
				AccountID:      uuid.New(),
				Debit:          eurDebit,
				AmountCurrency: sarEquivalent,
			},
			{
				ID:             uuid.New(),
				AccountID:      uuid.New(),
				Credit:         usdCredit,
				AmountCurrency: sarUnbalanced,
			},
		},
	}
	if err := engine.CreateDraft(unbalancedEntry); !errors.Is(err, financialtypes.ErrUnbalancedMove) {
		t.Fatalf("expected ErrUnbalancedMove for unbalanced multi-currency, got: %v", err)
	}
}

func TestReversalWithMakerCheckerThreshold(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	engine.SetMakerCheckerThreshold(decimal.NewFromInt(50))

	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}

	// Reversal should succeed because Reverse creates the entry with system-reversal as creator, posted by checker
	reversal, err := engine.Reverse(entry.CompanyID, entry.ID, "checker")
	if err != nil {
		t.Fatalf("reversal failed under maker-checker: %v", err)
	}
	if reversal.Status != EntryPosted {
		t.Fatalf("expected posted reversal, got %s", reversal.Status)
	}
}
