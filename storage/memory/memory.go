// Package memory exposes the in-memory reference storage adapter.
//
// It is intended for tests and development only and must be selected
// explicitly. The adapter is not durable and provides no cross-process
// concurrency or row-level security.
//
// The supported public surface is deliberately small: construct it, pass
// Ports() to the SDK, optionally expose reporting/governance capabilities, and
// use MasterDataStore() to prepare test data. Everything else is an
// implementation detail and lives behind internal/.
package memory

import (
	internalmemory "github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Adapter is an in-memory reference storage adapter.
type Adapter struct {
	driver *internalmemory.Driver
}

// New creates an isolated in-memory adapter.
func New() *Adapter {
	return &Adapter{driver: internalmemory.New()}
}

// Ports returns the stable storage ports consumed by the SDK.
func (a *Adapter) Ports() storage.Ports {
	if a == nil || a.driver == nil {
		return storage.Ports{}
	}
	return a.driver.Ports()
}

// MasterDataStore exposes the public master-data contract for provisioning
// companies, accounts, journals, years, and periods.
func (a *Adapter) MasterDataStore() publicmasterdata.Store {
	if a == nil || a.driver == nil {
		return nil
	}
	return a.driver.MasterDataStore()
}

// Reporting reports the optional reporting capability, or nil when the adapter
// is not configured.
func (a *Adapter) Reporting() storage.ReportingStore {
	if a == nil || a.driver == nil {
		return nil
	}
	return a.driver
}

// Governance reports the optional governance capability, or nil when the
// adapter is not configured.
func (a *Adapter) Governance() storage.GovernanceStore {
	if a == nil || a.driver == nil {
		return nil
	}
	return a.driver
}

// Close releases adapter resources. The in-memory adapter owns none.
func (a *Adapter) Close() error { return nil }
