package sdk

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

// buildModulePorts resolves the public dependency surface handed to standard
// modules. The modern ports path exposes storage.Ports readers directly; the
// deprecated core-store path is adapted without leaking the legacy contract to
// module code.
func (b *EngineBuilder) buildModulePorts(publicMasterData publicmasterdata.Store) standards.ModulePorts {
	var (
		entries standards.EntryReader
		balance standards.BalanceReader
		md      standards.MasterDataReader
	)

	if b.portsConfigured {
		if b.ports.Entries != nil {
			entries = b.ports.Entries
		}
		if b.ports.Balance != nil {
			balance = b.ports.Balance
		}
		if b.ports.MasterData != nil {
			md = b.ports.MasterData
		}
	} else {
		entries = legacyModuleEntryReader{store: b.coreStore}
		md = publicMasterData
		if md == nil {
			md = legacyModuleMasterDataReader{store: b.coreStore}
		}
	}

	return standards.NewModulePorts(entries, balance, md, b.useCases)
}

// legacyModuleEntryReader keeps the deprecated WithCoreStorage path working.
// The legacy contract already returns public ledger entries, so no internal
// model leaks into the standards SPI.
type legacyModuleEntryReader struct {
	store interface {
		GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error)
	}
}

func (r legacyModuleEntryReader) GetEntry(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
) (ledger.JournalEntry, error) {
	if r.store == nil {
		return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
	}
	return r.store.GetEntry(ctx, companyID, entryID)
}

// legacyModuleMasterDataReader is the last-resort adapter for deprecated stores
// that expose only the three legacy read methods. Capabilities that the legacy
// contract never had fail explicitly instead of being guessed.
type legacyModuleMasterDataReader struct {
	store interface {
		GetAccount(context.Context, types.CompanyID, types.AccountID) (ledger.Account, error)
	}
}

func (r legacyModuleMasterDataReader) GetCompany(
	context.Context,
	types.CompanyID,
) (publicmasterdata.Company, error) {
	return publicmasterdata.Company{}, types.ErrCapabilityNotImplemented
}

func (r legacyModuleMasterDataReader) GetAccount(
	ctx context.Context,
	companyID types.CompanyID,
	accountID types.AccountID,
) (publicmasterdata.Account, error) {
	if r.store == nil {
		return publicmasterdata.Account{}, types.ErrCapabilityNotImplemented
	}
	account, err := r.store.GetAccount(ctx, companyID, accountID)
	if err != nil {
		return publicmasterdata.Account{}, err
	}
	return publicmasterdata.Account{
		ID:                account.ID,
		CompanyID:         account.CompanyID,
		Code:              account.Code,
		Name:              account.Name,
		Type:              publicmasterdata.AccountType(account.Type),
		NormalBalance:     publicmasterdata.NormalBalance(account.NormalBalance),
		Currency:          account.Currency,
		ParentID:          account.ParentID,
		IsReconcilable:    account.IsReconcilable,
		IsActive:          account.IsActive,
		SubClassification: account.SubClassification,
	}, nil
}

func (r legacyModuleMasterDataReader) GetAccountByCode(
	context.Context,
	types.CompanyID,
	string,
) (publicmasterdata.Account, error) {
	return publicmasterdata.Account{}, types.ErrCapabilityNotImplemented
}

func (r legacyModuleMasterDataReader) GetJournalByCode(
	context.Context,
	types.CompanyID,
	string,
) (publicmasterdata.Journal, error) {
	return publicmasterdata.Journal{}, types.ErrCapabilityNotImplemented
}

// compile-time proof that the adapters satisfy the public standards contract.
var (
	_ standards.EntryReader      = legacyModuleEntryReader{}
	_ standards.MasterDataReader = legacyModuleMasterDataReader{}
)
