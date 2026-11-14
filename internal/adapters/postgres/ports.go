package postgres

import (
	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Ports exposes the stable SPI implemented by the PostgreSQL adapter, including
// transaction-level atomicity, uniqueness constraints, and RLS enforcement.
func (d *Driver) Ports() storage.Ports {
	ports := portbridge.NewPorts(d)
	ports.TxRunner = d
	ports.UOWRunner = d
	ports.Events = publicevents.Stores{Idempotency: d, Mappings: d}
	return ports
}
