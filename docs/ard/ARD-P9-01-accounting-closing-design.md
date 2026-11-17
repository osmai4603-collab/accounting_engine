# ARD-P9-01: Accounting year-end closing

## Status

Implemented and verified with Memory and PostgreSQL transaction tests.

## Scope

This design covers annual closing only. Quarterly and other interim close
entries are out of scope. The close is performed after periods 1–12 are hard
closed and is recorded in adjustment period 13. Period 14 remains available
for audit adjustments under its existing policy.

## Accounting decisions

1. Only posted revenue and expense movements in the fiscal year are closed.
   Balance-sheet accounts are not closed.
2. The closing entry reverses each nominal account's year-to-date balance. A
   credit-normal balance is debited and a debit-normal balance is credited.
   The offset is the net result in the retained-earnings account: a profit
   credits retained earnings; a loss debits it.
3. `ClosingPolicyDirectToRetainedEarnings` records the offsets in one closing
   entry. `ClosingPolicyIncomeSummary` first offsets nominal accounts to an
   income-summary account, then transfers that account's balance to retained
   earnings in a second closing entry. Both entries are posted in period 13.
4. Amounts are accounted for in functional currency. Source transaction
   currency, exchange rate, and analytical dimensions must remain attached to
   generated lines whenever the source balance has a single unambiguous
   source. Currency translation differences are not booked as new FX gains or
   losses by the close; IAS 21 remeasurement remains a separate pre-close
   procedure. If existing data cannot be represented without an unsupported
   conversion, closing must fail rather than manufacture a plug.
5. Opening balances carry forward posted permanent-account balances only.
   Revenue and expense accounts begin at zero. Retained earnings includes the
   fiscal year's net result. Opening entries are posted to the following
   fiscal year's first normal period.

## Preconditions and state changes

Before creating entries, the application must verify that the fiscal year is
open, periods 1–12 are hard closed, adjustment period 13 exists and is open,
and no in-scope drafts or other close-checklist blockers remain. Both closing
entries, any opening entry, audit evidence, and the transition of period 13
and the fiscal year to hard-closed must commit together. Adapter capabilities
that cannot provide this single transaction must reject the automated close;
sequential writes with compensating state changes are not atomic.

## Reopening and audit

Reopening is a separate audited workflow. It must preserve all original
entries and use posted reversals with an actor and non-empty reason. The
close/reopen audit record identifies the company, fiscal year, generated
entry IDs, actor, reason, and timestamp. A year is not reopened if the
reversals or audited state transition cannot complete together.

## Review notes

The existing ledger stores transaction currency and analytical dimensions on
individual lines, while some reporting balance readers expose only
account-level functional-currency totals. The implementation must not claim
that source-currency/dimension fidelity is guaranteed by such aggregate data.
Full automated close requires data access and posting/state transitions that
share the adapter's transaction context.

## Implementation and verification

The SDK exposes an optional `YearEndClosingService`. With a reporting
repository and serializable unit-of-work runner, `ExecuteYearEndClosing`
generates and posts the selected closing entries, optionally posts the next
year's opening entry, updates period/year state, and appends audit evidence
within one UoW. `ReopenYearEndClosing` requires an approval reference and
reason, posts reversals for linked closing/opening entries, updates the
period/year state, and records the reopen audit in one UoW. Adapters lacking
these capabilities do not receive a sequential-write fallback.

- Generation of balanced period-13 close entries for both policies. Source
  transaction currency, rate, and dimensions are retained on each generated
  nominal and offset line.
- A separate next-year opening-entry generator that carries permanent-account
  balances by source currency, rate, and analytical dimensions. It rejects a
  grouped balance when the functional amount cannot be represented by its
  source amount and rate instead of inventing an exchange-rate plug.
- Pre-close checks for an open fiscal year, hard-closed periods 1–12, an open
  period 13, pending drafts, suspense balances, and available reconciliation
  evidence.
- The Memory adapter's transaction snapshot includes fiscal-year and period
  state, posted entries, sequences, balances, and audit records. PostgreSQL
  posting validation accepts authorized closing entries in period 13 and
  opening entries in the next year's normal period 1.

Verification covers both close policies, profit and loss, opening entries,
reopening with reversals, and injected opening-period failure with rollback on
Memory and PostgreSQL. Reporting-engine tests additionally cover source
currency/rate and analytical-dimension retention and reject unsupported
balance conversions. PostgreSQL timestamps are stored at microsecond
precision; period validation normalizes to that precision at the adapter
boundary so the generated year-end sentinel remains in its intended period.
