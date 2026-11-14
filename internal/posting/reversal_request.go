package posting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ReversalRequest struct {
	ActorID        string
	Reason         string
	EffectiveDate  time.Time
	Reference      string
	SourceSystem   string
	IdempotencyKey string
}

type reversalRequestFingerprint struct {
	CompanyID      uuid.UUID `json:"company_id"`
	OriginalEntry  uuid.UUID `json:"original_entry_id"`
	ActorID        string    `json:"actor_id"`
	Reason         string    `json:"reason"`
	EffectiveDate  string    `json:"effective_date,omitempty"`
	Reference      string    `json:"reference"`
	SourceSystem   string    `json:"source_system"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func ReversalRequestHash(companyID, originalID uuid.UUID, request ReversalRequest) (string, error) {
	effectiveDate := ""
	if !request.EffectiveDate.IsZero() {
		effectiveDate = request.EffectiveDate.UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(reversalRequestFingerprint{
		CompanyID: companyID, OriginalEntry: originalID,
		ActorID: strings.TrimSpace(request.ActorID), Reason: strings.TrimSpace(request.Reason),
		EffectiveDate: effectiveDate, Reference: strings.TrimSpace(request.Reference),
		SourceSystem:   strings.TrimSpace(request.SourceSystem),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
