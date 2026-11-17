# Contract Parity Report — P3-07 / P7-01

- **Generated (UTC):** 2026-11-17T00:22:55Z
- **Engine version:** (devel)
- **Adapters:** Memory, External SPI adapter, PostgreSQL (when `POSTGRES_URL` is configured).

| Engine | Scenario | Adapter | Result | Semantic differences | Duration |
| :--- | :--- | :--- | :---: | :--- | ---: |
| (devel) | CapabilitiesMatrix | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CapabilitiesMatrix | memory | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CapabilitiesMatrix | postgres | PASS | none; adapter contract assertions passed | 991ms |
| (devel) | CoreLifecycle | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CoreLifecycle | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | CoreLifecycle | postgres | PASS | none; adapter contract assertions passed | 1.539s |
| (devel) | CrossAdapterSemanticEquivalence | all | PASS | none; normalized entry, reversal, trial-balance, and general-ledger outputs match | 1.081s |
| (devel) | EventIdempotency | external | PASS | none; adapter contract assertions passed | 2ms |
| (devel) | EventIdempotency | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | EventIdempotency | postgres | PASS | none; adapter contract assertions passed | 974ms |
| (devel) | P7-GS-01-CoreDoubleEntry | external | PASS | none; account balances and trial-balance totals match GS-01 | 729ms |
| (devel) | P7-GS-01-CoreDoubleEntry | memory | PASS | none; account balances and trial-balance totals match GS-01 | 729ms |
| (devel) | P7-GS-01-CoreDoubleEntry | postgres | PASS | none; account balances and trial-balance totals match GS-01 | 729ms |
| (devel) | ReversalLifecycle | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | ReversalLifecycle | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | ReversalLifecycle | postgres | PASS | none; adapter contract assertions passed | 1.09s |
| (devel) | SemanticErrorsAndInvariants | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | SemanticErrorsAndInvariants | memory | PASS | none; adapter contract assertions passed | 0s |
| (devel) | SemanticErrorsAndInvariants | postgres | PASS | none; adapter contract assertions passed | 960ms |

- **Totals:** 19 passed, 0 failed, 0 skipped.

## Capability differences

- external: durability=false
- external: foreign_keys=false
- external: savepoints=false
- external: serializable=true
- memory: durability=false
- memory: foreign_keys=false
- memory: savepoints=false
- memory: serializable=true
- postgres: durability=true
- postgres: foreign_keys=true
- postgres: savepoints=true
- postgres: serializable=true

## Unexpected differences

None recorded.
