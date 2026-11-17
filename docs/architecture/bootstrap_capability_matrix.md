# Bootstrap Capability Integrity Matrix

- **Scope:** `pkg/sdk.NewBuilder`, `WithRequiredCapabilities`, and `Build`
- **Introduced by:** `P8-05` (Bootstrap Capability Integrity)
- **Rule:** `Enabled(capability) ⟹ Available(capability)`

The builder never starts an engine in a partially configured state. A host that
declares a capability through `WithRequiredCapabilities(...)` must also supply
every dependency that capability needs. Missing dependencies are reported
before any storage adapter is mutated, any standard module is initialized, or
any scheduled job is registered.

```go
engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(ports),
    sdk.WithStandard(ifrs15.NewModule()),
    sdk.WithRequiredCapabilities(
        sdk.BootstrapReporting,
        sdk.BootstrapStandards,
    ),
).Build(ctx)
```

`WithRequiredCapabilities` is a **verification** contract, not an activation
switch. It never enables a capability by itself; it only proves that the
dependencies supplied through other options are complete.

---

## 1. Capability Dependency Matrix

| Capability constant | Value | Required dependencies | Satisfied by (legacy `WithCoreStorage`) | Satisfied by (`WithStoragePorts`) | Remediation in error |
| --- | --- | --- | --- | --- | --- |
| `BootstrapReporting` | `"reporting"` | A reporting store **or** the `Balance`, `MasterData` and `Entries` readers together | `WithReporting(store)` | `Ports.Balance` + `Ports.MasterData` + `Ports.Entries` | `WithReporting(store)` or supply the three ports |
| `BootstrapGovernance` | `"governance"` | A governance store **or** an audit writer | `WithGovernance(store)` | `Ports.Audit` | `WithGovernance(store)` or supply the `Audit` port |
| `BootstrapStandards` | `"standards"` | At least one `WithStandard(module)`; on the ports path also `Entries` and `MasterData` | `WithStandard(module)` | `WithStandard(module)` + `Ports.Entries` + `Ports.MasterData` | call `WithStandard(module)` and complete module ports |
| `BootstrapEventIngestion` | `"event_ingestion"` | Durable idempotency store and account-mapping store | core store implements `PublicEventIdempotencyStore` and `PublicEventMappingStore` | `Ports.Events.Idempotency` + `Ports.Events.Mappings` | supply `Events.Idempotency` and `Events.Mappings` |
| `BootstrapSubledgers` | `"subledgers"` | Subledger repository and control-account readers | core store implements `subledgers.Repository` | **Unavailable** — subledger ports are not part of `storage.Ports` | use `WithCoreStorage` for the subledger surface |
| `BootstrapPostingPolicy` | `"posting_policy"` | `PostingConfig.RequirePolicy = true` plus a policy provider | core store implements `policy.Repository` | posting capabilities expose `PolicyUnit` | set `WithPostingConfig(PostingConfig{RequirePolicy: true})` and provide a policy provider |

---

## 2. Validation Order

`Build` validates in this order:

1. **Option errors** — invalid option combinations accumulated while applying
   options.
2. **Storage source** — exactly one of `WithStoragePorts` or `WithCoreStorage`.
3. **Ports structural validation** — `storage.Ports.Validate()` requires
   `Drafts`, `Entries`, `Posting`, `Sequences` and `MasterData`.
4. **Registry preflight** — duplicate module names and unresolved module
   dependency graphs.
5. **Required capability validation** — every capability listed in
   `WithRequiredCapabilities`, checked against the dependency matrix above.
   All failures are collected and returned together.
6. **Application layer** — built only after preflight succeeds.
7. **Standard module dependency validation** — `ValidateDependencies(ModulePorts)`.
8. **Standard module initialization and rule registration** — with rollback of
   already-initialized modules on failure.

Because step 5 runs before step 6, a partially available engine cannot be
constructed and then fail later at runtime.

---

## 3. Error Message Contract

Every failure names the capability value, the exact missing dependency, and the
option or port that resolves it. Examples:

```text
bootstrap: capability "reporting" is required but unavailable: configure WithReporting(store) or supply Balance, MasterData and Entries ports
```

```text
bootstrap: capability "event_ingestion" requires durable event stores; missing: [Events.Idempotency Events.Mappings]
```

```text
bootstrap: capability "standards" is required but no standard module is configured: call WithStandard(module)
```

```text
bootstrap: capability "subledgers" is unavailable through WithStoragePorts: subledger stores and control-account readers are not part of storage.Ports
```

```text
bootstrap: capability "posting_policy" is required but posting policy enforcement is disabled: set WithPostingConfig(PostingConfig{RequirePolicy: true})
```

```text
bootstrap: unknown required capability "not-a-capability"
```

Unknown capability names fail immediately in `WithRequiredCapabilities`, before
`Build` runs.

---

## 4. Capabilities and Standard Modules

A standard module declares its own feature requirements through
`ValidateDependencies(standards.ModulePorts)`. The builder passes a
`ModulePorts` value whose optional readers are `nil` when the host did not
configure them; it never emulates a missing capability.

| ModulePorts accessor | Ports path | Legacy path |
| --- | --- | --- |
| `Entries()` | `Ports.Entries` | adapter over `CoreLedgerStore.GetEntry` |
| `Balance()` | `Ports.Balance` (optional) | `nil` — the legacy contract has no balance reader |
| `MasterData()` | `Ports.MasterData` | `PublicMasterDataStore` when available, otherwise a read-only adapter |
| `Application()` | public application service | public application service |

`BootstrapStandards` verifies the structural minimum (`Application`,
`MasterData`, `Entries`) in addition to module presence. Feature-specific
requirements remain the module's responsibility and fail in
`ValidateDependencies`.

---

## 5. Test Evidence

| Test | File | Proves |
| --- | --- | --- |
| Reporting fail-fast | `test/bootstrap/capability_integrity_test.go` | Missing `Balance` reader fails with `WithReporting` guidance |
| Governance fail-fast | `test/bootstrap/capability_integrity_test.go` | Missing `Audit` port fails with `WithGovernance` guidance |
| Standards fail-fast | `test/bootstrap/capability_integrity_test.go` | No module fails with `WithStandard` guidance |
| Event ingestion fail-fast | `test/bootstrap/capability_integrity_test.go` | Missing event stores reports both missing ports |
| Subledgers fail-fast | `test/bootstrap/capability_integrity_test.go` | Ports path explicitly rejects subledgers |
| Posting policy fail-fast | `test/bootstrap/capability_integrity_test.go` | Disabled enforcement reports `RequirePolicy` |
| Unknown capability | `test/bootstrap/capability_integrity_test.go` | Typos fail immediately |
| Satisfied capabilities | `test/bootstrap/capability_integrity_test.go` | A fully provisioned legacy build succeeds |
| Satisfied through ports | `test/bootstrap/capability_integrity_test.go` | Reporting, governance, standards and event ingestion are satisfied by `WithStoragePorts` |

---

## 6. Related Documents

- [Unit of Work & Transactions](./unit_of_work_and_transactions.md)
- [Compatibility Policy](../compatibility_policy.md)
- [ARD-P8-05](../ard/ARD-P8-05.md)
