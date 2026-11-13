package memory

import (
	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Ports exposes the stable SPI implemented by the in-memory adapter.
//
// Memory does not provide durability, RLS, or cross-process concurrency. It is
// intended for tests and development only and must be selected explicitly.
func (d *Driver) Ports() storage.Ports {
	return portbridge.NewPorts(d)
}
