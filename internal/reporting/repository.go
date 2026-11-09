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

// Repository is intentionally left as an open SPI. Ledger implementations can
// expose any of the typed repository capabilities below.
type Repository interface{}

type AccountRepository interface {
	ListAccounts() ([]masterdata.Account, error)
}

type LedgerRepository interface {
	ListPostedEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
}

type EntryDateRepository interface {
	ListEntries(time.Time, time.Time) ([]posting.JournalEntry, error)
}

type FiscalYearRepository interface {
	GetFiscalYear(uuid.UUID) (masterdata.FiscalYear, error)
	UpdatePeriodState(uuid.UUID, masterdata.PeriodState) error
}

var ErrCompanyScopeRequired = errors.New("company-scoped reporting repository is required")

type CompanyScopedReportingRepository interface {
	ListAccountsForCompany(uuid.UUID) ([]masterdata.Account, error)
	ListEntriesForCompany(uuid.UUID, time.Time, time.Time) ([]posting.JournalEntry, error)
}

type CompanyScopedSubledgerReportingRepository interface {
	ListFixedAssetsForCompany(uuid.UUID) ([]subledgers.FixedAsset, error)
	ListOpenLinesForCompany(uuid.UUID, string) ([]subledgers.OpenLine, error)
}

type companyReportingRepository struct {
	companyID uuid.UUID
	ledger    CompanyScopedReportingRepository
	subledger CompanyScopedSubledgerReportingRepository
}

func newCompanyReportingRepository(repository Repository, companyID uuid.UUID) (Repository, error) {
	if companyID == uuid.Nil {
		return nil, ErrCompanyScopeRequired
	}
	ledger, ok := repository.(CompanyScopedReportingRepository)
	if !ok {
		return nil, ErrCompanyScopeRequired
	}
	subledger, _ := repository.(CompanyScopedSubledgerReportingRepository)
	return companyReportingRepository{companyID: companyID, ledger: ledger, subledger: subledger}, nil
}

func (r companyReportingRepository) ListAccounts() ([]masterdata.Account, error) {
	return r.ledger.ListAccountsForCompany(r.companyID)
}

func (r companyReportingRepository) ListEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	return r.ledger.ListEntriesForCompany(r.companyID, start, end)
}

func (r companyReportingRepository) ListPostedEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	return r.ListEntries(start, end)
}

func (r companyReportingRepository) ListFixedAssets() ([]subledgers.FixedAsset, error) {
	if r.subledger == nil {
		return nil, ErrCompanyScopeRequired
	}
	return r.subledger.ListFixedAssetsForCompany(r.companyID)
}

func (r companyReportingRepository) ListOpenLines(partnerID string) ([]subledgers.OpenLine, error) {
	if r.subledger == nil {
		return nil, ErrCompanyScopeRequired
	}
	return r.subledger.ListOpenLinesForCompany(r.companyID, partnerID)
}

func (r companyReportingRepository) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	if provider, ok := r.ledger.(interface {
		GetRateForCompany(context.Context, uuid.UUID, string, string, time.Time) (decimal.Decimal, error)
	}); ok {
		return provider.GetRateForCompany(ctx, r.companyID, from, to, date)
	}
	if provider, ok := r.ledger.(valuation.RateProviderSPI); ok {
		return provider.GetRate(ctx, from, to, date)
	}
	return decimal.Zero, valuation.ErrRateNotFound
}

func (r companyReportingRepository) GetRateOfType(
	ctx context.Context,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if provider, ok := r.ledger.(interface {
		GetRateOfTypeForCompany(context.Context, uuid.UUID, valuation.RateType, string, string, time.Time) (decimal.Decimal, error)
	}); ok {
		return provider.GetRateOfTypeForCompany(ctx, r.companyID, rateType, from, to, date)
	}
	if provider, ok := r.ledger.(interface {
		GetRateOfType(context.Context, valuation.RateType, string, string, time.Time) (decimal.Decimal, error)
	}); ok {
		return provider.GetRateOfType(ctx, rateType, from, to, date)
	}
	return r.GetRate(ctx, from, to, date)
}
