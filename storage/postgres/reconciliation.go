package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
)

func (d *Driver) SaveReconciliationReport(ctx context.Context, report subledgers.DiscrepancyReport) error {
	if report.CompanyID == uuid.Nil || report.ControlAccountID == uuid.Nil ||
		report.Domain == "" || report.FunctionalCurr == "" || report.AsOfDate.IsZero() {
		return subledgers.ErrInvalidOpenLine
	}
	details, err := json.Marshal(report)
	if err != nil {
		return err
	}
	status := "balanced"
	if report.HasVariance() {
		status = "discrepancy"
	}
	return d.WithCompanyTx(ctx, report.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO subledger_reconciliation_logs
			(company_id,domain,control_account_id,as_of_date,functional_curr,
			 subledger_balance,gl_balance,difference,status,details,certified_by,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'SYSTEM_DAEMON',$11)`,
			report.CompanyID, report.Domain, report.ControlAccountID, report.AsOfDate,
			report.FunctionalCurr, report.SubledgerBalance, report.GLBalance, report.Difference,
			status, details, report.DiscoveredAt)
		return err
	})
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
