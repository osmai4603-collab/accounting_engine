package memory_test

import (
	"testing"

	"github.com/osmai4603-collab/accounting_engine/storage/masterdatacontract"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
)

func TestPublicMasterDataContract(t *testing.T) {
	masterdatacontract.Run(t, memory.New().MasterDataStore())
}

func TestPublicMasterDataUnsupportedCapability(t *testing.T) {
	var driver *memory.Driver
	masterdatacontract.AssertUnsupported(t, driver.MasterDataStore())
}
