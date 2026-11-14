package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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

func scanJournal(row pgx.Row) (masterdata.Journal, error) {
	var journal masterdata.Journal
	err := row.Scan(
		&journal.ID, &journal.CompanyID, &journal.Code, &journal.Name, &journal.LastNumber,
		&journal.Active, &journal.AllowedCurrencies, &journal.NumberingPolicy,
	)
	return journal, err
}

func (d *Driver) CreateAccount(ctx context.Context, account masterdata.Account) error {
	return d.WithCompanyTx(ctx, account.CompanyID, func(tx pgx.Tx) error {
		var inserted bool
		err := tx.QueryRow(ctx,
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
			display_order=EXCLUDED.display_order, updated_at=EXCLUDED.updated_at
		RETURNING (xmax = 0)`,
			account.ID, account.CompanyID, account.Code, account.Name, account.AccountType, account.NormalBalance,
			nullableString(account.CurrencyID, "SAR"), account.ParentID, account.IsReconcilable,
			account.IsControlAccount, account.IsContraAccount, account.IsActive, account.RequiresCostCenter,
			account.IsCashEquivalent, account.IsNonCashAdjustment,
			nullableString(account.CashFlowActivity, "unclassified"),
			nullableString(account.SubClassification, "unclassified"),
			nullableString(account.IFRSCategory, "unclassified"), account.DisplayOrder,
			defaultTime(account.CreatedAt), defaultTime(account.UpdatedAt)).Scan(&inserted)
		if err != nil {
			return err
		}
		action := "ACCOUNT_UPDATED"
		if inserted {
			action = "ACCOUNT_CREATED"
		}
		return appendEntityAuditTx(ctx, tx, account.CompanyID, "account", account.ID, action, uuid.Nil, nil, account)
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

func (d *Driver) GetJournalByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Journal, error) {
	var journal masterdata.Journal
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var scanErr error
		journal, scanErr = scanJournal(tx.QueryRow(ctx,
			`SELECT id, company_id, code, name, current_sequence,
			 is_active,allowed_currencies,numbering_policy
			 FROM journals WHERE company_id=$1 AND lower(code)=lower($2)`, companyID, code))
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return masterdata.ErrJournalNotFound
		}
		return scanErr
	})
	return journal, err
}

// UpdateJournalSettingsContext changes activation or journal-scoped currency
// and numbering rules. Rule changes are frozen once drafts or posted entries
// exist; callers may still deactivate a used journal.
func (d *Driver) UpdateJournalSettingsContext(
	ctx context.Context,
	companyID, journalID uuid.UUID,
	active bool,
	allowedCurrencies []string,
	numberingPolicy masterdata.JournalNumberingPolicy,
) error {
	journal := masterdata.Journal{
		Type: masterdata.JournalTypeGeneral, Active: active,
		AllowedCurrencies: append([]string(nil), allowedCurrencies...),
		NumberingPolicy:   numberingPolicy,
	}
	for i := range journal.AllowedCurrencies {
		journal.AllowedCurrencies[i] = strings.ToUpper(strings.TrimSpace(journal.AllowedCurrencies[i]))
	}
	if journal.NumberingPolicy == "" {
		journal.NumberingPolicy = masterdata.JournalNumberingPerJournal
	}
	if err := journal.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var currentCurrencies []string
		var currentPolicy masterdata.JournalNumberingPolicy
		var currentActive bool
		err := tx.QueryRow(ctx, `SELECT is_active,allowed_currencies,numbering_policy
			FROM journals WHERE company_id=$1 AND id=$2 FOR UPDATE`,
			companyID, journalID).Scan(&currentActive, &currentCurrencies, &currentPolicy)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrJournalNotFound
		}
		if err != nil {
			return err
		}
		var hasEntries bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM account_moves WHERE company_id=$1 AND journal_id=$2
		)`, companyID, journalID).Scan(&hasEntries); err != nil {
			return err
		}
		if hasEntries && (currentPolicy != journal.NumberingPolicy ||
			!equalStringSet(currentCurrencies, journal.AllowedCurrencies)) {
			return masterdata.ErrJournalSettingsInUse
		}
		if _, err := tx.Exec(ctx, `UPDATE journals
			SET is_active=$3,allowed_currencies=$4,numbering_policy=$5
			WHERE company_id=$1 AND id=$2`,
			companyID, journalID, active, journal.AllowedCurrencies, journal.NumberingPolicy); err != nil {
			return err
		}
		return appendEntityAuditTx(ctx, tx, companyID, "journal", journalID, "JOURNAL_SETTINGS_CHANGED", uuid.Nil,
			map[string]any{
				"is_active": currentActive, "allowed_currencies": currentCurrencies,
				"numbering_policy": currentPolicy,
			},
			map[string]any{
				"is_active": active, "allowed_currencies": journal.AllowedCurrencies,
				"numbering_policy": journal.NumberingPolicy,
			})
	})
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	set := make(map[string]int, len(left))
	for _, value := range left {
		set[strings.ToUpper(strings.TrimSpace(value))]++
	}
	for _, value := range right {
		key := strings.ToUpper(strings.TrimSpace(value))
		if set[key] == 0 {
			return false
		}
		set[key]--
	}
	return true
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
			start_date,end_date,state,period_type,COALESCE(last_state_actor_id,'00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE(last_state_reason,''),last_state_at FROM fiscal_periods
			WHERE company_id=$1 AND year_id=$2 ORDER BY period_number`, companyID, fiscalYearID)
		if err != nil {
			return err
		}
		defer rows.Close()
		periods := make([]masterdata.FiscalPeriod, 0)
		for rows.Next() {
			var period masterdata.FiscalPeriod
			if err := rows.Scan(&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number,
				&period.Name, &period.StartDate, &period.EndDate, &period.State, &period.Type,
				&period.LastStateActorID, &period.LastStateReason, &period.LastStateAt); err != nil {
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
		if err != nil {
			return err
		}
		action := "FISCAL_YEAR_STATE_CHANGED"
		if state == masterdata.FiscalYearHardClosed {
			action = "FISCAL_YEAR_CLOSED"
		}
		return appendEntityAuditTx(ctx, tx, companyID, "fiscal_year", id, action, uuid.Nil,
			map[string]any{"state": current}, map[string]any{"state": state})
	})
}

func (d *Driver) UpdateFiscalYearStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.FiscalYearState) error {
	return d.UpdateFiscalYearState(ctx, companyID, id, state)
}

func (d *Driver) ReopenFiscalYearForCompanyContext(ctx context.Context, companyID, id, actorID uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if actorID == uuid.Nil || reason == "" {
		return masterdata.ErrPeriodReopenRequiresAudit
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var current masterdata.FiscalYearState
		err := tx.QueryRow(ctx, `SELECT state FROM fiscal_years WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalYearNotFound
		}
		if err != nil {
			return err
		}
		if err := masterdata.ValidateFiscalYearReopen(current, masterdata.FiscalYearOpen, actorID, reason); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE fiscal_years SET state=$1 WHERE company_id=$2 AND id=$3`, masterdata.FiscalYearOpen, companyID, id)
		return err
	})
}

func (d *Driver) GetPeriodByDate(ctx context.Context, date time.Time) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetPeriodByDateForCompany(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type,
			 COALESCE(last_state_actor_id,'00000000-0000-0000-0000-000000000000'::uuid),
			 COALESCE(last_state_reason,''),last_state_at
		 FROM fiscal_periods
		 WHERE company_id=$1 AND start_date <= $2 AND end_date >= $2
		   AND period_type = $3
		 ORDER BY period_number ASC LIMIT 1`,
			companyID, date, masterdata.FiscalPeriodTypeNormal).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type, &period.LastStateActorID,
			&period.LastStateReason, &period.LastStateAt,
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
			`SELECT id, company_id, year_id, period_number, name, start_date, end_date, state, period_type,
			 COALESCE(last_state_actor_id,'00000000-0000-0000-0000-000000000000'::uuid),
			 COALESCE(last_state_reason,''),last_state_at
		 FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 AND period_number=$3`,
			companyID, fiscalYearID, number).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type, &period.LastStateActorID,
			&period.LastStateReason, &period.LastStateAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return err
	})
	return period, err
}

func (d *Driver) UpdatePeriodState(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	return d.transitionPeriodState(ctx, companyID, id, state, uuid.Nil, "", false, false)
}

func (d *Driver) TransitionPeriodStateContext(
	ctx context.Context,
	companyID, id uuid.UUID,
	state masterdata.PeriodState,
	actorID uuid.UUID,
	reason string,
) error {
	return d.transitionPeriodState(ctx, companyID, id, state, actorID, reason, true, false)
}

func (d *Driver) ReopenPeriodContext(
	ctx context.Context,
	companyID, id, actorID uuid.UUID,
	reason string,
) error {
	return d.transitionPeriodState(ctx, companyID, id, masterdata.PeriodOpen, actorID, reason, true, true)
}

func (d *Driver) transitionPeriodState(
	ctx context.Context,
	companyID, id uuid.UUID,
	state masterdata.PeriodState,
	actorID uuid.UUID,
	reason string,
	requireAudit, reopening bool,
) error {
	reason = strings.TrimSpace(reason)
	if requireAudit && (actorID == uuid.Nil || reason == "") {
		if reopening {
			return masterdata.ErrPeriodReopenRequiresAudit
		}
		return masterdata.ErrPeriodTransitionNeedsAudit
	}
	if reopening && state != masterdata.PeriodOpen {
		return masterdata.ErrInvalidPeriodTransition
	}
	if !reopening && state == masterdata.PeriodOpen {
		return masterdata.ErrInvalidPeriodTransition
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var current masterdata.PeriodState
		err := tx.QueryRow(ctx, `SELECT state FROM fiscal_periods
			WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		if err != nil {
			return err
		}
		if reopening {
			if err := masterdata.ValidatePeriodReopen(current, state, actorID, reason); err != nil {
				return err
			}
		} else if err := masterdata.ValidatePeriodTransition(current, state); err != nil {
			return err
		}
		var changedAt time.Time
		if err := tx.QueryRow(ctx, `UPDATE fiscal_periods
			SET state=$3,
			    closed_at=CASE WHEN $3::varchar='open' THEN NULL ELSE COALESCE(closed_at,NOW()) END,
			    closed_by=CASE WHEN $3::varchar='open' THEN closed_by ELSE NULLIF($4::uuid,'00000000-0000-0000-0000-000000000000'::uuid) END,
			    last_state_actor_id=NULLIF($4::uuid,'00000000-0000-0000-0000-000000000000'::uuid),
			    last_state_reason=NULLIF($5,''),
			    last_state_at=NOW()
			WHERE company_id=$1 AND id=$2
			RETURNING last_state_at`, companyID, id, state, actorID, reason).Scan(&changedAt); err != nil {
			return err
		}
		action := "FISCAL_PERIOD_STATE_CHANGED"
		if state == masterdata.PeriodHardClosed {
			action = "FISCAL_PERIOD_CLOSED"
		}
		if reopening {
			action = "FISCAL_PERIOD_REOPENED"
		}
		return appendEntityAuditTx(ctx, tx, companyID, "fiscal_period", id, action, actorID,
			map[string]any{"state": current}, map[string]any{
				"state": state, "actor_id": actorID, "reason": reason, "timestamp": changedAt,
			})
	})
}

func (d *Driver) UpdatePeriodStateForCompanyContext(ctx context.Context, companyID, id uuid.UUID, state masterdata.PeriodState) error {
	return d.UpdatePeriodState(ctx, companyID, id, state)
}

// GetNextSequenceWithLock atomically increments and returns the next journal
// sequence number. The UPDATE ... RETURNING form takes the journal row lock
// and advances the counter in a single statement, so concurrent callers can
// never observe (or hand out) the same sequence number.
func (d *Driver) GetNextSequenceWithLock(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	var sequence int64
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`UPDATE journals
			 SET current_sequence = current_sequence + 1
			 WHERE company_id=$1 AND id=$2
			 RETURNING current_sequence`,
			companyID, journalID).Scan(&sequence)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrJournalNotFound
		}
		return err
	})
	return sequence, err
}
