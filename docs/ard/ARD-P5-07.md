# ARD-P5-07: Bounded IFRS 9 ECL Measurement

## Status

Implemented as a bounded calculation capability. No independent professional
or accounting-policy review has been performed; this record is not evidence of
IFRS 9 compliance.

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

`EvaluateVersionedECL` only returns a calculation; it has no journal-entry,
posting, persistence, or application-service operation and cannot post. Any
future posting integration must require source provenance and model version,
use a real journal mapping, and follow the existing application posting
pattern. The module descriptor explicitly marks the workflow as
measurement-only. The legacy simplified receivables API may
return a balanced draft when configured with account identifiers, but does
not persist or post it; that separate API is not evidence of a configured
production journal mapping.

## Reproducibility and Evidence

`internal/ifrs9/versioned_test.go:TestEvaluateVersionedECLGolden` is a
deterministic synthetic golden scenario that checks the Stage 2 lifetime
calculation, Stage 3 cure transition to Stage 1, allowance movement, and
provenance. Adjacent tests verify ordering-independent fingerprints,
fingerprint sensitivity to model version, required provenance, and scenario
completeness. Stage-transition tests cover the 30/90-day boundaries, SICR,
default, and explicit cure. These tests are calculation fixtures, not
portfolio backtesting or evidence of predictive model performance.

The caller-supplied source hash is recorded and included in the fingerprint;
this package does not retrieve the source snapshot or verify that the supplied
hash corresponds to it. Reproduction requires the same source snapshot,
model implementation/version, explicit inputs, and calculation convention.

## Review Status and Limitations

No qualified IFRS 9 specialist or model validation review is represented as
having occurred. The implementation uses a simplified, one-horizon EIR
discount approximation and supplied risk estimates; it does not implement a
full expected-cash-shortfall schedule, independently establish significant
increase in credit risk, or test whether scenarios are reasonable and
supportable. A product owner and qualified accounting/model-risk reviewers
must assess those limitations, the applicable issued standard and jurisdiction
before production use. Module edition, amendments, effective date, and
jurisdiction remain unconfigured.
