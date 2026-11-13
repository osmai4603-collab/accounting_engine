package subledgers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
)

type AlertNotifier interface {
	SendDiscrepancyAlert(context.Context, DiscrepancyReport) error
}

type reconciliationLogRepository interface {
	SaveReconciliationReport(context.Context, DiscrepancyReport) error
	SaveReconciliationCertificate(context.Context, ReconciliationCertificate) error
}

type ReconciliationDaemon struct {
	engine   *LedgerReconciliationEngine
	notifier AlertNotifier
	interval time.Duration
}

func NewReconciliationDaemon(engine *LedgerReconciliationEngine, notifier AlertNotifier, interval time.Duration) *ReconciliationDaemon {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &ReconciliationDaemon{engine: engine, notifier: notifier, interval: interval}
}

func (d *ReconciliationDaemon) RunDailyCheck(ctx context.Context, companyID uuid.UUID, asOf time.Time) error {
	_, err := d.RunDailyCheckCertificate(ctx, companyID, asOf)
	return err
}

func (d *ReconciliationDaemon) RunDailyCheckCertificate(ctx context.Context, companyID uuid.UUID, asOf time.Time) (ReconciliationCertificate, error) {
	if d == nil || d.engine == nil || companyID == uuid.Nil || asOf.IsZero() {
		return ReconciliationCertificate{}, ErrReconciliationNotConfigured
	}
	asOf = reconciliationCutoff(asOf)
	reports, err := d.engine.ReconcileAllARControls(ctx, companyID, asOf)
	if err != nil {
		return ReconciliationCertificate{}, fmt.Errorf("reconcile AR: %w", err)
	}
	apReports, err := d.engine.ReconcileAllAPControls(ctx, companyID, asOf)
	if err != nil {
		return ReconciliationCertificate{}, fmt.Errorf("reconcile AP: %w", err)
	}
	reports = append(reports, apReports...)
	fixedAssetReports, err := d.engine.ReconcileFixedAssetsControl(ctx, companyID, asOf)
	if err != nil {
		return ReconciliationCertificate{}, fmt.Errorf("reconcile fixed assets: %w", err)
	}
	reports = append(reports, fixedAssetReports...)
	treasuryReports, err := d.engine.ReconcileAllTreasuryControls(ctx, companyID, asOf)
	if err != nil {
		return ReconciliationCertificate{}, fmt.Errorf("reconcile treasury: %w", err)
	}
	reports = append(reports, treasuryReports...)

	certificate := ReconciliationCertificate{
		CompanyID:   companyID,
		AsOfDate:    time.Date(asOf.UTC().Year(), asOf.UTC().Month(), asOf.UTC().Day(), 0, 0, 0, 0, time.UTC),
		CertifiedAt: time.Now().UTC(), Clean: true, Reports: reports,
	}
	for _, report := range reports {
		if report.HasVariance() {
			certificate.Clean = false
			break
		}
	}
	writer, ok := d.engine.glRepo.(reconciliationLogRepository)
	if !ok {
		return ReconciliationCertificate{}, ErrReconciliationNotConfigured
	}
	var resultErr error
	for _, report := range reports {
		if err := writer.SaveReconciliationReport(ctx, report); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("save %s reconciliation report: %w", report.Domain, err))
			continue
		}
		if !report.HasVariance() {
			continue
		}
		if err := appendReconciliationAudit(ctx, d.engine.glRepo, report); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("audit %s reconciliation discrepancy: %w", report.Domain, err))
		}
		if d.notifier != nil {
			if err := d.notifier.SendDiscrepancyAlert(ctx, report); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("notify %s reconciliation discrepancy: %w", report.Domain, err))
			}
		}
	}
	if err := writer.SaveReconciliationCertificate(ctx, certificate); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("save reconciliation certificate: %w", err))
	}
	if err := appendCertificateAudit(ctx, d.engine.glRepo, certificate); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("audit reconciliation certificate: %w", err))
	}
	if resultErr != nil {
		return certificate, resultErr
	}
	return certificate, nil
}

func (d *ReconciliationDaemon) Run(ctx context.Context, companyID uuid.UUID) error {
	if d == nil || d.engine == nil || companyID == uuid.Nil {
		return ErrReconciliationNotConfigured
	}
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := d.RunDailyCheck(ctx, companyID, now); err != nil {
				return err
			}
		}
	}
}

func appendReconciliationAudit(ctx context.Context, repository any, report DiscrepancyReport) error {
	auditRepository, ok := repository.(governance.Repository)
	if !ok {
		return ErrReconciliationNotConfigured
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return governance.NewAuditChain(auditRepository).Append(ctx, governance.FinancialAuditLog{
		CompanyID: report.CompanyID, EntityType: "SUBLEDGER_RECONCILIATION",
		EntityID: report.ControlAccountID, Action: "DISCREPANCY_DETECTED",
		ActorID: reconciliationDaemonActorID(), PayloadAfter: payload, Timestamp: report.DiscoveredAt,
	})
}

func appendCertificateAudit(ctx context.Context, repository any, certificate ReconciliationCertificate) error {
	auditRepository, ok := repository.(governance.Repository)
	if !ok {
		return ErrReconciliationNotConfigured
	}
	payload, err := json.Marshal(certificate)
	if err != nil {
		return err
	}
	return governance.NewAuditChain(auditRepository).Append(ctx, governance.FinancialAuditLog{
		CompanyID: certificate.CompanyID, EntityType: "SUBLEDGER_RECONCILIATION_CERTIFICATE",
		EntityID: certificate.CompanyID, Action: "DAILY_CERTIFICATE_ISSUED",
		ActorID: reconciliationDaemonActorID(), PayloadAfter: payload, Timestamp: certificate.CertifiedAt,
	})
}

func reconciliationDaemonActorID() uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("SYSTEM_DAEMON"))
}
