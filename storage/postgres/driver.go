package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
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
	return &Driver{pool: pool}, nil
}

func NewWithPool(pool *pgxpool.Pool) *Driver { return &Driver{pool: pool} }
func (d *Driver) Close()                     { d.pool.Close() }
func (d *Driver) Pool() *pgxpool.Pool        { return d.pool }

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
		nullableString(account.IFRSCategory, "operating"), account.DisplayOrder,
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
	_, err = d.pool.Exec(context.Background(), `INSERT INTO fiscal_years(id,company_id,code,year,start_date,end_date,year_data) VALUES($1,$2,$3,$4,$5,$6,$7)
	 ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,code=EXCLUDED.code,year=EXCLUDED.year,start_date=EXCLUDED.start_date,end_date=EXCLUDED.end_date,year_data=EXCLUDED.year_data`,
		year.ID, year.CompanyID, year.Name, year.StartDate.UTC().Year(), year.StartDate, year.EndDate, data)
	return err
}

func (d *Driver) GetFiscalYear(id uuid.UUID) (masterdata.FiscalYear, error) {
	var data []byte
	err := d.pool.QueryRow(context.Background(), `SELECT year_data FROM fiscal_years WHERE id=$1`, id).Scan(&data)
	if err != nil {
		return masterdata.FiscalYear{}, err
	}
	var year masterdata.FiscalYear
	return year, json.Unmarshal(data, &year)
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
func (d *Driver) UpdatePeriodState(id uuid.UUID, state masterdata.PeriodState) error {
	_, err := d.pool.Exec(context.Background(),
		`UPDATE fiscal_periods SET state=$2, closed_at=CASE WHEN $2='open' THEN NULL ELSE COALESCE(closed_at, NOW()) END WHERE id=$1`,
		id, state)
	return err
}
func (d *Driver) GetNextSequenceWithLock(journalID uuid.UUID) (int64, error) {
	ctx := context.Background()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var sequence int64
	err = tx.QueryRow(ctx,
		`INSERT INTO journals(id, company_id, code, next_sequence)
		 VALUES($1, $2, $3, 1)
		 ON CONFLICT(id) DO UPDATE SET next_sequence=journals.next_sequence+1
		 RETURNING next_sequence`,
		journalID, uuid.Nil, "GEN-"+journalID.String()).Scan(&sequence)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return sequence, err
}

func (d *Driver) SaveDraft(entry posting.JournalEntry) error {
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
	date := entry.EntryDate
	if date.IsZero() {
		date = entry.Date
	}
	_, err = d.pool.Exec(ctx,
		`INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,status,entry) VALUES($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,journal_id=EXCLUDED.journal_id,period_id=EXCLUDED.period_id,entry_date=EXCLUDED.entry_date,status=EXCLUDED.status,entry=EXCLUDED.entry`,
		entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.Status, data)
	return err
}
func (d *Driver) PostAtomic(entry posting.JournalEntry) error {
	tx, err := d.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if entry.SequenceNumber == 0 {
		var sequence int64
		err := tx.QueryRow(context.Background(),
			`INSERT INTO journals(id, company_id, code, next_sequence)
			 VALUES($1, $2, $3, 1)
			 ON CONFLICT(id) DO UPDATE SET next_sequence=journals.next_sequence+1
			 RETURNING next_sequence`,
			entry.JournalID, entry.CompanyID, "GEN-"+entry.JournalID.String()).Scan(&sequence)
		if err != nil {
			return err
		}
		entry.SequenceNumber = sequence
	}
	if err := d.saveEntryTx(context.Background(), tx, entry); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}
func (d *Driver) saveEntryTx(ctx context.Context, tx pgx.Tx, entry posting.JournalEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	date := entry.EntryDate
	if date.IsZero() {
		date = entry.Date
	}
	_, err = tx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,status,entry) VALUES($1,$2,$3,$4,$5,$6,$7)
	 ON CONFLICT(id) DO UPDATE SET journal_id=EXCLUDED.journal_id,period_id=EXCLUDED.period_id,entry_date=EXCLUDED.entry_date,status=EXCLUDED.status,entry=EXCLUDED.entry`,
		entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.Status, data)
	if err != nil {
		return err
	}
	for _, line := range entry.Lines {
		lineData, err := json.Marshal(line)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_move_lines (
			move_id,company_id,line_id,account_id,partner_id,cost_center_id,project_id,business_unit_id,matching_number,reconciled_amount,
			debit,credit,amount_currency,currency_id,line_data
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT(move_id,line_id) DO UPDATE SET
			account_id=EXCLUDED.account_id, debit=EXCLUDED.debit, credit=EXCLUDED.credit, line_data=EXCLUDED.line_data`,
			entry.ID, entry.CompanyID, line.ID, line.AccountID, line.PartnerID, line.CostCenterID, line.ProjectID, line.BusinessUnitID, line.MatchingNumber,
			line.ReconciledAmount, line.Debit.Amount(), line.Credit.Amount(), line.AmountCurrency.Amount(),
			line.Currency, lineData); err != nil {
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
	rows, err := d.pool.Query(context.Background(), `SELECT entry FROM account_moves WHERE status=$1 AND entry_date BETWEEN $2 AND $3 ORDER BY entry_date`, posting.EntryPosted, start, end)
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
	_, err = d.pool.Exec(context.Background(), `INSERT INTO subledger_events(company_id,idempotency_key,event) VALUES($1,$2,$3) ON CONFLICT(idempotency_key) DO UPDATE SET company_id=EXCLUDED.company_id,event=EXCLUDED.event`, event.CompanyID, event.IdempotencyKey, data)
	return err
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
func (d *Driver) SaveOpenLine(line subledgers.OpenLine) error {
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	var partner any
	if line.PartnerID != nil {
		partner = *line.PartnerID
	}
	_, err = d.pool.Exec(context.Background(), `INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,partner_id=EXCLUDED.partner_id,kind=EXCLUDED.kind,line_data=EXCLUDED.line_data`,
		line.ID, line.CompanyID, partner, line.Kind, data)
	return err
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
func (d *Driver) SaveFixedAsset(asset subledgers.FixedAsset) error {
	data, err := json.Marshal(asset)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(context.Background(), `INSERT INTO fixed_assets(id,company_id,asset_data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET company_id=EXCLUDED.company_id,asset_data=EXCLUDED.asset_data`, asset.ID, asset.CompanyID, data)
	return err
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
func (d *Driver) SaveBankItem(item subledgers.BankItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(context.Background(), `INSERT INTO bank_items(company_id,account_id,item_id,item_data) VALUES($1,$2,$3,$4)`, item.CompanyID, item.AccountID, item.ID, data)
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
func (d *Driver) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO financial_audit_logs(company_id,entity_type,entity_id,action,actor_id,payload_before,payload_after,timestamp,previous_log_hash,current_log_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		log.CompanyID, log.EntityType, log.EntityID, log.Action, log.ActorID, log.PayloadBefore, log.PayloadAfter, log.Timestamp, log.PreviousLogHash, log.CurrentLogHash)
	return err
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
