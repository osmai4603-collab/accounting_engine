package memory

import (
	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Ports exposes the stable SPI implemented by the in-memory adapter.
//
// Memory does not provide durability, RLS, or cross-process concurrency. It is
// intended for tests and development only and must be selected explicitly.
func (d *Driver) Ports() storage.Ports {
	ports := portbridge.NewPorts(transactionAwareDriver{driver: d})
	ports.TxRunner = d
	ports.UOWRunner = d
	ports.Idempotency = memoryIdempotencyStore{driver: d}
	ports.Audit = memoryAuditWriter{driver: d}
	ports.Balance = memoryBalanceReader{driver: d}
	ports.Events = publicevents.Stores{Idempotency: d, Mappings: d}
	ports.Private = storage.PrivateExtensions{
		storage.PostingLockerExtensionKey: d,
	}
	return ports
}
