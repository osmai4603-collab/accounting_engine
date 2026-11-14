# Unit of Work & Transaction Architecture

## 1. Overview and Core Philosophy

In the accounting engine, all state mutations must satisfy rigorous atomicity and consistency guarantees. A business operation (such as posting an entry, reversing an entry, or saving a draft) must never leave the underlying persistence store in a partially updated or indeterminate state.

To preserve infrastructure independence:

- **No leak of adapter-specific types**: Public SPI contracts never expose `pgx.Tx`, `sql.Tx`, database connections, or table names.
- **Explicit transaction boundary**: Use cases interact with persistence via `UnitOfWork` and `UnitOfWorkRunner` (or `TransactionRunner`).
- **Declared capabilities**: Storage adapters explicitly declare their guarantees (e.g. durability, isolation levels, deadlock detection) rather than having callers implicitly assume them.

---

## 2. Use Case Atomicity Specifications

Every engine use case defines a strict atomic boundary:

| Use Case | Atomic Operations Included | Isolation Level Required | Failure / Rollback Semantics |
| :--- | :--- | :--- | :--- |
| **Post Entry** | 1. Header & Lines insertion<br/>2. Journal sequence allocation (`NextSequence`)<br/>3. Account balance updates<br/>4. Immutable audit trail entry<br/>5. Idempotency record claim/completion | `REPEATABLE READ` or `SERIALIZABLE` | Failure at any step (e.g. sequence collision, balance constraint breach, audit failure) rolls back **all** steps. |
| **Reverse Entry** | 1. Validation & state check of original entry<br/>2. Original entry status update to `REVERSED`<br/>3. Creation & posting of reversal entry & lines<br/>4. Sequence allocation for reversal entry<br/>5. Net balance reversal<br/>6. Audit record logging actor & permanent reason<br/>7. Idempotency record completion | `REPEATABLE READ` or `SERIALIZABLE` | If reversal fails before completion, the original entry remains unreversed. Original and reversal entries are never left in opposing states. |
| **Save Draft** | 1. Validation of company, account, and fiscal period<br/>2. Draft persistence | `READ COMMITTED` | Draft rejected if company/account/period metadata is invalid. |
| **Event Ingestion** | 1. Idempotency claim with Canonical Payload Hash<br/>2. Use case execution (Draft/Post)<br/>3. Response payload & status persistence | Inherited from use case (Post/Reverse) | Re-delivery with identical key and payload returns identical result without duplicate posting. Divergent payload returns conflict error. |

---

## 3. Concurrency, Locking, and Deadlock Prevention

To prevent deadlocks in concurrent posting environments:

1. **Sequence Allocation**: Journal sequences must be ordered predictably per `(company_id, journal_id)`.
2. **Account Balances**: When updating multiple account balances within a transaction, account locks or updates must be ordered deterministically (e.g., ascending by account code or UUID) to eliminate lock inversion deadlocks.
3. **Transient Error Handling**:
   - `ErrSerializationFailure` and `ErrDeadlockDetected` are classified as retryable.
   - The engine provides `ExecuteWithRetry` with exponential backoff and jitter.
   - Non-retryable errors (such as unique constraint violations on conflicting payloads or validation failures) abort immediately.

---

## 4. Tenant / Company Context and Connection Pool Hygiene

Multi-tenant isolation is critical:

- `WithCompanyContext(ctx, companyID)` and `WithActorContext(ctx, actorID)` attach tenant and identity metadata to the transaction context.
- Storage adapters must extract these values and configure session-level tenant variables (e.g. `SET LOCAL app.current_company_id = ...` in PostgreSQL RLS).
- Adapters must ensure that session-level variables are cleanly reset or discarded upon transaction completion so that connections returned to the pool cannot leak state to subsequent requests.
