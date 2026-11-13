package storage

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type LedgerApplication interface {
	PostEntry(context.Context, ledger.JournalEntry) (ledger.JournalEntry, error)
	GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error)
	ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error)
}

type LedgerApplicationProvider interface {
	NewLedgerApplication() (LedgerApplication, error)
}
