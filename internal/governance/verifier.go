package governance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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
	expectedPreviousHash := make(map[uuid.UUID]string)
	err := repository.IterateAuditLogs(ctx, 0, func(log FinancialAuditLog) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if log.ID == 0 {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		previous, found := expectedPreviousHash[log.CompanyID]
		if !found {
			previous = GenesisHash
		}
		if log.PreviousLogHash != previous {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		if CalculateHash(log) != log.CurrentLogHash {
			return &AuditIntegrityError{LogID: log.ID, Transaction: log.EntityID.String(), Err: ErrAuditChainBroken}
		}
		expectedPreviousHash[log.CompanyID] = log.CurrentLogHash
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
