package portbridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

// Driver is the internal capability set that a concrete storage adapter must
// provide to expose stable storage ports. Both PostgreSQL and Memory implement
// it; the adapter remains responsible for atomicity and locking.
type Driver interface {
	SaveDraftContext(context.Context, posting.JournalEntry) error
	GetEntryByIDForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (posting.JournalEntry, error)
	PostAtomicContext(context.Context, posting.JournalEntry) error
	PostReversalAtomicContext(context.Context, posting.JournalEntry, posting.JournalEntry) (posting.JournalEntry, error)
	GetNextSequenceForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (int64, error)

	GetCompanyByIDContext(context.Context, uuid.UUID) (masterdata.Company, error)
	GetAccountByIDForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (masterdata.Account, error)
	GetAccountByCodeForCompany(context.Context, uuid.UUID, string) (masterdata.Account, error)
	HasChildrenForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	GetFiscalYearForCompany(context.Context, uuid.UUID, uuid.UUID) (masterdata.FiscalYear, error)
	GetPeriodByDateForCompany(context.Context, uuid.UUID, time.Time) (masterdata.FiscalPeriod, error)
}

// NewPorts adapts an internal driver to the stable storage ports.
func NewPorts(driver Driver) storage.Ports {
	return storage.Ports{
		Drafts:     &draftPort{driver: driver},
		Entries:    &entryPort{driver: driver},
		Posting:    &postingPort{driver: driver},
		Sequences:  &sequencePort{driver: driver},
		MasterData: &masterDataPort{driver: driver},
	}
}

type draftPort struct{ driver Driver }

func (p *draftPort) SaveDraft(ctx context.Context, entry ledger.JournalEntry) error {
	internal, err := ToInternalEntry(entry, EntryOptions{
		GenerateMissingIDs: true,
		Now:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return err
	}
	return p.driver.SaveDraftContext(ctx, internal)
}

type entryPort struct{ driver Driver }

func (p *entryPort) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	entry, err := p.driver.GetEntryByIDForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return ToPublicEntry(entry)
}

type postingPort struct{ driver Driver }

func (p *postingPort) CommitPosting(ctx context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	if commit.Entry.ID == (types.JournalEntryID{}) {
		return ledger.JournalEntry{}, ErrMissingEntryID
	}
	internal, err := ToInternalEntry(commit.Entry, EntryOptions{})
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	switch commit.Operation {
	case storage.OperationReverse:
		if commit.OriginalEntryID == nil {
			return ledger.JournalEntry{}, errors.New("portbridge: reversal requires the original entry id")
		}
		original, err := p.driver.GetEntryByIDForCompanyContext(ctx, uuid.UUID(commit.Entry.CompanyID), uuid.UUID(*commit.OriginalEntryID))
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		result, err := p.driver.PostReversalAtomicContext(ctx, original, internal)
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		return ToPublicEntry(result)
	case storage.OperationPost, "":
		if err := p.driver.PostAtomicContext(ctx, internal); err != nil {
			return ledger.JournalEntry{}, err
		}
		return commit.Entry, nil
	default:
		return ledger.JournalEntry{}, fmt.Errorf("portbridge: unsupported posting operation %q", commit.Operation)
	}
}

type sequencePort struct{ driver Driver }

func (p *sequencePort) NextSequence(ctx context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	return p.driver.GetNextSequenceForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(journalID))
}

type masterDataPort struct{ driver Driver }

func (p *masterDataPort) GetCompany(ctx context.Context, companyID types.CompanyID) (publicmasterdata.Company, error) {
	company, err := p.driver.GetCompanyByIDContext(ctx, uuid.UUID(companyID))
	if err != nil {
		return publicmasterdata.Company{}, err
	}
	return ToPublicCompany(company), nil
}

func (p *masterDataPort) GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (publicmasterdata.Account, error) {
	account, err := p.driver.GetAccountByIDForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(accountID))
	if err != nil {
		return publicmasterdata.Account{}, err
	}
	return ToPublicAccount(account), nil
}

func (p *masterDataPort) GetAccountByCode(ctx context.Context, companyID types.CompanyID, code string) (publicmasterdata.Account, error) {
	account, err := p.driver.GetAccountByCodeForCompany(ctx, uuid.UUID(companyID), code)
	if err != nil {
		return publicmasterdata.Account{}, err
	}
	return ToPublicAccount(account), nil
}

func (p *masterDataPort) HasAccountChildren(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (bool, error) {
	return p.driver.HasChildrenForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(accountID))
}

func (p *masterDataPort) GetFiscalYear(ctx context.Context, companyID types.CompanyID, fiscalYearID types.FiscalYearID) (publicmasterdata.FiscalYear, error) {
	year, err := p.driver.GetFiscalYearForCompany(ctx, uuid.UUID(companyID), uuid.UUID(fiscalYearID))
	if err != nil {
		return publicmasterdata.FiscalYear{}, err
	}
	return ToPublicFiscalYear(year), nil
}

func (p *masterDataPort) GetPeriodForDate(ctx context.Context, companyID types.CompanyID, date time.Time) (publicmasterdata.FiscalPeriod, error) {
	period, err := p.driver.GetPeriodByDateForCompany(ctx, uuid.UUID(companyID), date)
	if err != nil {
		return publicmasterdata.FiscalPeriod{}, err
	}
	return ToPublicFiscalPeriod(period), nil
}

// Optional balance adapter used when the driver supports balance reads.
type BalanceDriver interface {
	GetAccountBalanceForCompanyContext(context.Context, uuid.UUID, uuid.UUID, time.Time, string) (decimal.Decimal, error)
}
