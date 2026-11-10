package postgres

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/governance"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
