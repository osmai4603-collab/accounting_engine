# ARD-P2-02: Unified Application Use Cases

## Status

Implemented within the P2-02 scope.

## Decision

Use `internal/sdkapplication` as the single implementation of the public
`pkg/spi/application.Service` contract. The SDK composition boundary builds
that service from public storage ports or the deprecated core-store adapter.
The public SPI remains independent of `internal` packages.

SDK ledger calls and event posting are adapted into the same CreateDraft,
Validate, Post, Reverse, and GetEntry use cases. HTTP and gRPC continue to
call the SDK ledger/events interfaces and therefore share those use cases.
The legacy IFRS CLI now obtains its posting engine through
`internal/legacyengine` rather than constructing `internal/posting.Engine`
itself.

The application service requires a non-empty actor and explicit company
membership, then invokes the optional host `Authorizer`. Hosts can attach a
verified principal to a request context with `application.WithPrincipal`.
Legacy SDK methods retain their old signatures; when an Authorizer is
configured, those methods require a principal in context instead of treating
request data as verified identity.

## Implementation

- `internal/sdkapplication/public_service.go` implements the five public
  use cases and performs authorization before application operations.
- `internal/sdkapplication/service.go` validates existing drafts using the
  posting domain validation instead of treating a successful read as
  validation.
- `pkg/sdk/builder.go` installs the same service for SDK ledger and event
  paths for both public ports and compatible core storage.
- `pkg/sdk/application.go` exposes a public factory that accepts only public
  storage ports and contracts.
- `cmd/agy/main.go` delegates engine construction to the accounting
  composition root.
- `scripts/check_application_layer.sh` rejects direct imports of
  `internal/posting` from `cmd` and `pkg/sdk`; CI runs this gate.

## Execution paths

```text
HTTP / gRPC / SDK -> pkg/sdk LedgerService -> pkg/spi/application.Service
  -> internal/sdkapplication -> internal/posting -> storage port adapter

Accounting event -> pkg/sdk EventIngestionService -> the same
pkg/spi/application.Service -> internal/sdkapplication -> posting/storage
```

## Verification

| Acceptance evidence | Result |
|---|---|
| SDK, HTTP/gRPC ledger endpoints, and event posting use the application service | Verified through the SDK-backed transport paths and SDK event tests |
| Caller identity, company membership, and optional authorization hook are enforced | Covered by application service tests |
| Command packages and SDK cannot directly import `internal/posting` | Architecture gate passes and is wired into CI |
| Public application SPI does not import or expose internal types | `scripts/check_public_api_purity.sh` passes |
| Public mapping errors and posting failures are returned to callers | Covered by conversion, SDK, and application tests |

## Deliberate follow-up boundaries

P2-03 remains responsible for a single Posting Engine factory and complete
policy, approval, dimensions, and maker-checker configuration. P2-06 remains
responsible for moving event mapping and durable idempotency fully behind the
same application boundary. This record does not claim either task is complete.
