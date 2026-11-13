package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
)

type publicEventIngestionKey struct {
	companyID uuid.UUID
	key       string
}

type publicEventMappingKey struct {
	companyID uuid.UUID
	eventType string
	ledgerID  string
}

type publicEventState struct {
	ingestions map[publicEventIngestionKey]publicevents.Ingestion
	mappings   map[publicEventMappingKey]publicevents.AccountMapping
}

var (
	publicEventMu    sync.Mutex
	publicEventByDrv = map[*Driver]*publicEventState{}
)

func (d *Driver) publicEventState() *publicEventState {
	publicEventMu.Lock()
	defer publicEventMu.Unlock()
	st, ok := publicEventByDrv[d]
	if !ok {
		st = &publicEventState{
			ingestions: map[publicEventIngestionKey]publicevents.Ingestion{},
			mappings:   map[publicEventMappingKey]publicevents.AccountMapping{},
		}
		publicEventByDrv[d] = st
	}
	return st
}

func (d *Driver) GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (publicevents.Ingestion, bool, error) {
	if err := ctx.Err(); err != nil {
		return publicevents.Ingestion{}, false, err
	}
	st := d.publicEventState()
	d.mu.RLock()
	defer d.mu.RUnlock()
	rec, ok := st.ingestions[publicEventIngestionKey{companyID: companyID, key: strings.TrimSpace(idempotencyKey)}]
	return rec, ok, nil
}

func (d *Driver) ClaimPublicEventIngestion(ctx context.Context, record publicevents.Ingestion) (publicevents.Ingestion, bool, error) {
	if err := ctx.Err(); err != nil {
		return publicevents.Ingestion{}, false, err
	}
	key := strings.TrimSpace(record.IdempotencyKey)
	record.IdempotencyKey = key
	if record.Status == "" {
		record.Status = "processing"
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := publicEventIngestionKey{companyID: record.CompanyID, key: key}
	if existing, ok := st.ingestions[k]; ok {
		if existing.Status == "failed" && existing.PayloadHash == record.PayloadHash {
			existing.Status = "processing"
			existing.FailureReason = ""
			st.ingestions[k] = existing
			return existing, true, nil
		}
		return existing, false, nil
	}
	st.ingestions[k] = record
	return record, true, nil
}

func (d *Driver) CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := publicEventIngestionKey{companyID: companyID, key: strings.TrimSpace(idempotencyKey)}
	rec, ok := st.ingestions[k]
	if !ok {
		return publicevents.ErrRecordNotFound
	}
	if rec.EntryID != uuid.Nil && rec.EntryID != uuid.UUID(entry.ID) {
		return publicevents.ErrStateConflict
	}
	if rec.Status != "processing" && (rec.Status != "processed" || rec.EntryID != uuid.UUID(entry.ID)) {
		return publicevents.ErrStateConflict
	}
	rec.Status = "processed"
	rec.FailureReason = ""
	rec.EntryID = uuid.UUID(entry.ID)
	rec.Entry = entry
	st.ingestions[k] = rec
	return nil
}

func (d *Driver) FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := publicEventIngestionKey{companyID: companyID, key: strings.TrimSpace(idempotencyKey)}
	rec, ok := st.ingestions[k]
	if !ok {
		return publicevents.ErrRecordNotFound
	}
	if rec.Status != "processing" {
		return publicevents.ErrStateConflict
	}
	rec.Status = "failed"
	rec.FailureReason = reason
	st.ingestions[k] = rec
	return nil
}

func (d *Driver) GetPublicEventAccountMapping(ctx context.Context, companyID uuid.UUID, eventType, ledgerID string) (publicevents.AccountMapping, bool, error) {
	if err := ctx.Err(); err != nil {
		return publicevents.AccountMapping{}, false, err
	}
	st := d.publicEventState()
	d.mu.RLock()
	defer d.mu.RUnlock()
	m, ok := st.mappings[publicEventMappingKey{companyID: companyID, eventType: strings.TrimSpace(eventType), ledgerID: strings.TrimSpace(ledgerID)}]
	return m, ok, nil
}

func (d *Driver) SavePublicEventAccountMapping(ctx context.Context, mapping publicevents.AccountMapping) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	st.mappings[publicEventMappingKey{companyID: mapping.CompanyID, eventType: strings.TrimSpace(mapping.EventType), ledgerID: strings.TrimSpace(mapping.LedgerID)}] = mapping
	_ = json.RawMessage{}
	return nil
}

var (
	_ publicevents.IdempotencyStore = (*Driver)(nil)
	_ publicevents.MappingStore     = (*Driver)(nil)
)
