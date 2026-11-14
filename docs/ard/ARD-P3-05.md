# ARD-P3-05: External Adapter Consumer Against Public SPI

## Status

Implemented as an experimental compliance artifact. The public atomic-reversal
fallback is implemented in `internal/portbridge/repository.go`; the adapter,
composition root, tests, and consumption gate live under
`test/external_adapter_consumer`.

## Context and Problem

The repository had an in-module external consumer that implemented the storage
ports without importing `internal`. That proves the use-case layer can be built
from public ports, but it does not prove that a *separately versioned module*
can consume the accounting engine as a library:

- The build must not depend on the repository's `internal/` packages or on the
  bundled Memory/PostgreSQL adapters.
- The adapter must contain persistence mechanics only; posting policy,
  validation, balancing, reversal construction, and audit semantics stay in the
  engine.
- The SDK must be constructible from only the public ports plus optional
  reporting and currency capabilities.
- The complete flow (draft, validate, post, read, reverse, report) must work
  through public SDK entry points.
- The atomic reversal path had to work for adapters that cannot implement an
  engine-internal port.

## Decision

### 1. Independent module and composition root

`test/external_adapter_consumer` is its own Go module
(`github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer`).
Its `go.mod` carries a `replace` directive **only** for local pre-release
verification; the published path removes it.

The composition root (`consumer/scenario.go`) constructs the SDK exactly as an
external application would:

```go
ports := store.Ports() // Drafts, Entries, Posting, Sequences, MasterData
useCases, err := sdk.NewApplicationService(ports)
engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(ports),
    sdk.WithReporting(store),
).Build(ctx)
```

Reporting is supplied as a separate optional capability, not folded into
`storage.Ports`. The scenario then executes `CreateDraft`, `Validate`, `Post`,
`GetEntry`, `Reverse` through `pkg/spi/application.Service`, and `GetEntry`,
`ReverseEntry`, `GetTrialBalance`, `GetGeneralLedger` through `pkg/sdk.Engine`.

### 2. Adapter responsibilities and boundaries

The adapter implements `DraftStore`, `EntryReader`, `PostingWriter`,
`SequenceAllocator`, and `MasterDataReader`, plus the optional `ReportingStore`
and a minimal `ExchangeRateSPI`. It:

- Clones entry headers and lines at every storage boundary, so callers cannot
  mutate persisted state through returned slices or pointers.
- Rejects cross-company access on every port.
- Allocates per-`(company, journal)` sequences deterministically.
- Performs the original/reversal status transition under a single write lock so
  persistence is atomic.
- Never invents master-data identifiers; `Fixture` supplies the company,
  journal, accounts, fiscal year, and fiscal period.
- Returns an explicit typed `ErrReportNotSupported` for unimplemented reports
  instead of silently returning empty data.

The fixture provisions a company, general journal, asset (`1000`) and income
(`4000`) accounts, an open FY2026 fiscal year, an open January 2026 period, USD
as functional currency, and an exchange rate of `1`.

### 3. Public atomic-reversal fallback (only repository change)

External adapters cannot implement `portbridge.atomicReversalPostingPort`
because its method signature is expressed in `internal/posting` types. Before
this task, `Repository.CreateAndPostReversalAtomicContext` required that port
and returned `ErrAtomicReversalRequired` otherwise, which made reversal
impossible for any adapter that implements only the public SPI.

`internal/portbridge/repository.go` now:

1. Keeps the internal atomic port as the preferred path for the bundled
   adapters.
2. Falls back to `PostingWriter.CommitPosting` with `OperationReverse` for
   adapters that expose only the public ports.
3. Validates the original/reversal identity before committing and rejects a
   persisted result whose company or entry ID differs from the intended
   reversal.

The adapter is then responsible for committing the reversal and updating the
original atomically. No `pkg/spi/**`, bundled adapter, or SDK code was changed
for this task.

## Verification

Independent module:

```sh
cd test/external_adapter_consumer
gofmt -l .
go build ./...
go test -count=1 ./...
```

Both gates:

```sh
./scripts/check_external_adapter_consumer.sh
./scripts/check_external_adapter_consumer.sh --published
```

The shell gate build-tests the module, independently greps for forbidden
imports, and in `--published` mode copies the module, removes `replace`, builds
a file-based module proxy for a per-run pre-release version, runs
`go mod tidy`, `go build ./...`, and `go test ./...` against the versioned
module.

Shared suites:

```sh
go test ./internal/portbridge/...
go build ./...
```

Tests added:

- `TestExternalAdapterFullScenario` — draft, validate, post, read, reverse,
  original status transition, trial balance, and general ledger, including
  post-reversal zero ending balances.
- `TestEngineReverseEntryUsesPublicPostingWriter` — the `Engine.Ledger()`
  convenience path, with a fixture period covering the engine's current-time
  effective date.
- `TestStoreClonesEntriesAndScopesTenant` — boundary cloning and company-scope
  rejection.
- `TestStoreReportsTypedCapabilityError` — typed unsupported-report error.
- `TestNoForbiddenImports` — parses every Go file and rejects `internal/`,
  `storage/memory`, `storage/postgres`, and non-`pkg/` engine imports.
- `TestReplaceIsLocalOnly` — permits the local `replace` only in local mode and
  requires its absence in published mode.

## Findings and Limitations

- **Public atomic reversal gap:** before this task the reversal flow required
  an internal port, so public adapters could not reverse. The fallback closes
  the gap; the bundled adapters keep using their internal atomic path.
- **Idempotent-key reversal:** the public `CommitPosting` fallback does not
  carry an idempotency claim, so adapters that want idempotent reversals must
  enforce them in their own commit. The scenario deliberately uses an empty
  reversal idempotency key.
- **Engine reversal date:** `pkg/sdk`'s `ReverseEntry` convenience method
  hardcodes `EffectiveDate: time.Now().UTC()`. The deterministic January
  scenario therefore reverses through the public application service with an
  explicit effective date; a second test uses the Engine path with a fixture
  period that covers the current date.
- **Reporting scope:** the adapter implements trial balance and general ledger
  only. Balance sheet and income statement return `ErrReportNotSupported`; they
  are not claimed as supported capabilities.
- **Experimental status:** the module is a compliance harness, not a production
  adapter. It is in-memory, process-local, and unverified for durability,
  concurrency across processes, RLS, or crash recovery.
- **Unrelated suite failures:** during verification, `go test ./pkg/sdk/...`
  and `./scripts/check_api_compat.sh` reported failures from parallel work in
  `pkg/sdk` event idempotency and `pkg/spi/events`/`pkg/spi/storage`. These
  files were outside this task's edit scope and were left unchanged.
