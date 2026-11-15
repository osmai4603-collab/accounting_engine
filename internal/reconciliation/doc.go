// Package reconciliation defines persisted allocations that reconcile account
// movements or open items.
//
// A Reconciliation groups dated items for an account and currency. Each Item
// identifies a source move line and the amount allocated to it; optional
// realized foreign-exchange movement metadata records a related FX result.
// Repository defines the create, add-item, and list operations, leaving
// storage and transaction policy to the implementation.
//
// This package contains the reconciliation data contract rather than a
// calculation engine. Callers construct and validate the intended allocation,
// persist it through a Repository, and retrieve it for later review or
// reporting. Repository errors are passed through to the caller.
//
// NewCapability requires an explicit comparison contract: distinct sources,
// a common currency and date window, one or more match keys, a tolerance, and
// exception treatment. The current Repository stores reconciliation records;
// it does not read or compare source ledgers.
package reconciliation
