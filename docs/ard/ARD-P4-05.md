# ARD-P4-05: Subledgers, Control Accounts, and Reconciliation

- **Task:** `P4-05`
- **Phase:** `P4`
- **Status:** Implemented
- **Scope:** `internal/subledgers`, `internal/reconciliation`, reporting adapters, and PostgreSQL persistence

## Subledger inventory and GL contract

| Subledger | Status | GL linkage and reconciliation boundary |
| --- | --- | --- |
| Receivables | Complete within scope | A posted receivable requires a company-scoped, reconcilable asset control account. The open line is stored atomically with its posted journal entry; balances are reconciled as of a date in transaction and functional currency. |
| Payables | Complete within scope | A posted payable requires a company-scoped, reconcilable liability control account. The open line is stored atomically with its posted journal entry; balances are reconciled as of a date in transaction and functional currency. |
| Fixed assets | Partial | Asset cost, revaluation, accumulated depreciation, components, and impairment are grouped by GL account and reconciled as of a date. The current asset snapshot is not a complete historical register replay; unavailable history is an explicit error. |
| Treasury and bank items | Partial | Company-scoped bank items and linked journal entries are compared to an as-of GL balance. Bank statement parsing is available, but this is not a complete bank-feed ingestion or statement lifecycle. |
| Generic reconciliation contract | Partial | `internal/reconciliation` defines persisted account allocations; it is a data contract, not an independent reconciliation calculation engine. |
| Economic-event bridge | Complete within scope | Idempotent company-scoped events produce traceable journal drafts through atomic repository operations. Posting remains the official posting service's responsibility. |

This task does not add end-to-end sales, purchasing, or automatic balance corrections.

## Decisions and implementation

1. Control-account linkage is validated when posting open-line attachments: company, account classification, reconcilable/control flags, journal entry, partner, and amount must agree. PostgreSQL keeps the open-line attachment and journal posting within the same transaction. Asset operations and bank matching that create journal drafts use their corresponding atomic repository methods.
2. GL and subledger comparisons use the same company, account, as-of date, and functional currency. Mixed transaction currencies are reported as a sorted breakdown with functional totals; missing conversion or historical match data fails explicitly.
3. Posted and reversed journal entries both contribute to control-account balance calculation, so a source entry and its posted reversal net to zero rather than silently dropping the reversed source.
4. A non-zero daemon reconciliation report creates a durable `unmatched` exception. Exceptions transition among `unmatched`, `matching`, `disputed`, and `resolved` only through validated transitions with an actor and non-empty reason. Updates use an expected status to reject stale concurrent transitions. The exception and its append-only transition history can be read after restart.
5. Exceptions record evidence; they do not silently adjust the GL. Any accounting correction remains a draft/posted journal entry through the official posting path.
6. PostgreSQL exception tables are company-scoped, protected by RLS, and introduced in a reversible migration.

## Verification

Targeted tests cover:

- Natural-side control-account balances and source/reversal netting.
- AR/AP mismatch, mixed transaction currencies, and as-of open-line match/unmatch history.
- Fixed-asset cost/depreciation and treasury unmatched-ledger-item reconciliation.
- Daemon creation of a traceable exception for a reported variance.
- Memory persistence, actor/reason history, company isolation, and stale-transition rejection.
- PostgreSQL migration reversibility and adapter compilation.

Run:

```sh
go test ./internal/subledgers ./internal/adapters/memory ./internal/adapters/postgres
```

PostgreSQL-specific exception lifecycle behavior should also be exercised against an available PostgreSQL test database; the unit suite does not claim that a database integration run occurred.
