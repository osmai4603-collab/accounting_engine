package legacyengine_test

import (
	accountingengine "github.com/osmai4603-collab/accounting_engine/internal/legacyengine"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
)

// Compile-time contract: the PostgreSQL driver must satisfy every segregated
// storage port independently as well as the compatibility aggregate. This
// guards the Phase 6 ISP split against accidentally widening or dropping a
// port from the concrete adapter.
var (
	_ accountingengine.StorageDriver         = (*postgres.Driver)(nil)
	_ accountingengine.PostingStoragePort    = (*postgres.Driver)(nil)
	_ accountingengine.ReportingStoragePort  = (*postgres.Driver)(nil)
	_ accountingengine.MasterDataStoragePort = (*postgres.Driver)(nil)
	_ accountingengine.SubledgerStoragePort  = (*postgres.Driver)(nil)
	_ accountingengine.GovernanceStoragePort = (*postgres.Driver)(nil)
)
