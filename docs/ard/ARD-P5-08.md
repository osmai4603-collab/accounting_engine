# ARD-P5-08: Bounded IAS 12 Tax Capability

## Status

Implemented as a bounded calculation capability. No independent professional tax or accounting review is claimed or recorded.

## Context

The existing IAS 12 package had deferred-tax and journal-draft helpers, but did not require a tax profile, source provenance for carrying/tax bases, effective-date rate selection, or quantified evidence limiting deductible differences. Its current-tax entry helper accepted an amount but did not calculate one.

## Decision

1. `TaxProfile` records company, jurisdiction, currency, version, effective date, profile source, and enacted rates with enactment/effective dates and source references.
2. `MeasureWithProfile` keeps accounting carrying amount distinct from tax base, classifies temporary differences, requires source references, and selects the latest enacted rate effective no later than the expected reversal date. A later rate is used only when enacted by the measurement date and effective by reversal.
3. Deductible temporary differences require dated, referenced probable-profit evidence. Recognition is capped at the evidence-supported deductible amount; absent or negative evidence yields no recognized DTA.
4. `MeasureCurrentTax` is only an estimate on a supplied nonnegative taxable-profit base, using the profile rate effective at period end. The caller supplies and identifies that tax base.
5. Configured entry helpers require host-supplied journal-mapping and application-configuration references and mapped account IDs before creating drafts. The helpers do not verify host configuration, authorize, persist, or post. Existing lower-level generators remain draft-generation primitives; the module installs no fixed posting rules.
6. This is not a local tax-law engine. Tax-return adjustments, losses, credits, uncertain tax positions, offsetting, presentation, and disclosure are excluded. The module descriptor keeps standard edition/amendments and reporting jurisdiction unconfigured until the host supplies them.

## Verification

Targeted tests in `internal/ias12/profile_test.go` cover effective-date rate selection, carrying/tax-base provenance, bounded DTA evidence, current-tax estimation, and rejection of entry generation without mapping/application references. Existing IAS 12 measurement and journal tests remain in place. The tests verify implemented calculations only; they are not professional tax review or evidence of full IAS 12 compliance.

## Review status and limitations

Implementation review is limited to code and automated tests. No tax professional review, jurisdiction-specific legal analysis, tax-return validation, or independent accounting sign-off was performed for this change. A qualified review and explicit jurisdiction/profile approval are required before production use.
