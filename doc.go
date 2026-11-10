// Package accounting_engine composes the accounting services into one
// application-facing engine.
//
// The package is the integration boundary for storage and the internal
// accounting domains. A StorageDriver supplies the repositories and policies
// required by master data, posting, subledgers, valuation, reporting, and
// governance. NewEngine wires those repositories into services; New accepts
// the same driver and Config in either argument order for compatibility.
//
// The typical lifecycle is to construct a driver, create an engine, then use
// its service fields to configure companies and accounts, record and post
// journals, process operational subledgers, value balances, and generate
// reports. The driver owns persistence resources and must be closed by its
// creator when the engine is no longer in use. Config currently supplies the
// base currency, defaulting to USD when omitted.
//
// The package also provides convenience methods for common reports and
// dimensions, and StartDailyAuditExports to run company audit exports
// concurrently under a cancellable context. It validates company identifiers
// before starting and reports export or context errors on its returned channel.
package accounting_engine
