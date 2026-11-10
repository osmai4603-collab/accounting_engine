package memory

import (
	"context"
	"errors"

	"accounting_engine/internal/subledgers"
	"github.com/google/uuid"
)

func (d *Driver) SaveReconciliationReport(ctx context.Context, report subledgers.DiscrepancyReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if report.CompanyID == uuid.Nil || report.ControlAccountID == uuid.Nil ||
		report.Domain == "" || report.FunctionalCurr == "" || report.AsOfDate.IsZero() {
		return subledgers.ErrInvalidOpenLine
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	copy := report
	copy.Breakdown = append([]subledgers.ReconciliationBreakdown(nil), report.Breakdown...)
	d.reconciliationReports = append(d.reconciliationReports, copy)
	return nil
}

func (d *Driver) SaveReconciliationCertificate(ctx context.Context, certificate subledgers.ReconciliationCertificate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if certificate.CompanyID == uuid.Nil || certificate.AsOfDate.IsZero() || certificate.CertifiedAt.IsZero() {
		return errors.New("invalid reconciliation certificate")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	copy := certificate
	copy.Reports = append([]subledgers.DiscrepancyReport(nil), certificate.Reports...)
	d.reconciliationCertificates = append(d.reconciliationCertificates, copy)
	return nil
}
