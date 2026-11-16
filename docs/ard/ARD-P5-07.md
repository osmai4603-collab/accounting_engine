# ARD-P5-07: Bounded IFRS 9 ECL Measurement

## Status

منفذة (Implemented & Verified). Bounded ECL measurement and journal posting capability validated against Golden Scenario GS-09 and approved under the independent accounting audit record.

## Scope

The capability supports:

- The existing simplified provision-matrix calculation for host-supplied trade
  receivables.
- A new in-memory general-approach ECL evaluation for host-supplied exposures.
  The caller provides a model version, named assumptions, evaluation date,
  source snapshot identifier and hash, scenario weights, 12-month and lifetime
  PD estimates, LGD, EAD, remaining life, EIR, prior stage evidence, and
  existing allowance. The source hash must be supplied as a SHA-256 digest.
- Stage 1 12-month PD and Stages 2/3 lifetime PD, scenario-weighted from
  explicitly supplied PDs. Stage 1 is capped at a 12-month discount horizon;
  Stages 2/3 use the supplied remaining life.
- EIR discounting at one expected-loss horizon using the existing
  `DiscountAtEIR` convention. The result includes the required allowance, the
  supplied existing allowance, the change between them, and per-exposure
  measurements.
- A SHA-256 input fingerprint. It covers the evaluation date, model version,
  source reference and hash, named assumptions, scenario weights and PDs,
  exposure amounts/currency, EIR/LGD/life, stage evidence, discount method,
  and existing allowance. Input ordering does not affect the fingerprint.
- The versioned evaluator requires explicit assumptions for the supported
  12-month Stage 1 horizon, 30-day Stage 2 backstop, 90-day Stage 3 backstop,
  and EIR discount convention. It rejects conflicting values rather than
  silently reporting assumptions different from those used by the calculation.

For the new general-approach evaluator, stages are derived from supplied
evidence: default or more than 90 days past due assigns Stage 3; SICR or more
than 30 days past due assigns Stage 2; otherwise Stage 1. An exposure already
in Stage 3 remains there unless the caller explicitly confirms cure. Stage
evidence, SICR, default, cure, PD, LGD, and source-snapshot integrity remain
the responsibility of the calling system and its reviewers.

## Exclusions and Posting Boundary

Business-model/SPPI classification, financial-asset measurement categories,
derecognition, hedge accounting, validated portfolio sourcing, persistence,
write-off/recovery workflows, and a complete general ECL framework are not
implemented. This capability does not validate whether a supplied estimate,
threshold, assumption, or scenario is appropriate.

## Posting Integration and Exactly-Once Handling

`ECLPostingCapability` and `PostVersionedECL` connect versioned ECL evaluations to journal entries:
1. `ECLPostingCapability` checks posting availability and resolves configured impairment loss and allowance accounts for a specified mapping ID.
2. `BuildVersionedECLEntry` generates a balanced draft for the allowance change (Dr Impairment Loss / Cr Allowance for increases; Dr Allowance / Cr Recovery for decreases).
3. The entry carries deterministic UUID (SHA-1), idempotency key derived from company, journal, model version, and input fingerprint, and an idempotency payload hash.
4. `PostVersionedECL` delegates execution to the host application's `ECLPostingApplication`, ensuring that retries with the same evaluation result are idempotent and do not duplicate postings.

## Reproducibility, Evidence, and Audit Approval

- `internal/ifrs9/versioned_test.go:TestEvaluateVersionedECLGolden` verifies the Stage 2 lifetime calculation, Stage 3 cure transition, allowance movement, and provenance.
- `TestGoldenScenario_GS09_IFRS9VersionedECLPostingAndIdempotency` verifies end-to-end measurement, account resolution, balanced entry drafting, and idempotent posting without duplicates.
- The IFRS 9 ECL capability is formally documented in `docs/golden_accounting_scenarios.md` (GS-09) and ratified under the independent accounting audit record.
