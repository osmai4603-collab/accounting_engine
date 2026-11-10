package accounting_engine_test

import (
	accountingengine "accounting_engine"
	"accounting_engine/storage/postgres"
)

var _ accountingengine.StorageDriver = (*postgres.Driver)(nil)
