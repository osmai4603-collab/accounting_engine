# ARD-P3-07: Contract Parity Suite

## Status

Implemented. The shared suite lives in `test/contract_parity`; run it with
`make test-contract-parity`. It runs each shared contract against Memory, the
independent public-SPI adapter, and PostgreSQL when configured. PostgreSQL
connection failures are fatal when `POSTGRES_URL` is explicitly set.

The generated report is written to
`test/contract_parity/parity_report.md` and
`docs/reports/contract_parity_report.md`. The CI `test` job uploads the former
as the `contract-parity-report` artifact.

## Decision

The suite shares one deterministic master-data fixture and one adapter
harness. Its contracts cover draft/validate/post/read, reversal and source
linkage, event idempotency and payload conflicts, closed periods, unbalanced
entries, missing accounts, company scoping, trial balance, general ledger, and
declared adapter capabilities. A cross-adapter scenario also compares
normalized posted/reversal entries and report results, while excluding only
generated reversal identifiers and checking their relationships explicitly.

The external adapter returns the public SPI error sentinels, enforces account
references in its draft storage path, allocates reversal sequences, links
reversed entries to their reversals, and calculates the same trial-balance and
general-ledger views as the bundled adapters. The test report records observed
scenario results, skipped adapters, capability declarations, and failed
assertions rather than assuming success.

## Capability differences

- Memory and the independent external adapter are process-local and do not
  guarantee durability, database-enforced foreign keys, or savepoints.
- PostgreSQL declares durability, foreign-key enforcement, and savepoint
  support.
- RLS behavior is not claimed or tested by this suite. Company-scoped contract
  tests exercise adapter-level isolation; they do not prove deployment-level
  PostgreSQL RLS configuration.

## Finding carried forward

The bundled Memory and PostgreSQL reporting behavior excludes the source entry
after it transitions to `reversed`, while reporting the posted reversal. Their
trial balances remain mutually equal and balanced, but individual account
ending balances do not net to zero in the reversal scenario. The external
adapter now matches this existing behavior. This is a shared reporting
semantics question, not an adapter parity difference; resolve and update the
golden expectation with the reporting work in `P4-05`, without weakening this
suite's cross-adapter comparisons.

## Verification

```sh
make test-contract-parity
```

In CI, the same contracts run as part of `go test -count=1 -coverprofile=coverage.out ./...`
against the PostgreSQL service; the generated parity report is uploaded for
inspection and re-execution.
