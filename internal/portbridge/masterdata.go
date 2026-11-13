package portbridge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// MasterDataAdapter exposes the read-only MasterDataReader port to the
// internal master-data service. Provisioning operations are not part of the
// posting dependency and return ErrCapabilityNotImplemented instead of
// pretending to succeed.
type MasterDataAdapter struct {
	Ports storage.Ports
}

var _ masterdata.ContextStorageRepository = (*MasterDataAdapter)(nil)

func NewMasterDataService(ports storage.Ports) *masterdata.Service {
	return masterdata.NewService(masterdata.NewRepositoryAdapter(&MasterDataAdapter{Ports: ports}))
}

func (a *MasterDataAdapter) reader() (storage.MasterDataReader, error) {
	if a.Ports.MasterData == nil {
		return nil, errors.New("portbridge: master data reader is not configured")
	}
	return a.Ports.MasterData, nil
}

func unsupported() error { return types.ErrCapabilityNotImplemented }

func (a *MasterDataAdapter) CreateCompany(context.Context, masterdata.Company) error {
	return unsupported()
}

func (a *MasterDataAdapter) GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (masterdata.Company, error) {
	reader, err := a.reader()
	if err != nil {
		return masterdata.Company{}, err
	}
	company, err := reader.GetCompany(ctx, types.CompanyID(companyID))
	if err != nil {
		return masterdata.Company{}, err
	}
	return ToInternalCompany(company), nil
}

func (a *MasterDataAdapter) CreateAccount(context.Context, masterdata.Account) error {
	return unsupported()
}

func (a *MasterDataAdapter) GetAccountByID(context.Context, uuid.UUID) (masterdata.Account, error) {
	return masterdata.Account{}, masterdata.ErrCompanyScopeRequired
}

func (a *MasterDataAdapter) GetAccountByCode(context.Context, string) (masterdata.Account, error) {
	return masterdata.Account{}, masterdata.ErrCompanyScopeRequired
}

func (a *MasterDataAdapter) ListAccounts(context.Context) ([]masterdata.Account, error) {
	return nil, unsupported()
}

func (a *MasterDataAdapter) HasChildren(context.Context, uuid.UUID) (bool, error) {
	return false, masterdata.ErrCompanyScopeRequired
}

func (a *MasterDataAdapter) CreateFiscalYear(context.Context, masterdata.FiscalYear) error {
	return unsupported()
}

func (a *MasterDataAdapter) GetFiscalYear(context.Context, uuid.UUID) (masterdata.FiscalYear, error) {
	return masterdata.FiscalYear{}, masterdata.ErrCompanyScopeRequired
}

func (a *MasterDataAdapter) UpdateFiscalYearState(context.Context, uuid.UUID, uuid.UUID, masterdata.FiscalYearState) error {
	return unsupported()
}

func (a *MasterDataAdapter) GetPeriodByDate(context.Context, time.Time) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, masterdata.ErrCompanyScopeRequired
}

func (a *MasterDataAdapter) UpdatePeriodState(context.Context, uuid.UUID, uuid.UUID, masterdata.PeriodState) error {
	return unsupported()
}

func (a *MasterDataAdapter) GetNextSequenceWithLock(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	if a.Ports.Sequences == nil {
		return 0, unsupported()
	}
	return a.Ports.Sequences.NextSequence(ctx, types.CompanyID(companyID), types.JournalID(journalID))
}

func (a *MasterDataAdapter) GetAccountByIDForCompany(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	return a.GetAccountByIDForCompanyContext(ctx, companyID, accountID)
}

func (a *MasterDataAdapter) GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	reader, err := a.reader()
	if err != nil {
		return masterdata.Account{}, err
	}
	account, err := reader.GetAccount(ctx, types.CompanyID(companyID), types.AccountID(accountID))
	if err != nil {
		return masterdata.Account{}, err
	}
	return ToInternalAccount(account), nil
}

func (a *MasterDataAdapter) GetAccountByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Account, error) {
	return a.GetAccountByCodeForCompanyContext(ctx, companyID, code)
}

func (a *MasterDataAdapter) GetAccountByCodeForCompanyContext(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Account, error) {
	reader, err := a.reader()
	if err != nil {
		return masterdata.Account{}, err
	}
	account, err := reader.GetAccountByCode(ctx, types.CompanyID(companyID), code)
	if err != nil {
		return masterdata.Account{}, err
	}
	return ToInternalAccount(account), nil
}

func (a *MasterDataAdapter) GetFiscalYearForCompany(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	return a.GetFiscalYearForCompanyContext(ctx, companyID, fiscalYearID)
}

func (a *MasterDataAdapter) GetFiscalYearForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	reader, err := a.reader()
	if err != nil {
		return masterdata.FiscalYear{}, err
	}
	year, err := reader.GetFiscalYear(ctx, types.CompanyID(companyID), types.FiscalYearID(fiscalYearID))
	if err != nil {
		return masterdata.FiscalYear{}, err
	}
	return ToInternalFiscalYear(year), nil
}

func (a *MasterDataAdapter) GetPeriodByDateForCompany(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	return a.GetPeriodByDateForCompanyContext(ctx, companyID, date)
}

func (a *MasterDataAdapter) GetPeriodByDateForCompanyContext(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	reader, err := a.reader()
	if err != nil {
		return masterdata.FiscalPeriod{}, err
	}
	period, err := reader.GetPeriodForDate(ctx, types.CompanyID(companyID), date)
	if err != nil {
		return masterdata.FiscalPeriod{}, err
	}
	return ToInternalFiscalPeriod(period), nil
}

func (a *MasterDataAdapter) GetPeriodByNumberForCompany(context.Context, uuid.UUID, uuid.UUID, int) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, unsupported()
}

func (a *MasterDataAdapter) GetPeriodByNumberForCompanyContext(context.Context, uuid.UUID, uuid.UUID, int) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, unsupported()
}

func (a *MasterDataAdapter) HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	reader, err := a.reader()
	if err != nil {
		return false, err
	}
	return reader.HasAccountChildren(ctx, types.CompanyID(companyID), types.AccountID(accountID))
}
