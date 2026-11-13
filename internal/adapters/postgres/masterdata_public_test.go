package postgres_test

import (
	"context"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/storage/masterdatacontract"
)

func TestPublicMasterDataContract(t *testing.T) {
	driver, err := newIsolatedPostgresDriver(t, context.Background())
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	masterdatacontract.Run(t, driver.MasterDataStore())
}

func TestPublicMasterDataUnsupportedCapability(t *testing.T) {
	var driver *postgres.Driver
	masterdatacontract.AssertUnsupported(t, driver.MasterDataStore())
}
