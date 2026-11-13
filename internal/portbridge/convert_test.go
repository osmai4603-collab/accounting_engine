package portbridge

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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
		CreatedBy: "actor", CreatedAt: created, UpdatedAt: created.Add(time.Hour),
		EntryNumber: "JE-1", SequenceNumber: 7,
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
}
