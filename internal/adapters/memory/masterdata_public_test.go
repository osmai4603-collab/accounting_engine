package memory_test

import (
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/storage/masterdatacontract"
)

func TestPublicMasterDataContract(t *testing.T) {
	masterdatacontract.Run(t, memory.New().MasterDataStore())
}

func TestPublicMasterDataUnsupportedCapability(t *testing.T) {
	var driver *memory.Driver
	masterdatacontract.AssertUnsupported(t, driver.MasterDataStore())
}
