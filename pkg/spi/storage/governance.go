package storage

import (
	"context"

	"accounting_engine/pkg/api/v1/types"
)

type GovernanceStore interface {
	RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error
	GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error)
}
