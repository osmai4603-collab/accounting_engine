package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	last, err := c.repository.GetLastAuditLog(ctx)
	if err != nil {
		return err
	}
	if last == nil {
		log.PreviousLogHash = GenesisHash
	} else {
		log.PreviousLogHash = last.CurrentLogHash
	}
	log.Timestamp = log.Timestamp.UTC()
	log.CurrentLogHash = CalculateHash(log)
	return c.repository.AppendAuditLog(ctx, log)
}
