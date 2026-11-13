package memory_test

import (
	"testing"

	"accounting_engine/storage/masterdatacontract"
	"accounting_engine/storage/memory"
)

func TestPublicMasterDataContract(t *testing.T) {
	masterdatacontract.Run(t, memory.New().MasterDataStore())
}

func TestPublicMasterDataUnsupportedCapability(t *testing.T) {
	var driver *memory.Driver
	masterdatacontract.AssertUnsupported(t, driver.MasterDataStore())
}
