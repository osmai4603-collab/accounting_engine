# Accounting Engine Compatibility Policy

- **Module Path:** `github.com/osmai4603-collab/accounting_engine`
- **Initial Version:** `v0.1.0` (Embedded SDK / Developer Preview)
- **Governing ADRs:** [ADR-0002](../docs/adr/ADR-0002-module-path-semver.md), [ADR-0001](../docs/adr/ADR-0001-public-entry-point.md), [ADR-0006](../docs/adr/ADR-0006-release-form-storage-toolchain.md).

---

## 1. Scope and Versioning Scheme

This project adheres to **Semantic Versioning 2.0.0** (`MAJOR.MINOR.PATCH`).

```text
v<MAJOR>.<MINOR>.<PATCH>
```

- **MAJOR:** Incompatible API, SPI, or behavioral breaking changes, breaking schema migrations, removal of deprecated symbols, or breaking Protobuf wire changes.
- **MINOR:** Backward-compatible new functionality (new query, use case, optional SPI method, or standard module capability) or non-breaking database schema additions.
- **PATCH:** Backward-compatible bug fixes and accounting edge-case corrections that preserve intended financial invariants.

> [!NOTE]
> Under SemVer 2.0.0 (Clause 4), major version zero (`0.y.z`) indicates an evolving API surface. The current `v0.2.0` baseline is considered production-ready for the embedded SDK and standalone service, with the previous NO-GO conditions formally cleared in [Release Readiness Report v0.2.0](./release_readiness_v0.2.0.md).

---

## 2. Public API Surface (`pkg/`)

The public Go interface consists strictly of packages located under `pkg/` and top-level storage drivers under `storage/`:

| Package Path | Stability Classification | Description |
| --- | --- | --- |
| `pkg/sdk` | Public API | Primary SDK entry point (`NewBuilder`, `Engine`, `PostingService`, `EventService`, `ReportService`). |
| `pkg/api/v1/ledger` | Public API Contract | Journal entry commands, lines, statuses, and ledger queries. |
| `pkg/api/v1/types` | Public API Contract | Canonical financial primitives (`Money`, `JournalEntryID`, `AccountID`, `CompanyID`). |
| `pkg/api/v1/reporting` | Public API Contract | Balance sheet, income statement, trial balance DTOs and filters. |
| `pkg/api/v1/events` | Public API Contract | Public event ingestion envelopes and idempotent processing types. |
| `pkg/spi/storage` | Public SPI Contract | Extension point for custom storage drivers (`StorageDriver`, `LedgerStore`, `BalanceStore`). |
| `pkg/spi/application` | Public SPI Contract | Application service ports, unit of work, and execution context. |
| `pkg/spi/standards` | Public SPI Contract | Modular standards registration (`ModuleDescriptor`, `DescribedModule`, `Registry`). |
| `storage/postgres` | Public Storage Driver | PostgreSQL driver implementation with RLS and durability guarantees. |
| `storage/memory` | Public Storage Driver | In-memory ephemeral storage driver intended for testing and development. |

### Import Boundary Rules

- **No Internal Imports:** External consumers must never import packages under `internal/**`. Enforced automatically via `scripts/check_public_api_purity.sh` and `./scripts/check_published_module.sh`.
- **No Exported Internal Types:** Exported types in `pkg/**` or `storage/**` must never leak or alias types defined in `internal/**`.
- **No Host-Side Engine SQL:** Consumers must interact exclusively via typed Go contracts and SPI methods without crafting SQL against internal table schemas.

---

## 3. Deprecation Policy

When an existing API or SPI symbol is superseded:

1. The symbol will be annotated with `// Deprecated: <Alternative and rationale>` in code comments.
2. The deprecation will be announced in `RELEASE_NOTES.md`.
3. The symbol will be retained for at least one full MINOR release cycle before removal in the next MAJOR release.

### Root Package Facade Deprecation

The root package `accounting_engine` (`github.com/osmai4603-collab/accounting_engine`) contains a legacy backward-compatibility facade (`Engine`).

- **Status:** **Deprecated** as of `v0.1.0`.
- **Replacement:** Use `pkg/sdk.NewBuilder` with explicit storage and configuration injection.
- **Planned Removal:** Removal will occur in `v1.0.0` (as decided in [ADR-0001](../docs/adr/ADR-0001-public-entry-point.md)).

### Standard Module SPI Migration (P8-01)

`pkg/spi/standards.StandardModule` no longer depends on `legacy.CoreLedgerStore`. Its
`ValidateDependencies` and `Initialize` methods now receive
`standards.ModulePorts`, a public, implementation-neutral surface that exposes
`Entries()`, `Balance()`, `MasterData()` and `Application()` only.

- **Status:** **Current** as of the `P8` line. Modules implemented against the
  old `CoreLedgerStore` signatures no longer compile and must migrate to
  `ModulePorts`.
- **Modern path:** `WithStoragePorts(ports)` + `WithStandard(module)` is
  supported. Missing optional ports are `nil`, and a module that needs one must
  fail in `ValidateDependencies`.
- **Legacy path:** `WithCoreStorage(store)` + `WithStandard(module)` keeps
  working through internal adapters in `pkg/sdk/module_ports.go`. The legacy
  path is **deprecated** and will be removed in the next MAJOR release together
  with `WithCoreStorage`; it will not receive new capabilities (for example, it
  has no `BalanceReader`).
- **Import boundary:** `pkg/spi/standards/**` must not import `internal/**` or
  the legacy storage contract. This is enforced by
  `pkg/spi/standards/imports_test.go` and `scripts/check_public_api_purity.sh`.
- **Rationale and evidence:** [ARD-P8-01](./ard/ARD-P8-01.md).

---

## 4. Storage and Schema Compatibility

### Storage Drivers

- **PostgreSQL (`storage/postgres`):** Intended for production deployments. Guarantees ACID transactions, row-level security (RLS) tenant isolation, serializable posting consistency, and tamper-evident audit chaining.
- **Memory (`storage/memory`):** Intended strictly for local testing, CI, and rapid prototyping. It does not provide durability, foreign key enforcement, or cross-restart persistence. Production configurations automatically reject memory storage unless explicitly flagged.

### Database Migrations (Expand/Contract Policy)

Database schema changes follow the zero-downtime **Expand / Contract** pattern detailed in [Migration Policy](./migration_policy.md):

1. **Expand:** Add new nullable columns or tables; existing versions continue reading without failure.
2. **Backfill:** Populate new columns asynchronously in manageable batches.
3. **Switch:** Deploy application version that writes to and reads from new schema constructs.
4. **Contract:** Remove deprecated columns/tables only after all older application instances have stopped running.

All migrations are versioned sequentially and protected by cryptographic checksum validation on startup. Modifying or re-ordering applied migrations is strictly rejected (`ErrSchemaIncompatible`).

---

## 5. Protobuf and RPC Wire Compatibility

Protobuf definitions under `proto/accounting/v1/` follow strict wire-compatibility guidelines:

- Field numbers and field names once assigned must never be reused.
- Deleted fields must be reserved using `reserved <field_number>;` and `reserved "<field_name>";`.
- Adding new optional fields is permitted in minor releases.
- Message semantics must remain backward-compatible.

---

## 6. Host Application Boundary and Security Guarantees

According to [Product Boundary](./product_boundary.md) and [ADR-0004](../docs/adr/ADR-0004-auth-and-company-context.md):

- **Authentication & User Directory:** Belongs entirely to the host application or identity provider (IdP). The engine does not maintain user tables, passwords, or session tokens.
- **Commercial Authorization & Roles:** Belongs to the host system. The engine does not enforce business roles or approval workflows.
- **Tenant Isolation:** Enforced strictly by the engine. The host system supplies an authenticated, trusted `ExecutionContext` containing `CompanyID`, `ActorID`, and `CorrelationID`. The engine enforces that all reads, writes, postings, and audit records remain within the company boundary via Postgres RLS and memory partitioning.
