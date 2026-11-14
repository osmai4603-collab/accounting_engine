# Accounting entry validation invariants

This document describes the entry-level validation contract in `internal/posting`.
Domain validation checks values and relationships available on the entry itself;
existence, ownership, activation, and posting eligibility of master records are
checked by the posting flow through master data and storage.

| Scope | Invariant | Enforcement |
| --- | --- | --- |
| Header | At least two lines; at least one debit and one credit; functional totals balance by one functional currency. | `JournalEntry.Validate` |
| Header | Journal ID is non-nil; at least one of `EntryDate` or legacy `Date` is set; an optional source-event ID cannot be the nil UUID. | `JournalEntry.Validate` |
| Header/company | A line with a company ID requires the header company ID and must match it. | `JournalEntry.Validate` |
| Header/classification | A non-empty classification must be one of the defined entry classifications. | `JournalEntry.Validate` |
| Line/references | Account ID is non-nil. Optional partner, cost center, project, segment, and business-unit references cannot contain the nil UUID. | `JournalEntryLine.Validate`; configured master-data/dimension policies validate supported references and company ownership during posting. |
| Line/amounts | Transaction amount is positive; exactly one functional debit or credit is positive; negative values and zero-sided lines are rejected. | `JournalEntryLine.Validate` |
| Line/currencies | Currency fields use a registered, exactly three-letter uppercase ISO code. Transaction and functional currencies must agree with their associated `Money` values. | `JournalEntryLine.Validate` |
| Line/conversion | Exchange rate is positive and transaction amount multiplied by the rate must match the functional amount under the selected rounding function. A rounding function may not change currency. | `JournalEntryLine.ValidateWithRounding` |
| Rounding precision | Banker's rounding to the currency minor unit is the default validation policy. A custom policy is explicit; residual settlement belongs to the rounding policy flow rather than an implicit currency or sign conversion. | `JournalEntryLine.ValidateWithRounding`, rounding policy |
| Posting references | With the relevant adapters configured, company is loaded and checked for activity, account posting is checked in company scope, and period is resolved for company and effective date. Journal scope is enforced by company-scoped sequence/repository operations. | Posting flow, master-data adapters, and repository; record existence cannot be established by the value-only domain validator. |

Deprecated legacy amount fields remain supported for backward compatibility.
Their normalization does not permit taking the absolute value of a negative
amount, accepting conflicting currency fields, or treating an explicit but
incomplete currency layer as a complete conversion. New entries should provide
transaction amount/currency, exchange rate, and functional debit/credit
explicitly.

The domain validator cannot determine whether an arbitrary non-nil UUID names a
real or company-owned master record. Such checks must be performed by the
posting flow against the authoritative master-data/store implementation and
configured dimension policies; UUID shape checks are not a substitute for
record validation.
