package portbridge

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

func publicEntryFixture(t *testing.T) ledger.JournalEntry {
	t.Helper()
	amount, err := types.NewMoneyFromString("100.00", "EUR")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "EUR")
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	created := time.Date(2026, time.March, 1, 10, 30, 0, 0, time.UTC)
	approvedAt := created.Add(2 * time.Hour)
	postedAt := created.Add(3 * time.Hour)
	rate, err := decimal.NewFromString("1.25")
	if err != nil {
		t.Fatalf("rate: %v", err)
	}
	return ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: types.NewCompanyID(), JournalID: types.NewJournalID(),
		FiscalPeriodID: types.NewFiscalPeriodID(), Status: ledger.EntryDraft,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC),
		PostingDate:    time.Date(2026, time.March, 16, 0, 0, 0, 0, time.UTC),
		Description:    "explicit conversion", SourceModule: "portbridge-test",
		TransactionCurrency: "EUR", ExchangeRate: rate,
		CreatedBy: "creator", CreatedAt: created, UpdatedAt: created.Add(time.Hour),
		ApprovedBy: "approver", ApprovedAt: &approvedAt,
		PostedBy: "poster", PostedAt: &postedAt,
		EntryNumber: "JE-1", SequenceNumber: 7,
		ReferenceID:    "invoice:INV-2042",
		ReversalReason: "correction",
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: types.NewAccountID(),
				TransactionAmount: amount, TransactionCurrency: "EUR",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: types.NewAccountID(),
				TransactionAmount: amount, TransactionCurrency: "EUR",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
}

func TestToInternalEntryRejectsMissingIdentityWithoutGeneration(t *testing.T) {
	entry := publicEntryFixture(t)
	entry.ID = types.JournalEntryID{}

	if _, err := ToInternalEntry(entry, EntryOptions{}); !errors.Is(err, ErrMissingEntryID) {
		t.Fatalf("error = %v, want ErrMissingEntryID", err)
	}
}

func TestToInternalEntryGeneratesIdentityOnlyWhenExplicitlyRequested(t *testing.T) {
	entry := publicEntryFixture(t)
	entry.ID = types.JournalEntryID{}
	entry.Lines[0].ID = types.JournalEntryLineID{}
	entry.CreatedAt = time.Time{}
	entry.UpdatedAt = time.Time{}
	now := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)

	internal, err := ToInternalEntry(entry, EntryOptions{
		GenerateMissingIDs: true,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("convert with explicit generation: %v", err)
	}
	if internal.ID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("entry id was not generated")
	}
	if internal.Lines[0].ID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("line id was not generated")
	}
	if internal.PeriodID != uuid.UUID(entry.FiscalPeriodID) {
		t.Fatal("draft conversion changed the supplied fiscal period id")
	}
	if !internal.CreatedAt.Equal(now) {
		t.Fatalf("generated created timestamp = %s, want %s", internal.CreatedAt, now)
	}
	if !internal.UpdatedAt.IsZero() {
		t.Fatalf("missing updated timestamp was synthesized: %s", internal.UpdatedAt)
	}

	entry.FiscalPeriodID = types.FiscalPeriodID{}
	internal, err = ToInternalEntry(entry, EntryOptions{
		GenerateMissingIDs: true,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if internal.PeriodID != uuid.Nil {
		t.Fatal("draft conversion fabricated a missing fiscal period id")
	}
}

func TestToInternalEntryRejectsMissingRequiredIdentityAndClock(t *testing.T) {
	now := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*ledger.JournalEntry)
		opts EntryOptions
		want error
	}{
		{"company", func(e *ledger.JournalEntry) { e.CompanyID = types.CompanyID{} }, EntryOptions{}, ErrMissingCompanyID},
		{"journal", func(e *ledger.JournalEntry) { e.JournalID = types.JournalID{} }, EntryOptions{GenerateMissingIDs: true, Now: func() time.Time { return now }}, ErrMissingJournalID},
		{"period", func(e *ledger.JournalEntry) { e.FiscalPeriodID = types.FiscalPeriodID{} }, EntryOptions{}, ErrMissingPeriodID},
		{"account", func(e *ledger.JournalEntry) { e.Lines[0].AccountID = types.AccountID{} }, EntryOptions{GenerateMissingIDs: true, Now: func() time.Time { return now }}, ErrMissingAccountID},
		{"created timestamp", func(e *ledger.JournalEntry) { e.CreatedAt = time.Time{} }, EntryOptions{}, ErrMissingCreatedAt},
		{"generation clock", func(e *ledger.JournalEntry) { e.CreatedAt = time.Time{} }, EntryOptions{GenerateMissingIDs: true}, ErrMissingClock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := publicEntryFixture(t)
			tt.edit(&entry)
			if _, err := ToInternalEntry(entry, tt.opts); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestToInternalEntryDoesNotSynthesizeDatesOrMoney(t *testing.T) {
	entry := publicEntryFixture(t)
	entry.PostingDate = time.Time{}
	internal, err := ToInternalEntry(entry, EntryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !internal.Date.IsZero() {
		t.Fatalf("missing posting date was replaced with %s", internal.Date)
	}

	entry = publicEntryFixture(t)
	entry.Lines[0].TransactionAmount = types.Money{}
	if _, err := ToInternalEntry(entry, EntryOptions{}); !errors.Is(err, ErrMissingMoneyCurrency) {
		t.Fatalf("error = %v, want ErrMissingMoneyCurrency", err)
	}
}

func TestToInternalEntryPreservesCurrencyDatesAndIdentity(t *testing.T) {
	entry := publicEntryFixture(t)
	internal, err := ToInternalEntry(entry, EntryOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if internal.ID != uuid.UUID(entry.ID) {
		t.Fatalf("entry id changed")
	}
	if internal.EntryDate != entry.EffectiveDate || internal.Date != entry.PostingDate {
		t.Fatalf("dates changed: entry=%s posting=%s", internal.EntryDate, internal.Date)
	}
	if internal.TransactionCurrency != "EUR" {
		t.Fatalf("transaction currency = %q, want EUR", internal.TransactionCurrency)
	}
	if !internal.ExchangeRate.Equal(entry.ExchangeRate) {
		t.Fatalf("exchange rate = %s, want %s", internal.ExchangeRate, entry.ExchangeRate)
	}
	if internal.SequenceNumber != 7 || internal.LegalNumber != "JE-1" {
		t.Fatalf("numbers changed: sequence=%d legal=%q", internal.SequenceNumber, internal.LegalNumber)
	}
	if internal.ReversalReason != "correction" {
		t.Fatalf("reversal reason = %q", internal.ReversalReason)
	}

	roundTrip, err := ToPublicEntry(internal)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if roundTrip.ID != entry.ID || roundTrip.CompanyID != entry.CompanyID ||
		roundTrip.JournalID != entry.JournalID || roundTrip.FiscalPeriodID != entry.FiscalPeriodID {
		t.Fatalf("identity was not preserved on round trip")
	}
	if !roundTrip.EffectiveDate.Equal(entry.EffectiveDate) || !roundTrip.PostingDate.Equal(entry.PostingDate) {
		t.Fatalf("dates changed on round trip")
	}
	if roundTrip.SequenceNumber != 7 || roundTrip.EntryNumber != "JE-1" || roundTrip.ReversalReason != "correction" {
		t.Fatalf("numbers/reason changed on round trip: %+v", roundTrip)
	}
	if roundTrip.CreatedBy != entry.CreatedBy || roundTrip.ApprovedBy != entry.ApprovedBy ||
		roundTrip.PostedBy != entry.PostedBy || !roundTrip.CreatedAt.Equal(entry.CreatedAt) ||
		!roundTrip.UpdatedAt.Equal(entry.UpdatedAt) || roundTrip.ApprovedAt == nil ||
		!roundTrip.ApprovedAt.Equal(*entry.ApprovedAt) || roundTrip.PostedAt == nil ||
		!roundTrip.PostedAt.Equal(*entry.PostedAt) {
		t.Fatalf("actor or timestamp semantics changed: %+v", roundTrip)
	}
	if roundTrip.ReferenceID != entry.ReferenceID {
		t.Fatalf("reference changed: got %q, want %q", roundTrip.ReferenceID, entry.ReferenceID)
	}
	if internal.SourceEventID != nil {
		t.Fatal("ReferenceID was converted into a SourceEventID")
	}
}

func TestToPublicEntryRejectsMissingRequiredIdentityAndMoneyCurrency(t *testing.T) {
	base, err := ToInternalEntry(publicEntryFixture(t), EntryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*posting.JournalEntry)
		want error
	}{
		{"company", func(e *posting.JournalEntry) { e.CompanyID = uuid.Nil }, ErrMissingCompanyID},
		{"entry", func(e *posting.JournalEntry) { e.ID = uuid.Nil }, ErrMissingEntryID},
		{"journal", func(e *posting.JournalEntry) { e.JournalID = uuid.Nil }, ErrMissingJournalID},
		{"period", func(e *posting.JournalEntry) { e.PeriodID = uuid.Nil }, ErrMissingPeriodID},
		{"line", func(e *posting.JournalEntry) { e.Lines[0].ID = uuid.Nil }, ErrMissingLineID},
		{"account", func(e *posting.JournalEntry) { e.Lines[0].AccountID = uuid.Nil }, ErrMissingAccountID},
		{"money currency", func(e *posting.JournalEntry) {
			e.Lines[0].TransactionAmount = financialtypes.Money{}
		}, ErrMissingMoneyCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := base
			entry.Lines = append([]posting.JournalEntryLine(nil), base.Lines...)
			tt.edit(&entry)
			if _, err := ToPublicEntry(entry); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
