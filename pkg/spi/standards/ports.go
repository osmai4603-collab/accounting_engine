package standards

import (
	"context"
	"errors"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

// EntryReader reads journal entries belonging to one company. It is the only
// ledger-read surface a standard module may depend on.
type EntryReader interface {
	GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error)
}

// BalanceReader reads deterministic account balances as of a date in a
// functional currency.
type BalanceReader interface {
	GetAccountBalance(
		ctx context.Context,
		companyID types.CompanyID,
		accountID types.AccountID,
		asOf time.Time,
		currency string,
	) (decimal.Decimal, error)
}

// MasterDataReader exposes the read-only master data a standard module needs to
// resolve companies, accounts and journals. Provisioning master data remains a
// separate host capability.
type MasterDataReader interface {
	GetCompany(ctx context.Context, companyID types.CompanyID) (masterdata.Company, error)
	GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (masterdata.Account, error)
	GetAccountByCode(ctx context.Context, companyID types.CompanyID, code string) (masterdata.Account, error)
	GetJournalByCode(ctx context.Context, companyID types.CompanyID, code string) (masterdata.Journal, error)
}

// ModulePorts is the complete, implementation-neutral dependency surface a
// StandardModule may use.
//
// It is intentionally narrower than storage.Ports: modules cannot allocate
// sequences, mutate balances, or write directly to storage. Journal drafts and
// postings must be submitted through the application layer so every accounting
// invariant, authorization check and idempotency rule keeps applying.
//
// Entries, Balance and MasterData may be nil when the host did not configure
// the corresponding capability. A module must declare which ports it needs in
// ValidateDependencies and fail explicitly when a required port is missing.
type ModulePorts interface {
	Entries() EntryReader
	Balance() BalanceReader
	MasterData() MasterDataReader
	Application() publicapplication.Service
	Posting() StandardPostingCapability
}

type modulePorts struct {
	entries     EntryReader
	balance     BalanceReader
	masterData  MasterDataReader
	application publicapplication.Service
}

// NewModulePorts builds a ModulePorts value from the supplied capabilities.
// Nil arguments mean the capability is unavailable, not silently emulated.
func NewModulePorts(
	entries EntryReader,
	balance BalanceReader,
	masterData MasterDataReader,
	application publicapplication.Service,
) ModulePorts {
	return &modulePorts{
		entries:     entries,
		balance:     balance,
		masterData:  masterData,
		application: application,
	}
}

func (p *modulePorts) Entries() EntryReader                   { return p.entries }
func (p *modulePorts) Balance() BalanceReader                 { return p.balance }
func (p *modulePorts) MasterData() MasterDataReader           { return p.masterData }
func (p *modulePorts) Application() publicapplication.Service { return p.application }
func (p *modulePorts) Posting() StandardPostingCapability     { return NewStandardPostingCapability(p) }

// ValidateStandardModulePorts reports every port that is structurally required
// to initialize any standard module. Feature-specific requirements remain the
// responsibility of each module's ValidateDependencies implementation.
func ValidateStandardModulePorts(ports ModulePorts) error {
	if ports == nil {
		return errors.New("standards: module ports are required")
	}
	var errs []error
	if ports.Application() == nil {
		errs = append(errs, errors.New("standards: application port is required"))
	}
	if ports.MasterData() == nil {
		errs = append(errs, errors.New("standards: master data reader is required"))
	}
	if ports.Entries() == nil {
		errs = append(errs, errors.New("standards: entry reader is required"))
	}
	return errors.Join(errs...)
}
