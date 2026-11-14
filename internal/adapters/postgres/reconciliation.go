package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
)

func (d *Driver) SaveReconciliationReport(ctx context.Context, report subledgers.DiscrepancyReport) error {
	var exception *subledgers.ReconciliationException
	if report.HasVariance() {
		created := subledgers.NewReconciliationException(report)
		exception = &created
	}
	return d.SaveReconciliationOutcome(ctx, report, exception)
}

func (d *Driver) SaveReconciliationOutcome(
	ctx context.Context,
	report subledgers.DiscrepancyReport,
	exception *subledgers.ReconciliationException,
) error {
	if err := subledgers.ValidateReconciliationOutcome(report, exception); err != nil {
		return err
	}
	details, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, report.CompanyID, func(tx pgx.Tx) error {
		if err := insertReconciliationReport(ctx, tx, report, details); err != nil {
			return err
		}
		if exception != nil {
			return insertReconciliationException(ctx, tx, *exception)
		}
		return nil
	})
}

func insertReconciliationReport(ctx context.Context, tx pgx.Tx, report subledgers.DiscrepancyReport, details []byte) error {
	status := "balanced"
	if report.HasVariance() {
		status = "discrepancy"
	}
	_, err := tx.Exec(ctx, `INSERT INTO subledger_reconciliation_logs
		(company_id,domain,control_account_id,as_of_date,functional_curr,
		 subledger_balance,gl_balance,difference,status,details,certified_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'SYSTEM_DAEMON',$11)`,
		report.CompanyID, report.Domain, report.ControlAccountID, report.AsOfDate,
		report.FunctionalCurr, report.SubledgerBalance, report.GLBalance, report.Difference,
		status, details, report.DiscoveredAt)
	return err
}

func (d *Driver) SaveReconciliationCertificate(ctx context.Context, certificate subledgers.ReconciliationCertificate) error {
	if certificate.CompanyID == uuid.Nil || certificate.AsOfDate.IsZero() || certificate.CertifiedAt.IsZero() {
		return subledgers.ErrInvalidOpenLine
	}
	details, err := json.Marshal(certificate)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, certificate.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO subledger_reconciliation_certificates
			(company_id,as_of_date,is_clean,details,certified_at)
			VALUES($1,$2,$3,$4,$5)`,
			certificate.CompanyID, certificate.AsOfDate, certificate.Clean, details, certificate.CertifiedAt)
		return err
	})
}

func (d *Driver) CreateReconciliationException(ctx context.Context, exception subledgers.ReconciliationException) error {
	if err := subledgers.ValidateReconciliationException(exception); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, exception.CompanyID, func(tx pgx.Tx) error {
		return insertReconciliationException(ctx, tx, exception)
	})
}

func insertReconciliationException(ctx context.Context, tx pgx.Tx, exception subledgers.ReconciliationException) error {
	if err := subledgers.ValidateReconciliationException(exception); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO subledger_reconciliation_exceptions
		(id,company_id,domain,control_account_id,as_of_date,functional_curr,
		 subledger_balance,gl_balance,difference,status,actor_id,reason,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		exception.ID, exception.CompanyID, exception.Domain, exception.ControlAccountID,
		exception.AsOfDate.UTC(), exception.FunctionalCurr, exception.SubledgerBalance,
		exception.GLBalance, exception.Difference, exception.Status, exception.ActorID,
		strings.TrimSpace(exception.Reason), exception.CreatedAt.UTC(), exception.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO subledger_reconciliation_exception_events
		(company_id,exception_id,from_status,to_status,actor_id,reason,changed_at)
		VALUES($1,$2,NULL,$3,$4,$5,$6)`,
		exception.CompanyID, exception.ID, exception.Status, exception.ActorID,
		strings.TrimSpace(exception.Reason), exception.CreatedAt.UTC())
	return err
}

func (d *Driver) GetReconciliationException(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
) (result subledgers.ReconciliationException, resultErr error) {
	if companyID == uuid.Nil || exceptionID == uuid.Nil {
		return result, subledgers.ErrInvalidOpenLine
	}
	resultErr = d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, reconciliationExceptionSelect+`
			WHERE company_id=$1 AND id=$2`, companyID, exceptionID).Scan(
			&result.ID, &result.CompanyID, &result.Domain, &result.ControlAccountID,
			&result.AsOfDate, &result.FunctionalCurr, &result.SubledgerBalance,
			&result.GLBalance, &result.Difference, &result.Status, &result.ActorID,
			&result.Reason, &result.CreatedAt, &result.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrReconciliationExceptionNotFound
		}
		return err
	})
	return result, resultErr
}

func (d *Driver) ListReconciliationExceptions(
	ctx context.Context,
	companyID uuid.UUID,
) (result []subledgers.ReconciliationException, resultErr error) {
	if companyID == uuid.Nil {
		return nil, subledgers.ErrInvalidOpenLine
	}
	resultErr = d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, reconciliationExceptionSelect+`
			WHERE company_id=$1 ORDER BY as_of_date, id`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		result = make([]subledgers.ReconciliationException, 0)
		for rows.Next() {
			var exception subledgers.ReconciliationException
			if err := rows.Scan(
				&exception.ID, &exception.CompanyID, &exception.Domain, &exception.ControlAccountID,
				&exception.AsOfDate, &exception.FunctionalCurr, &exception.SubledgerBalance,
				&exception.GLBalance, &exception.Difference, &exception.Status, &exception.ActorID,
				&exception.Reason, &exception.CreatedAt, &exception.UpdatedAt,
			); err != nil {
				return err
			}
			result = append(result, exception)
		}
		return rows.Err()
	})
	return result, resultErr
}

func (d *Driver) ListReconciliationExceptionTransitions(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
) (result []subledgers.ReconciliationExceptionTransition, resultErr error) {
	if companyID == uuid.Nil || exceptionID == uuid.Nil {
		return nil, subledgers.ErrInvalidOpenLine
	}
	resultErr = d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT exception_id,company_id,
			COALESCE(from_status,''),to_status,actor_id,reason,changed_at
			FROM subledger_reconciliation_exception_events
			WHERE company_id=$1 AND exception_id=$2 ORDER BY changed_at,id`,
			companyID, exceptionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		result = make([]subledgers.ReconciliationExceptionTransition, 0)
		for rows.Next() {
			var transition subledgers.ReconciliationExceptionTransition
			if err := rows.Scan(
				&transition.ExceptionID, &transition.CompanyID, &transition.FromStatus,
				&transition.ToStatus, &transition.ActorID, &transition.Reason, &transition.ChangedAt,
			); err != nil {
				return err
			}
			result = append(result, transition)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result) == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM subledger_reconciliation_exceptions
				WHERE company_id=$1 AND id=$2)`, companyID, exceptionID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return subledgers.ErrReconciliationExceptionNotFound
			}
		}
		return nil
	})
	return result, resultErr
}

func (d *Driver) TransitionReconciliationException(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
	expected, next subledgers.ReconciliationExceptionStatus,
	actorID uuid.UUID,
	reason string,
	changedAt time.Time,
) error {
	reason = strings.TrimSpace(reason)
	if companyID == uuid.Nil || exceptionID == uuid.Nil || actorID == uuid.Nil ||
		!subledgers.ValidReconciliationExceptionTransition(expected, next) ||
		reason == "" || changedAt.IsZero() {
		return subledgers.ErrInvalidReconciliationTransition
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var current subledgers.ReconciliationExceptionStatus
		err := tx.QueryRow(ctx, `SELECT status FROM subledger_reconciliation_exceptions
			WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, exceptionID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrReconciliationExceptionNotFound
		}
		if err != nil {
			return err
		}
		if current != expected {
			return subledgers.ErrReconciliationExceptionConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE subledger_reconciliation_exceptions
			SET status=$3,actor_id=$4,reason=$5,updated_at=$6
			WHERE company_id=$1 AND id=$2`,
			companyID, exceptionID, next, actorID, reason, changedAt.UTC()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO subledger_reconciliation_exception_events
			(company_id,exception_id,from_status,to_status,actor_id,reason,changed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7)`,
			companyID, exceptionID, expected, next, actorID, reason, changedAt.UTC())
		return err
	})
}

const reconciliationExceptionSelect = `SELECT id,company_id,domain,control_account_id,
	as_of_date,functional_curr,subledger_balance,gl_balance,difference,status,
	actor_id,reason,created_at,updated_at FROM subledger_reconciliation_exceptions `
