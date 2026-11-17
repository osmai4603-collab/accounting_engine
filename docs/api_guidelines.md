# API Guidelines

This document records conventions that apply to the public API under `pkg/` and
the public SPI under `pkg/spi/`. It complements
[`docs/compatibility_policy.md`](./compatibility_policy.md) and the
[compatibility ADRs](./adr/).

---

## 1. Account Classification

### 1.1 `AccountType` is the fundamental accounting equation role

`ledger.AccountType` (and `masterdata.AccountType`) describes only the role an
account plays in the accounting equation:

| Constant | Wire value | Status |
| --- | --- | --- |
| `AccountTypeAsset` | `"asset"` | Active |
| `AccountTypeLiability` | `"liability"` | Active |
| `AccountTypeEquity` | `"equity"` | Active |
| `AccountTypeIncome` | `"income"` | **Active (canonical)** |
| `AccountTypeExpense` | `"expense"` | Active |
| `AccountTypeRevenue` | `"income"` | **Deprecated alias** |

`AccountTypeRevenue` is retained only for backward compatibility. It carries the
same literal value as `AccountTypeIncome`, so persisted accounts, journal
entries, and reports are unaffected. New code **must** use
`AccountTypeIncome`. See
[ADR-0008](./adr/ADR-0008-account-type-revenue-alias.md).

### 1.2 Revenue vs. Other Income

`AccountType` does not distinguish operating revenue from other income. Do not
introduce a new account type for that distinction. Use one of the report-level
classifications instead:

- **Hierarchy:** place operating revenue accounts under a dedicated parent via
  `Account.ParentID` and `Account.SubClassification`.
- **Report taxonomy:** use `Account.IFRSCategory` (for example `operating` vs.
  `investing` / `financing`) and `Account.CashFlowActivity` where relevant.
- **Presentation:** income-statement and cash-flow builders decide line
  presentation from those classifications, not from `AccountType`.

This keeps a single persisted vocabulary (`"income"`) for every income account
and makes report semantics explicit and configurable per company.

### 1.3 Adding or removing account types

- Adding a value is a **MINOR** change only if existing consumers can ignore it.
- Changing an existing literal value or removing a constant is **MAJOR** and
  requires an ADR plus a migration plan.
- Aliases must be documented as deprecated in the same release that introduces
  the canonical name.

---

## 2. Deprecation Conventions

Every deprecated exported symbol must:

1. Carry a GoDoc line that starts with `Deprecated: ` and names the replacement.
2. Remain functional for at least one full MINOR release before removal in the
   next MAJOR release.
3. Be listed in `RELEASE_NOTES.md` when the deprecation ships.
4. Avoid introducing new internal call sites in the same release.

Example:

```go
// AccountTypeRevenue is a deprecated alias for AccountTypeIncome.
//
// Deprecated: Use AccountTypeIncome instead.
AccountTypeRevenue AccountType = "income"
```

---

## 3. Public Surface Boundaries

- Public API/SPI packages live under `pkg/`; storage drivers live under
  `storage/`. Nothing else is a supported import path.
- `pkg/**` must not import `internal/**` or expose internal types. Purity is
  enforced by `scripts/check_public_api_purity.sh`.
- Standard modules depend on `pkg/spi/standards.ModulePorts`, never on
  `legacy.CoreLedgerStore` or adapter internals.
- Storage capabilities are expressed through `pkg/spi/storage` ports; optional
  capabilities must fail explicitly when unavailable rather than being silently
  emulated.
