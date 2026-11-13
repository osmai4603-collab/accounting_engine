// Package postgres exposes the PostgreSQL reference storage adapter.
//
// The supported public surface is deliberately small: construct it with New
// (which applies pending migrations), pass Ports() to the SDK, optionally
// expose reporting/governance capabilities, and use MasterDataStore() to
// provision master data. SQL handles, pgx types, and internal engine models are
// not part of the public API; everything else lives behind internal/.
package postgres

import (
	"context"

	internalpostgres "github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Adapter is a PostgreSQL-backed storage adapter.
type Adapter struct {
	driver *internalpostgres.Driver
}

// New connects to PostgreSQL, verifies connectivity, applies pending numbered
// migrations, and returns the adapter. The caller owns Close.
func New(ctx context.Context, connectionString string) (*Adapter, error) {
	driver, err := internalpostgres.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	return &Adapter{driver: driver}, nil
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

// Ping verifies database connectivity without exposing the connection pool.
func (a *Adapter) Ping(ctx context.Context) error {
	if a == nil || a.driver == nil {
		return nil
	}
	return a.driver.Ping(ctx)
}

// Close releases the database connection pool.
func (a *Adapter) Close() {
	if a != nil && a.driver != nil {
		a.driver.Close()
	}
}
