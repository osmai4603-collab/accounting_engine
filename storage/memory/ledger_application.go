package memory

import (
	"github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func (d *Driver) NewLedgerApplication() (storage.LedgerApplication, error) {
	return sdkapplication.New(d)
}
