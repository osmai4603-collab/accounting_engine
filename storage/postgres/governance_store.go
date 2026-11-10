package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) HasRole(ctx context.Context, companyID, userID uuid.UUID, role governance.Role) (bool, error) {
	granted := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM user_role_assignments
			WHERE company_id=$1 AND user_id=$2 AND role=$3
		)`, companyID, userID, role).Scan(&granted)
	})
	return granted, err
}

func (d *Driver) GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (masterdata.Company, error) {
	var company masterdata.Company
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,code,legal_name,functional_currency,presentation_currency,is_active
			FROM companies WHERE id=$1`, companyID).Scan(
			&company.ID, &company.Code, &company.LegalName, &company.FunctionalCurrency,
			&company.PresentationCurrency, &company.Active)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrInvalidCompany
		}
		return err
	})
	return company, err
}

func (d *Driver) GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	var account masterdata.Account
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var scanErr error
		account, scanErr = scanAccount(tx.QueryRow(ctx, accountSelect+` WHERE company_id=$1 AND id=$2`, companyID, accountID))
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return masterdata.ErrAccountNotFound
		}
		return scanErr
	})
	return account, err
}

func (d *Driver) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	var entry posting.JournalEntry
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx, `SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2`,
			companyID, entryID).Scan(&data)
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

func (d *Driver) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	return d.GetEntryByIDForCompany(ctx, companyID, entryID)
}

func (d *Driver) GetFiscalYearForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID) (masterdata.FiscalYear, error) {
	var year masterdata.FiscalYear
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		var state masterdata.FiscalYearState
		err := tx.QueryRow(ctx, `SELECT state,year_data FROM fiscal_years
			WHERE company_id=$1 AND id=$2`, companyID, fiscalYearID).Scan(&state, &data)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalYearNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &year); err != nil {
			return err
		}
		year.CompanyID, year.State = companyID, state
		rows, err := tx.Query(ctx, `SELECT id,company_id,year_id,COALESCE(period_number,0),COALESCE(name,''),
			start_date,end_date,state,period_type
			FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 ORDER BY period_number`,
			companyID, fiscalYearID)
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

func (d *Driver) GetPeriodByDateForCompanyContext(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,company_id,year_id,period_number,name,start_date,end_date,state,period_type
			FROM fiscal_periods WHERE company_id=$1 AND start_date <= $2 AND end_date >= $2
			AND period_type=$3 ORDER BY period_number ASC LIMIT 1`,
			companyID, date, masterdata.FiscalPeriodTypeNormal).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return err
	})
	return period, err
}

func (d *Driver) GetPeriodByNumberForCompanyContext(ctx context.Context, companyID, fiscalYearID uuid.UUID, number int) (masterdata.FiscalPeriod, error) {
	var period masterdata.FiscalPeriod
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,company_id,year_id,period_number,name,start_date,end_date,state,period_type
			FROM fiscal_periods WHERE company_id=$1 AND year_id=$2 AND period_number=$3`,
			companyID, fiscalYearID, number).Scan(
			&period.ID, &period.CompanyID, &period.FiscalYearID, &period.Number, &period.Name,
			&period.StartDate, &period.EndDate, &period.State, &period.Type)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return err
	})
	return period, err
}

func (d *Driver) HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	var exists bool
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM accounts WHERE company_id=$1 AND parent_id=$2
		)`, companyID, accountID).Scan(&exists)
	})
	return exists, err
}

func (d *Driver) ListEntriesForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
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

func (d *Driver) AssignRole(ctx context.Context, assignment governance.RoleAssignment) error {
	if assignment.CompanyID == uuid.Nil || assignment.UserID == uuid.Nil || assignment.GrantedBy == uuid.Nil ||
		!governance.ValidRole(assignment.Role) {
		return governance.ErrInvalidRole
	}
	if assignment.GrantedAt.IsZero() {
		assignment.GrantedAt = time.Now().UTC()
	}
	return d.WithCompanyTx(ctx, assignment.CompanyID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO governance_roles(company_id,role)
			VALUES($1,$2) ON CONFLICT(company_id,role) DO NOTHING`,
			assignment.CompanyID, assignment.Role); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_role_assignments(
			company_id,user_id,role,granted_by,granted_at
		) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(company_id,user_id,role) DO UPDATE SET granted_by=EXCLUDED.granted_by,granted_at=EXCLUDED.granted_at`,
			assignment.CompanyID, assignment.UserID, assignment.Role, assignment.GrantedBy, assignment.GrantedAt)
		return err
	})
}

func (d *Driver) SaveApproval(ctx context.Context, request governance.ApprovalRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, request.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO approval_requests(
			id,company_id,entry_id,journal_type,creator_id,amount,currency,created_at,completed_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(company_id,id) DO UPDATE SET
			completed_at=EXCLUDED.completed_at`,
			request.ID, request.CompanyID, request.EntryID, request.JournalType, request.CreatorID,
			request.Amount.Amount(), request.Amount.Currency(), request.CreatedAt, request.CompletedAt)
		if err != nil {
			return err
		}
		for i, step := range request.Steps {
			_, err := tx.Exec(ctx, `INSERT INTO approval_steps(
				company_id,approval_id,sequence,required_role,approved_by,approved_at
			) VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(company_id,approval_id,sequence) DO UPDATE SET
				approved_by=EXCLUDED.approved_by,approved_at=EXCLUDED.approved_at`,
				request.CompanyID, request.ID, i+1, step.Role, nullableUUID(step.ApprovedBy), step.ApprovedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) SaveApprovalWithAudit(
	ctx context.Context,
	request governance.ApprovalRequest,
	actorID uuid.UUID,
	role governance.Role,
	at time.Time,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if actorID == uuid.Nil || at.IsZero() {
		return governance.ErrInvalidApprovalRequest
	}
	return d.WithCompanyTx(ctx, request.CompanyID, func(tx pgx.Tx) error {
		var creatorID uuid.UUID
		var completedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT creator_id,completed_at FROM approval_requests
			WHERE company_id=$1 AND id=$2 FOR UPDATE`, request.CompanyID, request.ID).
			Scan(&creatorID, &completedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return governance.ErrApprovalNotFound
		}
		if err != nil {
			return err
		}
		if completedAt != nil {
			return governance.ErrApprovalComplete
		}
		if creatorID != request.CreatorID || actorID == creatorID {
			return governance.ErrSelfApprovalProhibited
		}

		rows, err := tx.Query(ctx, `SELECT sequence,required_role,approved_by,approved_at
			FROM approval_steps WHERE company_id=$1 AND approval_id=$2 ORDER BY sequence`,
			request.CompanyID, request.ID)
		if err != nil {
			return err
		}
		stored := make([]governance.ApprovalStep, 0, len(request.Steps))
		firstPending := -1
		sequence := 0
		for rows.Next() {
			var current governance.ApprovalStep
			var rowSequence int
			if err := rows.Scan(&rowSequence, &current.Role, &current.ApprovedBy, &current.ApprovedAt); err != nil {
				rows.Close()
				return err
			}
			sequence++
			if rowSequence != sequence {
				rows.Close()
				return governance.ErrInvalidApprovalRequest
			}
			if current.ApprovedAt == nil && firstPending == -1 {
				firstPending = len(stored)
			}
			stored = append(stored, current)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(stored) != len(request.Steps) {
			return governance.ErrInvalidApprovalRequest
		}
		if firstPending == -1 {
			return governance.ErrApprovalComplete
		}
		for _, prior := range stored[:firstPending] {
			if prior.ApprovedBy != nil && *prior.ApprovedBy == actorID {
				return governance.ErrApproverReused
			}
		}
		if stored[firstPending].Role != role || request.Steps[firstPending].Role != role ||
			request.Steps[firstPending].ApprovedBy == nil ||
			*request.Steps[firstPending].ApprovedBy != actorID ||
			request.Steps[firstPending].ApprovedAt == nil ||
			!request.Steps[firstPending].ApprovedAt.Equal(at) {
			return governance.ErrApprovalSequence
		}
		for i := range stored {
			if i != firstPending && (stored[i].Role != request.Steps[i].Role ||
				!sameApprovalActor(stored[i].ApprovedBy, request.Steps[i].ApprovedBy) ||
				!sameApprovalTime(stored[i].ApprovedAt, request.Steps[i].ApprovedAt)) {
				return governance.ErrApprovalSequence
			}
		}
		var authorized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM user_role_assignments
			WHERE company_id=$1 AND user_id=$2 AND role=$3
		)`, request.CompanyID, actorID, role).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return governance.ErrApprovalRoleRequired
		}
		result, err := tx.Exec(ctx, `UPDATE approval_steps
			SET approved_by=$4,approved_at=$5
			WHERE company_id=$1 AND approval_id=$2 AND sequence=$3
			  AND approved_by IS NULL AND approved_at IS NULL`,
			request.CompanyID, request.ID, firstPending+1, actorID, at.UTC())
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return governance.ErrApprovalSequence
		}
		if request.CompletedAt != nil {
			result, err = tx.Exec(ctx, `UPDATE approval_requests SET completed_at=$3
				WHERE company_id=$1 AND id=$2 AND completed_at IS NULL`,
				request.CompanyID, request.ID, request.CompletedAt.UTC())
			if err != nil {
				return err
			}
			if result.RowsAffected() != 1 {
				return governance.ErrApprovalSequence
			}
		}
		_, err = appendAuditLogTx(ctx, tx, governance.FinancialAuditLog{
			CompanyID: request.CompanyID, EntityType: "journal_entry", EntityID: request.EntryID,
			Action: "APPROVAL:" + string(role), ActorID: actorID, Timestamp: at.UTC(),
		})
		return err
	})
}

func sameApprovalActor(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameApprovalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func nullableUUID(id *uuid.UUID) *uuid.UUID {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	copy := *id
	return &copy
}

func (d *Driver) GetApproval(ctx context.Context, companyID, approvalID uuid.UUID) (governance.ApprovalRequest, error) {
	var request governance.ApprovalRequest
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var amount string
		var currency string
		err := tx.QueryRow(ctx, `SELECT id,company_id,entry_id,journal_type,creator_id,amount::text,currency,created_at,completed_at
			FROM approval_requests WHERE company_id=$1 AND id=$2`,
			companyID, approvalID).Scan(
			&request.ID, &request.CompanyID, &request.EntryID, &request.JournalType, &request.CreatorID,
			&amount, &currency, &request.CreatedAt, &request.CompletedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return governance.ErrApprovalNotFound
		}
		if err != nil {
			return err
		}
		request.Amount, err = financialtypes.NewMoneyFromString(amount, currency)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT required_role,approved_by,approved_at
			FROM approval_steps WHERE company_id=$1 AND approval_id=$2 ORDER BY sequence`,
			companyID, approvalID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var step governance.ApprovalStep
			if err := rows.Scan(&step.Role, &step.ApprovedBy, &step.ApprovedAt); err != nil {
				return err
			}
			request.Steps = append(request.Steps, step)
		}
		return rows.Err()
	})
	return request, err
}

func (d *Driver) IsEntryApproved(ctx context.Context, companyID, entryID uuid.UUID) (bool, error) {
	var approved bool
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM approval_requests
			WHERE company_id=$1 AND entry_id=$2 AND completed_at IS NOT NULL
		)`, companyID, entryID).Scan(&approved)
	})
	return approved, err
}

func (d *Driver) RecordApproval(ctx context.Context, companyID, entryID, actorID uuid.UUID, role governance.Role, at time.Time) error {
	_, err := d.AppendAuditLogAtomic(ctx, governance.FinancialAuditLog{
		CompanyID: companyID, EntityType: "journal_entry", EntityID: entryID,
		Action: "APPROVAL:" + string(role), ActorID: actorID, Timestamp: at,
	})
	return err
}

func (d *Driver) GetDayFinalHash(ctx context.Context, companyID uuid.UUID, date time.Time) (string, int64, error) {
	var hash string
	var count int64
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*),
			COALESCE((SELECT current_log_hash FROM financial_audit_logs
				WHERE company_id=$1 AND timestamp >= $2 AND timestamp < $2 + interval '1 day'
				ORDER BY id DESC LIMIT 1),'')
			FROM financial_audit_logs
			WHERE company_id=$1 AND timestamp >= $2 AND timestamp < $2 + interval '1 day'`,
			companyID, date.UTC().Truncate(24*time.Hour)).Scan(&count, &hash)
	})
	return hash, count, err
}

func (d *Driver) VerifyDay(ctx context.Context, companyID uuid.UUID, _ time.Time) error {
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,company_id,entity_type,entity_id,action,actor_id,
			payload_before,payload_after,timestamp,previous_log_hash,current_log_hash
			FROM financial_audit_logs WHERE company_id=$1 ORDER BY id`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		lastHash := governance.GenesisHash
		for rows.Next() {
			var log governance.FinancialAuditLog
			if err := rows.Scan(&log.ID, &log.CompanyID, &log.EntityType, &log.EntityID, &log.Action,
				&log.ActorID, &log.PayloadBefore, &log.PayloadAfter, &log.Timestamp,
				&log.PreviousLogHash, &log.CurrentLogHash); err != nil {
				return err
			}
			if log.PreviousLogHash != lastHash || governance.CalculateHash(log) != log.CurrentLogHash {
				return governance.ErrAuditChainBroken
			}
			lastHash = log.CurrentLogHash
		}
		return rows.Err()
	})
}

func (d *Driver) SaveAccountVersion(ctx context.Context, version masterdata.AccountVersion) error {
	if err := masterdata.ValidateAccountVersions([]masterdata.AccountVersion{version}); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, version.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO account_versions(
			company_id,account_id,name,account_type,ifrs_category,effective_from,effective_to
		) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			version.CompanyID, version.AccountID, version.Name, version.AccountType,
			version.IFRSCategory, version.EffectiveFrom, version.EffectiveTo)
		return err
	})
}

func (d *Driver) GetAccountVersionForCompanyAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (masterdata.AccountVersion, error) {
	var version masterdata.AccountVersion
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT company_id,account_id,name,account_type,ifrs_category,effective_from,effective_to
			FROM account_versions WHERE company_id=$1 AND account_id=$2
			AND effective_from <= $3 AND (effective_to IS NULL OR effective_to > $3)
			ORDER BY effective_from DESC LIMIT 1`, companyID, accountID, asOf).Scan(
			&version.CompanyID, &version.AccountID, &version.Name, &version.AccountType,
			&version.IFRSCategory, &version.EffectiveFrom, &version.EffectiveTo)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrAccountVersionNotFound
		}
		return err
	})
	return version, err
}

func (d *Driver) SaveCostCenter(ctx context.Context, center masterdata.CostCenter) error {
	if err := center.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, center.CompanyID, func(tx pgx.Tx) error {
		if center.ParentID != nil {
			var createsCycle bool
			err := tx.QueryRow(ctx, `WITH RECURSIVE ancestors(id,parent_id) AS (
				SELECT id,parent_id FROM cost_centers WHERE company_id=$1 AND id=$2
				UNION
				SELECT parent.id,parent.parent_id FROM cost_centers parent
				JOIN ancestors child ON parent.id=child.parent_id
				WHERE parent.company_id=$1
			)
			SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$3)`,
				center.CompanyID, *center.ParentID, center.ID).Scan(&createsCycle)
			if err != nil {
				return err
			}
			if createsCycle {
				return masterdata.ErrCostCenterCycle
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO cost_centers(id,company_id,parent_id,code,name,is_active)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(company_id,id) DO UPDATE SET
				parent_id=EXCLUDED.parent_id,code=EXCLUDED.code,name=EXCLUDED.name,is_active=EXCLUDED.is_active`,
			center.ID, center.CompanyID, center.ParentID, center.Code, center.Name, center.Active)
		return err
	})
}

func (d *Driver) GetCostCenterForCompany(ctx context.Context, companyID, centerID uuid.UUID) (masterdata.CostCenter, error) {
	var center masterdata.CostCenter
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,company_id,parent_id,code,name,is_active
			FROM cost_centers WHERE company_id=$1 AND id=$2`, companyID, centerID).Scan(
			&center.ID, &center.CompanyID, &center.ParentID, &center.Code, &center.Name, &center.Active)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrInvalidCostCenter
		}
		return err
	})
	return center, err
}

func (d *Driver) RequiresCostCenter(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	var required bool
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT requires_cost_center FROM accounts WHERE company_id=$1 AND id=$2`,
			companyID, accountID).Scan(&required)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrAccountNotFound
		}
		return err
	})
	return required, err
}

func (d *Driver) ValidateCostCenter(ctx context.Context, companyID, centerID uuid.UUID) error {
	if centerID == uuid.Nil {
		return masterdata.ErrInvalidCostCenter
	}
	var active bool
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT is_active FROM cost_centers WHERE company_id=$1 AND id=$2`,
			companyID, centerID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrInvalidCostCenter
		}
		return err
	})
	if err != nil {
		return err
	}
	if !active {
		return masterdata.ErrInvalidCostCenter
	}
	return nil
}

func (d *Driver) SaveAnalyticalDimension(ctx context.Context, value masterdata.AnalyticalDimensionValue) error {
	if err := value.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, value.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO financial_dimension_values(
			company_id,kind,id,code,name,is_active
		) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(company_id,kind,id) DO UPDATE SET
			code=EXCLUDED.code,name=EXCLUDED.name,is_active=EXCLUDED.is_active`,
			value.CompanyID, value.Kind, value.ID, value.Code, value.Name, value.Active)
		return err
	})
}

func (d *Driver) ValidateAnalyticalDimension(
	ctx context.Context,
	companyID uuid.UUID,
	kind masterdata.AnalyticalDimensionKind,
	dimensionID uuid.UUID,
) error {
	if companyID == uuid.Nil || dimensionID == uuid.Nil {
		return masterdata.ErrInvalidAnalyticalDimension
	}
	var active bool
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT is_active FROM financial_dimension_values
			WHERE company_id=$1 AND kind=$2 AND id=$3`, companyID, kind, dimensionID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrInvalidAnalyticalDimension
		}
		return err
	})
	if err != nil {
		return err
	}
	if !active {
		return masterdata.ErrInvalidAnalyticalDimension
	}
	return nil
}

func (d *Driver) CheckAccountVersionOverlap(ctx context.Context, version masterdata.AccountVersion) error {
	if err := masterdata.ValidateAccountVersions([]masterdata.AccountVersion{version}); err != nil {
		return err
	}
	var overlaps bool
	err := d.WithCompanyTx(ctx, version.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM account_versions
			WHERE company_id=$1 AND account_id=$2
				AND daterange(effective_from,effective_to,'[)') &&
					daterange($3::date,$4::date,'[)')
		)`, version.CompanyID, version.AccountID, version.EffectiveFrom, version.EffectiveTo).Scan(&overlaps)
	})
	if err != nil {
		return err
	}
	if overlaps {
		return masterdata.ErrOverlappingAccountVersion
	}
	return nil
}

var _ governance.AuthorizationRepository = (*Driver)(nil)
var _ governance.ApprovalRepository = (*Driver)(nil)
var _ governance.ApprovalAuditWriter = (*Driver)(nil)
var _ governance.AuditExportRepository = (*Driver)(nil)
var _ masterdata.CostCenterRepository = (*Driver)(nil)
var _ masterdata.ContextCompanyScopedRepository = (*Driver)(nil)
var _ posting.CompanyScopedEntryReader = (*Driver)(nil)
var _ posting.ContextDraftRepository = (*Driver)(nil)
var _ posting.ContextAtomicPostingRepository = (*Driver)(nil)
var _ posting.ContextAtomicPostingWithOpenLineRepository = (*Driver)(nil)
var _ posting.ContextAtomicReversalRepository = (*Driver)(nil)
