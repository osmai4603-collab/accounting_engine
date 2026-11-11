// Package masterdata owns the accounting reference data and its validation.
//
// The domain includes companies and their currency settings, chart-of-account
// records and hierarchies, journals, fiscal years and periods, accounting
// configuration, cost centers, analytical dimensions, and effective-dated
// account versions. Repository interfaces separate validation and business
// rules from storage; adapters support context-aware and company-scoped
// persistence.
//
// Service is the main lifecycle boundary. Callers register or update validated
// master data before using it in accounting operations. The service normalizes
// account codes, checks duplicate codes and parent relationships, prevents
// cycles and disallowed changes to accounts with posted entries, and ensures
// that posting targets are active leaf accounts. Company-scoped operations
// require an explicit company and a repository that implements the matching
// scoped interface. Errors from validation and repository access are returned
// rather than converted to success-shaped defaults.
//
// GetPeriodForCompanyDateContext deliberately returns a period regardless of
// close state for policy checks; posting should otherwise use an open-period
// lookup.
package masterdata
