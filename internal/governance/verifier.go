package governance

import (
	"context"
	"fmt"
)

type AuditIntegrityError struct {
	LogID       int64
	Transaction string
	Err         error
}

func (e *AuditIntegrityError) Error() string {
	return fmt.Sprintf("%v: audit log %d, transaction %q", e.Err, e.LogID, e.Transaction)
}

func (e *AuditIntegrityError) Unwrap() error {
	return e.Err
}

func VerifyAuditIntegrity(ctx context.Context, repository Repository) (bool, error) {
	expectedPreviousHash := GenesisHash
	expectedID := int64(1)
	first := true
	err := repository.IterateAuditLogs(ctx, 0, func(log FinancialAuditLog) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if log.ID == 0 {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		if log.ID != expectedID && expectedID != 0 {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		if log.PreviousLogHash != expectedPreviousHash {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		if first {
			if log.PreviousLogHash != GenesisHash {
				return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
			}
			first = false
		}
		if CalculateHash(log) != log.CurrentLogHash {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		expectedPreviousHash = log.CurrentLogHash
		expectedID++
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *AuditChain) VerifyIntegrity(ctx context.Context) (bool, error) {
	return VerifyAuditIntegrity(ctx, c.repository)
}

func (c *AuditChain) VerifyAuditIntegrity(ctx context.Context) (bool, error) {
	return VerifyAuditIntegrity(ctx, c.repository)
}
