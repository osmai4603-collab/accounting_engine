// Package accounting_engine composes the accounting services into one
// application-facing engine.
//
// The package is the integration boundary for storage and the internal
// accounting domains. StorageDriver provides a convenient unified adapter,
// while StoragePorts and NewWithPorts allow each domain to use an independent
// implementation. New accepts a StorageDriver and Config in that order.
//
// The typical lifecycle is to construct a driver, create an engine, then use
// its service fields to configure companies and accounts, record and post
// journals, process operational subledgers, value balances, and generate
// reports. The driver owns persistence resources and must be closed by its
// creator when the engine is no longer in use. Company functional currency is
// authoritative; Config does not override company currency or report currency.
//
// The package also provides convenience methods for common reports and
// dimensions, and StartDailyAuditExports to run company audit exports
// concurrently under a cancellable context. It validates company identifiers
// before starting and reports export or context errors on its returned channel.
package accounting_engine
