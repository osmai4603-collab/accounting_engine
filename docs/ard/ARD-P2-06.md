# ARD-P2-06: Application-Layer Event and Transport Ingestion

## Status

Implemented.

## Decision

SDK, HTTP, gRPC, and event ingestion use the same public application commands.
Event ingestion creates a draft through `CreateDraft` and posts it through
`Post`; retries that return a stored event result first read it through the
company-scoped application query so authorization is still enforced.

Posting requests must include a company, a journal, at least two lines, and an
effective or posting date. The posting application resolves and validates the
fiscal period from the entry date where needed. The current gRPC `PostEntry`
contract only contains a company and description, so it now fails with
`InvalidArgument` rather than attempting to post an incomplete entry. A
complete gRPC posting contract remains part of P6.

The SDK no longer falls back to an adapter-created legacy application when it
cannot build the SDK application layer. Such a fallback would bypass use-case
authorization and error semantics; the builder returns the application-layer
capability error instead.

Caller identity comes from a verified principal in context. Correlation ID,
idempotency key, and source-system metadata use a transport-neutral context
contract; HTTP headers and gRPC metadata are normalized into that contract.
Application errors map to protocol-specific client statuses, while unknown
errors return a generic internal message.

## Verification

Tests cover event validation, application command routing, trusted identity and
metadata propagation, HTTP/gRPC event equivalence, rejection of incomplete
posting requests, public error mapping, and refusal to use the legacy
application bypass.
