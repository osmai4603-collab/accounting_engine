package governance

import (
	"context"

	"github.com/google/uuid"
)

type AuditLogHandler func(FinancialAuditLog) error

type Repository interface {
	AppendAuditLog(ctx context.Context, log FinancialAuditLog) error
	GetLastAuditLog(ctx context.Context) (*FinancialAuditLog, error)
	IterateAuditLogs(ctx context.Context, fromID int64, handler AuditLogHandler) error
}

type CompanyAuditRepository interface {
	GetLastAuditLogForCompany(ctx context.Context, companyID uuid.UUID) (*FinancialAuditLog, error)
}

type AtomicAuditRepository interface {
	AppendAuditLogAtomic(ctx context.Context, log FinancialAuditLog) (FinancialAuditLog, error)
}
