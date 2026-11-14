# ARD-P2-04: Public and Internal Entry Mapping

## Status

Implemented.

## Decision

`internal/portbridge` is the canonical public DTO/internal posting model
conversion boundary. Mapping preserves values and reports conversion errors;
it does not repair incomplete existing entries with inferred IDs, dates, or
money values.

Draft creation may generate only the entry ID and line IDs when the caller
explicitly enables `GenerateMissingIDs`. A missing created timestamp may be
set from the supplied clock in that same draft-creation mode. Company, journal,
and account IDs are required and are never generated. A missing fiscal period
is preserved as missing in draft mode for higher-level resolution; it is
rejected for existing-entry conversion, and is never generated. Existing-entry
conversion disables ID generation and requires an entry ID and created
timestamp.

Effective and posting dates map independently; a missing posting date stays
missing. Created, updated, approved, and posted timestamps map independently;
missing updated/approved/posted timestamps are not substituted from another
timestamp. CreatedBy, ApprovedBy, and PostedBy likewise retain distinct actor
semantics.

Each money value must have a currency and conversion errors are returned.
Transaction currency metadata is copied as supplied and is not inferred from
another amount. Public `ReferenceID` maps to internal `DocumentReference` and
back as an opaque string. It is not interpreted as `SourceEventID`, which has
no corresponding public DTO field.

## Verification

Focused tests cover round-tripping IDs, line dimensions, money, dates,
timestamps, actors, references, and reversal metadata; draft-only identity
generation; and rejection of missing required IDs, timestamps, clocks, and
money currencies.
