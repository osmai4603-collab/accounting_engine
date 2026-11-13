package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"accounting_engine/pkg/api/v1/ledger"
	publicevents "accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (publicevents.Ingestion, bool, error) {
	var rec publicevents.Ingestion
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var entryID *uuid.UUID
		var snapshot []byte
		var status, eventType, payloadHash string
		scanErr := tx.QueryRow(ctx,
			`SELECT event_type, payload_hash, entry_id, entry_snapshot, status, failure_reason
			   FROM public_event_ingestions
			  WHERE company_id=$1 AND idempotency_key=$2`,
			companyID, strings.TrimSpace(idempotencyKey),
		).Scan(&eventType, &payloadHash, &entryID, &snapshot, &status, &rec.FailureReason)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return pgx.ErrNoRows
		}
		if scanErr != nil {
			return scanErr
		}
		rec.CompanyID = companyID
		rec.IdempotencyKey = strings.TrimSpace(idempotencyKey)
		rec.EventType = eventType
		rec.PayloadHash = payloadHash
		rec.Status = status
		if entryID != nil {
			rec.EntryID = *entryID
		}
		if len(snapshot) > 0 && string(snapshot) != "{}" && string(snapshot) != "null" {
			if err := json.Unmarshal(snapshot, &rec.Entry); err != nil {
				return fmt.Errorf("decode public event ingestion entry snapshot: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return publicevents.Ingestion{}, false, nil
	}
	if err != nil {
		return publicevents.Ingestion{}, false, err
	}
	return rec, true, nil
}

func (d *Driver) ClaimPublicEventIngestion(ctx context.Context, record publicevents.Ingestion) (publicevents.Ingestion, bool, error) {
	key := strings.TrimSpace(record.IdempotencyKey)
	if record.Status == "" {
		record.Status = "processing"
	}
	snapshot, err := json.Marshal(record.Entry)
	if err != nil {
		return publicevents.Ingestion{}, false, fmt.Errorf("encode public event ingestion entry snapshot: %w", err)
	}
	if len(snapshot) == 0 || string(snapshot) == "null" {
		snapshot = []byte("{}")
	}
	var existing publicevents.Ingestion
	reclaimed := false
	err = d.WithCompanyTx(ctx, record.CompanyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO public_event_ingestions(company_id,idempotency_key,event_type,payload_hash,entry_id,entry_snapshot,status,created_at,updated_at)
			 VALUES($1,$2,$3,$4,NULLIF($5,'00000000-0000-0000-0000-000000000000'::uuid),$6,$7,NOW(),NOW())
			 ON CONFLICT (company_id,idempotency_key) DO NOTHING`,
			record.CompanyID, key, record.EventType, record.PayloadHash, record.EntryID, snapshot, record.Status,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		var entryID *uuid.UUID
		var snap []byte
		var status, eventType, payloadHash, failureReason string
		if err := tx.QueryRow(ctx,
			`SELECT event_type, payload_hash, entry_id, entry_snapshot, status, failure_reason
			   FROM public_event_ingestions
			  WHERE company_id=$1 AND idempotency_key=$2
			  FOR UPDATE`,
			record.CompanyID, key,
		).Scan(&eventType, &payloadHash, &entryID, &snap, &status, &failureReason); err != nil {
			return err
		}
		if status == "failed" && payloadHash == record.PayloadHash {
			if _, err := tx.Exec(ctx,
				`UPDATE public_event_ingestions
				    SET status='processing', failure_reason='', updated_at=NOW()
				  WHERE company_id=$1 AND idempotency_key=$2`,
				record.CompanyID, key,
			); err != nil {
				return err
			}
			status = "processing"
			failureReason = ""
			reclaimed = true
			existing.Status = "processing"
		}
		existing.CompanyID = record.CompanyID
		existing.IdempotencyKey = key
		existing.EventType = eventType
		existing.PayloadHash = payloadHash
		existing.Status = status
		existing.FailureReason = failureReason
		if entryID != nil {
			existing.EntryID = *entryID
		}
		if len(snap) > 0 && string(snap) != "{}" && string(snap) != "null" {
			if err := json.Unmarshal(snap, &existing.Entry); err != nil {
				return fmt.Errorf("decode public event ingestion entry snapshot: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return publicevents.Ingestion{}, false, err
	}
	if existing.IdempotencyKey != "" || existing.Status != "" || existing.EntryID != uuid.Nil {
		if reclaimed {
			return existing, true, nil
		}
		return existing, false, nil
	}
	// claimed
	record.IdempotencyKey = key
	return record, true, nil
}

func (d *Driver) CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error {
	snapshot, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE public_event_ingestions
			    SET status='processed', entry_id=$3, entry_snapshot=$4, failure_reason='', updated_at=NOW()
			  WHERE company_id=$1 AND idempotency_key=$2
			    AND (entry_id IS NULL OR entry_id=$3)
			    AND (status='processing' OR (status='processed' AND entry_id=$3))`,
			companyID, strings.TrimSpace(idempotencyKey), uuid.UUID(entry.ID), snapshot,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return publicevents.ErrStateConflict
		}
		return nil
	})
}

func (d *Driver) FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error {
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE public_event_ingestions
			    SET status='failed', failure_reason=$3, updated_at=NOW()
			  WHERE company_id=$1 AND idempotency_key=$2 AND status='processing'`,
			companyID, strings.TrimSpace(idempotencyKey), reason,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			    SELECT 1 FROM public_event_ingestions
			     WHERE company_id=$1 AND idempotency_key=$2
			)`,
			companyID, strings.TrimSpace(idempotencyKey),
		).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return publicevents.ErrRecordNotFound
		}
		return publicevents.ErrStateConflict
	})
}

func (d *Driver) GetPublicEventAccountMapping(ctx context.Context, companyID uuid.UUID, eventType, ledgerID string) (publicevents.AccountMapping, bool, error) {
	var m publicevents.AccountMapping
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT company_id,event_type,ledger_id,debit_account_code,credit_account_code,description_template,journal_code
			   FROM public_event_account_mappings
			  WHERE company_id=$1 AND event_type=$2 AND ledger_id=$3`,
			companyID, strings.TrimSpace(eventType), strings.TrimSpace(ledgerID),
		).Scan(&m.CompanyID, &m.EventType, &m.LedgerID, &m.DebitAccountCode, &m.CreditAccountCode, &m.DescriptionTemplate, &m.JournalCode)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return publicevents.AccountMapping{}, false, nil
	}
	if err != nil {
		return publicevents.AccountMapping{}, false, err
	}
	return m, true, nil
}

func (d *Driver) SavePublicEventAccountMapping(ctx context.Context, mapping publicevents.AccountMapping) error {
	return d.WithCompanyTx(ctx, mapping.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO public_event_account_mappings(company_id,event_type,ledger_id,debit_account_code,credit_account_code,description_template,journal_code,updated_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,NOW())
			 ON CONFLICT (company_id,event_type,ledger_id) DO UPDATE SET
			   debit_account_code=EXCLUDED.debit_account_code,
			   credit_account_code=EXCLUDED.credit_account_code,
			   description_template=EXCLUDED.description_template,
			   journal_code=EXCLUDED.journal_code,
			   updated_at=NOW()`,
			mapping.CompanyID, strings.TrimSpace(mapping.EventType), strings.TrimSpace(mapping.LedgerID),
			strings.TrimSpace(mapping.DebitAccountCode), strings.TrimSpace(mapping.CreditAccountCode),
			mapping.DescriptionTemplate, strings.TrimSpace(mapping.JournalCode),
		)
		return err
	})
}

var (
	_ publicevents.IdempotencyStore = (*Driver)(nil)
	_ publicevents.MappingStore     = (*Driver)(nil)
)
