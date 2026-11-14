# ARD-P3-01: Public Unit of Work and Transaction Ports

## Status

Implemented.

## Context and Problem

The accounting engine requires absolute atomicity across posting, reversal, sequence allocation, balance updates, audit logging, and idempotency records. Exposing infrastructure types like `pgx.Tx` or `sql.Tx` in public SPI contracts would tightly couple the core engine to PostgreSQL and leak implementation details. Conversely, leaving transaction boundaries undefined allows storage adapters that fail to guarantee atomicity.

## Decision

1. **Infrastructure-Neutral Contracts**:
   - Defined `UnitOfWork` and `UnitOfWorkRunner` in `pkg/spi/storage`.
   - `UnitOfWork` exposes transactional stores (`Drafts()`, `Entries()`, `Posting()`, `Sequences()`, `Audit()`, `Idempotency()`, `Balance()`) scoped to the active transaction.
   - `TransactionRunner` and `AdvancedTransactionRunner` allow executing arbitrary callbacks within transactional boundaries configured with `TxOptions` (isolation level and read-only flags) without exposing connection handles.

2. **Explicit Atomic Boundaries**:
   - Posting and Reversal boundaries require atomic execution of header insertion, line creation, sequence reservation, balance effects, audit record logging, and idempotency completion.
   - Failure at any intermediate step results in an immediate rollback of all changes.

3. **Standard Concurrency Errors and Retry Policy**:
   - Standardized `ErrSerializationFailure`, `ErrDeadlockDetected`, `ErrUniqueConstraint`, and `ErrTransactionAborted`.
   - Provided `IsRetryable` and `ExecuteWithRetry` with backoff configuration to safely handle transient concurrency conflicts without re-running non-idempotent side effects.

4. **Multi-Tenant Context and Pool Hygiene**:
   - Defined `WithCompanyContext`, `CompanyFromContext`, `WithActorContext`, and `ActorFromContext`.
   - Required storage adapters to enforce tenant isolation and ensure session parameters do not leak across pooled connections.

5. **Declared Adapter Capabilities**:
   - Created `AdapterCapabilities` struct allowing adapters (PostgreSQL, Memory, and external implementations) to explicitly declare their guarantees (durability, repeatable read, serializable, deadlock detection).

## Verification

Contract tests in `pkg/spi/storage/unit_of_work_contract_test.go` prove:

- Atomicity and commit upon success.
- Complete rollback when a callback returns an error or panics.
- Context cancellation correctly aborts transactions.
- Retry policy retries transient serialization errors and aborts on terminal errors.
- Tenant and actor metadata propagate correctly through context.
- Adapter capability declarations differentiate durable (PostgreSQL) from non-durable (Memory) implementations.
