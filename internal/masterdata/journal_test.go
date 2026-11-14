package masterdata

import (
	"errors"
	"testing"
)

func TestJournalNumberingAndCurrencyRules(t *testing.T) {
	journal := Journal{
		Type: JournalTypeGeneral, AllowedCurrencies: []string{"USD", "EUR"},
		NumberingPolicy: JournalNumberingPerJournal,
	}
	if err := journal.Validate(); err != nil {
		t.Fatalf("valid journal configuration rejected: %v", err)
	}
	journal.AllowedCurrencies = []string{"usd"}
	if err := journal.Validate(); !errors.Is(err, ErrJournalCurrencyNotAllowed) {
		t.Fatalf("lowercase currency error = %v, want invalid currency rule", err)
	}
	journal.AllowedCurrencies = nil
	journal.NumberingPolicy = "global"
	if err := journal.Validate(); !errors.Is(err, ErrInvalidJournalNumberingPolicy) {
		t.Fatalf("unsupported numbering policy error = %v, want invalid policy", err)
	}
}
