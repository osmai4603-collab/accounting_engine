# External adapter consumer (P3-05)

This is an **independent Go module** that proves an external consumer can
implement the public storage SPI and run a complete accounting scenario through
`pkg/sdk` without importing `internal/*`, `storage/memory`, `storage/postgres`,
PostgreSQL, or re-implementing any use case.

## Layout

| Path | Purpose |
| --- | --- |
| `adapter/` | Experimental in-memory storage implementing `pkg/spi/storage` ports (`Drafts`, `Entries`, `Posting`, `Sequences`, `MasterData`), the optional `ReportingStore`, and `ExchangeRateSPI`. It contains persistence mechanics only: no balance validation, approval, posting policy, or reversal construction. |
| `consumer/` | External composition root. It provisions the fixture, calls `sdk.NewApplicationService` and `sdk.NewBuilder(+ WithReporting)` with the adapter, and runs draft → validate → post → get → reverse → trial balance / general ledger. |
| `imports_test.go` | Fails on any `internal/`, `storage/memory`, `storage/postgres`, or non-`pkg/` engine import. |

The adapter clones entry headers and lines on every read/write, enforces company
scope on every port, allocates per-journal sequences deterministically, and
performs the original/reversal status transition under one lock. Reversal is
committed through the public `PostingWriter.CommitPosting` port with
`OperationReverse`; see `docs/ard/ARD-P3-05.md` for why this fallback exists.

## Local verification

```sh
cd test/external_adapter_consumer
go mod tidy
go build ./...
go test -count=1 ./...
```

or from the repository root:

```sh
./scripts/check_external_adapter_consumer.sh
```

## Module hygiene

`go.mod` contains a `replace` directive **only for local pre-release
verification** against the working tree. The published-consumer path is:

```sh
./scripts/check_external_adapter_consumer.sh --published
```

It copies this module into a temporary directory, removes the `replace`,
builds a file-based module proxy from the repository, and runs `go build ./...`
and `go test ./...` against a per-run pre-release version
(`v0.0.0-externaladapter.<unix>`). The generic
no-replace gate `./scripts/check_published_module.sh` remains in place for the
repository as a whole.
