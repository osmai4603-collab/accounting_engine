# Performance Baseline v0.2.0

- **Status:** Initial measured baseline for `P12-03`; observational, not a pass/fail latency SLO.
- **Date:** 2026-11-17
- **Benchmark package:** [`test/benchmark`](../../test/benchmark)

## Environment

| Component | Value |
|---|---|
| OS / architecture | Linux 7.2.6 x86_64 |
| CPU | Intel Core i7-4610M @ 3.00 GHz |
| Logical CPUs | 4 (`-cpu=4`) |
| Memory | 15.5 GiB |
| Go | go1.26.3 linux/amd64 |
| PostgreSQL | 18.6 (Ubuntu 18.6-1.pgdg24.04+2) |
| PostgreSQL access | Local Unix socket; each benchmark uses a fresh isolated schema |

PostgreSQL connection uses `POSTGRES_URL` when set; otherwise it uses the local `accounting_test` database. Benchmark schemas are uniquely named, migrated for the run, then dropped. No benchmark writes to the shared `public` schema.

## Workloads

All six Go benchmarks run against both the memory adapter and PostgreSQL:

1. `BenchmarkDraftCreation` — create drafts with 2, 10, and 50 journal lines.
2. `BenchmarkPostingPipeline` — create and post one balanced two-line journal entry.
3. `BenchmarkConcurrentPosting` — concurrent draft creation and posting against one company and journal. PostgreSQL uses its adapter-provided advisory posting lock.
4. `BenchmarkBalanceSheetGeneration` — generate a balanced statement for a 1,000-account chart (500 asset and 500 equity accounts) with a 1,000-line posted entry.
5. `BenchmarkCashFlowGeneration` — IAS 7 direct and indirect reports for one posted cash receipt.
6. `BenchmarkYearEndClosing` — execute the public atomic year-end close, including opening-entry generation. Fixture creation is excluded from the timed interval; it uses revenue of 2.00, expense of 1.00, a closed prior fiscal year, and an open following year.

Fixture setup is excluded from timed intervals. The benchmark operations themselves, including adapter persistence and report construction, are timed.

## Results

Command:

```sh
go test ./test/benchmark -run '^$' -bench . -benchtime=1s -count=1
```

| Benchmark | Adapter / case | ns/op | ops/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| Draft creation | Memory, 2 lines | 15,311 | 65,311 | 6,091 | 104 |
| Draft creation | Memory, 10 lines | 60,848 | 16,434 | 28,408 | 496 |
| Draft creation | Memory, 50 lines | 285,149 | 3,507 | 133,942 | 2,456 |
| Draft creation | PostgreSQL, 2 lines | 3,618,734 | 276.3 | 38,095 | 872 |
| Draft creation | PostgreSQL, 10 lines | 6,381,851 | 156.7 | 147,448 | 3,662 |
| Draft creation | PostgreSQL, 50 lines | 20,117,444 | 49.71 | 697,459 | 17,610 |
| Posting pipeline | Memory | 34,757 | 28,771 | 12,091 | 201 |
| Posting pipeline | PostgreSQL | 9,318,365 | 107.3 | 110,177 | 1,901 |
| Concurrent posting | Memory | 33,053 | 30,255 | 11,319 | 199 |
| Concurrent posting | PostgreSQL | 7,316,817 | 136.7 | 110,142 | 1,864 |
| Balance sheet, 1,000 accounts | Memory | 12,475,634 | 80.16 | 4,349,073 | 16,222 |
| Balance sheet, 1,000 accounts | PostgreSQL | 128,731,404 | 7.768 | 19,067,028 | 159,340 |
| Cash flow | Memory, indirect | 375,268 | 2,665 | 120,313 | 1,293 |
| Cash flow | Memory, direct | 24,917 | 40,133 | 14,624 | 68 |
| Cash flow | PostgreSQL, indirect | 6,966,011 | 143.6 | 231,326 | 3,578 |
| Cash flow | PostgreSQL, direct | 3,253,516 | 307.4 | 61,284 | 1,011 |
| Year-end closing | Memory | 386,931 | 2,584 | 121,898 | 992 |
| Year-end closing | PostgreSQL | 37,097,773 | 26.96 | 820,743 | 12,735 |

## Reproduction and interpretation

- Run the focused correctness and execution check with `go test ./test/benchmark -run '^$' -bench . -benchtime=100ms -count=1`.
- Run the concurrent benchmark under the race detector with `go test -race ./test/benchmark -run '^$' -bench '^BenchmarkConcurrentPosting$' -benchtime=100ms -count=1`.
- The baseline run and race-detector run both passed.
- Values are specific to this host, PostgreSQL instance, cache state, and workload size. The single baseline sample is a reference point, not a portable performance guarantee. Compare repeated runs on equivalent hardware before treating a change as a regression; use `benchstat` in a future CI comparison rather than enforcing absolute timings here.
