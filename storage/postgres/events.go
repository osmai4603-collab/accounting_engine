package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveEvent(ctx context.Context, event subledgers.EconomicEvent) error {
	if event.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = subledgers.EventReceived
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, event.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO subledger_events(
			 company_id,source_system,idempotency_key,event_id,external_id,event_type,event_version,
			 payload_bytes,status,generated_entry_id,error_message,created_at,processed_at,event
			 ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			 ON CONFLICT(company_id,source_system,idempotency_key) DO NOTHING`,
			event.CompanyID, event.SourceSystem, event.IdempotencyKey, event.EventID, event.ExternalID,
			event.EventType, event.EventVersion, event.PayloadJSON, event.Status, event.GeneratedEntryID,
			event.ErrorMessage, event.CreatedAt, event.ProcessedAt, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return subledgers.ErrAlreadyExists
		}
		return nil
	})
}

func (d *Driver) SaveEventAccountingRule(ctx context.Context, rule subledgers.EventAccountingRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, rule.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO event_accounting_rules(id,company_id,event_type,priority,rule_data)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(company_id,event_type,priority) DO UPDATE
			 SET id=EXCLUDED.id,rule_data=EXCLUDED.rule_data`,
			rule.ID, rule.CompanyID, rule.EventType, rule.Priority, data)
		return err
	})
}

func (d *Driver) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	rules := make([]subledgers.EventAccountingRule, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT rule_data FROM event_accounting_rules
			 WHERE company_id=$1 AND event_type=$2 ORDER BY priority DESC, id`,
			companyID, eventType)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var rule subledgers.EventAccountingRule
			if err := json.Unmarshal(data, &rule); err != nil {
				return err
			}
			rules = append(rules, rule)
		}
		return rows.Err()
	})
	return rules, err
}

func (d *Driver) SaveEventAndDraft(ctx context.Context, event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = subledgers.EventProcessed
	}
	if event.GeneratedEntryID == nil {
		entryID := entry.ID
		event.GeneratedEntryID = &entryID
	}
	if *event.GeneratedEntryID != entry.ID {
		return posting.ErrInvalidEntry
	}
	event.Status = subledgers.EventProcessed
	now := time.Now().UTC()
	if event.ProcessedAt == nil {
		event.ProcessedAt = &now
	}
	eventID := event.EventID
	if entry.SourceEventID == nil {
		entry.SourceEventID = &eventID
	}
	if *entry.SourceEventID != event.EventID {
		return posting.ErrInvalidEntry
	}
	tx, err := d.beginCompanyTx(ctx, event.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`INSERT INTO subledger_events(
		 company_id,source_system,idempotency_key,event_id,external_id,event_type,event_version,
		 payload_bytes,status,generated_entry_id,error_message,created_at,processed_at,event
		 ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT(company_id,source_system,idempotency_key) DO NOTHING`,
		event.CompanyID, event.SourceSystem, event.IdempotencyKey, event.EventID, event.ExternalID,
		event.EventType, event.EventVersion, event.PayloadJSON, event.Status, event.GeneratedEntryID,
		event.ErrorMessage, event.CreatedAt, event.ProcessedAt, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var stored subledgers.EconomicEvent
		var storedData, storedPayload []byte
		lookupErr := tx.QueryRow(ctx,
			`SELECT event,payload_bytes FROM subledger_events
			 WHERE company_id=$1 AND source_system=$2 AND idempotency_key=$3 FOR UPDATE`,
			event.CompanyID, event.SourceSystem, event.IdempotencyKey).Scan(&storedData, &storedPayload)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if lookupErr != nil {
			return lookupErr
		}
		if err := json.Unmarshal(storedData, &stored); err != nil {
			return err
		}
		stored.PayloadJSON = append([]byte(nil), storedPayload...)
		if stored.ResultEntryID != uuid.Nil {
			return subledgers.ErrAlreadyExists
		}
		if stored.EventID != uuid.Nil {
			event.EventID = stored.EventID
		}
		if stored.CreatedAt.IsZero() == false {
			event.CreatedAt = stored.CreatedAt
		}
		if stored.PayloadHash != "" && stored.PayloadHash != event.PayloadHash {
			return subledgers.ErrAlreadyExists
		}
	}
	eventID = event.EventID
	entry.SourceEventID = &eventID
	entryID := entry.ID
	event.GeneratedEntryID = &entryID
	event.ResultEntryID = entry.ID
	event.Status = subledgers.EventProcessed
	if event.ProcessedAt == nil {
		event.ProcessedAt = &now
	}
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	data, err = json.Marshal(event)
	if err != nil {
		return err
	}
	result, err = tx.Exec(ctx,
		`UPDATE subledger_events
		 SET event_id=$4,external_id=$5,event_type=$6,event_version=$7,payload_bytes=$8,status=$9,
		     generated_entry_id=$10,error_message=$11,created_at=$12,processed_at=$13,event=$14
		 WHERE company_id=$1 AND source_system=$2 AND idempotency_key=$3`,
		event.CompanyID, event.SourceSystem, event.IdempotencyKey, event.EventID, event.ExternalID,
		event.EventType, event.EventVersion, event.PayloadJSON, event.Status, event.GeneratedEntryID,
		event.ErrorMessage, event.CreatedAt, event.ProcessedAt, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return subledgers.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetEventByIdempotencyKey(ctx context.Context, key string) (subledgers.EconomicEvent, bool, error) {
	return subledgers.EconomicEvent{}, false, subledgers.ErrCompanyScopedEventsRequired
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	var event subledgers.EconomicEvent
	found := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT event,payload_bytes FROM subledger_events
			 WHERE company_id=$1 AND idempotency_key=$2`,
			companyID, key)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if found {
				return subledgers.ErrInvalidEvent
			}
			var data, payload []byte
			if err := rows.Scan(&data, &payload); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return err
			}
			event.PayloadJSON = append([]byte(nil), payload...)
			found = true
		}
		return rows.Err()
	})
	return event, found, err
}

func (d *Driver) GetEventByIdentity(ctx context.Context, companyID uuid.UUID, sourceSystem, key string) (subledgers.EconomicEvent, bool, error) {
	var event subledgers.EconomicEvent
	found := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var lookupErr error
		event, found, lookupErr = getEventByIdentityTx(ctx, tx, companyID, sourceSystem, key)
		return lookupErr
	})
	return event, found, err
}

func (d *Driver) GetEventByID(ctx context.Context, companyID, eventID uuid.UUID) (subledgers.EconomicEvent, error) {
	var event subledgers.EconomicEvent
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data, payload []byte
		err := tx.QueryRow(ctx,
			`SELECT event,payload_bytes FROM subledger_events WHERE company_id=$1 AND event_id=$2`,
			companyID, eventID).Scan(&data, &payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		event.PayloadJSON = append([]byte(nil), payload...)
		return nil
	})
	return event, err
}

func (d *Driver) UpdateEventStatus(ctx context.Context, event subledgers.EconomicEvent) error {
	if event.CompanyID == uuid.Nil || event.EventID == uuid.Nil {
		return subledgers.ErrInvalidEvent
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, event.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`UPDATE subledger_events
			 SET status=$4,error_message=$5,processed_at=$6,generated_entry_id=$7,event=$8
			 WHERE company_id=$1 AND source_system=$2 AND idempotency_key=$3 AND event_id=$9`,
			event.CompanyID, event.SourceSystem, event.IdempotencyKey, event.Status, event.ErrorMessage,
			event.ProcessedAt, event.GeneratedEntryID, data, event.EventID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrNotFound
		}
		return nil
	})
}

func getEventByIdentityTx(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, sourceSystem, key string) (subledgers.EconomicEvent, bool, error) {
	var event subledgers.EconomicEvent
	var data, payload []byte
	err := tx.QueryRow(ctx,
		`SELECT event,payload_bytes FROM subledger_events
		 WHERE company_id=$1 AND source_system=$2 AND idempotency_key=$3`,
		companyID, sourceSystem, key).Scan(&data, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return event, false, nil
	}
	if err != nil {
		return event, false, err
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return event, false, err
	}
	event.PayloadJSON = append([]byte(nil), payload...)
	return event, true, nil
}
