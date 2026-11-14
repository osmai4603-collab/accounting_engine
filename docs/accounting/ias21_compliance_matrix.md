# IAS 21 Compliance Matrix & Multi-Currency Accounting Framework

## 1. Executive Summary & Scope

Standard: **IAS 21 — The Effects of Changes in Foreign Exchange Rates** (Official IFRS Foundation Standard).

The Accounting Engine provides a strict, mathematically verified multi-currency framework that enforces the core requirements of IAS 21 across transaction entry, period-end valuation, settlement, and financial statement translation.

---

## 2. Core Currency Semantics & Terminology (IAS 21.8)

| Concept | Definition under IAS 21 | Engine Implementation & Boundary |
| --- | --- | --- |
| **Functional Currency** | The currency of the primary economic environment in which the entity operates (IAS 21.9–14). | Defined at the **Company** level (`masterdata.Company.FunctionalCurrency`). All journal entry lines calculate and balance in this functional currency. It cannot be altered casually without a formal change of functional currency procedure. |
| **Presentation Currency** | The currency in which the entity's financial statements are presented (IAS 21.8, 38). | Defined at the **Company** level (`masterdata.Company.PresentationCurrency`) or requested for reporting. Used exclusively for financial statement translation. **Never used as an alias for functional currency.** |
| **Foreign Currency (Transaction Currency)** | A currency other than the functional currency of the entity (IAS 21.8). | Recorded on every journal entry line (`posting.JournalEntryLine.TransactionCurrency` and `TransactionAmount`). Converted to Functional Currency via `ExchangeRate`. |

---

## 3. Exchange Rate Classification & Traceability

Exchange rates are managed via `internal/valuation/rates.go` and the `RateProviderSPI`. Each exchange rate record carries:

- `FromCurrency` & `ToCurrency`: ISO 4217 3-letter codes.
- `Rate`: Decimal precision (internal scale 12).
- `EffectiveDate`: UTC calendar date.
- `Type`: Classified rate type.
- `Source`: Provider / market source (e.g. `CENTRAL_BANK`, `ECB_OFFICIAL`, `REUTERS`).

### Supported Rate Types

1. **SpotRate (IAS 21.21–22):** Used at the date of initial transaction recognition.
2. **ClosingRate (IAS 21.23(a)):** The spot exchange rate at the end of the reporting period. Used for revaluing monetary items and translating balance sheet assets/liabilities.
3. **AverageRate (IAS 21.25, 40):** A rate that approximates the exchange rates at the dates of the transactions (e.g. monthly or annual average rate). Permitted for income and expense translation when exchange rates do not fluctuate significantly.
4. **HistoricalRate (IAS 21.23(b)):** The exchange rate at the date of the historical transaction. Applied to non-monetary items carried at historical cost and equity balances (e.g., Share Capital).

### Rate Inversion & Provenance

If a direct pair quote `(From -> To)` is absent, the engine computes the inverse quote `1 / (To -> From)` rounded to internal scale, flagging `isInverted = true` in rate record lookups (`GetRateRecord`) for complete auditability.

---

## 4. Monetary vs. Non-Monetary Items (IAS 21.16, 23)

### A. Monetary Items (IAS 21.23(a))

- **Definition:** Units of currency held and assets and liabilities to be received or paid in a fixed or determinable number of units of currency (e.g., Cash and bank balances, Trade Receivables, Trade Payables, Loans payable).
- **Subsequent Measurement:** Must be retranslated using the **Closing Rate** at the end of each reporting period.
- **Accounting Treatment:** Exchange differences are recognized in **Profit or Loss (P&L)** in the period in which they arise (IAS 21.28):
  - **Asset appreciation / Liability depreciation:** Credit `Unrealized FX Gain` (P&L).
  - **Asset depreciation / Liability appreciation:** Debit `Unrealized FX Loss` (P&L).
- **Engine Enforcement:** `valuation.RevaluationEngine.Calculate()` and `GenerateRevaluationEntry()`.

### B. Non-Monetary Items (IAS 21.23(b))

- **Definition:** Items whose primary characteristic is the absence of a right to receive (or an obligation to deliver) a fixed or determinable number of units of currency (e.g., Property, Plant & Equipment, Inventories, Intangibles, Share Capital).
- **Subsequent Measurement:** Translated using the historical exchange rate at the transaction date.
- **Engine Enforcement:** `valuation.RevaluationEngine.Revalue()` explicitly skips items marked `Type: valuation.NonMonetary`, ensuring that historical cost carrying amounts in functional currency remain untouched.

---

## 5. Settlement of Monetary Items (IAS 21.28)

When a foreign currency receivable or payable is settled:

1. The difference between the settlement rate (cash received or paid) and the original spot rate (initial carrying amount) is calculated via `valuation.RealizedFXDifference()`.
2. A balanced double-entry transaction is created via `valuation.GenerateRealizedFXSettlementEntry()`:
   - **Receivable Settlement (Gain scenario):**
     - Debit Bank / Cash (at Settlement Rate)
     - Credit Trade Receivables (at Original Spot Rate)
     - Credit Realized FX Gain (P&L) (for the positive differential)
   - **Payable Settlement (Loss scenario):**
     - Debit Trade Payables (at Original Spot Rate)
     - Debit Realized FX Loss (P&L) (for the negative differential)
     - Credit Bank / Cash (at Settlement Rate)

---

## 6. Translation to Presentation Currency (IAS 21.39)

When financial statements are presented in a currency different from the entity's functional currency:

1. **Balance Sheet:**
   - Assets and liabilities are translated at the **Closing Rate** at the date of that balance sheet.
   - Equity items (such as share capital) are translated at their **Historical Rates** (`HistoricalDate`).
2. **Income Statement:**
   - Income and expenses are translated at **Average Rate** (or actual transaction spot rates).
3. **Foreign Currency Translation Reserve (FCTR / CTA):**
   - The resulting balancing difference does NOT flow into net income / P&L.
   - Under IAS 21.39(c), this difference is recognized in **Other Comprehensive Income (OCI)** and accumulated in a separate component of equity (`reporting.TranslationReserve`).
   - Verified by `internal/reporting/phase1_test.go:TestPresentationCurrencyDifferenceIsReportedInOCI` and `currency_translation.go`.

---

## 7. Compliance Matrix Table

| Standard Paragraph | Requirement | Engine Status | Implementation Module / Test |
| --- | --- | --- | --- |
| **IAS 21.8** | Definitions of Functional, Presentation, and Foreign Currencies | **Compliant** | `internal/types/currency.go`, `internal/masterdata/company.go` |
| **IAS 21.9–14** | Determination of Functional Currency | **Compliant** | Host/MasterData sets company functional currency; immutable per posting run |
| **IAS 21.21–22** | Initial recognition of foreign currency transaction at spot rate | **Compliant** | `internal/posting/`, `internal/valuation/conversion.go` |
| **IAS 21.23(a)** | Retranslation of monetary items at closing rate at reporting date | **Compliant** | `internal/valuation/revaluation.go`, `Calculate()` |
| **IAS 21.23(b)** | Non-monetary items measured at historical cost not retranslated | **Compliant** | `internal/valuation/revaluation.go:89`, `currency_ias21_test.go` |
| **IAS 21.28** | Exchange differences on monetary items recognized in P&L | **Compliant** | `internal/valuation/revaluation.go`, `GenerateRevaluationEntry()`, `GenerateRealizedFXSettlementEntry()` |
| **IAS 21.39(a)** | Translation of assets and liabilities at closing rate | **Compliant** | `internal/reporting/currency_translation.go:TranslateBalanceSheet` |
| **IAS 21.39(b)** | Translation of income and expenses at average/spot rates | **Compliant** | `internal/reporting/currency_translation.go:TranslateIncomeStatement` |
| **IAS 21.39(c)** | Exchange differences on presentation translation recognized in OCI / Equity Reserve | **Compliant** | `internal/reporting/currency_translation.go:TranslateBalanceSheet`, `reporting/phase1_test.go` |
| **IAS 21.42–43** | Translation of hyperinflationary economies (IAS 29) | **Out of Scope** | Hyperinflationary restatements require external pre-processing |
| **IAS 21.48–49** | Disposal or partial disposal of foreign operation recycling from OCI to P&L | **Out of Scope** | Requires group consolidation lifecycle (Phase 5) |
