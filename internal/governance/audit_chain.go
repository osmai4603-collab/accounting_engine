package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

var ErrAuditChainBroken = errors.New("audit chain integrity violation")

type FinancialAuditLog struct {
	CompanyID       uuid.UUID
	ID              int64
	EntityType      string
	EntityID        uuid.UUID
	Action          string
	ActorID         uuid.UUID
	PayloadBefore   []byte
	PayloadAfter    []byte
	Timestamp       time.Time
	PreviousLogHash string
	CurrentLogHash  string
}

type AuditChain struct {
	repository Repository
	mu         sync.Mutex
}

func NewAuditChain(repository Repository) *AuditChain {
	return &AuditChain{repository: repository}
}

func CalculateHash(log FinancialAuditLog) string {
	timestamp := log.Timestamp.UTC().Format(time.RFC3339Nano)
	input := log.PreviousLogHash +
		log.CompanyID.String() +
		log.EntityType +
		log.EntityID.String() +
		log.Action +
		log.ActorID.String() +
		string(log.PayloadBefore) +
		string(log.PayloadAfter) +
		timestamp
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func CalculateAuditHash(log FinancialAuditLog) string {
	return CalculateHash(log)
}

func (c *AuditChain) Append(ctx context.Context, log FinancialAuditLog) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if repository, ok := c.repository.(AtomicAuditRepository); ok {
		_, err := repository.AppendAuditLogAtomic(ctx, log)
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		last, err := c.lastLogForCompany(ctx, log.CompanyID)
		if err != nil {
			return err
		}
		if last == nil {
			log.PreviousLogHash = GenesisHash
		} else {
			log.PreviousLogHash = last.CurrentLogHash
		}
		if log.Timestamp.IsZero() {
			log.Timestamp = time.Now().UTC()
		} else {
			log.Timestamp = log.Timestamp.UTC()
		}
		log.CurrentLogHash = CalculateHash(log)
		if err := c.repository.AppendAuditLog(ctx, log); err != nil {
			lastErr = err
			time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func (c *AuditChain) lastLogForCompany(ctx context.Context, companyID uuid.UUID) (*FinancialAuditLog, error) {
	if repository, ok := c.repository.(CompanyAuditRepository); ok {
		return repository.GetLastAuditLogForCompany(ctx, companyID)
	}
	var last *FinancialAuditLog
	err := c.repository.IterateAuditLogs(ctx, 0, func(log FinancialAuditLog) error {
		if log.CompanyID == companyID && (last == nil || log.ID > last.ID) {
			copy := log
			last = &copy
		}
		return nil
	})
	return last, err
}
