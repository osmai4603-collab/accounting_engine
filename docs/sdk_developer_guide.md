# SDK and external integration guide

This guide describes the public Go contracts in the current repository. The Go
module path is:

```text
github.com/osmai4603-collab/accounting_engine
```

Use the `pkg/sdk` package as the normal embedding entry point. The public data
contracts are in `pkg/api/v1/{types,ledger,reporting,events}`; integration and
adapter contracts are in `pkg/spi/{application,masterdata,storage,events,standards}`.
These paths are public; `internal/**` is not part of the consumer API.

## Choose the integration surface

| Surface | Intended use | Scope and limitation |
| --- | --- | --- |
| `pkg/sdk` | Embed the engine and call ledger, reporting, governance, event, or standards services | Builder validates core dependencies; optional services may be disabled and return `types.ErrCapabilityNotImplemented` |
| `pkg/spi/application` | Host-facing create, validate, post, reverse, and read use cases with an explicit principal and metadata | The host supplies verified identity; an optional `Authorizer` performs host-owned permission decisions |
| `pkg/spi/masterdata` | Provision and read company, journal, accounts, fiscal years, and periods | `Store` is a public setup contract; engine posting consumes the read-only storage port |
| `pkg/spi/storage` | Implement persistence for the application layer | Adapter owns persistence and transaction mechanics; it must meet the published port guarantees |
| `pkg/spi/events` and `pkg/api/v1/events` | Integrate canonical accounting events | Event idempotency and mapping stores are optional dependencies; without the durable stores, event ingestion is not available |
| `pkg/spi/standards` | Implement optional standard modules | Current SDK builder requires `WithCoreStorage` for standard modules; they cannot currently be initialized using only `WithStoragePorts` |
| `pkg/sdk/templates` | Reference industry adapter templates | Experimental templates; host-owned industry systems remain responsible for translating business events |

`WithCoreStorage` is retained for compatibility and is deprecated. Prefer
`WithStoragePorts` and an adapter's public `Ports()` method for new integrations.
Do not assume all listed services or capabilities are enabled merely because
the engine built successfully.

## Bootstrap and optional capabilities

`storage.Ports.Validate()` requires these five core dependencies:

| Required port | Responsibility |
| --- | --- |
| `Drafts` (`DraftStore`) | Save a validated draft |
| `Entries` (`EntryReader`) | Read an entry scoped to a company |
| `Posting` (`PostingWriter`) | Persist a prepared post or reversal |
| `Sequences` (`SequenceAllocator`) | Reserve the company/journal sequence |
| `MasterData` (`MasterDataReader`) | Read the company, accounts, journal, year, and period needed for validation |

Optional ports and builder options enable additional capabilities; omission
means the capability is unavailable, not silently emulated.

| Capability | Dependencies / discovery | Scope and limitation |
| --- | --- | --- |
| Ledger posting and reversal | The five required ports; `engine.Ledger()` | Posting validation and use cases are handled by the engine; adapter must persist commits atomically |
| Reporting | Configure `WithReporting(storage.ReportingStore)`; discover advertised engine capabilities via `engine.Standards().HasCapability(standards.CapReporting)` | Trial balance, balance sheet, income statement, and general ledger only; data ports alone do not configure the SDK reporting service |
| Governance / audit | `WithGovernance(storage.GovernanceStore)` or the applicable audit port | Exposes the SDK governance operations when configured; it is not a host identity or full approval-management system |
| Authorization | Optional `WithAuthorizer(application.Authorizer)` | Host authenticates users and decides business permissions. Without an authorizer the application layer still requires a principal and company membership on the application-service path |
| Event ingestion | Public event mapping and durable idempotency stores in `Ports.Events` | No process-local idempotency fallback; missing durable dependencies make ingestion unavailable |
| Standards modules | `WithStandard`; inspect `Diagnostics`, `Registry`, and `HasCapability` on `engine.Standards()` | Optional and currently requires the legacy core-store builder path |

The builder checks required ports and configured dependencies before it builds
the engine. Query optional capability state instead of inferring it from a
non-nil service object: disabled SDK services return a capability-not-
implemented error. The standards registry is the public capability-discovery
surface; it is not a deployment readiness or compliance signal.

## External consumer journey

The executable consumer examples live in
[`test/external_consumer`](../test/external_consumer/). They use the actual
module import path and public packages, create a company, journal, accounts,
fiscal year, and period, then post and read a balanced entry. The P7 industry
consumer test additionally exercises create-draft, validate, post, read, and
reverse through `pkg/spi/application`; it covers commercial, agriculture, and
manufacturing event-to-entry translations without adding industry concepts to
the core ledger.

Run the examples and public-boundary checks from the repository root:

```bash
cd test/external_consumer
go test -run 'TestPublicConsumerJourney|Example_publicConsumerJourney|TestNoInternalImports|TestP7_IndustryConsumersUseCanonicalAccounting|TestExternalPortsAdapterJourney'
```

The SDK consumer test exercises this post/read flow:

```go
engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(driver.Ports()),
).Build(ctx)
if err != nil {
    return err
}
defer engine.Close()

posted, err := engine.Ledger().PostEntry(ctx, draftEntry)
if err != nil {
    return err
}
_, err = engine.Ledger().GetEntry(ctx, companyID, posted.ID)
return err
```

`draftEntry` must be a complete draft: it must reference previously-created
master data and fiscal period, include valid IDs and dates, and contain balanced
functional-currency debit and credit lines. The P7 industry consumer test uses
`pkg/spi/application` to run the full create-draft, validate, post, read, and
reverse sequence. See
[`consumer_test.go`](../test/external_consumer/consumer_test.go) for the complete
setup and
[`p7_industry_consumers_test.go`](../test/external_consumer/p7_industry_consumers_test.go)
for the application-service flow. In an application integration, prefer the
use-case commands when the host needs an explicit actor, tenant, correlation
ID, source system, or authorization check.

## Host identity and execution context

The host owns authentication, user lifecycle, tenant membership, and business
authorization. It must construct a trusted `application.Principal` only after
verifying identity; never derive trust from a caller-supplied `CompanyID` or
`ActorID` alone. `Principal` carries `ActorID`, `TenantID`, permitted
`CompanyIDs`, and host permissions. `application.Context` also carries
correlation, idempotency, and source-system metadata. An `Authorizer` can be
provided via `sdk.WithAuthorizer` or the public application-service constructor.

For adapters that need explicit request scope, `storage.WithCompanyContext` and
`storage.WithActorContext` attach the active company and actor to a context.
These values do not authenticate the caller. Adapters using pooled connections
must enforce the company boundary and clear tenant-specific connection state
before reusing a connection.

The engine enforces company-scoped accounting operations and validates
company-scoped data; a storage adapter must ensure `GetEntry` does not return
another company's data and must enforce its own tenant boundary. The engine is
not an identity provider, and company scoping is not a substitute for host
authentication and authorization.

## Implement a storage adapter

Implement the interfaces in `pkg/spi/storage` and pass their instances in
`storage.Ports`. A minimal external adapter implements `DraftStore`,
`EntryReader`, `PostingWriter`, `SequenceAllocator`, and `MasterDataReader`;
`Ports.Validate()` reports missing required ports. The public
[`ports_adapter_test.go`](../test/external_consumer/ports_adapter_test.go)
shows a consumer-side implementation and compile-time interface assertions.

The adapter contract requires, among other things:

- `CommitPosting` atomically persists the posted entry and all adapter-owned
  effects for the operation, or persists none of them.
- `GetEntry` is company-scoped.
- Sequence allocation is unique per company and journal and is reserved in the
  posting transaction.
- A `UnitOfWorkRunner` commits all callback operations together and rolls them
  back if the callback fails or panics.
- Adapter transaction and tenant context does not expose a SQL connection,
  transaction handle, table name, or engine-private detail to the consumer.

Implementations must provide the guarantees, not just the interface methods.
Do not perform adapter-side posting policy or reversal construction; the
application layer supplies a validated `PostingCommit`. Consult
[`unit_of_work_and_transactions.md`](./architecture/unit_of_work_and_transactions.md)
for the transaction contract. `storage/memory` is a reference test/development
adapter and does not provide durable storage. `storage/postgres` is the
reference persistent adapter; choose and validate its deployment configuration
for the target environment.

## Errors and compatibility

Use `errors.Is` with public sentinel errors, including
`types.ErrCapabilityNotImplemented` and the error sentinels in
`pkg/spi/application`, instead of matching error strings. Application errors
carry a stable kind and a safe public message; inspect the cause only for
internal diagnostics. See [`error_taxonomy.md`](./error_taxonomy.md) for the
repository's error taxonomy.

The API/SPI inventory and generated v1 snapshot describe compatibility
boundaries. Do not depend on root-package compatibility aliases or
`pkg/spi/legacy` for new integrations. `pkg/sdk/templates` is experimental;
the stable integration path is public SDK/API/SPI.

## Deployment notes

This document covers library and adapter integration only. PostgreSQL
migrations, backup/restore, service authentication, health/readiness endpoints,
and production configuration are deployment-specific; consult the applicable
operations policy and runbooks. The existence of those documents or passing
unit/integration tests is not a production-readiness, security-certification,
or accounting-standard-compliance claim.
