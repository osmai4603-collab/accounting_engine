# ARD-P3-02: PostgreSQL Adapter for Public SPI and Unit of Work

## Status

Implemented within the PostgreSQL adapter scope.

## Context

The public storage contracts and unit-of-work API established by P3-01 must be
implemented without exposing PostgreSQL transaction types to the SDK. Posting
needs a transaction boundary that includes sequence allocation and all
transactional writes, while tenant and actor session state must not survive
the transaction or leak through pooled connections.

## Decision

- The PostgreSQL driver implements `TransactionRunner`,
  `AdvancedTransactionRunner`, and `UnitOfWorkRunner`. The unit of work exposes
  transaction-scoped draft, entry, posting, sequence, audit, idempotency, and
  balance ports. Compile-time assertions guard these implementations.
- A unit of work requires a non-empty company context, starts a pgx transaction
  with the requested isolation/read-only options, and sets company, actor, and
  trace context with transaction-local `set_config`. Callback errors,
  cancellation, panics, and rollback-only nested work do not commit.
- Sequence allocation is an atomic journal-row update. Reversal locks the
  original entry with `FOR UPDATE`; existing PostgreSQL posting paths lock
  journal, period, and entry rows within their transaction. Database triggers
  enforce posted-entry immutability and balanced postings, while composite
  foreign keys, unique constraints, checks, and forced tenant RLS protect
  stored relations and balances.
- The PostgreSQL idempotency table uses `(company_id, idempotency_key)` as its
  key and enforces the lifecycle and entry relationship in the database. Its
  policy is included in the forced-RLS coverage test.
- The public SDK and contract packages remain infrastructure-neutral; the
  PostgreSQL driver and pgx types remain in adapter implementation packages.

## Verification Evidence

The following checks passed against the configured PostgreSQL test database:

- `go test ./internal/adapters/postgres ./pkg/spi/storage ./storage/postgres -count=1`
- Migration reversibility, dangerous-migration guard, clean lifecycle,
  baseline-clean-run, and upgrade-preserves-data tests.
- `TestEveryFinancialTableHasForcedRLS` and
  `TestPostgresRejectsCrossCompanyAccountInPosting`.
- `TestPostgresUnitOfWorkRollsBackAndScopesTransaction` verifies sequence and
  idempotency mutations roll back together on callback failure and checks the
  company/actor values inside the transaction.
- `TestPostgresUnitOfWorkDoesNotLeakCompanyOrActorContextThroughPool` reserves
  one of two pool connections so repeated unit-of-work transactions reuse the
  other connection, then verifies company and actor settings are unset after
  commit.
- Posting context-cancellation atomicity, concurrent posting integrity,
  concurrent reversal serialization, and the PostgreSQL unit-of-work tests.
- `./scripts/check_public_api_purity.sh` confirms public contract packages do
  not import PostgreSQL and public SDK/storage APIs do not expose internal
  implementation types.
- `git diff --check` on the PostgreSQL task files.

## Limits

These tests establish behavior for the configured PostgreSQL test instance;
they are not a crash-recovery or production durability certification. Memory
parity, shared adapter contract-harness execution, and cross-adapter acceptance
remain separate tasks. Event-ingestion-specific idempotency behavior is outside
P3-02.
