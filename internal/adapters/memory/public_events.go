package memory

import (
	"context"
	"strings"
	"sync"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
)

type publicEventIngestionKey struct {
	companyID    uuid.UUID
	sourceSystem string
	key          string
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

func memoryIngestionKey(companyID uuid.UUID, sourceSystem, key string) publicEventIngestionKey {
	return publicEventIngestionKey{
		companyID:    companyID,
		sourceSystem: publicevents.NormalizeSourceSystem(sourceSystem),
		key:          strings.TrimSpace(key),
	}
}

func (d *Driver) GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (publicevents.Ingestion, bool, error) {
	return d.GetPublicEventIngestionForSource(ctx, companyID, publicevents.DefaultSourceSystem, idempotencyKey)
}

func (d *Driver) GetPublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey string) (publicevents.Ingestion, bool, error) {
	if err := ctx.Err(); err != nil {
		return publicevents.Ingestion{}, false, err
	}
	st := d.publicEventState()
	d.mu.RLock()
	defer d.mu.RUnlock()
	rec, ok := st.ingestions[memoryIngestionKey(companyID, sourceSystem, idempotencyKey)]
	if !ok {
		return publicevents.Ingestion{}, false, nil
	}
	return clonePublicEventIngestion(rec), true, nil
}

func (d *Driver) ClaimPublicEventIngestion(ctx context.Context, record publicevents.Ingestion) (publicevents.Ingestion, bool, error) {
	if err := ctx.Err(); err != nil {
		return publicevents.Ingestion{}, false, err
	}
	record.SourceSystem = publicevents.NormalizeSourceSystem(record.SourceSystem)
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = publicevents.StatusStarted
	}
	record.Entry = clonePublicEventEntry(record.Entry)

	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := memoryIngestionKey(record.CompanyID, record.SourceSystem, record.IdempotencyKey)
	if existing, ok := st.ingestions[k]; ok {
		if existing.PayloadHash != record.PayloadHash {
			return clonePublicEventIngestion(existing), false, nil
		}
		if publicevents.IsRetryableFailure(existing.Status) || existing.Status == publicevents.StatusFailed {
			// Reclaim a retryable (or legacy failed) attempt. Keep the original
			// entry identity so a committed entry can never be duplicated, and
			// preserve the caller's status vocabulary (legacy callers pass
			// "processing" while the SDK passes "started").
			existing.Status = record.Status
			existing.FailureReason = ""
			existing.FailureClass = ""
			st.ingestions[k] = existing
			return clonePublicEventIngestion(existing), true, nil
		}
		return clonePublicEventIngestion(existing), false, nil
	}
	st.ingestions[k] = clonePublicEventIngestion(record)
	return clonePublicEventIngestion(record), true, nil
}

func (d *Driver) CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error {
	// Preserve the legacy status value for callers that predate the canonical
	// lifecycle; it is equivalent to completed on read.
	return d.completePublicEventIngestion(ctx, companyID, publicevents.DefaultSourceSystem, idempotencyKey, "", publicevents.StatusProcessed, entry)
}

func (d *Driver) CompletePublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, payloadHash string, entry ledger.JournalEntry) error {
	return d.completePublicEventIngestion(ctx, companyID, sourceSystem, idempotencyKey, payloadHash, publicevents.StatusCompleted, entry)
}

func (d *Driver) completePublicEventIngestion(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, payloadHash, status string, entry ledger.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := memoryIngestionKey(companyID, sourceSystem, idempotencyKey)
	rec, ok := st.ingestions[k]
	if !ok {
		return publicevents.ErrRecordNotFound
	}
	if payloadHash != "" && rec.PayloadHash != "" && rec.PayloadHash != payloadHash {
		return publicevents.ErrStateConflict
	}
	if uuid.UUID(entry.ID) == uuid.Nil {
		return publicevents.ErrStateConflict
	}
	if publicevents.IsCompleted(rec.Status) {
		if rec.EntryID == uuid.UUID(entry.ID) {
			return nil
		}
		return publicevents.ErrStateConflict
	}
	if rec.EntryID != uuid.Nil && rec.EntryID != uuid.UUID(entry.ID) {
		return publicevents.ErrStateConflict
	}
	if !publicevents.IsStarted(rec.Status) && !publicevents.IsRetryableFailure(rec.Status) {
		return publicevents.ErrStateConflict
	}
	rec.Status = status
	rec.FailureReason = ""
	rec.FailureClass = ""
	rec.EntryID = uuid.UUID(entry.ID)
	rec.Entry = clonePublicEventEntry(entry)
	st.ingestions[k] = rec
	return nil
}

func (d *Driver) FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error {
	// Preserve the legacy status value for callers that predate classified
	// failures; it is equivalent to terminal_failure on read.
	return d.failPublicEventIngestion(ctx, companyID, publicevents.DefaultSourceSystem, idempotencyKey,
		publicevents.StatusFailed, publicevents.FailureClassTerminal, reason)
}

func (d *Driver) FailPublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, failureClass, reason string) error {
	return d.FailPublicEventIngestionClassified(ctx, companyID, sourceSystem, idempotencyKey, failureClass, reason)
}

func (d *Driver) FailPublicEventIngestionClassified(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, failureClass, reason string) error {
	status := publicevents.StatusTerminalFailure
	if failureClass == publicevents.FailureClassTransient {
		status = publicevents.StatusRetryableFailure
	}
	return d.failPublicEventIngestion(ctx, companyID, sourceSystem, idempotencyKey, status, failureClass, reason)
}

func (d *Driver) failPublicEventIngestion(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, status, failureClass, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := d.publicEventState()
	d.mu.Lock()
	defer d.mu.Unlock()
	k := memoryIngestionKey(companyID, sourceSystem, idempotencyKey)
	rec, ok := st.ingestions[k]
	if !ok {
		return publicevents.ErrRecordNotFound
	}
	if !publicevents.IsStarted(rec.Status) && !publicevents.IsRetryableFailure(rec.Status) {
		return publicevents.ErrStateConflict
	}
	rec.Status = status
	rec.FailureClass = failureClass
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
	return nil
}

func clonePublicEventIngestion(rec publicevents.Ingestion) publicevents.Ingestion {
	rec.Entry = clonePublicEventEntry(rec.Entry)
	return rec
}

func clonePublicEventEntry(entry ledger.JournalEntry) ledger.JournalEntry {
	if entry.PostedAt != nil {
		postedAt := *entry.PostedAt
		entry.PostedAt = &postedAt
	}
	if entry.Lines == nil {
		return entry
	}
	lines := make([]ledger.EntryLine, len(entry.Lines))
	for i, line := range entry.Lines {
		if line.PartnerID != nil {
			id := *line.PartnerID
			line.PartnerID = &id
		}
		if line.CostCenterID != nil {
			id := *line.CostCenterID
			line.CostCenterID = &id
		}
		if line.ProjectID != nil {
			id := *line.ProjectID
			line.ProjectID = &id
		}
		if line.SegmentID != nil {
			id := *line.SegmentID
			line.SegmentID = &id
		}
		if line.BusinessUnitID != nil {
			id := *line.BusinessUnitID
			line.BusinessUnitID = &id
		}
		lines[i] = line
	}
	entry.Lines = lines
	return entry
}

var (
	_ publicevents.IdempotencyStore           = (*Driver)(nil)
	_ publicevents.ScopedIdempotencyStore     = (*Driver)(nil)
	_ publicevents.ClassifiedIdempotencyStore = (*Driver)(nil)
	_ publicevents.MappingStore               = (*Driver)(nil)
)
