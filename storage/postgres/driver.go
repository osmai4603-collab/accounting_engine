package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var ErrNotImplemented = errors.New("postgres operation not implemented")

type Driver struct {
	pool *pgxpool.Pool
}

var _ posting.Repository = (*Driver)(nil)
var _ subledgers.Repository = (*Driver)(nil)
var _ masterdata.ContextStorageRepository = (*Driver)(nil)
var _ reporting.Repository = (*Driver)(nil)

func New(ctx context.Context, connectionString string) (*Driver, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := seedCurrencyRegistry(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Driver{pool: pool}, nil
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("unable to locate postgres schema files")
	}
	baseDir := filepath.Dir(file)
	content, err := os.ReadFile(filepath.Join(baseDir, "schema.sql"))
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, string(content)); err != nil {
		return err
	}
	return MigrateUp(ctx, pool)
}

func NewWithPool(pool *pgxpool.Pool) *Driver { return &Driver{pool: pool} }
func (d *Driver) Close()                     { d.pool.Close() }
func (d *Driver) Pool() *pgxpool.Pool        { return d.pool }

func seedCurrencyRegistry(ctx context.Context, pool *pgxpool.Pool) error {
	batch := &pgx.Batch{}
	currencies := financialtypes.AllCurrencies()
	for _, currency := range currencies {
		batch.Queue(`INSERT INTO currencies(code,numeric_code,name,minor_units)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(code) DO UPDATE SET numeric_code=EXCLUDED.numeric_code,name=EXCLUDED.name,minor_units=EXCLUDED.minor_units`,
			string(currency.Code), currency.NumericCode, currency.Name, currency.MinorUnit)
	}
	results := pool.SendBatch(ctx, batch)
	defer results.Close()
	for range currencies {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func nullableString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func defaultTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}

func (d *Driver) CreateCompany(ctx context.Context, company masterdata.Company) error {
	company.Code = strings.ToUpper(strings.TrimSpace(company.Code))
	company.FunctionalCurrency = strings.ToUpper(strings.TrimSpace(company.FunctionalCurrency))
	company.PresentationCurrency = strings.ToUpper(strings.TrimSpace(company.PresentationCurrency))
	if err := company.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, company.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO companies(id, code, legal_name, functional_currency, presentation_currency, is_active)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(id) DO UPDATE SET code=EXCLUDED.code, legal_name=EXCLUDED.legal_name,
		 functional_currency=EXCLUDED.functional_currency, presentation_currency=EXCLUDED.presentation_currency,
		 is_active=EXCLUDED.is_active`,
			company.ID, company.Code, company.LegalName, company.FunctionalCurrency,
			company.PresentationCurrency, company.Active)
		return err
	})
}

func (d *Driver) GetCompanyByID(ctx context.Context, id uuid.UUID) (masterdata.Company, error) {
	var company masterdata.Company
	err := d.WithCompanyTx(ctx, id, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, code, legal_name, functional_currency, presentation_currency, is_active
			 FROM companies WHERE id=$1`, id).Scan(
			&company.ID, &company.Code, &company.LegalName, &company.FunctionalCurrency,
			&company.PresentationCurrency, &company.Active)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrInvalidCompany
		}
		return err
	})
	return company, err
}

const accountSelect = `SELECT id, company_id, code, name, account_type, normal_balance, currency_id, parent_id,
	is_reconcilable, is_control_account, is_contra_account, is_active, requires_cost_center, is_cash_equivalent, is_non_cash_adjustment,
	cash_flow_activity, sub_classification, ifrs_category, display_order, created_at, updated_at
	FROM accounts`

func scanAccount(row pgx.Row) (masterdata.Account, error) {
	var account masterdata.Account
	err := row.Scan(
		&account.ID, &account.CompanyID, &account.Code, &account.Name, &account.AccountType, &account.NormalBalance,
		&account.CurrencyID, &account.ParentID, &account.IsReconcilable, &account.IsControlAccount,
		&account.IsContraAccount, &account.IsActive, &account.RequiresCostCenter,
		&account.IsCashEquivalent, &account.IsNonCashAdjustment, &account.CashFlowActivity,
		&account.SubClassification, &account.IFRSCategory, &account.DisplayOrder,
		&account.CreatedAt, &account.UpdatedAt,
	)
	return account, err
}

func (d *Driver) CreateAccount(ctx context.Context, account masterdata.Account) error {
	return d.WithCompanyTx(ctx, account.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO accounts (
			id, company_id, code, name, account_type, normal_balance, currency_id, parent_id,
			is_reconcilable, is_control_account, is_contra_account, is_active, requires_cost_center, is_cash_equivalent, is_non_cash_adjustment,
			cash_flow_activity, sub_classification, ifrs_category, display_order, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT(id) DO UPDATE SET
			company_id=EXCLUDED.company_id, code=EXCLUDED.code, name=EXCLUDED.name, account_type=EXCLUDED.account_type,
			normal_balance=EXCLUDED.normal_balance, currency_id=EXCLUDED.currency_id,
			parent_id=EXCLUDED.parent_id, is_reconcilable=EXCLUDED.is_reconcilable,
			is_control_account=EXCLUDED.is_control_account,
			is_contra_account=EXCLUDED.is_contra_account,
			is_active=EXCLUDED.is_active, requires_cost_center=EXCLUDED.requires_cost_center,
			is_cash_equivalent=EXCLUDED.is_cash_equivalent,
			is_non_cash_adjustment=EXCLUDED.is_non_cash_adjustment,
			cash_flow_activity=EXCLUDED.cash_flow_activity,
			sub_classification=EXCLUDED.sub_classification, ifrs_category=EXCLUDED.ifrs_category,
			display_order=EXCLUDED.display_order, updated_at=EXCLUDED.updated_at`,
			account.ID, account.CompanyID, account.Code, account.Name, account.AccountType, account.NormalBalance,
			nullableString(account.CurrencyID, "SAR"), account.ParentID, account.IsReconcilable,
			account.IsControlAccount, account.IsContraAccount, account.IsActive, account.RequiresCostCenter,
			account.IsCashEquivalent, account.IsNonCashAdjustment,
			nullableString(account.CashFlowActivity, "unclassified"),
			nullableString(account.SubClassification, "unclassified"),
			nullableString(account.IFRSCategory, "unclassified"), account.DisplayOrder,
			defaultTime(account.CreatedAt), defaultTime(account.UpdatedAt))
		return err
	})
}

func (d *Driver) GetAccountByID(ctx context.Context, id uuid.UUID) (masterdata.Account, error) {
	return masterdata.Account{}, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetAccountByIDForCompany(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	var account masterdata.Account
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var scanErr error
		account, scanErr = scanAccount(tx.QueryRow(ctx,
			accountSelect+" WHERE company_id=$1 AND id=$2", companyID, accountID))
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return masterdata.ErrAccountNotFound
		}
		return scanErr
	})
	return account, err
}

func (d *Driver) GetAccountByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Account, error) {
	var account masterdata.Account
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var scanErr error
		account, scanErr = scanAccount(tx.QueryRow(ctx,
			accountSelect+" WHERE company_id=$1 AND lower(code)=lower($2)", companyID, code))
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return masterdata.ErrAccountNotFound
		}
		return scanErr
	})
	return account, err
}

func (d *Driver) GetAccountByCode(ctx context.Context, code string) (masterdata.Account, error) {
	return masterdata.Account{}, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListAccounts(ctx context.Context) ([]masterdata.Account, error) {
	return nil, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListAccountsForCompany(ctx context.Context, companyID uuid.UUID) ([]masterdata.Account, error) {
	result := make([]masterdata.Account, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, accountSelect+" WHERE company_id=$1 ORDER BY code", companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			account, err := scanAccount(rows)
			if err != nil {
				return err
			}
			result = append(result, account)
		}
		return rows.Err()
	})
	return result, err
}

func (d *Driver) ListAccountsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]masterdata.Account, error) {
	return d.ListAccountsForCompany(ctx, companyID)
}

func (d *Driver) ListAccountsForCompanyAsOfContext(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]masterdata.Account, error) {
	if asOf.IsZero() {
		return nil, masterdata.ErrInvalidEffectivePeriod
	}
	accounts := make([]masterdata.Account, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, accountSelect+" WHERE company_id=$1 ORDER BY code", companyID)
		if err != nil {
			return err
		}
		for rows.Next() {
			account, scanErr := scanAccount(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			accounts = append(accounts, account)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		versions, err := tx.Query(ctx, `SELECT account_id,name,account_type,ifrs_category,effective_from,effective_to
			FROM account_versions WHERE company_id=$1 ORDER BY account_id,effective_from`, companyID)
		if err != nil {
			return err
		}
		defer versions.Close()
		byAccount := make(map[uuid.UUID][]masterdata.AccountVersion)
		for versions.Next() {
			var version masterdata.AccountVersion
			version.CompanyID = companyID
			if err := versions.Scan(
				&version.AccountID, &version.Name, &version.AccountType, &version.IFRSCategory,
				&version.EffectiveFrom, &version.EffectiveTo,
			); err != nil {
				return err
			}
			byAccount[version.AccountID] = append(byAccount[version.AccountID], version)
		}
		if err := versions.Err(); err != nil {
			return err
		}
		for i := range accounts {
			if history := byAccount[accounts[i].ID]; len(history) > 0 {
				version, err := masterdata.AccountVersionAt(history, asOf)
				if err != nil {
					return err
				}
				accounts[i].Name = version.Name
				accounts[i].AccountType = version.AccountType
				accounts[i].IFRSCategory = version.IFRSCategory
			}
		}
		return nil
	})
	return accounts, err
}

func (d *Driver) HasChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return false, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) CreateFiscalYear(ctx context.Context, year masterdata.FiscalYear) error {
	if year.State == "" {
		year.State = masterdata.FiscalYearOpen
	}
	data, err := json.Marshal(year)
	if err != nil {
		return err
	}
	tx, err := d.beginCompanyTx(ctx, year.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO fiscal_years(id,company_id,code,year,start_date,end_date,state,is_closed,year_data)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,code=EXCLUDED.code,
		year=EXCLUDED.year,start_date=EXCLUDED.start_date,end_date=EXCLUDED.end_date,state=EXCLUDED.state,
		is_closed=EXCLUDED.is_closed,year_data=EXCLUDED.year_data`,
		year.ID, year.CompanyID, year.Name, year.StartDate.UTC().Year(), year.StartDate, year.EndDate,
		year.State, year.State == masterdata.FiscalYearHardClosed, data); err != nil {
		return err
	}
	for _, period := range year.Periods {
		periodCompanyID := period.CompanyID
		if periodCompanyID == uuid.Nil {
			periodCompanyID = year.CompanyID
		}
		periodYearID := period.FiscalYearID
		if periodYearID == uuid.Nil {
			periodYearID = year.ID
		}
		periodType := period.Type
		if periodType == "" {
			periodType = masterdata.FiscalPeriodTypeNormal
		}
		closedAt := any(nil)
		if period.State != masterdata.PeriodOpen {
			closedAt = time.Now().UTC()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO fiscal_periods(
			id,company_id,year_id,code,start_date,end_date,state,period_number,name,period_type,closed_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,year_id=EXCLUDED.year_id,
			code=EXCLUDED.code,start_date=EXCLUDED.start_date,end_date=EXCLUDED.end_date,state=EXCLUDED.state,
			period_number=EXCLUDED.period_number,name=EXCLUDED.name,period_type=EXCLUDED.period_type,
			closed_at=EXCLUDED.closed_at`,
			period.ID, periodCompanyID, periodYearID, fmt.Sprintf("%02d", period.Number),
			period.StartDate, period.EndDate, period.State, period.Number, period.Name, periodType, closedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetFiscalYear(ctx context.Context, id uuid.UUID) (masterdata.FiscalYear, error) {
	return masterdata.FiscalYear{}, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetFiscalYearForCompany(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	var year masterdata.FiscalYear
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		var state masterdata.FiscalYearState
		if err := tx.QueryRow(ctx,
			`SELECT state,year_data FROM fiscal_years WHERE company_id=$1 AND id=$2`,
			companyID, fiscalYearID).Scan(&state, &data); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return masterdata.ErrFiscalYearNotFound
			}
			return err
		}
		if err := json.Unmarshal(data, &year); err != nil {
			return err
		}
		year.CompanyID, year.State = companyID, state
		rows, err := tx.Query(ctx, `SELECT id,company_id,year_id,COALESCE(period_number,0),COALESCE(name,''),
			start_date,end_date,state,period_type FROM fiscal_periods
			WHERE company_id=$1 AND year_id=$2 ORDER BY period_number`, companyID, fiscalYearID)
		if err != nil {
			return err
		}
		defer rows.Close()
		periods := make([]masterdata.FiscalPeriod, 0)
		for rows.Next() {
			var period masterdata.FiscalPeriod
			if err := rows.Scan(&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number,
				&period.Name, &period.StartDate, &period.EndDate, &period.State, &period.Type); err != nil {
				return err
			}
			periods = append(periods, period)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(periods) > 0 {
			year.Periods = periods
		}
		return nil
	})
	return year, err
}

func (d *Driver) UpdateFiscalYearState(ctx context.Context, companyID, id uuid.UUID, state masterdata.FiscalYearState) error {
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var current masterdata.FiscalYearState
		err := tx.QueryRow(ctx, `SELECT state FROM fiscal_years WHERE company_id=$1 AND id=$2 FOR UPDATE`,
			companyID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalYearNotFound
		}
		if err != nil {
			return err
		}
		if err := masterdata.ValidateFiscalYearTransition(current, state); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE fiscal_years SET state=$3::varchar,is_closed=$4,
			 year_data=jsonb_set(year_data,'{State}',to_jsonb($3::varchar),TRUE)
			 WHERE company_id=$1 AND id=$2`,
			companyID, id, state, state == masterdata.FiscalYearHardClosed)
		return err
	})
}

func (d *Driver) UpdateFiscalYearStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.FiscalYearState) error {
	return d.UpdateFiscalYearState(ctx, companyID, id, state)
}

func (d *Driver) GetPeriodByDate(ctx context.Context, date time.Time) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetPeriodByDateForCompany(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type
		 FROM fiscal_periods
		 WHERE company_id=$1 AND start_date <= $2 AND end_date >= $2
		   AND period_type = $3
		 ORDER BY period_number ASC LIMIT 1`,
			companyID, date, masterdata.FiscalPeriodTypeNormal).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return err
	})
	return period, err
}

func (d *Driver) GetPeriodByNumberForCompany(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type
		 FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 AND period_number=$3`,
			companyID, fiscalYearID, number).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return err
	})
	return period, err
}

func (d *Driver) UpdatePeriodState(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE fiscal_periods
			SET state=$3,closed_at=CASE WHEN $3='open' THEN NULL ELSE COALESCE(closed_at,NOW()) END
			WHERE company_id=$1 AND id=$2`, companyID, id, state)
		return err
	})
}

func (d *Driver) UpdatePeriodStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	return d.UpdatePeriodState(ctx, companyID, id, state)
}

func (d *Driver) GetNextSequenceWithLock(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	var sequence int64
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT current_sequence + 1 FROM journals WHERE company_id=$1 AND id=$2`,
			companyID, journalID).Scan(&sequence)
		if errors.Is(err, pgx.ErrNoRows) {
			sequence = 1
			return nil
		}
		return err
	})
	return sequence, err
}

func (d *Driver) SaveDraft(ctx context.Context, entry posting.JournalEntry) error {
	return d.SaveDraftContext(ctx, entry)
}

func (d *Driver) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) (result error) {
	if entry.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback draft transaction: %w", rollbackErr))
		}
	}()
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *Driver) GetEntryByID(ctx context.Context, companyID, id uuid.UUID) (posting.JournalEntry, error) {
	var entry posting.JournalEntry
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2`, companyID, id,
		).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.ErrEntryNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &entry)
	})
	return entry, err
}
func (d *Driver) PostAtomic(ctx context.Context, entry posting.JournalEntry) error {
	return d.PostAtomicContext(ctx, entry)
}

func (d *Driver) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) (result error) {
	if entry.SequenceNumber == 0 {
		_, err := d.PostAndSequenceContext(ctx, entry)
		return err
	}
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback atomic posting transaction: %w", rollbackErr))
		}
	}()
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) PostAndSequence(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	return d.PostAndSequenceContext(ctx, entry)
}

func (d *Driver) PostAndSequenceContext(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	return d.postAndSequenceContext(ctx, entry, nil)
}

func (d *Driver) PostAndSequenceWithOpenLine(ctx context.Context,
	entry posting.JournalEntry,
	attachment posting.OpenLineAttachment,
) (posting.JournalEntry, error) {
	return d.PostAndSequenceWithOpenLineContext(ctx, entry, attachment)
}

func (d *Driver) PostAndSequenceWithOpenLineContext(ctx context.Context, entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	return d.postAndSequenceContext(ctx, entry, &attachment)
}

func (d *Driver) postAndSequenceContext(ctx context.Context, entry posting.JournalEntry, attachment *posting.OpenLineAttachment) (result posting.JournalEntry, resultErr error) {
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback posting transaction: %w", rollbackErr))
		}
	}()

	if entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
		return posting.JournalEntry{}, posting.ErrInvalidEntry
	}
	var sequence int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM journals
		 WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		entry.CompanyID, entry.JournalID).Scan(&sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	var status posting.EntryStatus
	var draftData []byte
	if err := tx.QueryRow(ctx,
		`SELECT status, entry FROM account_moves
		 WHERE company_id=$1 AND id=$2 AND journal_id=$3 FOR UPDATE`,
		entry.CompanyID, entry.ID, entry.JournalID).Scan(&status, &draftData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	var current posting.JournalEntry
	if err := json.Unmarshal(draftData, &current); err != nil {
		return posting.JournalEntry{}, err
	}
	if status != posting.EntryDraft || current.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	sequence++
	if _, err := tx.Exec(ctx,
		`UPDATE journals SET current_sequence=$3 WHERE company_id=$1 AND id=$2`,
		entry.CompanyID, entry.JournalID, sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	entry.SequenceNumber = sequence
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	if attachment != nil {
		partnerID := attachment.PartnerID
		line := subledgers.OpenLine{
			CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
			ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
			Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
			ResidualAmount: attachment.Amount, DocumentDate: attachment.DocumentDate,
			DueDate: attachment.DueDate, Reference: attachment.Reference,
		}
		account, err := scanAccount(tx.QueryRow(ctx,
			accountSelect+` WHERE company_id=$1 AND id=$2 FOR SHARE`, entry.CompanyID, attachment.ControlAccountID))
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, masterdata.ErrAccountNotFound
		}
		if err != nil {
			return posting.JournalEntry{}, err
		}
		if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
			return posting.JournalEntry{}, err
		}
		data, err := json.Marshal(line)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5)`,
			line.ID, line.CompanyID, partnerID, line.Kind, data); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func (d *Driver) PostReversalAtomic(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	return d.PostReversalAtomicContext(ctx, original, reversal)
}

func (d *Driver) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (result posting.JournalEntry, resultErr error) {
	if original.CompanyID == uuid.Nil || original.CompanyID != reversal.CompanyID {
		return posting.JournalEntry{}, posting.ErrInvalidReversal
	}
	tx, err := d.beginCompanyTx(ctx, reversal.CompanyID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback reversal transaction: %w", rollbackErr))
		}
	}()

	var originalData []byte
	if err := tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		original.CompanyID, original.ID).Scan(&originalData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	var currentOriginal posting.JournalEntry
	if err := json.Unmarshal(originalData, &currentOriginal); err != nil {
		return posting.JournalEntry{}, err
	}
	if currentOriginal.Status != posting.EntryPosted || currentOriginal.ReversedEntryID != nil {
		return posting.JournalEntry{}, posting.ErrEntryAlreadyReversed
	}
	var reversalStatus posting.EntryStatus
	if err := tx.QueryRow(ctx,
		`SELECT status FROM account_moves WHERE company_id=$1 AND id=$2 AND journal_id=$3 FOR UPDATE`,
		reversal.CompanyID, reversal.ID, reversal.JournalID).Scan(&reversalStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	if reversalStatus != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}

	var sequence int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		reversal.CompanyID, reversal.JournalID).Scan(&sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	sequence++
	if _, err := tx.Exec(ctx,
		`UPDATE journals SET current_sequence=$3 WHERE company_id=$1 AND id=$2`,
		reversal.CompanyID, reversal.JournalID, sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	reversal.SequenceNumber = sequence
	if err := d.saveEntryTx(ctx, tx, reversal); err != nil {
		return posting.JournalEntry{}, err
	}
	reversalID := reversal.ID
	currentOriginal.ReversedEntryID = &reversalID
	currentOriginal.Status = posting.EntryReversed
	currentOriginal.UpdatedAt = time.Now().UTC()
	if err := d.saveEntryTx(ctx, tx, currentOriginal); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return posting.JournalEntry{}, err
	}
	return reversal, nil
}

func (d *Driver) saveEntryTx(ctx context.Context, tx pgx.Tx, entry posting.JournalEntry) error {
	if entry.ID == uuid.Nil || entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
		return posting.ErrInvalidEntry
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	classification := entry.Classification
	if classification == "" {
		classification = posting.EntryClassRegular
	}
	date := entry.EntryDate
	if date.IsZero() {
		date = entry.Date
	}
	if entry.Status == posting.EntryPosted || entry.Status == posting.EntryReversed {
		expectedStatus := posting.EntryDraft
		if entry.Status == posting.EntryReversed {
			expectedStatus = posting.EntryPosted
		}
		result, err := tx.Exec(ctx,
			`UPDATE account_moves SET journal_id=$3,period_id=$4,entry_date=$5,entry_number=$6,
			 status=$7,classification=$8,reversed_move_id=$9,entry=$10
			 WHERE id=$1 AND company_id=$2 AND status=$11`,
			entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.SequenceNumber,
			entry.Status, classification, entry.ReversedEntryID, data, expectedStatus)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return posting.ErrEntryNotDraft
		}
		return nil
	}
	if entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	result, err := tx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,entry_number,status,classification,reversed_move_id,entry) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	 ON CONFLICT(id) DO UPDATE SET journal_id=EXCLUDED.journal_id,period_id=EXCLUDED.period_id,
	 entry_date=EXCLUDED.entry_date,entry_number=EXCLUDED.entry_number,status=EXCLUDED.status,
	 classification=EXCLUDED.classification,reversed_move_id=EXCLUDED.reversed_move_id,entry=EXCLUDED.entry
	 WHERE account_moves.status='draft'`,
		entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.SequenceNumber,
		entry.Status, classification, entry.ReversedEntryID, data)
	if err != nil {
		return err
	}
	if affected := result.RowsAffected(); affected != 1 {
		return posting.ErrEntryNotDraft
	}
	if _, err := tx.Exec(ctx, `DELETE FROM account_move_lines WHERE move_id=$1`, entry.ID); err != nil {
		return err
	}
	for _, line := range entry.Lines {
		lineData, err := json.Marshal(line)
		if err != nil {
			return err
		}
		transactionAmount := line.TransactionAmount
		if transactionAmount.Currency() == "" {
			transactionAmount = line.Debit
			if transactionAmount.Amount().IsZero() {
				transactionAmount = line.Credit
			}
		}
		transactionCurrency := line.TransactionCurrency
		if transactionCurrency == "" {
			transactionCurrency = line.Currency
		}
		if transactionCurrency == "" {
			transactionCurrency = transactionAmount.Currency()
		}
		transactionMinorUnits, err := transactionAmount.MinorUnits()
		if err != nil {
			return err
		}
		zeroFunctional, err := financialtypes.Zero(transactionCurrency)
		if err != nil {
			return err
		}
		functionalDebit, functionalCredit := zeroFunctional, zeroFunctional
		if line.FunctionalDebit.Currency() != "" && line.FunctionalCredit.Currency() != "" {
			functionalDebit, functionalCredit = line.FunctionalDebit, line.FunctionalCredit
		} else if line.AmountCurrency.Currency() != "" {
			zero, zeroErr := financialtypes.Zero(line.AmountCurrency.Currency())
			if zeroErr != nil {
				return zeroErr
			}
			if !line.Debit.Amount().IsZero() {
				functionalDebit, functionalCredit = line.AmountCurrency, zero
			} else {
				functionalDebit, functionalCredit = zero, line.AmountCurrency
			}
		} else if !line.Debit.Amount().IsZero() {
			functionalDebit = line.Debit
		} else {
			functionalCredit = line.Credit
		}
		functionalDebitMinor, err := functionalDebit.MinorUnits()
		if err != nil {
			return err
		}
		functionalCreditMinor, err := functionalCredit.MinorUnits()
		if err != nil {
			return err
		}
		exchangeRate := line.ExchangeRate
		if exchangeRate.IsZero() {
			exchangeRate = decimal.NewFromInt(1)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_move_lines (
			move_id,company_id,line_id,account_id,partner_id,cost_center_id,project_id,segment_id,business_unit_id,matching_number,reconciled_amount,
			debit,credit,amount_currency,currency_id,transaction_amount_minor,transaction_currency,exchange_rate,
			functional_debit_minor,functional_credit_minor,line_data
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT(move_id,line_id) DO UPDATE SET
			account_id=EXCLUDED.account_id, debit=EXCLUDED.debit, credit=EXCLUDED.credit,
			transaction_amount_minor=EXCLUDED.transaction_amount_minor,
			transaction_currency=EXCLUDED.transaction_currency, exchange_rate=EXCLUDED.exchange_rate,
			functional_debit_minor=EXCLUDED.functional_debit_minor,
			functional_credit_minor=EXCLUDED.functional_credit_minor, line_data=EXCLUDED.line_data`,
			entry.ID, entry.CompanyID, line.ID, line.AccountID, line.PartnerID, line.CostCenterID, line.ProjectID, line.SegmentID, line.BusinessUnitID, line.MatchingNumber,
			line.ReconciledAmount, line.Debit.Amount(), line.Credit.Amount(), line.AmountCurrency.Amount(),
			transactionCurrency, transactionMinorUnits, transactionCurrency, exchangeRate,
			functionalDebitMinor, functionalCreditMinor, lineData); err != nil {
			return err
		}
	}
	return nil
}
func (d *Driver) GetNextSequence(ctx context.Context, companyID, id uuid.UUID) (int64, error) {
	return d.GetNextSequenceWithLock(ctx, companyID, id)
}

func (d *Driver) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	return d.GetNextSequenceWithLock(ctx, companyID, journalID)
}
func (d *Driver) PostBulk(ctx context.Context, entries []posting.JournalEntry) error {
	if len(entries) == 0 {
		return nil
	}
	companyID := entries[0].CompanyID
	if companyID == uuid.Nil {
		return posting.ErrInvalidEntry
	}
	for _, entry := range entries {
		if entry.CompanyID != companyID {
			return masterdata.ErrCompanyScopeRequired
		}
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, entry := range entries {
		if err := d.saveEntryTx(ctx, tx, entry); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) ReadAccountPeriodBalancesForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	if companyID == uuid.Nil || end.IsZero() || (!start.IsZero() && end.Before(start)) {
		return nil, false, nil
	}
	var result map[uuid.UUID]posting.AccountPeriodBalance
	available := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var functionalCurrency string
		if err := tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, companyID).
			Scan(&functionalCurrency); err != nil {
			return err
		}
		if functionalCurrency != currency {
			return nil
		}

		var yearID uuid.UUID
		var yearStart time.Time
		if err := tx.QueryRow(ctx, `SELECT id,start_date FROM fiscal_years
			WHERE company_id=$1 AND start_date <= $2::date AND end_date >= $2::date
			ORDER BY start_date DESC LIMIT 1`, companyID, end).
			Scan(&yearID, &yearStart); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !start.IsZero() && start.Before(yearStart) {
			return nil
		}

		type periodRange struct {
			id     uuid.UUID
			start  time.Time
			end    time.Time
			number int
		}
		rows, err := tx.Query(ctx, `SELECT id,start_date,end_date,COALESCE(period_number,0)
			FROM fiscal_periods WHERE company_id=$1 AND year_id=$2
			ORDER BY start_date,period_number`, companyID, yearID)
		if err != nil {
			return err
		}
		periods := make([]periodRange, 0)
		for rows.Next() {
			var period periodRange
			if err := rows.Scan(&period.id, &period.start, &period.end, &period.number); err != nil {
				rows.Close()
				return err
			}
			periods = append(periods, period)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(periods) == 0 {
			return nil
		}

		rangeStart := start
		if rangeStart.IsZero() {
			rangeStart = yearStart
		}
		first, last := -1, -1
		for i, period := range periods {
			if period.start.Before(rangeStart) && !period.end.Before(rangeStart) {
				return nil
			}
			if !period.start.After(end) && period.end.After(end) {
				return nil
			}
			if !period.start.Before(rangeStart) && !period.end.After(end) {
				if first == -1 {
					first = i
				}
				last = i
			}
		}
		if first == -1 || last == -1 {
			return nil
		}
		if !includeAdjusting {
			var hasAdjusting bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM account_moves moves
				JOIN fiscal_periods period
				  ON period.company_id=moves.company_id AND period.id=moves.period_id
				WHERE moves.company_id=$1 AND period.year_id=$2
				  AND period.end_date <= $3 AND moves.status IN ('posted','reversed')
				  AND moves.classification='adjusting'
			)`, companyID, yearID, periods[last].end).Scan(&hasAdjusting); err != nil {
				return err
			}
			if hasAdjusting {
				return nil
			}
		}

		rows, err = tx.Query(ctx, `SELECT balances.account_id,balances.beginning_debit,
			balances.beginning_credit,balances.period_debit,balances.period_credit,
			period.id,period.start_date,period.end_date
			FROM account_balances balances
			JOIN fiscal_periods period
			  ON period.company_id=balances.company_id AND period.id=balances.fiscal_period_id
			WHERE balances.company_id=$1 AND period.year_id=$2
			  AND period.end_date <= $3 AND balances.currency_id=$4
			ORDER BY period.start_date,period.period_number`, companyID, yearID, periods[last].end, currency)
		if err != nil {
			return err
		}
		balanceMap := make(map[uuid.UUID]posting.AccountPeriodBalance)
		periodIndexes := make(map[uuid.UUID]int, len(periods))
		for i, period := range periods {
			periodIndexes[period.id] = i
		}
		for rows.Next() {
			var accountID, periodID uuid.UUID
			var beginningDebit, beginningCredit, periodDebit, periodCredit decimal.Decimal
			var periodStart, periodEnd time.Time
			if err := rows.Scan(&accountID, &beginningDebit, &beginningCredit, &periodDebit, &periodCredit,
				&periodID, &periodStart, &periodEnd); err != nil {
				rows.Close()
				return err
			}
			index, ok := periodIndexes[periodID]
			if !ok {
				continue
			}
			balance := balanceMap[accountID]
			balance.AccountID = accountID
			if !beginningDebit.IsZero() {
				balance.BeginningDebit = balance.BeginningDebit.Add(beginningDebit)
			}
			if !beginningCredit.IsZero() {
				balance.BeginningCredit = balance.BeginningCredit.Add(beginningCredit)
			}
			if index < first {
				if !periodDebit.IsZero() {
					balance.BeginningDebit = balance.BeginningDebit.Add(periodDebit)
				}
				if !periodCredit.IsZero() {
					balance.BeginningCredit = balance.BeginningCredit.Add(periodCredit)
				}
			} else {
				if balance.PeriodDebit.IsZero() {
					balance.PeriodDebit = periodDebit
				} else if !periodDebit.IsZero() {
					balance.PeriodDebit = balance.PeriodDebit.Add(periodDebit)
				}
				if balance.PeriodCredit.IsZero() {
					balance.PeriodCredit = periodCredit
				} else if !periodCredit.IsZero() {
					balance.PeriodCredit = balance.PeriodCredit.Add(periodCredit)
				}
			}
			balance.EndingDebit = balance.BeginningDebit
			if !balance.PeriodDebit.IsZero() {
				if balance.EndingDebit.IsZero() {
					balance.EndingDebit = balance.PeriodDebit
				} else {
					balance.EndingDebit = balance.EndingDebit.Add(balance.PeriodDebit)
				}
			}
			balance.EndingCredit = balance.BeginningCredit
			if !balance.PeriodCredit.IsZero() {
				if balance.EndingCredit.IsZero() {
					balance.EndingCredit = balance.PeriodCredit
				} else {
					balance.EndingCredit = balance.EndingCredit.Add(balance.PeriodCredit)
				}
			}
			balanceMap[accountID] = balance
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		result = balanceMap
		available = true
		return nil
	})
	return result, available, err
}

func (d *Driver) ListPostedEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return d.ListEntries(ctx, start, end)
}
func (d *Driver) ListEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return nil, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListEntriesForCompany(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	result := make([]posting.JournalEntry, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT entry FROM account_moves
			WHERE company_id=$1 AND status=$2 AND entry_date BETWEEN $3 AND $4
			ORDER BY entry_date`, companyID, posting.EntryPosted, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var entry posting.JournalEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return err
			}
			result = append(result, entry)
		}
		return rows.Err()
	})
	return result, err
}

func (d *Driver) GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	account, err := d.GetAccountByIDForCompany(ctx, companyID, accountID)
	if err != nil {
		return financialtypes.Money{}, err
	}
	var functionalCurrency string
	if err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, companyID).Scan(&functionalCurrency)
	}); err != nil {
		return financialtypes.Money{}, err
	}
	entries, err := d.ListEntriesForCompany(ctx, companyID, time.Time{}, time.Now().UTC())
	if err != nil {
		return financialtypes.Money{}, err
	}
	return subledgers.ControlAccountBalanceFromEntries(account, entries, functionalCurrency)
}

func (d *Driver) SaveEvent(ctx context.Context, event subledgers.EconomicEvent) error {
	if event.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, event.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO subledger_events(company_id,idempotency_key,event) VALUES($1,$2,$3)
			 ON CONFLICT(company_id,idempotency_key) DO NOTHING`,
			event.CompanyID, event.IdempotencyKey, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return subledgers.ErrAlreadyExists
		}
		return nil
	})
}

func (d *Driver) SaveEventAccountingRule(ctx context.Context, rule subledgers.EventAccountingRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, rule.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO event_accounting_rules(id,company_id,event_type,priority,rule_data)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(company_id,event_type,priority) DO UPDATE
			 SET id=EXCLUDED.id,rule_data=EXCLUDED.rule_data`,
			rule.ID, rule.CompanyID, rule.EventType, rule.Priority, data)
		return err
	})
}

func (d *Driver) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	rules := make([]subledgers.EventAccountingRule, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT rule_data FROM event_accounting_rules
			 WHERE company_id=$1 AND event_type=$2 ORDER BY priority DESC, id`,
			companyID, eventType)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var rule subledgers.EventAccountingRule
			if err := json.Unmarshal(data, &rule); err != nil {
				return err
			}
			rules = append(rules, rule)
		}
		return rows.Err()
	})
	return rules, err
}

func (d *Driver) SaveEventAndDraft(ctx context.Context, event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	tx, err := d.beginCompanyTx(ctx, event.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`INSERT INTO subledger_events(company_id,idempotency_key,event)
		 VALUES($1,$2,$3) ON CONFLICT(company_id,idempotency_key) DO NOTHING`,
		event.CompanyID, event.IdempotencyKey, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return subledgers.ErrAlreadyExists
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetEventByIdempotencyKey(ctx context.Context, key string) (subledgers.EconomicEvent, bool, error) {
	return subledgers.EconomicEvent{}, false, subledgers.ErrCompanyScopedEventsRequired
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	var event subledgers.EconomicEvent
	found := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT event FROM subledger_events WHERE company_id=$1 AND idempotency_key=$2`,
			companyID, key).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return json.Unmarshal(data, &event)
	})
	return event, found, err
}
func (d *Driver) SaveOpenLine(ctx context.Context, line subledgers.OpenLine) error {
	return d.SaveOpenLines(ctx, []subledgers.OpenLine{line})
}

func (d *Driver) SaveOpenLineForPostedEntry(ctx context.Context, line subledgers.OpenLine) error {
	if line.ID == uuid.Nil || line.CompanyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, line.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	account, err := scanAccount(tx.QueryRow(ctx,
		accountSelect+` WHERE company_id=$1 AND id=$2 FOR SHARE`, line.CompanyID, line.ControlAccountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return masterdata.ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE id=$1 AND company_id=$2 AND status='posted' FOR SHARE`,
		line.JournalEntryID, line.CompanyID).Scan(&entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotPosted
	}
	if err != nil {
		return err
	}
	var entry posting.JournalEntry
	if err := json.Unmarshal(entryData, &entry); err != nil {
		return err
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	var partner any
	if line.PartnerID != nil {
		partner = *line.PartnerID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5)`,
		line.ID, line.CompanyID, partner, line.Kind, data); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) SaveOpenLines(ctx context.Context, lines []subledgers.OpenLine) error {
	if len(lines) == 0 {
		return nil
	}
	companyID := lines[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	for _, line := range lines {
		if line.CompanyID != companyID {
			return subledgers.ErrCompanyMismatch
		}
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, line := range lines {
		if line.ID == uuid.Nil || line.CompanyID == uuid.Nil {
			return subledgers.ErrInvalidEvent
		}
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(id) DO UPDATE SET partner_id=EXCLUDED.partner_id,kind=EXCLUDED.kind,line_data=EXCLUDED.line_data
			 WHERE subledger_open_lines.company_id=EXCLUDED.company_id`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveOpenLines(ctx context.Context, expected, updated []subledgers.OpenLine) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID != companyID || updated[i].ID != line.ID ||
			updated[i].CompanyID != line.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[line.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[line.ID] = line
		ids = append(ids, line.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT id, company_id, line_data FROM subledger_open_lines
		 WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.OpenLine
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldLine, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldLine.CompanyID || current.CompanyID != oldLine.CompanyID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			rows.Close()
			return subledgers.ErrConcurrentMatch
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	for _, line := range updated {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`UPDATE subledger_open_lines
			 SET partner_id=$3, kind=$4, line_data=$5
			 WHERE id=$1 AND company_id=$2`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentMatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveOpenLinesAndDraft(ctx context.Context, expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil || draft.CompanyID != companyID {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID != companyID || updated[i].ID != line.ID ||
			updated[i].CompanyID != line.CompanyID || draft.CompanyID != line.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[line.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[line.ID] = line
		ids = append(ids, line.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT id, company_id, line_data FROM subledger_open_lines
		 WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.OpenLine
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldLine, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldLine.CompanyID || current.CompanyID != oldLine.CompanyID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			rows.Close()
			return subledgers.ErrConcurrentMatch
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	if err := d.saveEntryTx(ctx, tx, draft); err != nil {
		return err
	}
	for _, line := range updated {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`UPDATE subledger_open_lines
			 SET partner_id=$3, kind=$4, line_data=$5
			 WHERE id=$1 AND company_id=$2`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentMatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetOpenLine(ctx context.Context, companyID, id uuid.UUID) (subledgers.OpenLine, error) {
	var line subledgers.OpenLine
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT line_data FROM subledger_open_lines WHERE company_id=$1 AND id=$2`,
			companyID, id).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &line)
	})
	return line, err
}

func (d *Driver) ConfirmOpenLineWriteOff(ctx context.Context, expected, updated subledgers.OpenLine, postedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || postedEntry.CompanyID != expected.CompanyID ||
		postedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT line_data FROM subledger_open_lines WHERE id=$1 AND company_id=$2 FOR UPDATE`,
		expected.ID, expected.CompanyID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.OpenLine
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentMatch
	}
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE id=$1 AND company_id=$2 AND status='posted' FOR SHARE`,
		postedEntry.ID, postedEntry.CompanyID).Scan(&entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotPosted
	}
	if err != nil {
		return err
	}
	var actualEntry posting.JournalEntry
	if err := json.Unmarshal(entryData, &actualEntry); err != nil {
		return err
	}
	if !subledgers.FixedAssetEntryMatches(actualEntry, postedEntry) {
		return posting.ErrEntryNotPosted
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`UPDATE subledger_open_lines SET line_data=$3 WHERE id=$1 AND company_id=$2`,
		updated.ID, updated.CompanyID, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return subledgers.ErrConcurrentMatch
	}
	return tx.Commit(ctx)
}

func (d *Driver) ListOpenLines(ctx context.Context, partnerID string) ([]subledgers.OpenLine, error) {
	return nil, subledgers.ErrCompanyScopedOpenLinesRequired
}

func (d *Driver) ListOpenLinesForCompany(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	query := `SELECT line_data FROM subledger_open_lines WHERE company_id=$1`
	args := []any{companyID}
	if partnerID != "" {
		query += ` AND partner_id=$2`
		args = append(args, partnerID)
	}
	result := make([]subledgers.OpenLine, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var line subledgers.OpenLine
			if err := json.Unmarshal(data, &line); err != nil {
				return err
			}
			result = append(result, line)
		}
		return rows.Err()
	})
	return result, err
}
func (d *Driver) SaveFixedAsset(ctx context.Context, asset subledgers.FixedAsset) error {
	if asset.CompanyID == uuid.Nil {
		return subledgers.ErrInvalidAsset
	}
	data, err := json.Marshal(asset)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, asset.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO fixed_assets(id,company_id,asset_data) VALUES($1,$2,$3)
			 ON CONFLICT(id) DO UPDATE SET asset_data=EXCLUDED.asset_data
			 WHERE fixed_assets.company_id=EXCLUDED.company_id`,
			asset.ID, asset.CompanyID, data)
		if err == nil && result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
		return err
	})
}

func (d *Driver) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	return d.ListOpenLinesForCompany(ctx, companyID, partnerID)
}

func (d *Driver) SaveFixedAssetDraft(ctx context.Context, expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
		return subledgers.ErrInvalidAsset
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expected.CompanyID, expected.ID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.FixedAsset
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:DEPRECIATION:") ||
		strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:REVALUATION:") {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM account_moves
				WHERE company_id=$1 AND status IN ('draft', 'posted')
				  AND entry->>'DocumentReference'=$2
			)`,
			entry.CompanyID, entry.DocumentReference).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return subledgers.ErrAlreadyExists
		}
	}
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) ConfirmFixedAssetMutation(ctx context.Context, expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidAsset
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expected.CompanyID, expected.ID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.FixedAsset
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}

	var moveStatus posting.EntryStatus
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT status, entry FROM account_moves
		 WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expectedEntry.CompanyID, expectedEntry.ID).Scan(&moveStatus, &entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotFound
	}
	if err != nil {
		return err
	}
	var actualEntry posting.JournalEntry
	if err := json.Unmarshal(entryData, &actualEntry); err != nil {
		return err
	}
	if moveStatus != posting.EntryPosted || !subledgers.FixedAssetEntryMatches(actualEntry, expectedEntry) {
		return posting.ErrEntryNotPosted
	}

	updatedData, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`UPDATE fixed_assets SET asset_data=$3 WHERE company_id=$1 AND id=$2`,
		updated.CompanyID, updated.ID, updatedData)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return subledgers.ErrCompanyMismatch
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetFixedAsset(ctx context.Context, companyID, id uuid.UUID) (subledgers.FixedAsset, error) {
	var asset subledgers.FixedAsset
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx, `SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2`,
			companyID, id).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &asset)
	})
	return asset, err
}

func (d *Driver) ListFixedAssets(ctx context.Context) ([]subledgers.FixedAsset, error) {
	return nil, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListFixedAssetsForCompany(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	assets := make([]subledgers.FixedAsset, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT asset_data FROM fixed_assets WHERE company_id=$1`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var asset subledgers.FixedAsset
			if err := json.Unmarshal(data, &asset); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}
func (d *Driver) SaveBankItem(ctx context.Context, item subledgers.BankItem) error {
	if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.AccountID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, item.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO bank_items(company_id,account_id,item_id,item_data) VALUES($1,$2,$3,$4)
			 ON CONFLICT(item_id) DO UPDATE SET item_data=EXCLUDED.item_data
			 WHERE bank_items.company_id=EXCLUDED.company_id AND bank_items.account_id=EXCLUDED.account_id`,
			item.CompanyID, item.AccountID, item.ID, data)
		if err == nil && result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
		return err
	})
}
func (d *Driver) ListBankItems(ctx context.Context, accountID string) ([]subledgers.BankItem, error) {
	return nil, subledgers.ErrCompanyScopedBankItemsRequired
}

func (d *Driver) ListFixedAssetsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	return d.ListFixedAssetsForCompany(ctx, companyID)
}

func (d *Driver) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	result := make([]subledgers.BankItem, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT item_data FROM bank_items WHERE company_id=$1 AND account_id=$2::uuid`,
			companyID, accountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var item subledgers.BankItem
			if err := json.Unmarshal(data, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}

func (d *Driver) CompareAndSaveBankItems(ctx context.Context, expected, updated []subledgers.BankItem) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID != companyID || item.AccountID == uuid.Nil ||
			updated[i].ID != item.ID || updated[i].CompanyID != item.CompanyID ||
			updated[i].AccountID != item.AccountID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[item.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[item.ID] = item
		ids = append(ids, item.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT item_id, company_id, account_id, item_data FROM bank_items
		 WHERE item_id = ANY($1) ORDER BY item_id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID, accountID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &accountID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.BankItem
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldItem, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldItem.CompanyID || accountID != oldItem.AccountID ||
			current.CompanyID != oldItem.CompanyID || current.AccountID != oldItem.AccountID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldItem) {
			rows.Close()
			return subledgers.ErrConcurrentBankItem
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	for _, item := range updated {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx,
			`UPDATE bank_items SET item_data=$4
			 WHERE item_id=$1 AND company_id=$2 AND account_id=$3`,
			item.ID, item.CompanyID, item.AccountID, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentBankItem
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveBankItemsAndDraft(ctx context.Context, expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil || draft.CompanyID != companyID {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID != companyID || item.AccountID == uuid.Nil ||
			updated[i].ID != item.ID || updated[i].CompanyID != item.CompanyID ||
			updated[i].AccountID != item.AccountID || draft.CompanyID != item.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[item.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[item.ID] = item
		ids = append(ids, item.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT item_id, company_id, account_id, item_data FROM bank_items
		 WHERE item_id = ANY($1) ORDER BY item_id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID, accountID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &accountID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.BankItem
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldItem, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldItem.CompanyID || accountID != oldItem.AccountID ||
			current.CompanyID != oldItem.CompanyID || current.AccountID != oldItem.AccountID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldItem) {
			rows.Close()
			return subledgers.ErrConcurrentBankItem
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	if err := d.saveEntryTx(ctx, tx, draft); err != nil {
		return err
	}
	for _, item := range updated {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx,
			`UPDATE bank_items SET item_data=$4
			 WHERE item_id=$1 AND company_id=$2 AND account_id=$3`,
			item.ID, item.CompanyID, item.AccountID, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentBankItem
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return decimal.Zero, masterdata.ErrCompanyScopeRequired
}
func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	return decimal.Zero, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetRateForCompany(ctx context.Context, companyID uuid.UUID, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.getRateForCompany(ctx, companyID, "", from, to, date)
}

func (d *Driver) GetRateOfTypeForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	return d.getRateForCompany(ctx, companyID, rateType, from, to, date)
}

func (d *Driver) getRateForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	if from == to {
		return decimal.NewFromInt(1), nil
	}
	query := `SELECT rate FROM exchange_rates
		WHERE company_id=$1 AND lower(from_currency)=lower($2) AND lower(to_currency)=lower($3)
		  AND effective_date<=$4::date`
	args := []any{companyID, from, to, date}
	if rateType != "" {
		query += ` AND rate_type=$5`
		args = append(args, rateType)
	}
	query += ` ORDER BY effective_date DESC LIMIT 1`
	var rate decimal.Decimal
	inverseRate := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, query, args...).Scan(&rate)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		inverseRate = true
		query = `SELECT rate FROM exchange_rates
			WHERE company_id=$1 AND lower(from_currency)=lower($3) AND lower(to_currency)=lower($2)
			  AND effective_date<=$4::date`
		args = []any{companyID, from, to, date}
		if rateType != "" {
			query += ` AND rate_type=$5`
			args = append(args, rateType)
		}
		query += ` ORDER BY effective_date DESC LIMIT 1`
		err = tx.QueryRow(ctx, query, args...).Scan(&rate)
		if errors.Is(err, pgx.ErrNoRows) {
			return valuation.ErrRateNotFound
		}
		return err
	})
	if err != nil {
		return decimal.Zero, err
	}
	if inverseRate {
		return decimal.NewFromInt(1).DivRound(rate, financialtypes.InternalScale), nil
	}
	return rate, nil
}

func (d *Driver) UpsertExchangeRate(ctx context.Context, companyID uuid.UUID, rate valuation.ExchangeRate) error {
	if companyID == uuid.Nil {
		return masterdata.ErrInvalidCompany
	}
	if err := rate.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO exchange_rates(
			company_id,from_currency,to_currency,rate,effective_date,rate_type
		) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(company_id,from_currency,to_currency,effective_date,rate_type)
		DO UPDATE SET rate=EXCLUDED.rate`,
			companyID, strings.ToUpper(strings.TrimSpace(rate.FromCurrency)),
			strings.ToUpper(strings.TrimSpace(rate.ToCurrency)), rate.Rate,
			rate.EffectiveDate, rate.Type)
		return err
	})
}

func (d *Driver) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	return d.WithCompanyTx(ctx, log.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO financial_audit_logs(company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,timestamp,previous_log_hash,current_log_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			log.CompanyID, log.EntityType, log.EntityID, log.Action, log.ActorID, log.PayloadBefore, log.PayloadAfter, log.Timestamp, log.PreviousLogHash, log.CurrentLogHash)
		return err
	})
}

func (d *Driver) AppendAuditLogAtomic(ctx context.Context, log governance.FinancialAuditLog) (governance.FinancialAuditLog, error) {
	if log.CompanyID == uuid.Nil {
		return governance.FinancialAuditLog{}, errors.New("audit record requires company id")
	}
	tx, err := d.beginCompanyTx(ctx, log.CompanyID)
	if err != nil {
		return governance.FinancialAuditLog{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	log, err = appendAuditLogTx(ctx, tx, log)
	if err != nil {
		return governance.FinancialAuditLog{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return governance.FinancialAuditLog{}, err
	}
	return log, nil
}

func appendAuditLogTx(ctx context.Context, tx pgx.Tx, log governance.FinancialAuditLog) (governance.FinancialAuditLog, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, log.CompanyID.String()); err != nil {
		return governance.FinancialAuditLog{}, err
	}
	var previousHash string
	err := tx.QueryRow(ctx,
		`SELECT current_log_hash FROM financial_audit_logs
		 WHERE company_id=$1 ORDER BY id DESC LIMIT 1`, log.CompanyID).Scan(&previousHash)
	if errors.Is(err, pgx.ErrNoRows) {
		previousHash = governance.GenesisHash
	} else if err != nil {
		return governance.FinancialAuditLog{}, err
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now().UTC()
	} else {
		log.Timestamp = log.Timestamp.UTC()
	}
	log.PreviousLogHash = previousHash
	log.CurrentLogHash = governance.CalculateHash(log)
	err = tx.QueryRow(ctx, `INSERT INTO financial_audit_logs(
		company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,
		timestamp,previous_log_hash,current_log_hash
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		log.CompanyID, log.EntityType, log.EntityID, log.Action, log.ActorID,
		log.PayloadBefore, log.PayloadAfter, log.Timestamp, log.PreviousLogHash, log.CurrentLogHash).Scan(&log.ID)
	if err != nil {
		return governance.FinancialAuditLog{}, err
	}
	return log, nil
}

func (d *Driver) GetLastAuditLogForCompany(ctx context.Context, companyID uuid.UUID) (*governance.FinancialAuditLog, error) {
	var log governance.FinancialAuditLog
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,
			payload_before,payload_after,timestamp,previous_log_hash,current_log_hash
			FROM financial_audit_logs WHERE company_id=$1 ORDER BY id DESC LIMIT 1`, companyID).Scan(
			&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action, &log.ActorID,
			&log.PayloadBefore, &log.PayloadAfter, &log.Timestamp, &log.PreviousLogHash, &log.CurrentLogHash)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &log, nil
}
func (d *Driver) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	return nil, governance.ErrCompanyAuditScopeRequired
}
func (d *Driver) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	return governance.ErrCompanyAuditScopeRequired
}

func (d *Driver) IterateAuditLogsForCompany(ctx context.Context, companyID uuid.UUID, fromID int64, handler governance.AuditLogHandler) error {
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,
			payload_before,payload_after,timestamp,previous_log_hash,current_log_hash
			FROM financial_audit_logs WHERE company_id=$1 AND id >= $2 ORDER BY id`,
			companyID, fromID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var log governance.FinancialAuditLog
			if err := rows.Scan(&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action,
				&log.ActorID, &log.PayloadBefore, &log.PayloadAfter, &log.Timestamp,
				&log.PreviousLogHash, &log.CurrentLogHash); err != nil {
				return err
			}
			if err := handler(log); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}
