package sdkapplication

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
)

// toInternalEntry is the draft-creation path: the application layer owns
// identity assignment for new drafts. Existing entries must use
// toInternalExistingEntry so missing identity is rejected.
func toInternalEntry(e ledger.JournalEntry) (posting.JournalEntry, error) {
	return portbridge.ToInternalEntry(e, portbridge.EntryOptions{
		GenerateMissingIDs: true,
		Now:                func() time.Time { return time.Now().UTC() },
	})
}

// toInternalExistingEntry converts an entry that is expected to already exist;
// synthetic journal, entry or line identifiers are rejected.
func toInternalExistingEntry(e ledger.JournalEntry) (posting.JournalEntry, error) {
	return portbridge.ToInternalEntry(e, portbridge.EntryOptions{})
}

func fromInternalEntry(e posting.JournalEntry) (ledger.JournalEntry, error) {
	return portbridge.ToPublicEntry(e)
}
