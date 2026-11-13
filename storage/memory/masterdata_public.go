package memory

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"accounting_engine/internal/masterdata"
	"accounting_engine/pkg/api/v1/types"
	publicmasterdata "accounting_engine/pkg/spi/masterdata"

	"github.com/google/uuid"
)

type publicMasterDataStore struct {
	driver *Driver
}

var _ publicmasterdata.Store = (*publicMasterDataStore)(nil)

// MasterDataStore returns the stable public Master Data contract adapter.
func (d *Driver) MasterDataStore() publicmasterdata.Store {
	return &publicMasterDataStore{driver: d}
}

func (s *publicMasterDataStore) available() bool { return s != nil && s.driver != nil }

func (s *publicMasterDataStore) CreateCompany(ctx context.Context, company publicmasterdata.Company) error {
	if !s.available() {
		return publicmasterdata.ErrCapabilityNotImplemented
	}
	return s.driver.CreateCompany(ctx, masterdata.Company{
		ID: uuid.UUID(company.ID), Code: company.Code, LegalName: company.LegalName,
		FunctionalCurrency: company.FunctionalCurrency, PresentationCurrency: company.PresentationCurrency,
		Active: company.Active,
	})
}

func (s *publicMasterDataStore) GetCompany(ctx context.Context, id types.CompanyID) (publicmasterdata.Company, error) {
	if !s.available() {
		return publicmasterdata.Company{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	company, err := s.driver.GetCompanyByIDContext(ctx, uuid.UUID(id))
	if err != nil {
		return publicmasterdata.Company{}, err
	}
	return publicmasterdata.Company{
		ID: types.CompanyID(company.ID), Code: company.Code, LegalName: company.LegalName,
		FunctionalCurrency: company.FunctionalCurrency, PresentationCurrency: company.PresentationCurrency,
		Active: company.Active,
	}, nil
}

func (s *publicMasterDataStore) CreateJournal(ctx context.Context, journal publicmasterdata.Journal) error {
	if !s.available() {
		return publicmasterdata.ErrCapabilityNotImplemented
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := journal.Validate(); err != nil {
		return err
	}

	d := s.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	companyID := uuid.UUID(journal.CompanyID)
	if _, ok := d.companies[companyID]; !ok {
		return publicmasterdata.ErrCompanyNotFound
	}
	for _, current := range d.journals {
		if current.CompanyID == companyID && current.Code == journal.Code {
			return publicmasterdata.ErrDuplicateJournalCode
		}
	}
	id := uuid.UUID(journal.ID)
	if _, exists := d.journals[id]; exists {
		return publicmasterdata.ErrDuplicateJournalID
	}
	d.journals[id] = journalRecord{
		ID: id, CompanyID: companyID, Code: journal.Code, Name: journal.Name,
	}
	return nil
}

func (s *publicMasterDataStore) GetJournalByCode(ctx context.Context, companyID types.CompanyID, code string) (publicmasterdata.Journal, error) {
	if !s.available() {
		return publicmasterdata.Journal{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	if err := ctx.Err(); err != nil {
		return publicmasterdata.Journal{}, err
	}
	if companyID == (types.CompanyID{}) || strings.TrimSpace(code) == "" || utf8.RuneCountInString(code) > 32 {
		return publicmasterdata.Journal{}, publicmasterdata.ErrInvalidJournal
	}
	d := s.driver
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, journal := range d.journals {
		if journal.CompanyID == uuid.UUID(companyID) && journal.Code == code {
			return publicmasterdata.Journal{
				ID: publicmasterdata.JournalID(journal.ID), CompanyID: types.CompanyID(journal.CompanyID),
				Code: journal.Code, Name: journal.Name,
			}, nil
		}
	}
	return publicmasterdata.Journal{}, publicmasterdata.ErrJournalNotFound
}

func (s *publicMasterDataStore) CreateAccount(ctx context.Context, account publicmasterdata.Account) error {
	if !s.available() {
		return publicmasterdata.ErrCapabilityNotImplemented
	}
	var parentID *uuid.UUID
	if account.ParentID != nil {
		parent := uuid.UUID(*account.ParentID)
		parentID = &parent
	}
	return s.driver.CreateAccount(ctx, masterdata.Account{
		ID: uuid.UUID(account.ID), CompanyID: uuid.UUID(account.CompanyID),
		Code: account.Code, Name: account.Name, AccountType: masterdata.AccountType(account.Type),
		NormalBalance: masterdata.NormalBalance(account.NormalBalance), CurrencyID: account.Currency,
		ParentID: parentID, IsReconcilable: account.IsReconcilable, IsControlAccount: account.IsControlAccount,
		IsContraAccount: account.IsContraAccount, IsActive: account.IsActive,
		RequiresCostCenter: account.RequiresCostCenter, IsCashEquivalent: account.IsCashEquivalent,
		IsNonCashAdjustment: account.IsNonCashAdjustment, CashFlowActivity: account.CashFlowActivity,
		SubClassification: account.SubClassification,
		IFRSCategory:      account.IFRSCategory, DisplayOrder: account.DisplayOrder,
	})
}

func (s *publicMasterDataStore) GetAccount(ctx context.Context, companyID types.CompanyID, id types.AccountID) (publicmasterdata.Account, error) {
	if !s.available() {
		return publicmasterdata.Account{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	account, err := s.driver.GetAccountByIDForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(id))
	if err != nil {
		return publicmasterdata.Account{}, err
	}
	var parentID *types.AccountID
	if account.ParentID != nil {
		parent := types.AccountID(*account.ParentID)
		parentID = &parent
	}
	return publicmasterdata.Account{
		ID: types.AccountID(account.ID), CompanyID: types.CompanyID(account.CompanyID),
		Code: account.Code, Name: account.Name, Type: publicmasterdata.AccountType(account.AccountType),
		NormalBalance: publicmasterdata.NormalBalance(account.NormalBalance), Currency: account.CurrencyID,
		ParentID: parentID, IsReconcilable: account.IsReconcilable, IsControlAccount: account.IsControlAccount,
		IsContraAccount: account.IsContraAccount, IsActive: account.IsActive,
		RequiresCostCenter: account.RequiresCostCenter, IsCashEquivalent: account.IsCashEquivalent,
		IsNonCashAdjustment: account.IsNonCashAdjustment, CashFlowActivity: account.CashFlowActivity,
		SubClassification: account.SubClassification,
		IFRSCategory:      account.IFRSCategory, DisplayOrder: account.DisplayOrder,
	}, nil
}

func (s *publicMasterDataStore) CreateFiscalYear(ctx context.Context, year publicmasterdata.FiscalYear) error {
	if !s.available() {
		return publicmasterdata.ErrCapabilityNotImplemented
	}
	return s.driver.CreateFiscalYear(ctx, masterdata.FiscalYear{
		ID: uuid.UUID(year.ID), CompanyID: uuid.UUID(year.CompanyID), Name: year.Name,
		StartDate: year.StartDate, EndDate: year.EndDate, State: masterdata.FiscalYearState(year.State),
	})
}

func (s *publicMasterDataStore) GetFiscalYear(ctx context.Context, companyID types.CompanyID, id types.FiscalYearID) (publicmasterdata.FiscalYear, error) {
	if !s.available() {
		return publicmasterdata.FiscalYear{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	year, err := s.driver.GetFiscalYearForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(id))
	if err != nil {
		return publicmasterdata.FiscalYear{}, err
	}
	return publicmasterdata.FiscalYear{
		ID: types.FiscalYearID(year.ID), CompanyID: types.CompanyID(year.CompanyID),
		Name: year.Name, StartDate: year.StartDate, EndDate: year.EndDate,
		State: publicmasterdata.FiscalYearState(year.State),
	}, nil
}

func (s *publicMasterDataStore) CreateFiscalPeriod(ctx context.Context, period publicmasterdata.FiscalPeriod) error {
	if !s.available() {
		return publicmasterdata.ErrCapabilityNotImplemented
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d := s.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	year, ok := d.years[uuid.UUID(period.FiscalYearID)]
	if !ok || year.CompanyID != uuid.UUID(period.CompanyID) {
		return masterdata.ErrFiscalYearNotFound
	}
	if _, exists := d.periods[uuid.UUID(period.ID)]; exists {
		return fmt.Errorf("fiscal period %s already exists", period.ID)
	}
	internalPeriod := masterdata.FiscalPeriod{
		ID: uuid.UUID(period.ID), CompanyID: uuid.UUID(period.CompanyID),
		FiscalYearID: uuid.UUID(period.FiscalYearID), Number: period.Number, Name: period.Name,
		StartDate: period.StartDate, EndDate: period.EndDate,
		State: masterdata.PeriodState(period.State), Type: masterdata.FiscalPeriodType(period.Type),
	}
	if internalPeriod.State == "" {
		internalPeriod.State = masterdata.PeriodOpen
	}
	if internalPeriod.Type == "" {
		internalPeriod.Type = masterdata.FiscalPeriodTypeNormal
	}
	d.periods[internalPeriod.ID] = internalPeriod
	year.Periods = append(year.Periods, internalPeriod)
	d.years[year.ID] = year
	return nil
}

func (s *publicMasterDataStore) GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, id types.FiscalPeriodID) (publicmasterdata.FiscalPeriod, error) {
	if !s.available() {
		return publicmasterdata.FiscalPeriod{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	period, err := s.driver.GetFiscalPeriod(ctx, companyID, id)
	if err != nil {
		return publicmasterdata.FiscalPeriod{}, err
	}
	return publicmasterdata.FiscalPeriod{
		ID: period.ID, CompanyID: period.CompanyID, FiscalYearID: period.FiscalYearID,
		Number: period.Number, Name: period.Name, StartDate: period.StartDate, EndDate: period.EndDate,
		State: publicmasterdata.PeriodState(period.State), Type: publicmasterdata.FiscalPeriodType(period.Type),
	}, nil
}
