package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := subledgers.ValidateReconciliationOutcome(report, exception); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if exception != nil {
		if _, exists := d.reconciliationExceptions[exception.ID]; exists {
			return subledgers.ErrAlreadyExists
		}
	}
	copy := report
	copy.Breakdown = append([]subledgers.ReconciliationBreakdown(nil), report.Breakdown...)
	d.reconciliationReports = append(d.reconciliationReports, copy)
	if exception != nil {
		d.storeReconciliationException(*exception)
	}
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

func (d *Driver) CreateReconciliationException(ctx context.Context, exception subledgers.ReconciliationException) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := subledgers.ValidateReconciliationException(exception); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.reconciliationExceptions[exception.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	d.storeReconciliationException(exception)
	return nil
}

func (d *Driver) storeReconciliationException(exception subledgers.ReconciliationException) {
	d.reconciliationExceptions[exception.ID] = exception
	d.reconciliationExceptionLog[exception.ID] = []subledgers.ReconciliationExceptionTransition{{
		ExceptionID: exception.ID, CompanyID: exception.CompanyID,
		ToStatus: exception.Status, ActorID: exception.ActorID,
		Reason: exception.Reason, ChangedAt: exception.CreatedAt,
	}}
}

func (d *Driver) GetReconciliationException(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
) (subledgers.ReconciliationException, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.ReconciliationException{}, err
	}
	if companyID == uuid.Nil || exceptionID == uuid.Nil {
		return subledgers.ReconciliationException{}, subledgers.ErrInvalidOpenLine
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	exception, ok := d.reconciliationExceptions[exceptionID]
	if !ok || exception.CompanyID != companyID {
		return subledgers.ReconciliationException{}, subledgers.ErrReconciliationExceptionNotFound
	}
	return exception, nil
}

func (d *Driver) ListReconciliationExceptions(
	ctx context.Context,
	companyID uuid.UUID,
) ([]subledgers.ReconciliationException, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil {
		return nil, subledgers.ErrInvalidOpenLine
	}
	if driver := transactionDriver(ctx, d); driver != d {
		return driver.ListReconciliationExceptions(ctx, companyID)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	exceptions := make([]subledgers.ReconciliationException, 0)
	for _, exception := range d.reconciliationExceptions {
		if exception.CompanyID == companyID {
			exceptions = append(exceptions, exception)
		}
	}
	sort.Slice(exceptions, func(i, j int) bool {
		if exceptions[i].AsOfDate.Equal(exceptions[j].AsOfDate) {
			return exceptions[i].ID.String() < exceptions[j].ID.String()
		}
		return exceptions[i].AsOfDate.Before(exceptions[j].AsOfDate)
	})
	return exceptions, nil
}

func (d *Driver) ListReconciliationExceptionTransitions(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
) ([]subledgers.ReconciliationExceptionTransition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil || exceptionID == uuid.Nil {
		return nil, subledgers.ErrInvalidOpenLine
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	exception, ok := d.reconciliationExceptions[exceptionID]
	if !ok || exception.CompanyID != companyID {
		return nil, subledgers.ErrReconciliationExceptionNotFound
	}
	return append([]subledgers.ReconciliationExceptionTransition(nil),
		d.reconciliationExceptionLog[exceptionID]...), nil
}

func (d *Driver) TransitionReconciliationException(
	ctx context.Context,
	companyID, exceptionID uuid.UUID,
	expected, next subledgers.ReconciliationExceptionStatus,
	actorID uuid.UUID,
	reason string,
	changedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil || exceptionID == uuid.Nil || actorID == uuid.Nil ||
		!subledgers.ValidReconciliationExceptionTransition(expected, next) ||
		strings.TrimSpace(reason) == "" || changedAt.IsZero() {
		return subledgers.ErrInvalidReconciliationTransition
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	exception, ok := d.reconciliationExceptions[exceptionID]
	if !ok || exception.CompanyID != companyID {
		return subledgers.ErrReconciliationExceptionNotFound
	}
	if exception.Status != expected {
		return subledgers.ErrReconciliationExceptionConflict
	}
	exception.Status = next
	exception.ActorID = actorID
	exception.Reason = strings.TrimSpace(reason)
	exception.UpdatedAt = changedAt.UTC()
	d.reconciliationExceptions[exceptionID] = exception
	d.reconciliationExceptionLog[exceptionID] = append(
		d.reconciliationExceptionLog[exceptionID],
		subledgers.ReconciliationExceptionTransition{
			ExceptionID: exceptionID, CompanyID: companyID, FromStatus: expected,
			ToStatus: next, ActorID: actorID, Reason: exception.Reason, ChangedAt: changedAt.UTC(),
		},
	)
	return nil
}
