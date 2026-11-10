package governance

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type AuditLogHandler func(FinancialAuditLog) error

var ErrCompanyAuditScopeRequired = errors.New("company-scoped audit access is required")

type Repository interface {
	AppendAuditLog(ctx context.Context, log FinancialAuditLog) error
	GetLastAuditLog(ctx context.Context) (*FinancialAuditLog, error)
	IterateAuditLogs(ctx context.Context, fromID int64, handler AuditLogHandler) error
}

type CompanyAuditRepository interface {
	GetLastAuditLogForCompany(ctx context.Context, companyID uuid.UUID) (*FinancialAuditLog, error)
}

type CompanyAuditLogReader interface {
	IterateAuditLogsForCompany(ctx context.Context, companyID uuid.UUID, fromID int64, handler AuditLogHandler) error
}

type AtomicAuditRepository interface {
	AppendAuditLogAtomic(ctx context.Context, log FinancialAuditLog) (FinancialAuditLog, error)
}
