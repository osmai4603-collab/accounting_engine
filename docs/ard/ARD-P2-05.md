# ARD-P2-05: Reversal Semantics and Atomicity

## Status

Implemented.

## Decision

Reversal creator/poster identity is the verified actor, while `ReversalReason`
is a separate required business value. The reversal entry persists the actor,
reason, creation/posting timestamps, source/reference metadata, idempotency key,
and a canonical request fingerprint in its durable entry record. The request
reference is stored in `ReversalReference`; the original description and
document reference remain unchanged.

Reversal creation and posting are one repository operation. Memory commits the
reversal, balance effects, and original-entry link under one lock. PostgreSQL
creates the draft, its lines, posts it, and links the original inside one
database transaction. The transient draft is never visible outside that
transaction. The unique company-scoped index on the reversal idempotency key
is backed by a transaction advisory lock, while the original row lock prevents
two active reversals of one entry.

An identical key and payload returns the previously persisted reversal,
including after restart. Reusing the key with a different actor, reason, date,
reference, source, or original entry returns a conflict. A request without an
idempotency key remains protected from duplicate active reversals by the
original-entry lock and status check.

## Failure and recovery

All writes that could leave a reversal draft, post its lines, update balances,
advance the journal sequence, or link the original are inside the same commit
boundary. On a PostgreSQL error before commit the transaction rolls back; the
memory adapter validates before mutating and performs all resulting writes
under its lock. There is therefore no durable intermediate reversal state to
recover.

## Verification

Focused tests cover actor/reason separation, durable entry round-trips,
same-key replay and payload conflict, concurrent requests against one original,
and repository failure without creating a draft or changing the original.
