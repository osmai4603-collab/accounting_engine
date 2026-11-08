package governance

import (
	"context"
)

type AuditLogHandler func(FinancialAuditLog) error

type Repository interface {
	AppendAuditLog(ctx context.Context, log FinancialAuditLog) error
	GetLastAuditLog(ctx context.Context) (*FinancialAuditLog, error)
	IterateAuditLogs(ctx context.Context, fromID int64, handler AuditLogHandler) error
}
