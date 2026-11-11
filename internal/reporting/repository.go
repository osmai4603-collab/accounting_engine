package reporting

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	"accounting_engine/internal/valuation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type AccountRepository interface {
	ListAccounts(context.Context) ([]masterdata.Account, error)
}

type LedgerRepository interface {
	ListPostedEntries(context.Context, time.Time, time.Time) ([]posting.JournalEntry, error)
}

type AccountPeriodBalanceReader interface {
	ReadAccountPeriodBalancesForCompanyContext(
		context.Context,
		uuid.UUID,
		time.Time,
		time.Time,
		string,
		bool,
	) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error)
}

type MaterializedBalanceReader interface {
	ReadAccountPeriodBalancesContext(
		context.Context,
		time.Time,
		time.Time,
		string,
		bool,
	) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error)
}

type EntryDateRepository interface {
	ListEntries(context.Context, time.Time, time.Time) ([]posting.JournalEntry, error)
}

type Repository interface {
	AccountRepository
	LedgerRepository
	CompanyScopedReportingRepository
	FiscalYearRepository
	CompanyScopedClosingRepository
	ListFixedAssets(context.Context) ([]subledgers.FixedAsset, error)
	ListOpenLines(context.Context, string) ([]subledgers.OpenLine, error)
}

type FiscalYearRepository interface {
	GetFiscalYearForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (masterdata.FiscalYear, error)
	UpdatePeriodStateForCompanyContext(context.Context, uuid.UUID, uuid.UUID, masterdata.PeriodState) error
	UpdateFiscalYearStateForCompanyContext(context.Context, uuid.UUID, uuid.UUID, masterdata.FiscalYearState) error
}

type FiscalYearHistoryRepository interface {
	IsFirstFiscalYearForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

type BalanceCarryForwardRepository interface {
	CarryForwardBalancesForCompanyContext(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
}

type CompanyScopedClosingRepository interface {
	GetEntryByIDForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (posting.JournalEntry, error)
}

var ErrCompanyScopeRequired = errors.New("company-scoped reporting repository is required")

type CompanyScopedReportingRepository interface {
	ListAccountsForCompanyContext(context.Context, uuid.UUID) ([]masterdata.Account, error)
	ListAccountsForCompanyAsOfContext(context.Context, uuid.UUID, time.Time) ([]masterdata.Account, error)
	ListEntriesForCompanyContext(context.Context, uuid.UUID, time.Time, time.Time) ([]posting.JournalEntry, error)
	ListFixedAssetsForCompanyContext(context.Context, uuid.UUID) ([]subledgers.FixedAsset, error)
	ListOpenLinesForCompanyContext(context.Context, uuid.UUID, string) ([]subledgers.OpenLine, error)
	GetRateForCompany(context.Context, uuid.UUID, string, string, time.Time) (decimal.Decimal, error)
	GetRateOfTypeForCompany(context.Context, uuid.UUID, valuation.RateType, string, string, time.Time) (decimal.Decimal, error)
}

type companyReportingRepository struct {
	ctx       context.Context
	companyID uuid.UUID
	asOf      time.Time
	ledger    Repository
}

func newCompanyReportingRepository(ctx context.Context, repository Repository, companyID uuid.UUID, asOf time.Time) (Repository, error) {
	if companyID == uuid.Nil {
		return nil, ErrCompanyScopeRequired
	}
	if ctx == nil {
		return nil, errors.New("reporting context is required")
	}
	return companyReportingRepository{ctx: ctx, companyID: companyID, asOf: asOf, ledger: repository}, nil
}

func (r companyReportingRepository) ListAccounts(ctx context.Context) ([]masterdata.Account, error) {
	ctx = r.ctx
	if !r.asOf.IsZero() {
		return r.ledger.ListAccountsForCompanyAsOfContext(ctx, r.companyID, r.asOf)
	}
	return r.ledger.ListAccountsForCompanyContext(ctx, r.companyID)
}

func (r companyReportingRepository) ListEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return r.ledger.ListEntriesForCompanyContext(r.ctx, r.companyID, start, end)
}

func (r companyReportingRepository) ListPostedEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return r.ListEntries(ctx, start, end)
}

func (r companyReportingRepository) ReadAccountPeriodBalancesContext(
	ctx context.Context,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	reader, ok := r.ledger.(AccountPeriodBalanceReader)
	if !ok {
		return nil, false, nil
	}
	return reader.ReadAccountPeriodBalancesForCompanyContext(
		r.ctx, r.companyID, start, end, currency, includeAdjusting,
	)
}

func (r companyReportingRepository) ListFixedAssets(ctx context.Context) ([]subledgers.FixedAsset, error) {
	return r.ledger.ListFixedAssetsForCompanyContext(r.ctx, r.companyID)
}

func (r companyReportingRepository) ListOpenLines(ctx context.Context, partnerID string) ([]subledgers.OpenLine, error) {
	return r.ledger.ListOpenLinesForCompanyContext(r.ctx, r.companyID, partnerID)
}

func (r companyReportingRepository) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return r.ledger.GetRateForCompany(r.ctx, r.companyID, from, to, date)
}

func (r companyReportingRepository) GetRateOfType(
	ctx context.Context,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	return r.ledger.GetRateOfTypeForCompany(r.ctx, r.companyID, rateType, from, to, date)
}

func (r companyReportingRepository) ListAccountsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]masterdata.Account, error) {
	if companyID != r.companyID {
		return nil, ErrCompanyScopeRequired
	}
	return r.ledger.ListAccountsForCompanyContext(r.ctx, companyID)
}

func (r companyReportingRepository) ListAccountsForCompanyAsOfContext(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]masterdata.Account, error) {
	if companyID != r.companyID {
		return nil, ErrCompanyScopeRequired
	}
	return r.ledger.ListAccountsForCompanyAsOfContext(r.ctx, companyID, asOf)
}

func (r companyReportingRepository) ListEntriesForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	if companyID != r.companyID {
		return nil, ErrCompanyScopeRequired
	}
	return r.ledger.ListEntriesForCompanyContext(r.ctx, companyID, start, end)
}

func (r companyReportingRepository) ListFixedAssetsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	if companyID != r.companyID {
		return nil, ErrCompanyScopeRequired
	}
	return r.ledger.ListFixedAssetsForCompanyContext(r.ctx, companyID)
}

func (r companyReportingRepository) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if companyID != r.companyID {
		return nil, ErrCompanyScopeRequired
	}
	return r.ledger.ListOpenLinesForCompanyContext(r.ctx, companyID, partnerID)
}

func (r companyReportingRepository) GetRateForCompany(ctx context.Context, companyID uuid.UUID, from, to string, date time.Time) (decimal.Decimal, error) {
	if companyID != r.companyID {
		return decimal.Zero, ErrCompanyScopeRequired
	}
	return r.ledger.GetRateForCompany(r.ctx, companyID, from, to, date)
}

func (r companyReportingRepository) GetRateOfTypeForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if companyID != r.companyID {
		return decimal.Zero, ErrCompanyScopeRequired
	}
	return r.ledger.GetRateOfTypeForCompany(r.ctx, companyID, rateType, from, to, date)
}

func (r companyReportingRepository) GetFiscalYearForCompanyContext(ctx context.Context, companyID, yearID uuid.UUID) (masterdata.FiscalYear, error) {
	if companyID != r.companyID {
		return masterdata.FiscalYear{}, ErrCompanyScopeRequired
	}
	return r.ledger.GetFiscalYearForCompanyContext(r.ctx, companyID, yearID)
}

func (r companyReportingRepository) IsFirstFiscalYearForCompanyContext(
	ctx context.Context,
	companyID, yearID uuid.UUID,
) (bool, error) {
	if companyID != r.companyID {
		return false, ErrCompanyScopeRequired
	}
	repository, ok := r.ledger.(FiscalYearHistoryRepository)
	if !ok {
		return false, ErrFiscalYearHistoryRepositoryRequired
	}
	return repository.IsFirstFiscalYearForCompanyContext(r.ctx, companyID, yearID)
}

func (r companyReportingRepository) CarryForwardBalancesForCompanyContext(
	ctx context.Context,
	companyID, closedYearID, newPeriodID uuid.UUID,
) error {
	if companyID != r.companyID {
		return ErrCompanyScopeRequired
	}
	repository, ok := r.ledger.(BalanceCarryForwardRepository)
	if !ok {
		return ErrAccountBalanceRepositoryRequired
	}
	return repository.CarryForwardBalancesForCompanyContext(r.ctx, companyID, closedYearID, newPeriodID)
}

func (r companyReportingRepository) UpdatePeriodStateForCompanyContext(ctx context.Context, companyID, periodID uuid.UUID, state masterdata.PeriodState) error {
	if companyID != r.companyID {
		return ErrCompanyScopeRequired
	}
	return r.ledger.UpdatePeriodStateForCompanyContext(r.ctx, companyID, periodID, state)
}

func (r companyReportingRepository) UpdateFiscalYearStateForCompanyContext(ctx context.Context, companyID, yearID uuid.UUID, state masterdata.FiscalYearState) error {
	if companyID != r.companyID {
		return ErrCompanyScopeRequired
	}
	return r.ledger.UpdateFiscalYearStateForCompanyContext(r.ctx, companyID, yearID, state)
}

func (r companyReportingRepository) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if companyID != r.companyID {
		return posting.JournalEntry{}, ErrCompanyScopeRequired
	}
	return r.ledger.GetEntryByIDForCompanyContext(r.ctx, companyID, entryID)
}
