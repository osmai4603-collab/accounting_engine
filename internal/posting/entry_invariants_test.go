package posting

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func makeTestMoney(t *testing.T, amountStr, currency string) financialtypes.Money {
	t.Helper()
	m, err := financialtypes.NewMoneyFromString(amountStr, currency)
	if err != nil {
		t.Fatalf("makeTestMoney(%s, %s): %v", amountStr, currency, err)
	}
	return m
}

// -----------------------------------------------------------------------------
// 1. Property-Based & Boundary Precision Tests
// -----------------------------------------------------------------------------

func TestPropertyBased_ConversionAndBalanceInvariants(t *testing.T) {
	// Property 1: For any positive transaction amount and strictly positive exchange rate,
	// if functional debit/credit matches TransactionAmount * ExchangeRate (rounded to currency),
	// the line passes validation.
	assertion := func(seed int64, rateCents int64) bool {
		// Constrain inputs to realistic accounting ranges
		r := rand.New(rand.NewSource(seed))
		amtUnits := r.Int63n(10_000_000) + 1 // 0.01 to 100,000.00
		rateUnits := (rateCents % 5000) + 1  // 0.0001 to 50.0000

		decAmt := decimal.New(amtUnits, -2)
		decRate := decimal.New(rateUnits, -4)
		if !decRate.IsPositive() || !decAmt.IsPositive() {
			return true
		}

		txMoney, err := financialtypes.NewMoney(decAmt, "USD")
		if err != nil {
			return false
		}

		expectedFuncDec := decAmt.Mul(decRate)
		funcMoney, err := financialtypes.NewMoney(expectedFuncDec, "SAR")
		if err != nil {
			return false
		}
		roundedFunc, err := funcMoney.Rounded()
		if err != nil {
			return false
		}

		zero, _ := financialtypes.Zero("SAR")
		line := JournalEntryLine{
			AccountID:           uuid.New(),
			TransactionAmount:   txMoney,
			TransactionCurrency: "USD",
			ExchangeRate:        decRate,
			FunctionalDebit:     roundedFunc,
			FunctionalCredit:    zero,
		}

		err = line.Validate()
		return err == nil
	}

	if err := quick.Check(assertion, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatalf("Property 1 failed: %v", err)
	}
}

func TestBoundaryPrecision_CurrenciesAndAmounts(t *testing.T) {
	testCases := []struct {
		name        string
		txAmt       string
		txCur       string
		rate        string
		funcAmt     string
		funcCur     string
		shouldPass  bool
		expectedErr error
	}{
		{
			name:        "Tiny amount 0.0001 with 4 decimals",
			txAmt:       "0.0001",
			txCur:       "USD",
			rate:        "3.7500",
			funcAmt:     "0.00", // 0.0001 * 3.75 = 0.000375 -> rounded to 2 decimals is 0.00 -> wait! zero amount functional line is rejected
			funcCur:     "SAR",
			shouldPass:  false,
			expectedErr: financialtypes.ErrZeroAmountLine,
		},
		{
			name:       "Smallest payable minor unit 0.01 USD -> SAR",
			txAmt:      "0.01",
			txCur:      "USD",
			rate:       "3.7500",
			funcAmt:    "0.04", // 0.01 * 3.75 = 0.0375 -> rounded to 0.04 SAR
			funcCur:    "SAR",
			shouldPass: true,
		},
		{
			name:       "Huge amount 100 billion USD",
			txAmt:      "100000000000.00",
			txCur:      "USD",
			rate:       "3.7500",
			funcAmt:    "375000000000.00",
			funcCur:    "SAR",
			shouldPass: true,
		},
		{
			name:       "3-decimal currency KWD (Kuwaiti Dinar)",
			txAmt:      "100.000",
			txCur:      "KWD",
			rate:       "12.2150",
			funcAmt:    "1221.50",
			funcCur:    "SAR",
			shouldPass: true,
		},
		{
			name:       "0-decimal currency JPY (Japanese Yen)",
			txAmt:      "10000",
			txCur:      "JPY",
			rate:       "0.0250",
			funcAmt:    "250.00",
			funcCur:    "SAR",
			shouldPass: true,
		},
		{
			name:        "Banker's rounding half to even boundary 0.005 -> 0.00 vs 0.015 -> 0.02",
			txAmt:       "1.00",
			txCur:       "USD",
			rate:        "0.005",
			funcAmt:     "0.00", // half-to-even rounded to 0.00 -> zero amount rejected
			funcCur:     "SAR",
			shouldPass:  false,
			expectedErr: financialtypes.ErrZeroAmountLine,
		},
		{
			name:       "Banker's rounding 0.015 -> 0.02 SAR",
			txAmt:      "1.00",
			txCur:      "USD",
			rate:       "0.015",
			funcAmt:    "0.02",
			funcCur:    "SAR",
			shouldPass: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			txMoney, err := financialtypes.NewMoneyFromString(tc.txAmt, tc.txCur)
			if err != nil {
				t.Fatalf("failed to create txMoney: %v", err)
			}
			funcMoney, err := financialtypes.NewMoneyFromString(tc.funcAmt, tc.funcCur)
			if err != nil {
				t.Fatalf("failed to create funcMoney: %v", err)
			}
			zero, _ := financialtypes.Zero(tc.funcCur)

			line := JournalEntryLine{
				AccountID:           uuid.New(),
				TransactionAmount:   txMoney,
				TransactionCurrency: tc.txCur,
				ExchangeRate:        decimal.RequireFromString(tc.rate),
				FunctionalDebit:     funcMoney,
				FunctionalCredit:    zero,
			}

			err = line.Validate()
			if tc.shouldPass && err != nil {
				t.Fatalf("expected valid line, got error: %v", err)
			}
			if !tc.shouldPass {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.expectedErr != nil && !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 2. Rejection of Fake and Mismatched References
// -----------------------------------------------------------------------------

func TestRejectsNilUUIDInDimensions(t *testing.T) {
	nilID := uuid.Nil
	validID := uuid.New()

	baseLine := func() JournalEntryLine {
		return JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
	}

	tests := []struct {
		name  string
		apply func(*JournalEntryLine)
	}{
		{"PartnerID is nil UUID", func(l *JournalEntryLine) { l.PartnerID = &nilID }},
		{"CostCenterID is nil UUID", func(l *JournalEntryLine) { l.CostCenterID = &nilID }},
		{"ProjectID is nil UUID", func(l *JournalEntryLine) { l.ProjectID = &nilID }},
		{"SegmentID is nil UUID", func(l *JournalEntryLine) { l.SegmentID = &nilID }},
		{"BusinessUnitID is nil UUID", func(l *JournalEntryLine) { l.BusinessUnitID = &nilID }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := baseLine()
			tc.apply(&line)

			// Line validation directly
			if err := line.Validate(); !errors.Is(err, ErrInvalidDimensionReference) {
				t.Fatalf("line.Validate() error = %v, want ErrInvalidDimensionReference", err)
			}

			// Entry validation
			otherLine := baseLine()
			otherLine.FunctionalDebit = makeTestMoney(t, "0.00", "USD")
			otherLine.FunctionalCredit = makeTestMoney(t, "100.00", "USD")

			entry := JournalEntry{
				JournalID: uuid.New(),
				Date:      time.Now().UTC(),
				Lines:     []JournalEntryLine{line, otherLine},
			}
			if err := entry.Validate(); !errors.Is(err, ErrInvalidDimensionReference) {
				t.Fatalf("entry.Validate() error = %v, want ErrInvalidDimensionReference", err)
			}
		})
	}
}

func TestRejectsHeaderLevelInvariants(t *testing.T) {
	now := time.Now().UTC()
	lineA := JournalEntryLine{
		AccountID:           uuid.New(),
		TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
		FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
	}
	lineB := JournalEntryLine{
		AccountID:           uuid.New(),
		TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     makeTestMoney(t, "0.00", "USD"),
		FunctionalCredit:    makeTestMoney(t, "100.00", "USD"),
	}

	t.Run("Rejects JournalID == uuid.Nil", func(t *testing.T) {
		entry := JournalEntry{
			JournalID: uuid.Nil,
			Date:      now,
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); !errors.Is(err, ErrMissingJournalID) {
			t.Fatalf("entry.Validate() error = %v, want ErrMissingJournalID", err)
		}
	})

	t.Run("Rejects zero date (both EntryDate and Date zero)", func(t *testing.T) {
		entry := JournalEntry{
			JournalID: uuid.New(),
			EntryDate: time.Time{},
			Date:      time.Time{},
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); !errors.Is(err, ErrZeroEntryDate) {
			t.Fatalf("entry.Validate() error = %v, want ErrZeroEntryDate", err)
		}
	})

	t.Run("Accepts entry when EntryDate is set even if Date is zero", func(t *testing.T) {
		entry := JournalEntry{
			JournalID: uuid.New(),
			EntryDate: now,
			Date:      time.Time{},
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); err != nil {
			t.Fatalf("entry with valid EntryDate should pass, got: %v", err)
		}
	})

	t.Run("Accepts entry when Date is set even if EntryDate is zero", func(t *testing.T) {
		entry := JournalEntry{
			JournalID: uuid.New(),
			EntryDate: time.Time{},
			Date:      now,
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); err != nil {
			t.Fatalf("entry with valid Date should pass, got: %v", err)
		}
	})
}

func TestRejectsCompanyIDMismatches(t *testing.T) {
	companyA := uuid.New()
	companyB := uuid.New()
	now := time.Now().UTC()

	lineA := JournalEntryLine{
		CompanyID:           companyA,
		AccountID:           uuid.New(),
		TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
		FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
	}
	lineB := JournalEntryLine{
		CompanyID:           companyB,
		AccountID:           uuid.New(),
		TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     makeTestMoney(t, "0.00", "USD"),
		FunctionalCredit:    makeTestMoney(t, "100.00", "USD"),
	}

	t.Run("Rejects entry with header CompanyA and line CompanyB", func(t *testing.T) {
		entry := JournalEntry{
			CompanyID: companyA,
			JournalID: uuid.New(),
			Date:      now,
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); !errors.Is(err, ErrCompanyMismatch) {
			t.Fatalf("entry.Validate() error = %v, want ErrCompanyMismatch", err)
		}
	})

	t.Run("Rejects entry with header uuid.Nil and line with non-nil CompanyID", func(t *testing.T) {
		entry := JournalEntry{
			CompanyID: uuid.Nil,
			JournalID: uuid.New(),
			Date:      now,
			Lines:     []JournalEntryLine{lineA, lineA},
		}
		if err := entry.Validate(); !errors.Is(err, ErrCompanyMismatch) {
			t.Fatalf("entry.Validate() error = %v, want ErrCompanyMismatch", err)
		}
	})

	t.Run("NewJournalEntry rejects lines with different CompanyIDs", func(t *testing.T) {
		_, err := NewJournalEntry(uuid.New(), now, "maker", []JournalEntryLine{lineA, lineB})
		if !errors.Is(err, ErrCompanyMismatch) {
			t.Fatalf("NewJournalEntry error = %v, want ErrCompanyMismatch", err)
		}
	})
}

func TestRejectsInvalidCurrencyCodes(t *testing.T) {
	invalidCodes := []string{
		"",     // empty
		"US",   // too short
		"USDD", // too long
		"usd",  // lowercase
		"Usd",  // mixed case
		"123",  // numeric
		"U$D",  // special char
		"ZZZ",  // unassigned ISO code
		"   ",  // whitespace
	}

	for _, code := range invalidCodes {
		t.Run(fmt.Sprintf("InvalidCode_%q", code), func(t *testing.T) {
			if err := ValidateISOCurrencyCode(code); !errors.Is(err, financialtypes.ErrInvalidCurrency) {
				t.Fatalf("ValidateISOCurrencyCode(%q) error = %v, want ErrInvalidCurrency", code, err)
			}

			line := JournalEntryLine{
				AccountID:           uuid.New(),
				TransactionCurrency: code,
				ExchangeRate:        decimal.NewFromInt(1),
			}
			if err := line.Validate(); err == nil {
				t.Fatalf("line.Validate() accepted invalid currency code: %q", code)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 3. Strict Debit XOR Credit & Zero/Negative Prevention
// -----------------------------------------------------------------------------

func TestStrictDebitCreditInvariants(t *testing.T) {
	validID := uuid.New()

	t.Run("Rejects both debit and credit zero", func(t *testing.T) {
		line := JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "0.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
		if err := line.Validate(); !errors.Is(err, financialtypes.ErrZeroAmountLine) {
			t.Fatalf("line.Validate() error = %v, want ErrZeroAmountLine", err)
		}
	})

	t.Run("Rejects both debit and credit positive (both sides)", func(t *testing.T) {
		line := JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "100.00", "USD"),
		}
		if err := line.Validate(); !errors.Is(err, ErrLineMustHaveOneSide) {
			t.Fatalf("line.Validate() error = %v, want ErrLineMustHaveOneSide", err)
		}
	})

	t.Run("Rejects negative functional debit", func(t *testing.T) {
		neg, _ := financialtypes.NewMoney(decimal.NewFromFloat(-100.00), "USD")
		zero, _ := financialtypes.Zero("USD")
		line := JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     neg,
			FunctionalCredit:    zero,
		}
		if err := line.Validate(); !errors.Is(err, financialtypes.ErrNegativeAmountProhibited) {
			t.Fatalf("line.Validate() error = %v, want ErrNegativeAmountProhibited", err)
		}
	})

	t.Run("Rejects negative transaction amount", func(t *testing.T) {
		neg, _ := financialtypes.NewMoney(decimal.NewFromFloat(-100.00), "USD")
		zero, _ := financialtypes.Zero("USD")
		line := JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   neg,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
			FunctionalCredit:    zero,
		}
		if err := line.Validate(); !errors.Is(err, financialtypes.ErrNegativeAmountProhibited) {
			t.Fatalf("line.Validate() error = %v, want ErrNegativeAmountProhibited", err)
		}
	})

	t.Run("Rejects non-positive exchange rate", func(t *testing.T) {
		zero, _ := financialtypes.Zero("USD")
		for _, rate := range []string{"0", "-1", "-0.0001"} {
			line := JournalEntryLine{
				AccountID:           validID,
				TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.RequireFromString(rate),
				FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
				FunctionalCredit:    zero,
			}
			if err := line.Validate(); !errors.Is(err, ErrInvalidExchangeRate) {
				t.Fatalf("line.Validate(rate=%s) error = %v, want ErrInvalidExchangeRate", rate, err)
			}
		}
	})

	t.Run("Rejects conversion mismatch: TransactionAmount * ExchangeRate != FunctionalAmount", func(t *testing.T) {
		zero, _ := financialtypes.Zero("SAR")
		line := JournalEntryLine{
			AccountID:           validID,
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.RequireFromString("3.75"),
			FunctionalDebit:     makeTestMoney(t, "370.00", "SAR"), // 100 * 3.75 = 375, not 370
			FunctionalCredit:    zero,
		}
		if err := line.Validate(); !errors.Is(err, financialtypes.ErrInvalidAmount) {
			t.Fatalf("line.Validate() error = %v, want ErrInvalidAmount", err)
		}
	})
}

// -----------------------------------------------------------------------------
// 4. Prevention of Silent Conversions & Concealed Errors
// -----------------------------------------------------------------------------

func TestPreventsSilentConversions(t *testing.T) {
	t.Run("Rejects missing account ID", func(t *testing.T) {
		line := JournalEntryLine{
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
		if err := line.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("line.Validate() error = %v, want ErrInvalidEntry", err)
		}
	})

	t.Run("Rejects negative ForeignAmount without silent Abs() conversion", func(t *testing.T) {
		line := JournalEntryLine{
			AccountID:           uuid.New(),
			ForeignAmount:       decimal.RequireFromString("-150.00"),
			TransactionCurrency: "USD",
			Debit:               makeTestMoney(t, "150.00", "USD"),
		}
		_, err := normalizeLineCurrencies(line)
		if !errors.Is(err, financialtypes.ErrNegativeAmountProhibited) {
			t.Fatalf("normalizeLineCurrencies() error = %v, want ErrNegativeAmountProhibited", err)
		}
	})

	t.Run("Rejects currency mismatch between legacy Currency and TransactionCurrency", func(t *testing.T) {
		line := JournalEntryLine{
			AccountID:           uuid.New(),
			Currency:            "EUR",
			TransactionCurrency: "USD",
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
		}
		_, err := normalizeLineCurrencies(line)
		if !errors.Is(err, financialtypes.ErrCurrencyMismatch) {
			t.Fatalf("normalizeLineCurrencies() error = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("Rejects non-canonical transaction currency instead of uppercasing it", func(t *testing.T) {
		for _, currency := range []string{"usd", " USD", "USD "} {
			line := JournalEntryLine{
				AccountID:           uuid.New(),
				TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
				TransactionCurrency: currency,
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
				FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
			}
			if err := line.Validate(); !errors.Is(err, financialtypes.ErrInvalidCurrency) {
				t.Errorf("line.Validate(currency=%q) error = %v, want ErrInvalidCurrency", currency, err)
			}
		}
	})

	t.Run("Rejects a rounding function that changes currency", func(t *testing.T) {
		line := JournalEntryLine{
			AccountID:           uuid.New(),
			TransactionAmount:   makeTestMoney(t, "100.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "100.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
		if err := line.ValidateWithRounding(func(m financialtypes.Money) (financialtypes.Money, error) {
			return financialtypes.NewMoney(m.Amount(), "EUR")
		}); !errors.Is(err, financialtypes.ErrCurrencyMismatch) {
			t.Fatalf("line.ValidateWithRounding() error = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("Rejects one-sided entry (all debits, no credits)", func(t *testing.T) {
		lineA := JournalEntryLine{
			AccountID:           uuid.New(),
			TransactionAmount:   makeTestMoney(t, "50.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "50.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
		lineB := JournalEntryLine{
			AccountID:           uuid.New(),
			TransactionAmount:   makeTestMoney(t, "50.00", "USD"),
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     makeTestMoney(t, "50.00", "USD"),
			FunctionalCredit:    makeTestMoney(t, "0.00", "USD"),
		}
		entry := JournalEntry{
			JournalID: uuid.New(),
			Date:      time.Now().UTC(),
			Lines:     []JournalEntryLine{lineA, lineB},
		}
		if err := entry.Validate(); !errors.Is(err, ErrMinimumLines) {
			t.Fatalf("entry.Validate() error = %v, want ErrMinimumLines", err)
		}
	})
}
