# Contract Parity Report — P3-07

- **Generated (UTC):** 2026-11-15T22:40:28Z
- **Adapters:** Memory, External SPI adapter, PostgreSQL (when `POSTGRES_URL` is configured).

| Scenario | Adapter | Result | Duration |
| :--- | :--- | :---: | ---: |
| CapabilitiesMatrix | external | PASS | 0s |
| CapabilitiesMatrix | memory | PASS | 0s |
| CapabilitiesMatrix | postgres | PASS | 476ms |
| CoreLifecycle | external | PASS | 0s |
| CoreLifecycle | memory | PASS | 1ms |
| CoreLifecycle | postgres | PASS | 726ms |
| CrossAdapterSemanticEquivalence | all | FAIL | 0s |
| EventIdempotency | external | PASS | 0s |
| EventIdempotency | memory | PASS | 1ms |
| EventIdempotency | postgres | PASS | 689ms |
| ReversalLifecycle | external | PASS | 0s |
| ReversalLifecycle | memory | PASS | 1ms |
| ReversalLifecycle | postgres | PASS | 482ms |
| SemanticErrorsAndInvariants | external | PASS | 0s |
| SemanticErrorsAndInvariants | memory | PASS | 0s |
| SemanticErrorsAndInvariants | postgres | PASS | 582ms |

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
