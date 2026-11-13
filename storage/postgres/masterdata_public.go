package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"accounting_engine/internal/masterdata"
	"accounting_engine/pkg/api/v1/types"
	publicmasterdata "accounting_engine/pkg/spi/masterdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	company, err := s.driver.GetCompanyByID(ctx, uuid.UUID(id))
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
	if err := journal.Validate(); err != nil {
		return err
	}
	companyID := uuid.UUID(journal.CompanyID)
	tx, err := s.driver.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var companyExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM companies WHERE id=$1)`, companyID).Scan(&companyExists); err != nil {
		return err
	}
	if !companyExists {
		return publicmasterdata.ErrCompanyNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO journals(id, company_id, code, name) VALUES($1,$2,$3,$4)`,
		uuid.UUID(journal.ID), companyID, journal.Code, journal.Name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "uq_journals_code":
				return publicmasterdata.ErrDuplicateJournalCode
			case "journals_pkey":
				return publicmasterdata.ErrDuplicateJournalID
			}
		}
		return err
	}
	return tx.Commit(ctx)
}

func (s *publicMasterDataStore) GetJournalByCode(ctx context.Context, companyID types.CompanyID, code string) (publicmasterdata.Journal, error) {
	if !s.available() {
		return publicmasterdata.Journal{}, publicmasterdata.ErrCapabilityNotImplemented
	}
	if companyID == (types.CompanyID{}) || strings.TrimSpace(code) == "" || utf8.RuneCountInString(code) > 32 {
		return publicmasterdata.Journal{}, publicmasterdata.ErrInvalidJournal
	}
	tx, err := s.driver.beginCompanyTx(ctx, uuid.UUID(companyID))
	if err != nil {
		return publicmasterdata.Journal{}, err
	}
	defer tx.Rollback(ctx)

	var journalID, journalCompanyID uuid.UUID
	var journal publicmasterdata.Journal
	err = tx.QueryRow(ctx, `SELECT id, company_id, code, name FROM journals WHERE company_id=$1 AND code=$2`,
		uuid.UUID(companyID), code).Scan(&journalID, &journalCompanyID, &journal.Code, &journal.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return publicmasterdata.Journal{}, publicmasterdata.ErrJournalNotFound
	}
	if err != nil {
		return publicmasterdata.Journal{}, err
	}
	journal.ID = publicmasterdata.JournalID(journalID)
	journal.CompanyID = types.CompanyID(journalCompanyID)
	if err := tx.Commit(ctx); err != nil {
		return publicmasterdata.Journal{}, err
	}
	return journal, nil
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
	account, err := s.driver.GetAccountByIDForCompany(ctx, uuid.UUID(companyID), uuid.UUID(id))
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
	year, err := s.driver.GetFiscalYearForCompany(ctx, uuid.UUID(companyID), uuid.UUID(id))
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
	state := period.State
	if state == "" {
		state = publicmasterdata.PeriodOpen
	}
	periodType := period.Type
	if periodType == "" {
		periodType = publicmasterdata.FiscalPeriodTypeNormal
	}
	var closedAt any
	if state != publicmasterdata.PeriodOpen {
		closedAt = time.Now().UTC()
	}
	tx, err := s.driver.beginCompanyTx(ctx, uuid.UUID(period.CompanyID))
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM fiscal_years WHERE company_id=$1 AND id=$2
	)`, uuid.UUID(period.CompanyID), uuid.UUID(period.FiscalYearID)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return masterdata.ErrFiscalYearNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO fiscal_periods(
		id,company_id,year_id,code,start_date,end_date,state,period_number,name,period_type,closed_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		uuid.UUID(period.ID), uuid.UUID(period.CompanyID), uuid.UUID(period.FiscalYearID),
		fmt.Sprintf("%02d", period.Number), period.StartDate, period.EndDate, state,
		period.Number, period.Name, periodType, closedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
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
