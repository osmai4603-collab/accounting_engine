# ARD-P3-04: Durable, Source-Scoped Public Event Idempotency

## Status

Implemented.

## Context and Problem

Public event ingestion accepted an idempotency key but stored it in a
process-local `map` together with an in-flight `sync.Map`. The fallback key was
only the caller's key: it did not include the company, the upstream source
system, or a verified payload fingerprint. Consequences:

- a restart or a second instance lost the record and could post the same event
  twice;
- equal keys sent by different producers collided inside one company;
- the same key with a different payload could replay an unrelated entry because
  the stored hash was not always compared;
- a crash between the posting commit and the response could leave no durable
  evidence, and retries could re-run a transaction that had already committed.

The engine therefore needed a durable idempotency record whose identity,
payload verification, lifecycle and failure classification are explicit and
atomic, without leaking PostgreSQL types into the public contracts.

## Decision

1. **Source-scoped composite identity**:
   - The durable identity is `(CompanyID, SourceSystem, IdempotencyKey)`.
   - `SourceSystem` defaults to `publicevents.DefaultSourceSystem` (`event`) so
     pre-P3-04 keys keep their meaning.
   - `NormalizeSourceSystem`, `ScopedIdempotencyKey` and
     `CompositeIdempotencyKey` document the identity and provide a stable
     string form for logs and single-key adapters.
   - PostgreSQL replaces its `(company_id, idempotency_key)` primary key with
     `(company_id, source_system, idempotency_key)` in the reversible migration
     `202611140003_public_event_idempotency_scope.sql`; memory keys the record
     by the same tuple.

2. **Canonical lifecycle with legacy aliases**:
   - Canonical states are `started`, `completed`, `retryable_failure` and
     `terminal_failure`.
   - Legacy `processing`, `processed` and `failed` values remain readable and
     are normalized by `NormalizeStatus`; `IsStarted`, `IsCompleted`,
     `IsRetryableFailure` and `IsTerminalFailure` are the supported predicates.
   - Failure class `transient` is reclaimable by a later retry; `terminal`
     permanently rejects retries.

3. **Payload fingerprint with a version**:
   - `pkg/sdk` computes a deterministic SHA-256 fingerprint over the accounting
     inputs (event type/ID, company, ledger, effective date, amount,
     dimensions, payload, journal draft and posting rule) and records
     `PublicEventPayloadHashVersion` (`v1`) with it.
   - A replay with the same key must present the same fingerprint. A different
     fingerprint returns `ErrIdempotencyKeyConflict` mapped to the public
     `ErrConflict` taxonomy; the stored record is never overwritten.

4. **Additive optional capabilities, no breaking signatures**:
   - The stable `publicevents.IdempotencyStore` signatures are unchanged.
   - `ScopedIdempotencyStore` adds source-aware get, payload-hash-verified
     completion and classified failure; `ClassifiedIdempotencyStore` adds the
     failure class explicitly.
   - `publicevents.Stores` groups `Idempotency` and `Mappings`, and
     `storage.Ports` exposes it as the additive `Events` field. Both built-in
     adapters populate it from `Ports()`.
   - The engine prefers the richer capabilities when present and otherwise
     uses the stable methods for the default source. A missing durable store is
     a hard `ErrDependencyUnavailable` with
     `sdk.ErrIdempotencyStateUnavailable`; there is no local fallback.

5. **Atomic adapter operations**:
   - Memory performs claim, complete and fail under the driver lock. Reclaiming
     a retryable (or legacy `failed`) record keeps its original entry identity
     and only clears the failure metadata. Returned entries are cloned.
   - PostgreSQL claims with a single CTE: `SELECT ... FOR UPDATE`, an atomic
     update when the row is retryable and the payload matches, an
     `INSERT ... ON CONFLICT DO NOTHING` for new identities, and a bounded retry
     when a concurrent insert wins the race. Completion verifies the optional
     payload hash and entry identity, and only treats the same entry as an
     idempotent completion.
   - `Fail` writes the canonical classified state through
     `FailPublicEventIngestionClassified`; the base method keeps the legacy
     `failed` value for old callers.

6. **Bounded waiting and committed-state verification**:
   - Default wait policy is 2s with a 25ms poll interval; it is configurable
     with the additive `WithEventIdempotencyWaitPolicy(timeout, interval)`.
   - An in-flight record is polled until it completes, fails, or the budget
     expires, after which the caller gets `ErrIdempotencyInProgress` or may
     take over recovery only when a concrete entry identity exists.
   - Retries only reclaim transient failures. Before retrying, the SDK checks
     whether the entry was already committed; a commit whose idempotency
     completion failed is recovered and completed instead of being posted
     again.
   - `WithCoreStorage` remains a legacy path: it exposes only the stable
     idempotency surface so existing records keep their
     `processing`/`processed`/`failed` vocabulary.

## Limitations

- Memory storage is durable only for the lifetime of the process. It is the
  test/development adapter; PostgreSQL provides cross-process durability.
- Claiming the key, committing the accounting entry and completing the
  idempotency record cannot share one database transaction through the frozen
  public `PostingWriter` port. The engine instead reserves the entry identity
  before posting and verifies committed state before any retry, so a crash at
  any point leaves a `started` record that the next attempt completes without
  creating a second entry.
- Rolling back the schema migration restores the narrower
  `(company_id, idempotency_key)` primary key. If several source systems share
  an equal key for one company, the operator must resolve those rows first.

## Verification

- `pkg/sdk/event_idempotency_test.go` covers concurrent same-key callers,
  different-payload conflicts, restart replay, explicit failure without a
  durable store, bounded waiting followed by `ErrIdempotencyInProgress`,
  terminal-failure retry rejection, transient failure before commit, and
  failure after commit but before the response.
- `internal/adapters/memory/public_events_test.go` covers transitions,
  source-system independence, payload mismatch, retryable reclaim, concurrent
  claims and cloned snapshots.
- `internal/adapters/postgres/public_events_test.go` exercises the same
  transitions, terminal non-reclaim, concurrent claims and missing records on
  an isolated schema.
- `pkg/sdk/event_recovery_test.go` continues to verify legacy recovery,
  classified failures and conflicting completion.
- The API compatibility snapshot is refreshed additively for the new
  `pkg/spi/events` exports and `storage.Ports.Events`.
