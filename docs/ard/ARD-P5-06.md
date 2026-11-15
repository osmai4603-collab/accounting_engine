# ARD-P5-06: Bounded IFRS 16 lease workflow

## Decision and scope

This implementation provides calculations for a lessee from host-supplied
contract terms. It is not a complete IFRS 16 implementation and does not
identify leases, assess contract completeness, or determine the accounting
conclusions that must be supplied by an accounting professional. Lessor
accounting is outside scope.

Supported calculations are initial lease-liability and right-of-use (ROU)
asset measurement, the dated liability and ROU depreciation roll-forward, and
remeasurement for a host-classified non-separate modification. There is also a
bounded early-termination calculation. The implementation does not decide
whether an event is a separate modification, qualifies for an exemption, or
requires reassessment.

## Calculation policy

* The supplied annual effective discount rate is compounded over each actual
  elapsed-day interval on an Actual/365 basis. For each payment interval,
  growth is `(1 + annual rate)^(elapsed days / 365)`. Initial liability is the
  sum of contractual payments discounted by the cumulative interval factors.
* Payment dates must be strictly increasing and after the commencement or
  remeasurement date. Payments must be positive and in one supported currency.
* Initial ROU equals initial liability plus supplied initial direct costs and
  prepayments less supplied lease incentives. Restoration obligations,
  non-lease components, and other adjustments are not inferred.
* Subsequent interest is calculated from each opening liability and interval
  growth factor; payment reduces liability by payment less interest. ROU
  depreciation is straight-line over the supplied number of schedule periods;
  zero or a value above the schedule length means the full schedule length.
  The final depreciation amount absorbs rounding so accumulated depreciation
  equals initial ROU. This period-count policy is an explicit simplification,
  not a substitute for determining the appropriate depreciation period under
  the applicable facts and standard.
* Internal calculations retain 12 decimal places where the decimal library
  operation specifies a scale. Present values, interest, principal and
  depreciation are rounded to the engine's four-place financial scale. Dates
  are normalized to UTC; intervals use elapsed durations, not calendar-month
  conventions. Currency validation uses the engine's supported currency
  catalogue; no FX translation is performed.
* For the supported non-separate modification calculation, the host supplies
  remaining payments, liability and ROU carrying amounts, and a revised
  discount rate. The implementation discounts those payments at that rate,
  adjusts the ROU for the liability movement, and recognizes a gain for a
  liability decrease in excess of the supplied ROU carrying amount. It does
  not determine the appropriate rate or event classification.

## Exemptions and unsupported cases

No short-term or low-value exemption is implemented or automatically applied.
Options, lease-term judgments, variable payments, index/rate reassessments,
separate modifications, impairment, transition accounting, foreign-currency
accounting, and a complete termination lifecycle are unsupported. Residual
value guarantees are not modeled separately; any amount included in cash flows
must be supplied by the caller. Lessor classification and measurement,
sale-and-leaseback, subleases, portfolio expedients, and legal/local
jurisdictional variations are out of scope.

The module descriptor deliberately leaves the applicable edition,
amendments, effective date and jurisdiction to the host. This record is an
implementation boundary and has not been independently reviewed or approved
by an accounting specialist.

## Journal drafts, mapping, and retries

Journal-draft construction is available only through `EntryBuilder`, which
requires a host-provided `LeasePostingCapability`, a positive ledger-posting
capability check, and a non-empty mapping identifier resolved by that
capability for the target company. A stable lease reference is mandatory.
Entries carry deterministic IDs, idempotency keys and payload hashes, plus
IFRS 16 source, lease reference and operation reference, so a retry for the
same reference, company, journal and operation reproduces the same identity.
Different mapped accounts change the payload hash.

The IFRS 16 module does not itself persist contracts, commit entries, provide
an outbox, or invoke the application posting service. The host must persist
and enforce the supplied idempotency identity transactionally; this module
alone cannot guarantee durable exactly-once posting. No production mapping or
application capability is bundled or implied.

## Evidence and review limitation

Focused Go tests cover the existing multi-currency/date golden schedule and
modification figures, balanced mapped drafts, missing capability/mapping
rejection, and retry-stable identity. These are implementation tests, not an
independent accounting opinion. The golden figures are fixed regression
benchmarks, not externally verified exemplars. Before production use, an
accounting specialist must review the applicable edition and jurisdiction,
lease-term and rate judgments, depreciation policy, exemption eligibility,
remeasurement classification, and all entity-specific mappings.
