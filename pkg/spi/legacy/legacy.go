// Package legacy contains the deprecated pre-Ports storage and application
// contracts.
//
// Deprecated: new adapters must implement pkg/spi/storage.Ports. This package
// exists so the deprecated SDK WithCoreStorage path keeps compiling until the
// next MAJOR release; it is not part of the stable storage SPI.
package legacy

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

// CoreLedgerStore is the deprecated monolithic adapter surface.
//
// Deprecated: implement pkg/spi/storage.Ports instead.
type CoreLedgerStore interface {
	SaveEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error)
	PostEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error)
	GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error)
}

// LedgerApplication is the deprecated application surface created by an
// adapter.
//
// Deprecated: the SDK builds its application layer from public ports.
type LedgerApplication interface {
	PostEntry(context.Context, ledger.JournalEntry) (ledger.JournalEntry, error)
	GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error)
	ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error)
}

// LedgerApplicationProvider lets a legacy adapter construct an application
// service.
//
// Deprecated: not part of the stable storage SPI.
type LedgerApplicationProvider interface {
	NewLedgerApplication() (LedgerApplication, error)
}
