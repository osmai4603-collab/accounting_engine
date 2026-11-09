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
	"sort"
	"strings"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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

func New(ctx context.Context, connectionString string) (*Driver, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := ensureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := seedCurrencyRegistry(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Driver{pool: pool}, nil
}

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("unable to locate postgres schema files")
	}
	baseDir := filepath.Dir(file)
	files := []string{filepath.Join(baseDir, "schema.sql")}
	entries, err := os.ReadDir(filepath.Join(baseDir, "migrations"))
	if err == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".sql") {
				files = append(files, filepath.Join(baseDir, "migrations", entry.Name()))
			}
		}
		sort.Strings(files[1:])
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			return err
		}
	}
	return nil
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

func (d *Driver) CreateCompany(company masterdata.Company) error {
	company.Code = strings.ToUpper(strings.TrimSpace(company.Code))
	company.FunctionalCurrency = strings.ToUpper(strings.TrimSpace(company.FunctionalCurrency))
	company.PresentationCurrency = strings.ToUpper(strings.TrimSpace(company.PresentationCurrency))
	if err := company.Validate(); err != nil {
		return err
	}
	_, err := d.pool.Exec(context.Background(),
		`INSERT INTO companies(id, code, legal_name, functional_currency, presentation_currency, is_active)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(id) DO UPDATE SET code=EXCLUDED.code, legal_name=EXCLUDED.legal_name,
		 functional_currency=EXCLUDED.functional_currency, presentation_currency=EXCLUDED.presentation_currency,
		 is_active=EXCLUDED.is_active`,
		company.ID, company.Code, company.LegalName, company.FunctionalCurrency,
		company.PresentationCurrency, company.Active)
	return err
}

func (d *Driver) GetCompanyByID(id uuid.UUID) (masterdata.Company, error) {
	var company masterdata.Company
	err := d.pool.QueryRow(context.Background(),
		`SELECT id, code, legal_name, functional_currency, presentation_currency, is_active
		 FROM companies WHERE id=$1`, id).Scan(
		&company.ID, &company.Code, &company.LegalName, &company.FunctionalCurrency,
		&company.PresentationCurrency, &company.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return masterdata.Company{}, masterdata.ErrInvalidCompany
	}
	return company, err
}

const accountSelect = `SELECT id, company_id, code, name, account_type, normal_balance, currency_id, parent_id,
	is_reconcilable, is_contra_account, is_active, is_cash_equivalent, is_non_cash_adjustment,
	cash_flow_activity, sub_classification, ifrs_category, display_order, created_at, updated_at
	FROM accounts`

func scanAccount(row pgx.Row) (masterdata.Account, error) {
	var account masterdata.Account
	err := row.Scan(
		&account.ID, &account.CompanyID, &account.Code, &account.Name, &account.AccountType, &account.NormalBalance,
		&account.CurrencyID, &account.ParentID, &account.IsReconcilable, &account.IsContraAccount, &account.IsActive,
		&account.IsCashEquivalent, &account.IsNonCashAdjustment, &account.CashFlowActivity,
		&account.SubClassification, &account.IFRSCategory, &account.DisplayOrder,
		&account.CreatedAt, &account.UpdatedAt,
	)
	return account, err
}

func (d *Driver) CreateAccount(account masterdata.Account) error {
	_, err := d.pool.Exec(context.Background(),
		`INSERT INTO accounts (
			id, company_id, code, name, account_type, normal_balance, currency_id, parent_id,
			is_reconcilable, is_contra_account, is_active, is_cash_equivalent, is_non_cash_adjustment,
			cash_flow_activity, sub_classification, ifrs_category, display_order, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT(id) DO UPDATE SET
			company_id=EXCLUDED.company_id, code=EXCLUDED.code, name=EXCLUDED.name, account_type=EXCLUDED.account_type,
			normal_balance=EXCLUDED.normal_balance, currency_id=EXCLUDED.currency_id,
			parent_id=EXCLUDED.parent_id, is_reconcilable=EXCLUDED.is_reconcilable,
			is_contra_account=EXCLUDED.is_contra_account,
			is_active=EXCLUDED.is_active, is_cash_equivalent=EXCLUDED.is_cash_equivalent,
			is_non_cash_adjustment=EXCLUDED.is_non_cash_adjustment,
			cash_flow_activity=EXCLUDED.cash_flow_activity,
			sub_classification=EXCLUDED.sub_classification, ifrs_category=EXCLUDED.ifrs_category,
			display_order=EXCLUDED.display_order, updated_at=EXCLUDED.updated_at`,
		account.ID, account.CompanyID, account.Code, account.Name, account.AccountType, account.NormalBalance,
		nullableString(account.CurrencyID, "SAR"), account.ParentID, account.IsReconcilable,
		account.IsContraAccount, account.IsActive, account.IsCashEquivalent, account.IsNonCashAdjustment,
		nullableString(account.CashFlowActivity, "unclassified"),
		nullableString(account.SubClassification, "unclassified"),
		nullableString(account.IFRSCategory, "unclassified"), account.DisplayOrder,
		defaultTime(account.CreatedAt), defaultTime(account.UpdatedAt))
	return err
}

func (d *Driver) GetAccountByID(id uuid.UUID) (masterdata.Account, error) {
	account, err := scanAccount(d.pool.QueryRow(context.Background(), accountSelect+" WHERE id=$1", id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.Account{}, masterdata.ErrAccountNotFound
		}
		return masterdata.Account{}, err
	}
	return account, nil
}

func (d *Driver) GetAccountByIDForCompany(companyID, accountID uuid.UUID) (masterdata.Account, error) {
	account, err := scanAccount(d.pool.QueryRow(context.Background(),
		accountSelect+" WHERE company_id=$1 AND id=$2", companyID, accountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.Account{}, masterdata.ErrAccountNotFound
		}
		return masterdata.Account{}, err
	}
	return account, nil
}

func (d *Driver) GetAccountByCodeForCompany(companyID uuid.UUID, code string) (masterdata.Account, error) {
	account, err := scanAccount(d.pool.QueryRow(context.Background(),
		accountSelect+" WHERE company_id=$1 AND lower(code)=lower($2)", companyID, code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.Account{}, masterdata.ErrAccountNotFound
		}
		return masterdata.Account{}, err
	}
	return account, nil
}

func (d *Driver) GetAccountByCode(code string) (masterdata.Account, error) {
	account, err := scanAccount(d.pool.QueryRow(context.Background(), accountSelect+" WHERE lower(code)=lower($1)", code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.Account{}, masterdata.ErrAccountNotFound
		}
		return masterdata.Account{}, err
	}
	return account, nil
}

func (d *Driver) ListAccounts() ([]masterdata.Account, error) {
	rows, err := d.pool.Query(context.Background(), accountSelect+" ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]masterdata.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, rows.Err()
}

func (d *Driver) ListAccountsForCompany(companyID uuid.UUID) ([]masterdata.Account, error) {
	rows, err := d.pool.Query(context.Background(), accountSelect+" WHERE company_id=$1 ORDER BY code", companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]masterdata.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, rows.Err()
}

func (d *Driver) HasChildren(id uuid.UUID) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM accounts WHERE parent_id=$1)`, id).Scan(&exists)
	return exists, err
}

func (d *Driver) CreateFiscalYear(year masterdata.FiscalYear) error {
	data, err := json.Marshal(year)
	if err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO fiscal_years(id,company_id,code,year,start_date,end_date,year_data)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,code=EXCLUDED.code,
		year=EXCLUDED.year,start_date=EXCLUDED.start_date,end_date=EXCLUDED.end_date,year_data=EXCLUDED.year_data`,
		year.ID, year.CompanyID, year.Name, year.StartDate.UTC().Year(), year.StartDate, year.EndDate, data); err != nil {
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

func (d *Driver) GetFiscalYear(id uuid.UUID) (masterdata.FiscalYear, error) {
	var data []byte
	var companyID uuid.UUID
	err := d.pool.QueryRow(context.Background(), `SELECT company_id, year_data FROM fiscal_years WHERE id=$1`, id).Scan(&companyID, &data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
		}
		return masterdata.FiscalYear{}, err
	}
	var year masterdata.FiscalYear
	if err := json.Unmarshal(data, &year); err != nil {
		return masterdata.FiscalYear{}, err
	}
	year.CompanyID = companyID
	return d.loadFiscalPeriods(year)
}

func (d *Driver) GetFiscalYearForCompany(companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(),
		`SELECT year_data FROM fiscal_years WHERE company_id=$1 AND id=$2`, companyID, fiscalYearID).Scan(&data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
		}
		return masterdata.FiscalYear{}, err
	}
	var year masterdata.FiscalYear
	if err := json.Unmarshal(data, &year); err != nil {
		return masterdata.FiscalYear{}, err
	}
	year.CompanyID = companyID
	return d.loadFiscalPeriods(year)
}

func (d *Driver) loadFiscalPeriods(year masterdata.FiscalYear) (masterdata.FiscalYear, error) {
	rows, err := d.pool.Query(context.Background(),
		`SELECT id, company_id, year_id, COALESCE(period_number, 0), COALESCE(name, ''),
		        start_date, end_date, state, period_type
		 FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 ORDER BY period_number`,
		year.CompanyID, year.ID)
	if err != nil {
		return masterdata.FiscalYear{}, err
	}
	defer rows.Close()
	periods := make([]masterdata.FiscalPeriod, 0, len(year.Periods))
	for rows.Next() {
		var period masterdata.FiscalPeriod
		if err := rows.Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type,
		); err != nil {
			return masterdata.FiscalYear{}, err
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return masterdata.FiscalYear{}, err
	}
	if len(periods) > 0 {
		year.Periods = periods
	}
	return year, nil
}

func (d *Driver) GetPeriodByDate(date time.Time) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.pool.QueryRow(context.Background(),
		`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type
		 FROM fiscal_periods WHERE start_date <= $1 AND end_date >= $1 AND (period_type = 'normal' OR period_type IS NULL) ORDER BY period_number ASC LIMIT 1`, date).Scan(
		&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
		&period.StartDate, &period.EndDate, &period.State, &period.Type,
	)
	return period, err
}

func (d *Driver) GetPeriodByDateForCompany(companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.pool.QueryRow(context.Background(),
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
		return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
	}
	return period, err
}

func (d *Driver) GetPeriodByNumberForCompany(companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.pool.QueryRow(context.Background(),
		`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type
		 FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 AND period_number=$3`,
		companyID, fiscalYearID, number).Scan(
		&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
		&period.StartDate, &period.EndDate, &period.State, &period.Type,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return masterdata.FiscalPeriod{}, masterdata.ErrFiscalPeriodNotFound
	}
	return period, err
}

func (d *Driver) UpdatePeriodState(id uuid.UUID, state masterdata.PeriodState) error {
	_, err := d.pool.Exec(context.Background(),
		`UPDATE fiscal_periods SET state=$2, closed_at=CASE WHEN $2='open' THEN NULL ELSE COALESCE(closed_at, NOW()) END WHERE id=$1`,
		id, state)
	return err
}
func (d *Driver) GetNextSequenceWithLock(journalID uuid.UUID) (int64, error) {
	var sequence int64
	err := d.pool.QueryRow(context.Background(),
		`SELECT current_sequence + 1 FROM journals WHERE id=$1`, journalID).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil
	}
	return sequence, err
}

func (d *Driver) SaveDraft(entry posting.JournalEntry) error {
	if entry.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	tx, err := d.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := d.saveEntryTx(context.Background(), tx, entry); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}
func (d *Driver) GetEntryByID(id uuid.UUID) (posting.JournalEntry, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(), `SELECT entry FROM account_moves WHERE id=$1`, id).Scan(&data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	var entry posting.JournalEntry
	return entry, json.Unmarshal(data, &entry)
}
func (d *Driver) saveEntry(ctx context.Context, entry posting.JournalEntry) error {
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
	_, err = d.pool.Exec(ctx,
		`INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,entry_number,status,classification,entry) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT(id) DO UPDATE SET journal_id=EXCLUDED.journal_id,period_id=EXCLUDED.period_id,
		 entry_date=EXCLUDED.entry_date,entry_number=EXCLUDED.entry_number,status=EXCLUDED.status,
		 classification=EXCLUDED.classification,entry=EXCLUDED.entry
		 WHERE account_moves.status='draft'`,
		entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.SequenceNumber,
		entry.Status, classification, data)
	return err
}
func (d *Driver) PostAtomic(entry posting.JournalEntry) error {
	if entry.SequenceNumber == 0 {
		_, err := d.PostAndSequence(entry)
		return err
	}
	tx, err := d.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := d.saveEntryTx(context.Background(), tx, entry); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}

func (d *Driver) PostAndSequence(entry posting.JournalEntry) (posting.JournalEntry, error) {
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer tx.Rollback(ctx)

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
	if err := tx.Commit(ctx); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func (d *Driver) PostReversalAtomic(original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer tx.Rollback(ctx)

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
func (d *Driver) GetNextSequence(id uuid.UUID) (int64, error) { return d.GetNextSequenceWithLock(id) }
func (d *Driver) PostBulk(entries []posting.JournalEntry) error {
	tx, err := d.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	for _, entry := range entries {
		if err := d.saveEntryTx(context.Background(), tx, entry); err != nil {
			return err
		}
	}
	return tx.Commit(context.Background())
}
func (d *Driver) ListPostedEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	return d.ListEntries(start, end)
}
func (d *Driver) ListEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	return d.listEntries(nil, start, end)
}

func (d *Driver) ListEntriesForCompany(companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	return d.listEntries(&companyID, start, end)
}

func (d *Driver) listEntries(companyID *uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	query := `SELECT entry FROM account_moves WHERE status=$1 AND entry_date BETWEEN $2 AND $3`
	args := []any{posting.EntryPosted, start, end}
	if companyID != nil {
		query += ` AND company_id=$4`
		args = append(args, *companyID)
	}
	query += ` ORDER BY entry_date`
	rows, err := d.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]posting.JournalEntry, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var entry posting.JournalEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (d *Driver) SaveEvent(event subledgers.EconomicEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := d.pool.Exec(context.Background(),
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
}

func (d *Driver) SaveEventAccountingRule(rule subledgers.EventAccountingRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(context.Background(),
		`INSERT INTO event_accounting_rules(id,company_id,event_type,priority,rule_data)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(company_id,event_type,priority) DO UPDATE
		 SET id=EXCLUDED.id,rule_data=EXCLUDED.rule_data`,
		rule.ID, rule.CompanyID, rule.EventType, rule.Priority, data)
	return err
}

func (d *Driver) ListEventAccountingRulesForCompany(companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	rows, err := d.pool.Query(context.Background(),
		`SELECT rule_data FROM event_accounting_rules
		 WHERE company_id=$1 AND event_type=$2 ORDER BY priority DESC, id`,
		companyID, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]subledgers.EventAccountingRule, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var rule subledgers.EventAccountingRule
		if err := json.Unmarshal(data, &rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (d *Driver) SaveEventAndDraft(event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
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

func (d *Driver) GetEventByIdempotencyKey(key string) (subledgers.EconomicEvent, bool, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(), `SELECT event FROM subledger_events WHERE idempotency_key=$1`, key).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.EconomicEvent{}, false, nil
	}
	if err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	var event subledgers.EconomicEvent
	return event, true, json.Unmarshal(data, &event)
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(),
		`SELECT event FROM subledger_events WHERE company_id=$1 AND idempotency_key=$2`,
		companyID, key).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.EconomicEvent{}, false, nil
	}
	if err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	var event subledgers.EconomicEvent
	return event, true, json.Unmarshal(data, &event)
}
func (d *Driver) SaveOpenLine(line subledgers.OpenLine) error {
	return d.SaveOpenLines([]subledgers.OpenLine{line})
}

func (d *Driver) SaveOpenLines(lines []subledgers.OpenLine) error {
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
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

func (d *Driver) CompareAndSaveOpenLines(expected, updated []subledgers.OpenLine) error {
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID == uuid.Nil || updated[i].ID != line.ID ||
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

func (d *Driver) CompareAndSaveOpenLinesAndDraft(expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID == uuid.Nil || updated[i].ID != line.ID ||
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

func (d *Driver) GetOpenLine(id uuid.UUID) (subledgers.OpenLine, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(), `SELECT line_data FROM subledger_open_lines WHERE id=$1`, id).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.OpenLine{}, subledgers.ErrNotFound
	}
	if err != nil {
		return subledgers.OpenLine{}, err
	}
	var line subledgers.OpenLine
	return line, json.Unmarshal(data, &line)
}
func (d *Driver) ListOpenLines(partnerID string) ([]subledgers.OpenLine, error) {
	query := `SELECT line_data FROM subledger_open_lines`
	args := []interface{}{}
	if partnerID != "" {
		query += ` WHERE partner_id=$1`
		args = append(args, partnerID)
	}
	rows, err := d.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]subledgers.OpenLine, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var line subledgers.OpenLine
		if err := json.Unmarshal(data, &line); err != nil {
			return nil, err
		}
		result = append(result, line)
	}
	return result, rows.Err()
}

func (d *Driver) ListOpenLinesForCompany(companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	query := `SELECT line_data FROM subledger_open_lines WHERE company_id=$1`
	args := []any{companyID}
	if partnerID != "" {
		query += ` AND partner_id=$2`
		args = append(args, partnerID)
	}
	rows, err := d.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]subledgers.OpenLine, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var line subledgers.OpenLine
		if err := json.Unmarshal(data, &line); err != nil {
			return nil, err
		}
		result = append(result, line)
	}
	return result, rows.Err()
}
func (d *Driver) SaveFixedAsset(asset subledgers.FixedAsset) error {
	data, err := json.Marshal(asset)
	if err != nil {
		return err
	}
	result, err := d.pool.Exec(context.Background(),
		`INSERT INTO fixed_assets(id,company_id,asset_data) VALUES($1,$2,$3)
		 ON CONFLICT(id) DO UPDATE SET asset_data=EXCLUDED.asset_data
		 WHERE fixed_assets.company_id=EXCLUDED.company_id`,
		asset.ID, asset.CompanyID, data)
	if err == nil && result.RowsAffected() != 1 {
		return subledgers.ErrCompanyMismatch
	}
	return err
}

func (d *Driver) SaveFixedAssetDraft(expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
		return subledgers.ErrInvalidAsset
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
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
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:DEPRECIATION:") {
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

func (d *Driver) ConfirmFixedAssetMutation(expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidAsset
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
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

func (d *Driver) GetFixedAsset(id uuid.UUID) (subledgers.FixedAsset, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(), `SELECT asset_data FROM fixed_assets WHERE id=$1`, id).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.FixedAsset{}, subledgers.ErrNotFound
	}
	if err != nil {
		return subledgers.FixedAsset{}, err
	}
	var asset subledgers.FixedAsset
	return asset, json.Unmarshal(data, &asset)
}

func (d *Driver) ListFixedAssets() ([]subledgers.FixedAsset, error) {
	rows, err := d.pool.Query(context.Background(), `SELECT asset_data FROM fixed_assets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]subledgers.FixedAsset, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var asset subledgers.FixedAsset
		if err := json.Unmarshal(data, &asset); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (d *Driver) ListFixedAssetsForCompany(companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	rows, err := d.pool.Query(context.Background(), `SELECT asset_data FROM fixed_assets WHERE company_id=$1`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]subledgers.FixedAsset, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var asset subledgers.FixedAsset
		if err := json.Unmarshal(data, &asset); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}
func (d *Driver) SaveBankItem(item subledgers.BankItem) error {
	if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.AccountID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := d.pool.Exec(context.Background(),
		`INSERT INTO bank_items(company_id,account_id,item_id,item_data) VALUES($1,$2,$3,$4)
		 ON CONFLICT(item_id) DO UPDATE SET item_data=EXCLUDED.item_data
		 WHERE bank_items.company_id=EXCLUDED.company_id AND bank_items.account_id=EXCLUDED.account_id`,
		item.CompanyID, item.AccountID, item.ID, data)
	if err == nil && result.RowsAffected() != 1 {
		return subledgers.ErrCompanyMismatch
	}
	return err
}
func (d *Driver) ListBankItems(accountID string) ([]subledgers.BankItem, error) {
	rows, err := d.pool.Query(context.Background(), `SELECT item_data FROM bank_items WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]subledgers.BankItem, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var item subledgers.BankItem
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *Driver) ListBankItemsForCompany(companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	rows, err := d.pool.Query(context.Background(),
		`SELECT item_data FROM bank_items WHERE company_id=$1 AND account_id=$2::uuid`,
		companyID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]subledgers.BankItem, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var item subledgers.BankItem
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *Driver) CompareAndSaveBankItems(expected, updated []subledgers.BankItem) error {
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.AccountID == uuid.Nil ||
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

func (d *Driver) CompareAndSaveBankItemsAndDraft(expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.AccountID == uuid.Nil ||
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
	var rate decimal.Decimal
	err := d.pool.QueryRow(ctx, `SELECT rate FROM exchange_rates WHERE lower(from_currency)=lower($1) AND lower(to_currency)=lower($2) AND effective_date<= $3::date ORDER BY effective_date DESC LIMIT 1`, from, to, date).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, valuation.ErrRateNotFound
	}
	return rate, err
}
func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	var rate decimal.Decimal
	err := d.pool.QueryRow(ctx, `SELECT rate FROM exchange_rates WHERE lower(from_currency)=lower($1) AND lower(to_currency)=lower($2) AND rate_type=$3 AND effective_date<= $4::date ORDER BY effective_date DESC LIMIT 1`, from, to, rateType, date).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, valuation.ErrRateNotFound
	}
	return rate, err
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
	err := d.pool.QueryRow(ctx, query, args...).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		query = `SELECT rate FROM exchange_rates
			WHERE company_id=$1 AND lower(from_currency)=lower($3) AND lower(to_currency)=lower($2)
			  AND effective_date<=$4::date`
		args = []any{companyID, from, to, date}
		if rateType != "" {
			query += ` AND rate_type=$5`
			args = append(args, rateType)
		}
		query += ` ORDER BY effective_date DESC LIMIT 1`
		err = d.pool.QueryRow(ctx, query, args...).Scan(&rate)
		if errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, valuation.ErrRateNotFound
		}
		if err == nil {
			return decimal.NewFromInt(1).DivRound(rate, financialtypes.InternalScale), nil
		}
	}
	return rate, err
}

func (d *Driver) UpsertExchangeRate(ctx context.Context, companyID uuid.UUID, rate valuation.ExchangeRate) error {
	if companyID == uuid.Nil {
		return masterdata.ErrInvalidCompany
	}
	if err := rate.Validate(); err != nil {
		return err
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO exchange_rates(
		company_id,from_currency,to_currency,rate,effective_date,rate_type
	) VALUES($1,$2,$3,$4,$5,$6)
	ON CONFLICT(company_id,from_currency,to_currency,effective_date,rate_type)
	DO UPDATE SET rate=EXCLUDED.rate`,
		companyID, strings.ToUpper(strings.TrimSpace(rate.FromCurrency)),
		strings.ToUpper(strings.TrimSpace(rate.ToCurrency)), rate.Rate,
		rate.EffectiveDate, rate.Type)
	return err
}

func (d *Driver) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO financial_audit_logs(company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,timestamp,previous_log_hash,current_log_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		log.CompanyID, log.EntityType, log.EntityID, log.Action, log.ActorID, log.PayloadBefore, log.PayloadAfter, log.Timestamp, log.PreviousLogHash, log.CurrentLogHash)
	return err
}

func (d *Driver) AppendAuditLogAtomic(ctx context.Context, log governance.FinancialAuditLog) (governance.FinancialAuditLog, error) {
	if log.CompanyID == uuid.Nil {
		return governance.FinancialAuditLog{}, errors.New("audit record requires company id")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return governance.FinancialAuditLog{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, log.CompanyID.String()); err != nil {
		return governance.FinancialAuditLog{}, err
	}
	var previousHash string
	err = tx.QueryRow(ctx,
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
	if err := tx.Commit(ctx); err != nil {
		return governance.FinancialAuditLog{}, err
	}
	return log, nil
}

func (d *Driver) GetLastAuditLogForCompany(ctx context.Context, companyID uuid.UUID) (*governance.FinancialAuditLog, error) {
	var log governance.FinancialAuditLog
	err := d.pool.QueryRow(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,
		payload_before,payload_after,timestamp,previous_log_hash,current_log_hash
		FROM financial_audit_logs WHERE company_id=$1 ORDER BY id DESC LIMIT 1`, companyID).Scan(
		&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action, &log.ActorID,
		&log.PayloadBefore, &log.PayloadAfter, &log.Timestamp, &log.PreviousLogHash, &log.CurrentLogHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &log, nil
}
func (d *Driver) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	var log governance.FinancialAuditLog
	err := d.pool.QueryRow(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,timestamp,previous_log_hash,current_log_hash FROM financial_audit_logs ORDER BY id DESC LIMIT 1`).Scan(&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action, &log.ActorID, &log.PayloadBefore, &log.PayloadAfter, &log.Timestamp, &log.PreviousLogHash, &log.CurrentLogHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &log, err
}
func (d *Driver) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	rows, err := d.pool.Query(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,timestamp,previous_log_hash,current_log_hash FROM financial_audit_logs WHERE id>= $1 ORDER BY id`, fromID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var log governance.FinancialAuditLog
		if err := rows.Scan(&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action, &log.ActorID, &log.PayloadBefore, &log.PayloadAfter, &log.Timestamp, &log.PreviousLogHash, &log.CurrentLogHash); err != nil {
			return err
		}
		if err := handler(log); err != nil {
			return err
		}
	}
	return rows.Err()
}
