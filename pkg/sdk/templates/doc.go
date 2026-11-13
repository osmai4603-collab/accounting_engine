// Package templates provides typed sector adapters that emit public AccountingEvent
// values and register consumer-configured PostingRule values with the SDK.
//
// The adapters intentionally ship no account codes or default debit/credit
// treatment. Consumers must provide a rule for each effect they use. The SDK
// resolves those account codes against company master data when an event is
// processed; adapters do not bypass that validation or fabricate account IDs.
package templates
