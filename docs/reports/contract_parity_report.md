# Contract Parity Report — P3-07 / P7-01

- **Generated (UTC):** 2026-11-17T02:37:00Z
- **Engine version:** (devel)
- **Adapters:** Memory, External SPI adapter, PostgreSQL (when `POSTGRES_URL` is configured).

| Engine | Scenario | Adapter | Result | Semantic differences | Duration |
| :--- | :--- | :--- | :---: | :--- | ---: |
| (devel) | CapabilitiesMatrix | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CapabilitiesMatrix | memory | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CapabilitiesMatrix | postgres | PASS | none; adapter contract assertions passed | 844ms |
| (devel) | CoreLifecycle | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CoreLifecycle | memory | PASS | none; adapter contract assertions passed | 0s |
| (devel) | CoreLifecycle | postgres | PASS | none; adapter contract assertions passed | 713ms |
| (devel) | CrossAdapterSemanticEquivalence | all | PASS | none; normalized entry, reversal, trial-balance, and general-ledger outputs match | 757ms |
| (devel) | EventIdempotency | external | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | EventIdempotency | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | EventIdempotency | postgres | PASS | none; adapter contract assertions passed | 799ms |
| (devel) | P7-GS-01-CoreDoubleEntry | external | PASS | none; account balances and trial-balance totals match GS-01 | 494ms |
| (devel) | P7-GS-01-CoreDoubleEntry | memory | PASS | none; account balances and trial-balance totals match GS-01 | 494ms |
| (devel) | P7-GS-01-CoreDoubleEntry | postgres | PASS | none; account balances and trial-balance totals match GS-01 | 494ms |
| (devel) | ReversalLifecycle | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | ReversalLifecycle | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | ReversalLifecycle | postgres | PASS | none; adapter contract assertions passed | 853ms |
| (devel) | SemanticErrorsAndInvariants | external | PASS | none; adapter contract assertions passed | 0s |
| (devel) | SemanticErrorsAndInvariants | memory | PASS | none; adapter contract assertions passed | 0s |
| (devel) | SemanticErrorsAndInvariants | postgres | PASS | none; adapter contract assertions passed | 652ms |
| (devel) | StandardsPostingWorkflow | external | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | StandardsPostingWorkflow | memory | PASS | none; adapter contract assertions passed | 1ms |
| (devel) | StandardsPostingWorkflow | postgres | PASS | none; adapter contract assertions passed | 611ms |

- **Totals:** 22 passed, 0 failed, 0 skipped.

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
