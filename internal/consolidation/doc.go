// Package consolidation combines company trial balances into group results.
//
// Group describes the reporting currency and the entities included in a
// consolidation. Consolidate validates the group and its source trial balances,
// translates company balances using the supplied RateProvider, and returns
// consolidated account balances together with translation and balancing
// information. The calculation is in-memory: callers load the required
// company data and applicable rates, call the engine, and decide how to
// present or persist the result.
//
// Ownership chains support partial or full goodwill, and foreign-currency
// translation differences are routed to the configured OCI translation
// reserve. Rate lookup and validation errors are returned to the caller; this
// package does not hide missing or invalid exchange-rate data behind defaults.
package consolidation
