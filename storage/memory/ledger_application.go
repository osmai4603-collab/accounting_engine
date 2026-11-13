package memory

import (
	"accounting_engine/internal/sdkapplication"
	"accounting_engine/pkg/spi/storage"
)

func (d *Driver) NewLedgerApplication() (storage.LedgerApplication, error) {
	return sdkapplication.New(d)
}
