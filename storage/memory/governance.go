package memory

import (
	"context"
	"sort"

	"github.com/osmai4603-collab/accounting_engine/internal/governance"
)

func (d *Driver) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	log.ID = int64(len(d.audit) + 1)
	copyLog := log
	copyLog.PayloadBefore = append([]byte(nil), log.PayloadBefore...)
	copyLog.PayloadAfter = append([]byte(nil), log.PayloadAfter...)
	d.audit = append(d.audit, &copyLog)
	return nil
}
func (d *Driver) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.audit) == 0 {
		return nil, nil
	}
	log := *d.audit[len(d.audit)-1]
	return &log, nil
}
func (d *Driver) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	d.mu.RLock()
	logs := make([]governance.FinancialAuditLog, 0, len(d.audit))
	for _, log := range d.audit {
		if log.ID >= fromID {
			logs = append(logs, *log)
		}

	}
	d.mu.RUnlock()
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })
	for _, log := range logs {
		if err := handler(log); err != nil {
			return err
		}
	}
	return nil
}
