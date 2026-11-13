package memory

import (
	"github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
)

func (d *Driver) NewLedgerApplication() (legacy.LedgerApplication, error) {
	return sdkapplication.New(d)
}
