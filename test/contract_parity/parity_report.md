# Contract Parity Report — P3-07

- **Generated (UTC):** 2026-11-14T20:46:27Z
- **Adapters:** Memory, External SPI adapter, PostgreSQL (when `POSTGRES_URL` is configured).

| Scenario | Adapter | Result | Duration |
| :--- | :--- | :---: | ---: |
| CapabilitiesMatrix | external | PASS | 0s |
| CapabilitiesMatrix | memory | PASS | 0s |
| CapabilitiesMatrix | postgres | PASS | 486ms |
| CoreLifecycle | external | PASS | 0s |
| CoreLifecycle | memory | PASS | 1ms |
| CoreLifecycle | postgres | PASS | 877ms |
| CrossAdapterSemanticEquivalence | all | FAIL | 0s |
| EventIdempotency | external | PASS | 0s |
| EventIdempotency | memory | PASS | 1ms |
| EventIdempotency | postgres | PASS | 524ms |
| ReversalLifecycle | external | PASS | 0s |
| ReversalLifecycle | memory | PASS | 0s |
| ReversalLifecycle | postgres | PASS | 579ms |
| SemanticErrorsAndInvariants | external | PASS | 0s |
| SemanticErrorsAndInvariants | memory | PASS | 1ms |
| SemanticErrorsAndInvariants | postgres | PASS | 723ms |

- **Totals:** 15 passed, 1 failed, 0 skipped.

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

- CrossAdapterSemanticEquivalence / all: cross-adapter assertions failed; see the corresponding go test output
