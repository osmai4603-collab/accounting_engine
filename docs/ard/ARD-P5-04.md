# ARD-P5-04: Standard Module Profiles and Bounded Workflows

## Status

Implemented.

## Context and Problem

The IFRS/IAS modules registered fixed account-code rules through `RegisterRules`, which could create postings without a configured journal mapping and implied broader standards coverage than the implemented calculations support. Module discovery also lacked standard edition, jurisdiction, effective-date, requirement, and workflow metadata.

## Decision

1. `ModuleDescriptor` now carries a `StandardProfile` and bounded `WorkflowDescriptor` entries. Supported requirements identify implementation and test evidence; excluded requirements have stable IDs and an explicit `not_implemented` status.
2. The four standard modules describe only their implemented measurement scope. Edition, amendments, effective date, and jurisdiction remain explicitly unconfigured; the registry keeps their standard capabilities disabled until those values are pinned.
3. Workflow metadata describes inputs, validation/measurement steps, and outputs. A workflow claiming to produce journal entries must declare an application contract and the ledger-posting capability, and may reference only requirements in its profile.
4. The standard modules no longer register fixed posting rules. The legacy `RegisterRules` hook remains a no-op for compatibility; posting requires a separately configured, real journal mapping and application workflow.
5. `docs/ENGINE_COMPLIANCE_MATRIX.md` records the limited scope and evidence without claiming full IFRS/IAS compliance.

## Verification

- `pkg/spi/standards/profile_test.go` verifies metadata validation, capability disabling for unconfigured profiles, and application-contract requirements for posting workflows.
- Tests in each `modules/ifrs15`, `modules/ifrs16`, `modules/ifrs9`, and `modules/ias12` package validate profiles/workflows and ensure no fixed posting rules are registered.
- `modules/ifrs15/module_test.go:TestModuleDoesNotPostWithoutConfiguredJournalMapping` verifies that the standard module cannot create a posting from its former fixed-rule event without an explicit mapping.
- `test/acid/industry_independence_test.go:TestIndustryIndependence` verifies that unconfigured standard capabilities remain inactive and standard events cannot post through hard-coded module rules.
